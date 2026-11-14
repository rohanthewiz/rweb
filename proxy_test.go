package rweb_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestProxy(t *testing.T) {
	// US Server
	usTgtReadyChan := make(chan struct{}, 1)
	usTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: usTgtReadyChan, Address: "localhost:"})
	usTgt.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from US server root")
	})
	usTgt.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from US server '/status'")
	})

	usTgt.Get("/us/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the US server landing!</h1>")
	})

	usTgt.Post("/us/proxy-incoming/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"status": "success",
		}
		return ctx.WriteJSON(data)
	})

	// Euro Server
	euTgtReadyChan := make(chan struct{}, 1)
	euTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: euTgtReadyChan, Address: "localhost:"})
	euTgt.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from EU server root")
	})

	euTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the EU server landing!</h1>")
	})

	euTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		fv := ctx.Request().FormValue("abc")
		return ctx.WriteString(ctx.Request().GetPostValue("def") + fv)
	})

	go func() {
		_ = usTgt.Run() // run with high-order port
	}()

	go func() {
		_ = euTgt.Run()
	}()

	<-usTgtReadyChan // wait for US server
	<-euTgtReadyChan // wait for EUR server

	// Proxy
	pxyReadyChan := make(chan struct{}, 1)
	pxy := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: pxyReadyChan, Address: "localhost:"})

	pxy.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from proxy server root")
	})

	// e.g. curl http://localhost:8080/via-proxy/us/status
	// 		- This will proxy to http://localhost:8081/usa/proxy-incoming/status
	// e.g. curl http://localhost:8080/via-proxy/us
	err := pxy.Proxy("/via-proxy/us",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", usTgt.GetListenPort()), 1)
	if err != nil {
		log.Fatal(err)
	}

	err = pxy.Proxy("/us",
		fmt.Sprintf("http://localhost:%s", usTgt.GetListenPort()), 1)
	if err != nil {
		log.Fatal(err)
	}

	// e.g. curl http://localhost:8080/via-proxy/eu/status
	err = pxy.Proxy("/via-proxy/eu",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", euTgt.GetListenPort()), 2)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		defer stopRun(pxy, usTgt, euTgt)

		<-pxyReadyChan // wait for proxy

		// Proxy some calls
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/us/status", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		respMap := map[string]string{}
		_ = json.Unmarshal(body, &respMap)

		if st, ok := respMap["status"]; ok {
			assert.Equal(t, st, "success")
		} else {
			t.Error("Response map does not contain 'status'")
		}

		// Proxy to US server root
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/us", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "<h1>Welcome to the US server landing!</h1>")

		// Proxy to US server root (shorter route)
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/us", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "Hi from US server root")

		// Proxy to US server root (shorter route) with trailing slash
		// For this to pass, server option `KeepTrailingSlash` must be left at default (false)
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/us/", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "Hi from US server root")

		// Proxy to EU
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/eu/status?abc=xyz", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=xyz&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "456xyz")
	}()

	_ = pxy.Run()
}

/*// TestProxySkewed tests proxying to one server by default and another server based on a prefix
func TestProxySkewed(t *testing.T) {
	// Init US Server
	usTgtReadyChan := make(chan struct{}, 1)
	usTgt := rweb.NewServer(rweb.WithOptions(rweb.ServerOptions{
		Verbose: true, ReadyChan: usTgtReadyChan, Address: "localhost:", // run with high-order port
	}))
	usTgt.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from US server root")
	})

	usTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the US server landing!</h1>")
	})

	usTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"status": "success",
		}
		return ctx.WriteJSON(data)
	})

	// Init Euro Server
	euTgtReadyChan := make(chan struct{}, 1)
	euTgt := rweb.NewServer(rweb.WithOptions(rweb.ServerOptions{Verbose: true, ReadyChan: euTgtReadyChan, Address: "localhost:"}))
	euTgt.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from EU server root")
	})

	euTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the EU server landing!</h1>")
	})

	euTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		fv := ctx.Request().FormValue("abc")
		return ctx.WriteString(ctx.Request().GetPostValue("def") + fv)
	})

	go func() {
		_ = usTgt.Run()
	}()

	go func() {
		_ = euTgt.Run()
	}()

	<-usTgtReadyChan // wait for US server
	<-euTgtReadyChan // wait for EUR server

	// Init Proxy
	pxyReadyChan := make(chan struct{}, 1)
	pxy := rweb.NewServer(rweb.WithOptions(rweb.ServerOptions{Verbose: true, ReadyChan: pxyReadyChan, Address: "localhost:"}))

	// e.g. curl http://localhost:8080/via-proxy/us/status
	// 		- This will proxy to http://localhost:8081/usa/proxy-incoming/status
	// e.g. curl http://localhost:8080/via-proxy/us
	err := pxy.Proxy("/",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", usTgt.GetListenPort()), 0)
	if err != nil {
		log.Fatal(err)
	}

	// e.g. curl http://localhost:8080/via-proxy/eu/status
	err = pxy.Proxy("/eu",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", euTgt.GetListenPort()), 1)
	if err != nil {
		log.Fatal(err)
	}

	pxy.Get("/admin*", func(ctx rweb.Context) error {
		return ctx.WriteString("At proxy server admin")
	})

	go func() {
		defer stopRun(pxy, usTgt, euTgt)

		<-pxyReadyChan // wait for proxy

		// Proxy to US server status
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s/status", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		respMap := map[string]string{}
		_ = json.Unmarshal(body, &respMap)

		if st, ok := respMap["status"]; ok {
			assert.Equal(t, st, "success")
		} else {
			t.Error("Response map does not contain 'status'")
		}

		// Call a Non-proxied route
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/admin/home", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "At proxy server admin")

		// Proxy to US server root
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "<h1>Welcome to the US server landing!</h1>")

		// Proxy to EU
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/eu/status?abc=xyz", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=xyz&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "456xyz")
	}()

	_ = pxy.Run()
}
*/

// ---------------------------------------------------------------------------
// Proxy behaviour over the wire (N-047): the upstream client, hop-by-hop and
// X-Forwarded-* headers, streamed responses, and upstream failures.
//
// Each test serves the proxy with Serve on its own loopback listener and
// shuts it down with Shutdown, so no signal is involved, and talks to it over
// a raw connection: the exact request headers are under the test's control,
// and http.ReadResponse shows the response as sent, hop-by-hop headers
// included. The upstream is an httptest.Server.
// ---------------------------------------------------------------------------

// serveProxyTest serves s on a loopback listener and returns its address. The
// server is shut down, and the listener closed, when the test ends; closing
// the listener also ends a Serve that had not started by then.
func serveProxyTest(t *testing.T, s *rweb.Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		_ = ln.Close()
	})
	return ln.Addr().String()
}

// dialProxyTest opens a raw client connection with a safety deadline.
func dialProxyTest(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c, bufio.NewReader(c)
}

// proxyRoundTrip writes a raw request and reads the response, body included
// (none for HEAD).
func proxyRoundTrip(t *testing.T, c net.Conn, br *bufio.Reader, method, rawReq string) (*http.Response, string) {
	t.Helper()
	if _, err := io.WriteString(c, rawReq); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// A 3xx from the upstream goes to the client as is. The proxy used
// http.DefaultClient, which followed the redirect itself: the client got the
// target's page under the original URL, and a Location meant for the
// browser (a login page, a canonical URL) never reached it.
func TestProxyPassesRedirectThrough(t *testing.T) {
	var targetHits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/new", http.StatusFound)
		case "/new":
			targetHits.Add(1)
			_, _ = io.WriteString(w, "new page")
		}
	}))
	t.Cleanup(up.Close)

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	resp, _ := proxyRoundTrip(t, c, br, "GET", "GET /up/old HTTP/1.1\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/new", resp.Header.Get("Location"))
	assert.Equal(t, int32(0), targetHits.Load())
}

// Hop-by-hop headers (RFC 9110 §7.6.1) describe one connection and must not
// be forwarded, in either direction: the fixed list, and every header the
// Connection header names. End-to-end headers, repeated ones included, pass.
func TestProxyStripsHopByHopHeaders(t *testing.T) {
	gotReq := make(chan http.Header, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq <- r.Header.Clone()
		h := w.Header()
		h.Set("Connection", "X-Up-Named")
		h.Set("X-Up-Named", "connection-scoped")
		h.Set("Keep-Alive", "timeout=9")
		h.Set("Proxy-Authenticate", `Basic realm="up"`)
		h.Set("Proxy-Connection", "keep-alive")
		h.Set("Upgrade", "example/1")
		h.Set("X-Up-End", "kept")
		h.Add("Set-Cookie", "a=1")
		h.Add("Set-Cookie", "b=2")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(up.Close)

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	resp, body := proxyRoundTrip(t, c, br, "GET", "GET /up/x HTTP/1.1\r\n"+
		"Host: front.test\r\n"+
		"Connection: keep-alive, X-Client-Named\r\n"+
		"X-Client-Named: connection-scoped\r\n"+
		"Keep-Alive: timeout=5\r\n"+
		"Proxy-Connection: keep-alive\r\n"+
		"Proxy-Authorization: Basic Zm9vOmJhcg==\r\n"+
		"TE: trailers\r\n"+
		"Upgrade: example/1\r\n"+
		"X-Client-End: kept\r\n"+
		"Accept: text/html\r\n"+
		"Accept: application/json\r\n"+
		"\r\n")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", body)

	reqH := <-gotReq
	for _, k := range []string{"Connection", "X-Client-Named", "Keep-Alive", "Proxy-Connection",
		"Proxy-Authorization", "Te", "Upgrade"} {
		if v, ok := reqH[k]; ok {
			t.Errorf("upstream received hop-by-hop request header %s: %q", k, v)
		}
	}
	assert.Equal(t, "kept", reqH.Get("X-Client-End"))
	// A repeated request header keeps every line (they were Set, last one
	// winning). Checked with Errorf so the response half below still runs.
	if got := strings.Join(reqH.Values("Accept"), "|"); got != "text/html|application/json" {
		t.Errorf("repeated request header: upstream got Accept %q, want both lines", got)
	}

	for _, k := range []string{"X-Up-Named", "Keep-Alive", "Proxy-Authenticate", "Proxy-Connection", "Upgrade"} {
		if v, ok := resp.Header[k]; ok {
			t.Errorf("client received hop-by-hop response header %s: %q", k, v)
		}
	}
	if conn := resp.Header.Get("Connection"); strings.Contains(strings.ToLower(conn), "x-up-named") {
		t.Errorf("client received the upstream's Connection header: %q", conn)
	}
	assert.Equal(t, "kept", resp.Header.Get("X-Up-End"))
	assert.Equal(t, "a=1|b=2", strings.Join(resp.Header.Values("Set-Cookie"), "|"))
}

// The upstream learns who asked: X-Forwarded-For gains the immediate peer
// (after whatever earlier proxies put there), X-Forwarded-Proto and
// X-Forwarded-Host describe this hop and replace client-sent values, and the
// Host the upstream sees is still the target's own.
func TestProxySetsForwardedHeaders(t *testing.T) {
	type seen struct {
		host string
		h    http.Header
	}
	got := make(chan seen, 4)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Host, r.Header.Clone()}
	}))
	t.Cleanup(up.Close)
	upHost := strings.TrimPrefix(up.URL, "http://")

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	// No earlier proxy.
	proxyRoundTrip(t, c, br, "GET", "GET /up/a HTTP/1.1\r\nHost: front.test\r\n\r\n")
	g := <-got
	assert.Equal(t, upHost, g.host)
	assert.Equal(t, "127.0.0.1", g.h.Get("X-Forwarded-For"))
	assert.Equal(t, "http", g.h.Get("X-Forwarded-Proto"))
	assert.Equal(t, "front.test", g.h.Get("X-Forwarded-Host"))

	// Earlier proxies on two header lines, and spoofed Proto/Host.
	proxyRoundTrip(t, c, br, "GET", "GET /up/b HTTP/1.1\r\nHost: front.test:8443\r\n"+
		"X-Forwarded-For: 198.51.100.1\r\n"+
		"X-Forwarded-For: 203.0.113.9\r\n"+
		"X-Forwarded-Proto: https\r\n"+
		"X-Forwarded-Host: evil.test\r\n\r\n")
	g = <-got
	// One line each: the earlier entries are folded into the new value,
	// not left as separate lines beside it.
	assert.Equal(t, "198.51.100.1, 203.0.113.9, 127.0.0.1", strings.Join(g.h.Values("X-Forwarded-For"), "|"))
	assert.Equal(t, "http", strings.Join(g.h.Values("X-Forwarded-Proto"), "|"))
	assert.Equal(t, "front.test:8443", strings.Join(g.h.Values("X-Forwarded-Host"), "|"))
}

// The response body is streamed, not read into memory first: the client
// receives the upstream's first bytes while the upstream is still holding
// the rest back. Sized (Content-Length) and unsized (chunked) upstream
// bodies both stream, and the request context stays live until the body is
// sent (a cancelled one would cut the upstream read short).
func TestProxyStreamsResponse(t *testing.T) {
	first := []byte("first-part;")
	rest := bytes.Repeat([]byte("0123456789abcdef"), 128<<10) // 2 MB
	for _, sized := range []bool{true, false} {
		name := "chunked"
		if sized {
			name = "sized"
		}
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			releaseUp := func() { once.Do(func() { close(release) }) }
			t.Cleanup(releaseUp) // a failed run must not leave the upstream parked

			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if sized {
					w.Header().Set("Content-Length", strconv.Itoa(len(first)+len(rest)))
				}
				_, _ = w.Write(first)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = w.Write(rest)
			}))
			t.Cleanup(up.Close)

			s := rweb.NewServer()
			assert.Nil(t, s.Proxy("/up", up.URL, 1))
			c, br := dialProxyTest(t, serveProxyTest(t, s))

			// Head and first bytes must arrive while the upstream holds the
			// rest: a buffering proxy sends nothing until the upstream is done.
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _ = io.WriteString(c, "GET /up/big HTTP/1.1\r\nHost: front.test\r\n\r\n")
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatalf("no response head while the upstream held its body back: %v", err)
			}
			got := make([]byte, len(first))
			if _, err := io.ReadFull(resp.Body, got); err != nil {
				t.Fatalf("first bytes did not arrive while the upstream held the rest: %v", err)
			}
			assert.Equal(t, string(first), string(got))
			if sized {
				assert.Equal(t, int64(len(first)+len(rest)), resp.ContentLength)
			} else {
				assert.Equal(t, "chunked", strings.Join(resp.TransferEncoding, "|"))
			}

			releaseUp()
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			tail, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the rest: %v", err)
			}
			if !bytes.Equal(tail, rest) {
				t.Fatalf("rest of body: got %d bytes, want %d", len(tail), len(rest))
			}

			// The connection is still good for another request.
			resp2, body2 := proxyRoundTrip(t, c, br, "GET", "GET /up/big HTTP/1.1\r\nHost: front.test\r\n\r\n")
			assert.Equal(t, http.StatusOK, resp2.StatusCode)
			assert.Equal(t, len(first)+len(rest), len(body2))
		})
	}
}

// An upstream that cannot be reached answers 502 Bad Gateway through the
// error handler (default: a logged error and its page), not 500: the
// failure is the upstream's, not this server's.
func TestProxyUnreachableUpstreamIs502(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", deadURL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))
	resp, _ := proxyRoundTrip(t, c, br, "GET", "GET /up/x HTTP/1.1\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

// An upstream that accepts the request but sends no response headers within
// ProxyResponseHeaderTimeout gets the client a 504 Gateway Timeout, and the
// upstream call is abandoned. http.DefaultClient had no timeout: a hung
// upstream held the client, a goroutine and a connection for as long as the
// client would wait.
func TestProxyUpstreamHeaderTimeoutIs504(t *testing.T) {
	upstreamGaveUp := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done(): // the proxy gave up and closed the connection
			upstreamGaveUp <- struct{}{}
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(up.Close)
	t.Cleanup(up.CloseClientConnections) // runs first: frees a parked handler on failure

	const headerTimeout = 200 * time.Millisecond
	s := rweb.NewServerWithOptions(rweb.WithProxyResponseHeaderTimeout(headerTimeout))
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	start := time.Now()
	resp, _ := proxyRoundTrip(t, c, br, "GET", "GET /up/slow HTTP/1.1\r\nHost: front.test\r\n\r\n")
	elapsed := time.Since(start)
	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
	if elapsed < headerTimeout || elapsed > 5*time.Second {
		t.Errorf("504 after %v, want about %v", elapsed, headerTimeout)
	}
	select {
	case <-upstreamGaveUp:
	case <-time.After(3 * time.Second):
		t.Error("the upstream request was not abandoned after the timeout")
	}

	// The option convention: a negative value disables the timeout, so the
	// same upstream is waited for (here until the test's own deadline on the
	// client side cuts it short, which the proxy sees as the client leaving).
	s2 := rweb.NewServerWithOptions(rweb.WithProxyResponseHeaderTimeout(-1))
	assert.Nil(t, s2.Proxy("/up", up.URL, 1))
	c2, br2 := dialProxyTest(t, serveProxyTest(t, s2))
	_ = c2.SetReadDeadline(time.Now().Add(4 * headerTimeout))
	_, _ = io.WriteString(c2, "GET /up/slow HTTP/1.1\r\nHost: front.test\r\n\r\n")
	if resp, err := http.ReadResponse(br2, nil); err == nil {
		t.Errorf("with the timeout disabled the proxy still answered %d within %v", resp.StatusCode, 4*headerTimeout)
	}
}

// HEAD and bodyless statuses through the proxy: a HEAD gets the upstream's
// Content-Length and no body, a 304 gets no body, and the next request on
// the same connection parses, which shows no stray body bytes were sent.
func TestProxyHeadAndBodylessResponses(t *testing.T) {
	const doc = "hello world"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(doc)))
		_, _ = io.WriteString(w, doc)
	}))
	t.Cleanup(up.Close)

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	resp, body := proxyRoundTrip(t, c, br, "HEAD", "HEAD /up/doc HTTP/1.1\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(len(doc)), resp.ContentLength)
	assert.Equal(t, "", body)

	resp, body = proxyRoundTrip(t, c, br, "GET", "GET /up/doc HTTP/1.1\r\nHost: front.test\r\nIf-None-Match: \"v1\"\r\n\r\n")
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.Equal(t, "", body)

	resp, body = proxyRoundTrip(t, c, br, "GET", "GET /up/doc HTTP/1.1\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, doc, body)
	assert.Equal(t, `"v1"`, resp.Header.Get("ETag"))
}

// An HTTP/1.0 client cannot read chunked encoding, so an upstream body of
// unknown length reaches it unframed, ended by the close of the connection.
func TestProxyHTTP10ClientUnknownLength(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, part := range []string{"one,", "two,", "three"} {
			_, _ = io.WriteString(w, part)
			w.(http.Flusher).Flush() // no Content-Length: the upstream answers chunked
		}
	}))
	t.Cleanup(up.Close)

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	resp, body := proxyRoundTrip(t, c, br, "GET", "GET /up/parts HTTP/1.0\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "one,two,three", body)
	assert.Equal(t, int64(-1), resp.ContentLength) // unframed: no Content-Length
	assert.Equal(t, 0, len(resp.TransferEncoding))
	assert.True(t, resp.Close)
}

// Shutdown closes the proxy's idle upstream connections. With
// http.DefaultClient they stayed open in a process-wide pool until the
// transport's idle timeout, holding a slot on the upstream long after this
// server had stopped.
func TestProxyShutdownClosesUpstreamIdleConns(t *testing.T) {
	upConnClosed := make(chan struct{}, 4)
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	up.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateClosed {
			upConnClosed <- struct{}{}
		}
	}
	up.Start()
	t.Cleanup(up.Close)

	s := rweb.NewServer()
	assert.Nil(t, s.Proxy("/up", up.URL, 1))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.Nil(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = s.Serve(ln) }()

	c, br := dialProxyTest(t, ln.Addr().String())
	resp, body := proxyRoundTrip(t, c, br, "GET", "GET /up/x HTTP/1.1\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.Nil(t, s.Shutdown(ctx))

	select {
	case <-upConnClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("the proxy's upstream connection was still open after Shutdown")
	}
}

// A proxy mounted on a group stays behind the group's middleware with the
// response streamed (N-058 covers the synthetic path in group_test.go).
func TestGroupProxyBehindMiddlewareOverWire(t *testing.T) {
	var upHits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upHits.Add(1)
		_, _ = io.WriteString(w, "UPSTREAM "+r.URL.Path)
	}))
	t.Cleanup(up.Close)

	s := rweb.NewServer()
	admin := s.Group("/admin", func(ctx rweb.Context) error {
		if ctx.Request().Header("X-Auth") != "ok" {
			return ctx.SetStatus(http.StatusUnauthorized).WriteString("denied")
		}
		return ctx.Next()
	})
	assert.Nil(t, admin.Proxy("/px", up.URL, 2))
	c, br := dialProxyTest(t, serveProxyTest(t, s))

	resp, body := proxyRoundTrip(t, c, br, "GET", "GET /admin/px/thing HTTP/1.1\r\nHost: front.test\r\n\r\n")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "denied", body)
	assert.Equal(t, int32(0), upHits.Load())

	resp, body = proxyRoundTrip(t, c, br, "GET", "GET /admin/px/thing HTTP/1.1\r\nHost: front.test\r\nX-Auth: ok\r\n\r\n")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "UPSTREAM /thing", body)
	assert.Equal(t, int32(1), upHits.Load())
}
