package rweb

// WebSocket upgrades through Proxy (N-066). Upgrade is hop-by-hop, so since
// N-047 the proxy stripped it and the upstream answered the handshake as a
// plain GET. Now a handshake keeps its upgrade, and on the upstream's 101 the
// client's connection is tied to the upstream's (relayProxiedUpgrade): frames
// are copied both ways until either side ends, or Shutdown.

import (
	"bufio"
	stdctx "context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// wsHandshake is a WebSocket opening handshake for path, with extra header
// lines (each ending in CRLF) if any.
func wsHandshake(path, extra string) string {
	return "GET " + path + " HTTP/1.1\r\nHost: front.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n" + extra + "\r\n"
}

// wsUpstream serves an rweb WebSocket upstream: /ws greets, then echoes
// until the peer closes, and reports on ended when its handler returns.
// AllowedOrigins admits the test's browser origin, which the upstream would
// otherwise refuse: the proxy sends it the target's Host, not front.test.
func wsUpstream(t *testing.T) (target string, ended <-chan struct{}) {
	t.Helper()
	done := make(chan struct{}, 4)
	up := NewServer()
	up.WebSocketWithOptions("/ws", WSOptions{
		AllowedOrigins: []string{"http://front.test"},
		Subprotocols:   []string{"chat"},
	}, func(ws *WSConn) error {
		defer func() { done <- struct{}{} }()
		if err := ws.WriteMessage(TextMessage, []byte("hello from upstream")); err != nil {
			return nil
		}
		for {
			msg, err := ws.ReadMessage()
			if err != nil || msg.Type == CloseMessage {
				return nil
			}
			if err := ws.WriteMessage(msg.Type, append([]byte("echo: "), msg.Data...)); err != nil {
				return nil
			}
		}
	})
	return "http://" + serveTest(t, up, transports[0]), done
}

// proxyFront serves a Proxy route /up -> target (prefix stripped) with Serve,
// so Shutdown can be tested, and returns the server and its address.
func proxyFront(t *testing.T, target string) (*Server, string) {
	t.Helper()
	s := NewServer()
	if err := s.Proxy("/up", target, 1); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, ln.Addr().String()
}

// A browser-like client reaches an upstream WebSocket through the proxy: the
// 101 carries the upstream's accept key and subprotocol, the upstream's
// greeting (sent right after its 101) arrives, frames echo both ways, and a
// close from the client ends the upstream's handler.
func TestProxyWebSocketTunnel(t *testing.T) {
	for _, tr := range transports { // the client's side of the proxy: tcp, tls
		t.Run(tr.name, func(t *testing.T) { testProxyWebSocketTunnel(t, tr) })
	}
}

func testProxyWebSocketTunnel(t *testing.T, tr transport) {
	target, ended := wsUpstream(t)
	front := NewServer()
	if err := front.Proxy("/up", target, 1); err != nil {
		t.Fatal(err)
	}
	c := dialTest(t, serveTest(t, front, tr), tr)
	writeAll(t, c, wsHandshake("/up/ws", "Origin: http://front.test\r\nSec-WebSocket-Protocol: chat\r\n"))

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d, want 101", resp.StatusCode)
	}
	// RFC 6455 §1.3: base64(SHA-1(key + GUID)) for the key above.
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("Sec-WebSocket-Accept = %q", got)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "chat" {
		t.Errorf("Sec-WebSocket-Protocol = %q, want the upstream's choice", got)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") || !strings.EqualFold(resp.Header.Get("Connection"), "Upgrade") {
		t.Errorf("Upgrade/Connection = %q/%q", resp.Header.Get("Upgrade"), resp.Header.Get("Connection"))
	}

	ws := NewWSConn(c, false)
	ws.r = br
	expectEcho(t, ws, "hello from upstream")
	for _, m := range []string{"one", "two", strings.Repeat("x", 200<<10)} {
		if err := ws.WriteMessage(TextMessage, []byte(m)); err != nil {
			t.Fatal(err)
		}
		expectEcho(t, ws, "echo: "+m)
	}
	_ = ws.Close(1000, "bye")
	recv(t, ended, "the upstream handler to end after the client's close")
}

// Frames the client sends in the same write as its handshake, before the
// 101, were already read by HTTP; they reach the upstream first, in order.
func TestProxyWebSocketLeadBytes(t *testing.T) {
	target, _ := wsUpstream(t)
	_, addr := proxyFront(t, target)
	c := dialTest(t, addr, transports[0])

	// A masked text frame "early", as a client writes it.
	frame := []byte{0x81, 0x80 | 5, 1, 2, 3, 4}
	for i, b := range []byte("early") {
		frame = append(frame, b^[]byte{1, 2, 3, 4}[i%4])
	}
	writeAll(t, c, wsHandshake("/up/ws", "Origin: http://front.test\r\n")+string(frame))
	ws := wsClient(t, c, bufio.NewReader(c))
	expectEcho(t, ws, "hello from upstream")
	expectEcho(t, ws, "echo: early")
}

// A handshake the upstream refuses comes back as the upstream's response,
// not as a 101 and not as a 502: here a page from another origin gets the
// upstream's 403.
func TestProxyWebSocketRefusedByUpstream(t *testing.T) {
	target, _ := wsUpstream(t)
	_, addr := proxyFront(t, target)
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, wsHandshake("/up/ws", "Origin: http://evil.test\r\n"))
	status, body := readBody(t, bufio.NewReader(c))
	if status != http.StatusForbidden || !strings.Contains(body, "cross-origin") {
		t.Fatalf("got %d %q, want the upstream's 403", status, body)
	}
}

// Shutdown ends a tunnel at once: both connections close, the upstream's
// handler returns, and Shutdown does not wait out its context.
func TestProxyWebSocketShutdown(t *testing.T) {
	target, ended := wsUpstream(t)
	s, addr := proxyFront(t, target)
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, wsHandshake("/up/ws", "Origin: http://front.test\r\n"))
	ws := wsClient(t, c, bufio.NewReader(c))
	expectEcho(t, ws, "hello from upstream")

	start := time.Now()
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Shutdown took %v with a tunnel open", took)
	}
	if _, err := ws.ReadMessage(); err == nil {
		t.Error("client read a message after Shutdown; want the tunnel closed")
	}
	recv(t, ended, "the upstream handler to end after Shutdown")
}

// A 101 that answers no handshake (the upstream switches a plain GET) is not
// relayed: the client gets 502.
func TestProxyUnaskedUpgradeIs502(t *testing.T) {
	up := NewServer()
	up.Get("/sw", func(ctx Context) error {
		c := ctx.(*context)
		conn := c.GetConn()
		c.wsUpgraded = true // the connection is ours; write a bare 101
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"))
		return nil
	})
	target := "http://" + serveTest(t, up, transports[0])
	_, addr := proxyFront(t, target)
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "GET /up/sw HTTP/1.1\r\nHost: front.test\r\n\r\n")
	status, _ := readBody(t, bufio.NewReader(c))
	if status != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", status)
	}
}
