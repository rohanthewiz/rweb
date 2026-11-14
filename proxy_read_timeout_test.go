package rweb_test

// ProxyReadTimeout (N-064): once a Proxy route has the upstream's response
// headers, each read of the upstream body must return within the timeout,
// or the upstream call is cancelled and the client's connection closed.
// Before, an upstream that stalled mid-body held the client connection
// until Shutdown. The timer runs only while a read waits on the upstream
// (proxyBodyReader), so a slow client is not taken for a stalled upstream.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// stallingUpstream sends part of a body and then stalls until released, the
// request is abandoned, or 10 s pass. sized chooses Content-Length (true)
// or chunked (false). gaveUp receives once the proxy has dropped the
// upstream request.
func stallingUpstream(t *testing.T, sized bool, release <-chan struct{}) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	gaveUp := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sized {
			w.Header().Set("Content-Length", "20")
		}
		_, _ = io.WriteString(w, "partial-") // 8 of the 20 bytes
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "rest-of-body")
		case <-r.Context().Done():
			gaveUp <- struct{}{}
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(up.Close)
	t.Cleanup(up.CloseClientConnections)
	return up, gaveUp
}

func TestProxyReadTimeoutCutsStalledBody(t *testing.T) {
	for _, sized := range []bool{true, false} {
		t.Run("sized="+strconv.FormatBool(sized), func(t *testing.T) {
			up, gaveUp := stallingUpstream(t, sized, nil)
			const readTimeout = 200 * time.Millisecond
			s := rweb.NewServerWithOptions(rweb.WithProxyReadTimeout(readTimeout))
			assert.Nil(t, s.Proxy("/up", up.URL, 1))
			c, br := dialProxyTest(t, serveProxyTest(t, s))

			start := time.Now()
			_, _ = io.WriteString(c, "GET /up/x HTTP/1.1\r\nHost: front.test\r\n\r\n")
			resp, err := http.ReadResponse(br, nil)
			assert.Nil(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			elapsed := time.Since(start)

			// The client gets what the upstream sent, then a body that ends
			// short (sized: fewer bytes than promised; chunked: no last
			// chunk), never one that looks complete.
			if err == nil {
				t.Fatalf("body read ended cleanly with %q; want a truncated body", body)
			}
			if string(body) != "partial-" {
				t.Errorf("body = %q, want the part sent before the stall", body)
			}
			if elapsed < readTimeout || elapsed > 5*time.Second {
				t.Errorf("cut off after %v, want about %v", elapsed, readTimeout)
			}
			select {
			case <-gaveUp:
			case <-time.After(3 * time.Second):
				t.Error("the upstream request was not abandoned after the read timeout")
			}
		})
	}
}

// A stall shorter than the timeout, and with the timeout off (the default)
// a stall of any length, leaves the body whole.
func TestProxyReadTimeoutSparesShortStallsAndIsOffByDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []rweb.ServerOption
	}{
		{"default (off)", nil},
		{"negative (off)", []rweb.ServerOption{rweb.WithProxyReadTimeout(-1)}},
		{"stall under the timeout", []rweb.ServerOption{rweb.WithProxyReadTimeout(2 * time.Second)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			up, _ := stallingUpstream(t, true, release)
			s := rweb.NewServerWithOptions(tc.opts...)
			assert.Nil(t, s.Proxy("/up", up.URL, 1))
			c, br := dialProxyTest(t, serveProxyTest(t, s))
			time.AfterFunc(400*time.Millisecond, func() { close(release) })
			_, body := proxyRoundTrip(t, c, br, "GET", "GET /up/x HTTP/1.1\r\nHost: front.test\r\n\r\n")
			assert.Equal(t, "partial-rest-of-body", body)
		})
	}
}

// The timer runs only while a read waits on the upstream. Here the upstream
// has the whole body ready at once, and the client reads it in pieces with
// pauses longer than the timeout: the proxy then waits on its writes to the
// client, not on the upstream, and must not cut the body off. A timer on
// the gap between two reads (nginx's proxy_read_timeout, which can afford it
// because nginx buffers the body) would fire here.
func TestProxyReadTimeoutIgnoresSlowClient(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 1<<17) // 2 MB
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	t.Cleanup(up.Close)

	const readTimeout = 100 * time.Millisecond
	s := rweb.NewServerWithOptions(rweb.WithProxyReadTimeout(readTimeout))
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))
	_, _ = io.WriteString(c, "GET /up/big HTTP/1.1\r\nHost: front.test\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	assert.Nil(t, err)

	got := make([]byte, 0, len(payload))
	piece := make([]byte, 256<<10)
	for {
		time.Sleep(3 * readTimeout) // a slow reader: the proxy's writes back up
		n, err := io.ReadFull(resp.Body, piece)
		got = append(got, piece[:n]...)
		if err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				t.Fatalf("after %d bytes: %v", len(got), err)
			}
			break
		}
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %d bytes, want the whole %d-byte body", len(got), len(payload))
	}
}
