package rweb

// Tests for Proxy's request body (N-057): a large upload is streamed to the
// upstream from the client's connection (proxyRequestBody, proxy.go) instead
// of being buffered first, a small one is still buffered, and the ways a
// streamed upload can end early leave the client with the right answer and
// the server with nothing stuck.
//
// The upstream is an httptest.Server; the proxy is served over a raw
// loopback listener (serveTest) and spoken to byte by byte.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// uploadProxy serves a proxy to upstream under /up and returns its address
// and a counter holding how many body bytes the proxy had buffered for the
// last request when its handler returned.
func uploadProxy(t *testing.T, upstream *httptest.Server, opts ServerOptions) (addr string, held *atomic.Int64) {
	t.Helper()
	return uploadProxyOver(t, upstream, opts, transports[0])
}

// uploadProxyOver is uploadProxy with the client side on the given transport.
func uploadProxyOver(t *testing.T, upstream *httptest.Server, opts ServerOptions, tr transport) (addr string, held *atomic.Int64) {
	t.Helper()
	held = &atomic.Int64{}
	s := NewServer(opts)
	s.Use(func(ctx Context) error {
		err := ctx.Next()
		held.Store(int64(bufferedLen(ctx)))
		return err
	})
	if err := s.Proxy("/up", upstream.URL, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeProxyIdleConns)
	return serveTest(t, s, tr), held
}

// hashingUpstream reads each request body to its end and answers with what
// it saw: the byte count, the hash, and how the body was framed.
func hashingUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, err := io.Copy(h, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "%d %s cl=%d te=%v", n, hex.EncodeToString(h.Sum(nil)), r.ContentLength, r.TransferEncoding)
	}))
	t.Cleanup(up.Close)
	return up
}

// A 3 MB upload passes through the proxy without being held: with a
// Content-Length not a byte of it is buffered and the upstream gets the same
// length; chunked, only the first 256 KB the proxy reads before deciding to
// stream, and the upstream gets it chunked. The connection then serves the
// next request, since the upstream read the upload to its end.
func TestProxyStreamsLargeUpload(t *testing.T) {
	data := bytes.Repeat([]byte("proxied-upload--"), 3<<20/16)
	for _, tc := range []struct {
		framing  string
		wantHeld int64
		wantSeen string
	}{
		{"content-length", 0, fmt.Sprintf("%d %s cl=%d te=[]", len(data), sum(data), len(data))},
		{"chunked", proxyBufferBodyMax, fmt.Sprintf("%d %s cl=-1 te=[chunked]", len(data), sum(data))},
	} {
		t.Run(tc.framing, func(t *testing.T) {
			addr, held := uploadProxy(t, hashingUpstream(t), ServerOptions{})
			c := dialTest(t, addr, transports[0])
			br := bufio.NewReader(c)

			head := "POST /up/store HTTP/1.1\r\nHost: test\r\n"
			body := data
			if tc.framing == "chunked" {
				head += "Transfer-Encoding: chunked\r\n\r\n"
				body = chunkedBody(data, 90_000)
			} else {
				head += fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
			}
			go func() {
				_, _ = io.WriteString(c, head)
				_, _ = c.Write(body)
			}()

			resp, got := readResp(t, br)
			if resp.StatusCode != 200 || got != tc.wantSeen {
				t.Fatalf("got %d %q, want %q", resp.StatusCode, got, tc.wantSeen)
			}
			if resp.Close {
				t.Fatal("Connection: close after an upload the upstream read to its end")
			}
			if held.Load() != tc.wantHeld {
				t.Fatalf("proxy buffered %d bytes of the upload, want %d", held.Load(), tc.wantHeld)
			}

			// Keep-alive: a second, small upload on the same connection.
			writeAll(t, c, "POST /up/store HTTP/1.1\r\nHost: test\r\nContent-Length: 5\r\n\r\nhello")
			if code, got := readBody(t, br); code != 200 || !strings.HasPrefix(got, "5 "+sum([]byte("hello"))) {
				t.Fatalf("second request on the connection: %d %q", code, got)
			}
		})
	}
}

// A small body is buffered as before, and so reaches the upstream with a
// Content-Length even when the client sent it chunked.
func TestProxyBuffersSmallUpload(t *testing.T) {
	data := bytes.Repeat([]byte("s"), 5000)
	addr, held := uploadProxy(t, hashingUpstream(t), ServerOptions{})
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "POST /up/store HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n"+string(chunkedBody(data, 700)))
	want := fmt.Sprintf("%d %s cl=%d te=[]", len(data), sum(data), len(data))
	if code, got := readBody(t, bufio.NewReader(c)); code != 200 || got != want {
		t.Fatalf("got %d %q, want %q", code, got, want)
	}
	if held.Load() != int64(len(data)) {
		t.Fatalf("proxy held %d bytes, want the whole %d", held.Load(), len(data))
	}
}

// The upstream answers without reading the upload, while the client is
// still in the middle of sending it. The client gets that answer, marked
// "Connection: close", and the connection closes; nothing waits for the
// rest of the 20 MB. Over TLS as well: the upload's read is parked in the
// client's connection when the request ends, and is woken with a read
// deadline, which a tls.Conn has to survive long enough for the close.
func TestProxyUpstreamAnswersBeforeUploadEnds(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "not for you", http.StatusForbidden)
			}))
			t.Cleanup(up.Close)
			addr, _ := uploadProxyOver(t, up, ServerOptions{}, tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)

			// The head, 300 KB of the body, and then silence.
			writeAll(t, c, "POST /up/store HTTP/1.1\r\nHost: test\r\nContent-Length: 20971520\r\n\r\n"+strings.Repeat("u", 300_000))
			resp, body := readResp(t, br)
			if resp.StatusCode != 403 || !strings.Contains(body, "not for you") || !resp.Close {
				t.Fatalf("got %d %q close=%v; want the upstream's 403 and close", resp.StatusCode, body, resp.Close)
			}
			expectConnClosed(t, c, br)
		})
	}
}

// The client's "Expect: 100-continue" reaches the upstream for a streamed
// upload, so the upstream decides whether the body is sent at all: one that
// refuses on the headers stops the upload at the client (no 100 Continue,
// just the refusal), and one that reads the body has the client told to
// send it.
func TestProxyPassesExpectContinueOn(t *testing.T) {
	const size = 1 << 20
	head := fmt.Sprintf("POST /up/store HTTP/1.1\r\nHost: test\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", size)

	t.Run("upstream refuses", func(t *testing.T) {
		var sawExpect atomic.Value
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sawExpect.Store(r.Header.Get("Expect"))
			http.Error(w, "who are you", http.StatusUnauthorized)
		}))
		t.Cleanup(up.Close)
		addr, _ := uploadProxy(t, up, ServerOptions{})
		c := dialTest(t, addr, transports[0])
		writeAll(t, c, head)
		got, _ := readUntilClosed(c)
		if !strings.HasPrefix(got, "HTTP/1.1 401 ") {
			t.Fatalf("first line %q, want the upstream's 401 with no 100 Continue before it", firstLine(got))
		}
		if v, _ := sawExpect.Load().(string); !strings.EqualFold(v, "100-continue") {
			t.Fatalf("upstream saw Expect %q", v)
		}
	})

	t.Run("upstream accepts", func(t *testing.T) {
		addr, _ := uploadProxy(t, hashingUpstream(t), ServerOptions{})
		c := dialTest(t, addr, transports[0])
		br := bufio.NewReader(c)
		writeAll(t, c, head)
		expectInterim(t, c, br)
		data := bytes.Repeat([]byte("e"), size)
		go func() { _, _ = c.Write(data) }()
		if code, got := readBody(t, br); code != 200 || !strings.HasPrefix(got, fmt.Sprintf("%d %s", size, sum(data))) {
			t.Fatalf("got %d %q", code, got)
		}
	})
}

// The client goes away in the middle of a streamed upload: the upstream's
// read of the body fails (it does not see a short body as a whole one), and
// the proxy is left able to serve the next client.
func TestProxyClientLeavesMidUpload(t *testing.T) {
	upErr := make(chan error, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, err := io.Copy(io.Discard, r.Body)
			upErr <- err
			return
		}
		_, _ = io.WriteString(w, "alive")
	}))
	t.Cleanup(up.Close)
	addr, _ := uploadProxy(t, up, ServerOptions{})

	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "POST /up/store HTTP/1.1\r\nHost: test\r\nContent-Length: 2097152\r\n\r\n"+strings.Repeat("u", 400_000))
	time.Sleep(settle) // let the upload reach the upstream
	_ = c.Close()

	if err := recv(t, upErr, "upstream's body read to end"); err == nil {
		t.Fatal("upstream read a cut-short upload to a clean end")
	}

	c2 := dialTest(t, addr, transports[0])
	writeAll(t, c2, getReq("/up/ping"))
	if code, got := readBody(t, bufio.NewReader(c2)); code != 200 || got != "alive" {
		t.Fatalf("proxy after the abandoned upload: %d %q", code, got)
	}
}

// A streamed chunked upload that turns out malformed past the part the proxy
// buffered: the client is answered 400 by the server, as for any malformed
// body, not 502 for an upstream that did nothing wrong.
func TestProxyStreamedUploadMalformedIs400(t *testing.T) {
	addr, _ := uploadProxy(t, hashingUpstream(t), ServerOptions{})
	c := dialTest(t, addr, transports[0])

	good := chunkedBody(bytes.Repeat([]byte("g"), 400_000), 50_000)
	good = good[:len(good)-len("0\r\n\r\n")] // drop the last-chunk marker
	go func() {
		_, _ = io.WriteString(c, "POST /up/store HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n")
		_, _ = c.Write(good)
		_, _ = io.WriteString(c, "5\r\nhelloXX\r\n0\r\n\r\n") // chunk data not followed by CRLF
	}()
	got, _ := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 400 ") {
		t.Fatalf("got %q, want 400", firstLine(got))
	}
}
