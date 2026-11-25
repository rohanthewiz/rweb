package rweb

import (
	"bufio"
	"bytes"
	stdctx "context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for DialWebSocket (websocket_client.go). Two kinds of peer:
//
//   - an rweb server (serveShutdownTest), over tcp and tls, for everything a
//     real session does: echo, close, shutdown, subprotocols, compression;
//   - a raw TCP server (rawDialServer) that writes exact bytes, for what an
//     rweb server never sends: frames in the same write as the 101,
//     fragments with a ping between them, malformed or malicious answers.
//
// Several cases are regressions of the 2025 draft client (branch
// roh/add-websocket-client, deleted 2026-11-25), which lost the frames
// behind the 101, hung on fragmented messages, sent no close frame and
// panicked on a 64-bit length with the top bit set.

// dialURL is the ws:// or wss:// URL of path on addr.
func dialURL(addr string, tr transport, path string) string {
	if tr.tls {
		return "wss://" + addr + path
	}
	return "ws://" + addr + path
}

// dialOpts trusts the TLS test server's self-signed certificate.
func dialOpts(tr transport) WSDialOptions {
	if tr.tls {
		return WSDialOptions{TLSConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return WSDialOptions{}
}

// mustDial dials or fails the test, and closes the connection at the end.
func mustDial(t *testing.T, url string, o WSDialOptions) (*WSConn, *http.Response) {
	t.Helper()
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := DialWebSocket(ctx, url, o)
	if err != nil {
		t.Fatalf("DialWebSocket(%s): %v", url, err)
	}
	t.Cleanup(func() { _ = ws.Close(1000, "") })
	return ws, resp
}

// readText reads one message and fails unless it is the text want.
func readText(t *testing.T, ws *WSConn, want string) {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(waitFor))
	msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("reading %q: %v", want, err)
	}
	if msg.Type != TextMessage || string(msg.Data) != want {
		t.Fatalf("got %v %q, want text %q", msg.Type, msg.Data, want)
	}
	_ = ws.SetReadDeadline(time.Time{})
}

// echoServer serves one WebSocket route that echoes every message, and
// sends what each session's last ReadMessage returned on ended.
func echoServer(opts WSOptions, ended chan<- *WSMessage) *Server {
	s := NewServer(ServerOptions{Logger: &captureLogger{}})
	s.WebSocketWithOptions("/ws", opts, func(ws *WSConn) error {
		for {
			msg, err := ws.ReadMessage()
			if err != nil || msg.Type == CloseMessage {
				if ended != nil {
					ended <- msg
				}
				return err
			}
			if err := ws.WriteMessage(msg.Type, msg.Data); err != nil {
				return err
			}
		}
	})
	return s
}

// rawDialServer accepts one connection, reads the upgrade request and hands
// it to fn with the conn and the reader that read it (which may hold the
// client's first frames). The conn closes when fn returns, and the listener
// and any conn still open when the test ends (a fn still sleeping).
func rawDialServer(t *testing.T, fn func(c net.Conn, br *bufio.Reader, req *http.Request)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Registered here, not from the accept goroutine: t.Cleanup must not be
	// called once the test may have finished.
	var mu sync.Mutex
	var open net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		if open != nil {
			_ = open.Close()
		}
		mu.Unlock()
	})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		open = c
		mu.Unlock()
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		fn(c, br, req)
	}()
	return ln.Addr().String()
}

// rawSession carries on a raw server's connection as a WebSocket server
// would, from where the script left off: it reads the client's frames (pings
// answered) until the client's close frame, which it answers. Without it a
// client's Close would wait out the 2 s close handshake on a silent peer.
func rawSession(c net.Conn, br *bufio.Reader) {
	srv := NewWSConn(c, true)
	srv.r = br
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if msg, err := srv.ReadMessage(); err != nil || msg.Type == CloseMessage {
			return
		}
	}
}

// upgradeHead is a valid 101 for req, plus extra header lines (each ending
// in CRLF).
func upgradeHead(req *http.Request, extra string) string {
	return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(req.Header.Get("Sec-WebSocket-Key")) + "\r\n" +
		extra + "\r\n"
}

// serverFrame is an unmasked frame as a server sends it.
func serverFrame(t *testing.T, opcode int, fin bool, data string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeRawFrame(&b, opcode, fin, false, []byte(data)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A session against an rweb server, over tcp and tls: the 101 is returned,
// text and a 70 KB binary message echo, and Close sends a close frame with
// its code and reason that the server's handler reads as a CloseMessage,
// then returns as soon as the server answers.
func TestDialWebSocketEcho(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			ended := make(chan *WSMessage, 1)
			addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, ended), tr)
			ws, resp := mustDial(t, dialURL(addr, tr, "/ws"), dialOpts(tr))
			if resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("status %d", resp.StatusCode)
			}

			if err := ws.WriteMessage(TextMessage, []byte("hello")); err != nil {
				t.Fatal(err)
			}
			readText(t, ws, "hello")
			big := bytes.Repeat([]byte{0, 1, 2, 3, 250}, 14000)
			if err := ws.WriteMessage(BinaryMessage, big); err != nil {
				t.Fatal(err)
			}
			msg, err := ws.ReadMessage()
			if err != nil || msg.Type != BinaryMessage || !bytes.Equal(msg.Data, big) {
				t.Fatalf("binary echo: err=%v type=%v len=%d", err, msg.Type, len(msg.Data))
			}

			start := time.Now()
			if err := ws.Close(1000, "bye"); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if d := time.Since(start); d > time.Second {
				t.Errorf("Close took %v; the server answers at once", d)
			}
			got := recv(t, ended, "server handler's end")
			if got == nil || got.Type != CloseMessage || len(got.Data) < 2 ||
				binary.BigEndian.Uint16(got.Data) != 1000 || string(got.Data[2:]) != "bye" {
				t.Fatalf("server saw %+v, want a close frame 1000 \"bye\"", got)
			}
		})
	}
}

// http:// is taken as ws:// (and https:// as wss://).
func TestDialWebSocketHTTPScheme(t *testing.T) {
	addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, nil), transports[0])
	ws, _ := mustDial(t, "http://"+addr+"/ws", WSDialOptions{})
	if err := ws.WriteMessage(TextMessage, []byte("via http://")); err != nil {
		t.Fatal(err)
	}
	readText(t, ws, "via http://")
}

// A frame the server sent in the same write as its 101 is read into the
// handshake's buffer with the response head; it must reach ReadMessage,
// not be dropped with that buffer.
func TestDialWebSocketKeepsFrameAfter101(t *testing.T) {
	addr := rawDialServer(t, func(c net.Conn, br *bufio.Reader, req *http.Request) {
		out := append([]byte(upgradeHead(req, "")), serverFrame(t, wsText, true, "welcome")...)
		_, _ = c.Write(out)
		rawSession(c, br)
	})
	ws, _ := mustDial(t, "ws://"+addr+"/", WSDialOptions{})
	readText(t, ws, "welcome")
}

// The same through an rweb handler that greets as it starts, as
// examples/websocket's echo route does: whether the greeting lands in the
// handshake's buffer is a race, so many connections are opened; none may
// miss it. (The draft client lost it in 22 of 30.)
func TestDialWebSocketGreetingFromHandler(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer(ServerOptions{Logger: &captureLogger{}})
			s.WebSocket("/ws", func(ws *WSConn) error {
				if err := ws.WriteMessage(TextMessage, []byte("welcome")); err != nil {
					return err
				}
				_, err := ws.ReadMessage()
				return err
			})
			addr, _ := serveShutdownTest(t, s, tr)
			for i := 0; i < 25; i++ {
				ws, _ := mustDial(t, dialURL(addr, tr, "/ws"), dialOpts(tr))
				readText(t, ws, "welcome")
				_ = ws.Close(1000, "")
			}
		})
	}
}

// A message in three fragments with a ping between two of them (RFC 6455
// §5.4 allows control frames inside a fragmented message) is reassembled,
// and the ping is answered with a masked pong carrying its payload.
func TestDialWebSocketFragmentsAndPing(t *testing.T) {
	pong := make(chan string, 1)
	addr := rawDialServer(t, func(c net.Conn, br *bufio.Reader, req *http.Request) {
		var out []byte
		out = append(out, upgradeHead(req, "")...)
		out = append(out, serverFrame(t, wsText, false, "hel")...)
		out = append(out, serverFrame(t, wsPing, true, "are you there")...)
		out = append(out, serverFrame(t, wsContinuation, false, "lo, ")...)
		out = append(out, serverFrame(t, wsContinuation, true, "world")...)
		_, _ = c.Write(out)
		// Read the client's frames as a server would (masked required).
		srv := NewWSConn(c, true)
		srv.r = br
		_ = c.SetReadDeadline(time.Now().Add(waitFor))
		op, _, _, data, err := srv.readFrame()
		if err != nil {
			pong <- "error: " + err.Error()
			return
		}
		pong <- fmt.Sprintf("op=%d %s", op, data)
		// Then on to the client's close frame, through the same srv (its
		// read buffer may already hold it).
		for {
			if msg, err := srv.ReadMessage(); err != nil || msg.Type == CloseMessage {
				return
			}
		}
	})
	ws, _ := mustDial(t, "ws://"+addr+"/", WSDialOptions{})
	readText(t, ws, "hello, world")
	if got, want := recv(t, pong, "the client's pong"), fmt.Sprintf("op=%d are you there", wsPong); got != want {
		t.Fatalf("server read %q, want %q", got, want)
	}
}

// Shutdown sends the client 1001; the client's ReadMessage returns it as a
// CloseMessage and answers, the server's handler reads that answer, and
// Shutdown returns at once instead of waiting out the close handshake.
func TestDialWebSocketServerShutdown(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			ended := make(chan *WSMessage, 1)
			s := echoServer(WSOptions{}, ended)
			addr, _ := serveShutdownTest(t, s, tr)
			ws, _ := mustDial(t, dialURL(addr, tr, "/ws"), dialOpts(tr))
			// One round trip, so the session is surely running before Shutdown.
			_ = ws.WriteMessage(TextMessage, []byte("ready?"))
			readText(t, ws, "ready?")

			got := make(chan *WSMessage, 1)
			go func() {
				msg, _ := ws.ReadMessage()
				got <- msg
			}()
			start := time.Now()
			if err := <-shutdownAsync(s, 5*time.Second); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if d := time.Since(start); d > time.Second {
				t.Errorf("Shutdown took %v; the client answers 1001 at once", d)
			}
			msg := recv(t, got, "the client's CloseMessage")
			if msg == nil || msg.Type != CloseMessage || binary.BigEndian.Uint16(msg.Data) != wsCloseGoingAway {
				t.Fatalf("client read %+v, want CloseMessage 1001", msg)
			}
			if srv := recv(t, ended, "server handler's end"); srv == nil || srv.Type != CloseMessage {
				t.Fatalf("server handler read %+v, want the client's answering close frame", srv)
			}
		})
	}
}

// A refusal comes back as ErrWebSocketBadHandshake with the response, its
// status and up to 64 KB of its body: here the origin check's 403 (the
// client sends the Origin it is given) and a 404.
func TestDialWebSocketRefused(t *testing.T) {
	addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, nil), transports[0])
	for _, tc := range []struct {
		name, path string
		header     http.Header
		status     int
		body       string
	}{
		{"cross-origin", "/ws", http.Header{"Origin": {"http://evil.example"}}, 403, "cross-origin"},
		{"no route", "/nope", nil, 404, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, resp, err := DialWebSocket(stdctx.Background(), "ws://"+addr+tc.path, WSDialOptions{Header: tc.header})
			if ws != nil || !errors.Is(err, ErrWebSocketBadHandshake) {
				t.Fatalf("ws=%v err=%v, want ErrWebSocketBadHandshake", ws, err)
			}
			if resp == nil || resp.StatusCode != tc.status {
				t.Fatalf("resp=%v, want status %d", resp, tc.status)
			}
			var body bytes.Buffer
			_, _ = body.ReadFrom(resp.Body)
			if !strings.Contains(body.String(), tc.body) {
				t.Fatalf("body %q does not contain %q", body.String(), tc.body)
			}
		})
	}
}

// Every 101 that does not complete the handshake fails the dial with
// ErrWebSocketBadHandshake (and closes the conn); the tolerated variants
// (token lists, case) succeed.
func TestDialWebSocketChecksResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compress bool
		protos   []string
		head     func(req *http.Request) string
		wantErr  string // "" = must succeed
	}{
		{"wrong accept", false, nil, func(*http.Request) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
				"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(make([]byte, 20)) + "\r\n\r\n"
		}, "Sec-WebSocket-Accept"},
		{"no upgrade", false, nil, func(req *http.Request) string {
			return "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\n" +
				"Sec-WebSocket-Accept: " + wsAcceptKey(req.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"
		}, "Upgrade: websocket"},
		{"no connection", false, nil, func(req *http.Request) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n" +
				"Sec-WebSocket-Accept: " + wsAcceptKey(req.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"
		}, "Connection: Upgrade"},
		{"subprotocol not offered", false, []string{"a"}, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Protocol: b\r\n")
		}, "not offered"},
		{"subprotocol without offer", false, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Protocol: a\r\n")
		}, "not offered"},
		{"two subprotocols", false, []string{"a", "b"}, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Protocol: a\r\nSec-WebSocket-Protocol: b\r\n")
		}, "more than one"},
		{"extension not offered", false, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate\r\n")
		}, "not offered"},
		{"client window", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits=10\r\n")
		}, "client_max_window_bits"},
		{"server window too small", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate; server_max_window_bits=7\r\n")
		}, "8 to 15"},
		{"unknown parameter", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate; x_y\r\n")
		}, "unknown parameter"},
		{"repeated parameter", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; server_no_context_takeover\r\n")
		}, "repeated"},
		{"two extensions", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate, permessage-deflate\r\n")
		}, "more than one extension"},
		{"other extension", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: x-webkit-deflate-frame\r\n")
		}, "not offered"},
		{"token lists and case", false, []string{"a", "b"}, func(req *http.Request) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: WebSocket\r\nConnection: keep-alive, upgrade\r\n" +
				"Sec-WebSocket-Accept: " + wsAcceptKey(req.Header.Get("Sec-WebSocket-Key")) + "\r\n" +
				"Sec-WebSocket-Protocol: b\r\n\r\n"
		}, ""},
		{"deflate with window and no context", true, nil, func(req *http.Request) string {
			return upgradeHead(req, "Sec-WebSocket-Extensions: permessage-deflate; server_max_window_bits=10; server_no_context_takeover; client_no_context_takeover\r\n")
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := make(chan bool, 1)
			addr := rawDialServer(t, func(c net.Conn, br *bufio.Reader, req *http.Request) {
				_, _ = c.Write([]byte(tc.head(req)))
				// The client closes the conn on a failed check.
				_ = c.SetReadDeadline(time.Now().Add(waitFor))
				_, err := br.ReadByte()
				closed <- err != nil && !errors.Is(err, os.ErrDeadlineExceeded)
			})
			ws, resp, err := DialWebSocket(stdctx.Background(), "ws://"+addr+"/",
				WSDialOptions{Compression: tc.compress, Subprotocols: tc.protos, PingInterval: -1})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("dial failed: %v", err)
				}
				_ = ws.conn.Close()
				return
			}
			if ws != nil || !errors.Is(err, ErrWebSocketBadHandshake) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ws=%v err=%v, want ErrWebSocketBadHandshake mentioning %q", ws, err, tc.wantErr)
			}
			if resp == nil || resp.StatusCode != 101 {
				t.Errorf("resp=%v, want the 101 that failed the checks", resp)
			}
			if !recv(t, closed, "the server's read") {
				t.Errorf("the client left the connection open after a failed handshake")
			}
		})
	}
}

// The request: target with query, Host (the URL's, or the caller's),
// credentials from the URL as Basic unless an Authorization is given, the
// caller's headers, offered subprotocols and the deflate offer.
func TestDialWebSocketRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		url    func(addr string) string
		opts   WSDialOptions
		checks map[string]string // header name (or ":target") → value
	}{
		{"defaults", func(a string) string { return "ws://" + a + "/chat?room=1&x=%20y" }, WSDialOptions{},
			map[string]string{":target": "/chat?room=1&x=%20y", "Sec-WebSocket-Version": "13",
				"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Protocol": "", "Sec-WebSocket-Extensions": ""}},
		{"options", func(a string) string { return "ws://" + a }, WSDialOptions{
			Header:       http.Header{"X-Token": {"t1"}, "Origin": {"https://app.example"}, "Host": {"virtual.example"}},
			Subprotocols: []string{"chat.v2", "chat.v1"},
			Compression:  true,
		}, map[string]string{":target": "/", ":host": "virtual.example", "X-Token": "t1", "Origin": "https://app.example",
			"Sec-WebSocket-Protocol": "chat.v2, chat.v1", "Sec-WebSocket-Extensions": "permessage-deflate"}},
		{"credentials", func(a string) string { return "ws://user:p%40ss@" + a + "/" }, WSDialOptions{},
			map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("user:p@ss"))}},
		{"own authorization wins", func(a string) string { return "ws://user:pass@" + a + "/" },
			WSDialOptions{Header: http.Header{"Authorization": {"Bearer abc"}}},
			map[string]string{"Authorization": "Bearer abc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reqs := make(chan *http.Request, 1)
			addr := rawDialServer(t, func(c net.Conn, _ *bufio.Reader, req *http.Request) {
				reqs <- req
				_, _ = c.Write([]byte(upgradeHead(req, "")))
				time.Sleep(time.Second)
			})
			o := tc.opts
			o.PingInterval = -1
			ws, _, err := DialWebSocket(stdctx.Background(), tc.url(addr), o)
			if err != nil {
				t.Fatal(err)
			}
			_ = ws.conn.Close()
			req := recv(t, reqs, "the request")
			for name, want := range tc.checks {
				var got string
				switch name {
				case ":target":
					got = req.RequestURI
				case ":host":
					got = req.Host
				default:
					got = strings.Join(req.Header.Values(name), "|")
				}
				if got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if req.Method != http.MethodGet || req.Proto != "HTTP/1.1" {
				t.Errorf("request line %s %s", req.Method, req.Proto)
			}
			if k, err := base64.StdEncoding.DecodeString(req.Header.Get("Sec-WebSocket-Key")); err != nil || len(k) != 16 {
				t.Errorf("Sec-WebSocket-Key %q is not 16 bytes of base64", req.Header.Get("Sec-WebSocket-Key"))
			}
		})
	}
}

// Options and URLs that cannot make a valid request are refused before
// anything is dialed.
func TestDialWebSocketRefusesBadInput(t *testing.T) {
	var dialed atomic.Bool
	dial := func(stdctx.Context, string, string) (net.Conn, error) {
		dialed.Store(true)
		return nil, errors.New("dialed")
	}
	for _, tc := range []struct {
		name, url string
		opts      WSDialOptions
		want      string
	}{
		{"scheme", "ftp://h/", WSDialOptions{}, "not ws or wss"},
		{"no host", "ws:///path", WSDialOptions{}, "no host"},
		{"space in query", "ws://h/a?b c", WSDialOptions{}, "space or control"},
		{"handshake header", "ws://h/", WSDialOptions{Header: http.Header{"Upgrade": {"x"}}}, "set by the handshake"},
		{"handshake header any case", "ws://h/", WSDialOptions{Header: http.Header{"sec-websocket-key": {"x"}}}, "set by the handshake"},
		{"framing header", "ws://h/", WSDialOptions{Header: http.Header{"Content-Length": {"5"}}}, "set by the handshake"},
		{"CRLF in value", "ws://h/", WSDialOptions{Header: http.Header{"X-Token": {"a\r\nX-Evil: 1"}}}, "control character"},
		{"bad name", "ws://h/", WSDialOptions{Header: http.Header{"Bad Name": {"x"}}}, "not a token"},
		{"two hosts", "ws://h/", WSDialOptions{Header: http.Header{"Host": {"a", "b"}}}, "exactly one"},
		{"bad subprotocol", "ws://h/", WSDialOptions{Subprotocols: []string{"a b"}}, "not a token"},
		{"compression level", "ws://h/", WSDialOptions{CompressionLevel: 42}, "out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialed.Store(false)
			o := tc.opts
			o.NetDialContext = dial
			_, _, err := DialWebSocket(stdctx.Background(), tc.url, o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want one mentioning %q", err, tc.want)
			}
			if dialed.Load() {
				t.Fatal("dialed despite invalid input")
			}
		})
	}
	// The error names the URL without its password.
	_, _, err := DialWebSocket(stdctx.Background(), "ws://u:secret@h/", WSDialOptions{
		NetDialContext: dial, Header: http.Header{"Upgrade": {"x"}}})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v leaks the password", err)
	}
}

// A server that accepts and never answers holds the dial no longer than
// HandshakeTimeout (an error wrapping context.DeadlineExceeded), and a
// cancelled ctx ends it at once (context.Canceled).
func TestDialWebSocketHandshakeBounded(t *testing.T) {
	silent := func(net.Conn, *bufio.Reader, *http.Request) { time.Sleep(3 * time.Second) }

	start := time.Now()
	_, _, err := DialWebSocket(stdctx.Background(), "ws://"+rawDialServer(t, silent)+"/",
		WSDialOptions{HandshakeTimeout: 200 * time.Millisecond})
	if !errors.Is(err, stdctx.DeadlineExceeded) {
		t.Fatalf("err=%v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("timeout of 200ms took %v", d)
	}

	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start = time.Now()
	_, _, err = DialWebSocket(ctx, "ws://"+rawDialServer(t, silent)+"/")
	if !errors.Is(err, stdctx.Canceled) {
		t.Fatalf("err=%v, want Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancel after 150ms took %v to end the dial", d)
	}
}

// The mapping behind those errors, case by case: the socket's deadline (the
// context's own) can fire a moment before the context's timer, leaving an
// i/o timeout while ctx.Err() is still nil; the dial must still report
// DeadlineExceeded. Not reproducible on demand end to end, hence this.
func TestHandshakeCtxErr(t *testing.T) {
	live := stdctx.Background()
	cancelled, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()
	refused := errors.New("connection refused")
	timeout := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	for _, tc := range []struct {
		name string
		ctx  stdctx.Context
		err  error
		want error
	}{
		{"socket timeout before the context's timer", live, timeout, stdctx.DeadlineExceeded},
		{"cancelled context", cancelled, timeout, stdctx.Canceled},
		{"other error", live, refused, refused},
	} {
		if got := handshakeCtxErr(tc.ctx, tc.err); !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A response head that never ends is cut off at 1 MB, not buffered whole.
func TestDialWebSocketResponseHeadCapped(t *testing.T) {
	addr := rawDialServer(t, func(c net.Conn, _ *bufio.Reader, _ *http.Request) {
		_, _ = c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n"))
		line := []byte("X-Pad: " + strings.Repeat("a", 1000) + "\r\n")
		chunk := bytes.Repeat(line, 64)
		for i := 0; i < 1000; i++ { // up to 64 MB; the client stops at 1 MB
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	})
	_, _, err := DialWebSocket(stdctx.Background(), "ws://"+addr+"/")
	if !errors.Is(err, ErrWebSocketBadHandshake) || !strings.Contains(err.Error(), "over") {
		t.Fatalf("err=%v, want the head cap", err)
	}
}

// Subprotocol negotiation against an rweb route: the client's preference
// order is offered and the server's pick is ws.Subprotocol() on both ends.
func TestDialWebSocketSubprotocol(t *testing.T) {
	serverSaw := make(chan string, 1)
	s := NewServer(ServerOptions{Logger: &captureLogger{}})
	s.WebSocketWithOptions("/ws", WSOptions{Subprotocols: []string{"chat.v1", "chat.v2"}}, func(ws *WSConn) error {
		serverSaw <- ws.Subprotocol()
		_, err := ws.ReadMessage()
		return err
	})
	addr, _ := serveShutdownTest(t, s, transports[0])
	ws, resp := mustDial(t, "ws://"+addr+"/ws", WSDialOptions{Subprotocols: []string{"chat.v2", "chat.v1"}})
	if ws.Subprotocol() != "chat.v2" || resp.Header.Get("Sec-WebSocket-Protocol") != "chat.v2" {
		t.Fatalf("client subprotocol %q, header %q, want chat.v2", ws.Subprotocol(), resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	if got := recv(t, serverSaw, "server subprotocol"); got != "chat.v2" {
		t.Fatalf("server subprotocol %q", got)
	}
}

// readCountConn counts the bytes read from the socket, to see compression
// on the wire.
type readCountConn struct {
	net.Conn
	read atomic.Int64
}

func (c *readCountConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Add(int64(n))
	return n, err
}

// permessage-deflate with an rweb server: the server keeps its window
// across messages (context takeover, so the client inflates with the last
// 32 KB as dictionary) and imposes client_no_context_takeover. Many similar
// JSON messages and one large one echo intact, and what crosses the wire is
// a fraction of the payload. Offered to a server that does not compress,
// the connection simply runs uncompressed.
func TestDialWebSocketCompression(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr, _ := serveShutdownTest(t, echoServer(WSOptions{Compression: true}, nil), tr)
			var counted *readCountConn
			o := dialOpts(tr)
			o.Compression = true
			o.NetDialContext = func(ctx stdctx.Context, network, a string) (net.Conn, error) {
				var d net.Dialer
				c, err := d.DialContext(ctx, network, a)
				if err != nil {
					return nil, err
				}
				counted = &readCountConn{Conn: c}
				return counted, nil
			}
			ws, resp := mustDial(t, dialURL(addr, tr, "/ws"), o)
			if ws.deflate == nil || !ws.deflate.writeNoContext || ws.deflate.readNoContext {
				t.Fatalf("deflate %+v from %q, want server context takeover and client_no_context_takeover",
					ws.deflate, resp.Header.Get("Sec-WebSocket-Extensions"))
			}

			before := counted.read.Load()
			sent := 0
			for i := 0; i < 40; i++ {
				// About 300 bytes, the same shape each time: what context
				// takeover is for. Long enough that TLS record overhead
				// (about 22 bytes a message) does not decide the ratio.
				m := fmt.Sprintf(`{"type":"update","seq":%d,"user":{"id":42,"name":"Ada Lovelace","email":"ada@example.com","roles":["admin","editor"]},`+
					`"items":[{"id":1,"label":"alpha","done":false},{"id":2,"label":"beta","done":true},{"id":3,"label":"gamma","done":false}],"ok":true}`, i)
				sent += len(m)
				if err := ws.WriteMessage(TextMessage, []byte(m)); err != nil {
					t.Fatal(err)
				}
				readText(t, ws, m)
			}
			if got := counted.read.Load() - before; got*3 > int64(sent) {
				t.Errorf("read %d bytes off the wire for %d bytes of messages; want under a third", got, sent)
			} else {
				t.Logf("%d bytes of messages arrived as %d bytes on the wire", sent, got)
			}
			big := strings.Repeat("compressible text, ", 20000) // 380 KB
			if err := ws.WriteMessage(TextMessage, []byte(big)); err != nil {
				t.Fatal(err)
			}
			readText(t, ws, big)
		})
	}

	t.Run("declined", func(t *testing.T) {
		addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, nil), transports[0])
		ws, _ := mustDial(t, "ws://"+addr+"/ws", WSDialOptions{Compression: true})
		if ws.deflate != nil {
			t.Fatal("deflate state without the server's acceptance")
		}
		_ = ws.WriteMessage(TextMessage, []byte("plain"))
		readText(t, ws, "plain")
	})
}

// With pings on, a server that goes silent (no pong, no data) makes
// ReadMessage fail after about PingInterval + PongTimeout, and a live rweb
// server keeps the connection open well past that window.
func TestDialWebSocketLiveness(t *testing.T) {
	addr := rawDialServer(t, func(c net.Conn, _ *bufio.Reader, req *http.Request) {
		_, _ = c.Write([]byte(upgradeHead(req, "")))
		time.Sleep(3 * time.Second) // never reads, never answers a ping
	})
	ws, _ := mustDial(t, "ws://"+addr+"/", WSDialOptions{PingInterval: 100 * time.Millisecond, PongTimeout: 100 * time.Millisecond})
	start := time.Now()
	_, err := ws.ReadMessage()
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err=%v, want a read timeout", err)
	}
	if d := time.Since(start); d < 150*time.Millisecond || d > time.Second {
		t.Fatalf("silent server noticed after %v, want about 200ms", d)
	}
	// The peer is gone as far as this client knows: drop the socket rather
	// than have the cleanup's Close wait out a close handshake it never answers.
	_ = ws.conn.Close()

	live, _ := serveShutdownTest(t, echoServer(WSOptions{PingInterval: -1}, nil), transports[0])
	ws2, _ := mustDial(t, "ws://"+live+"/ws", WSDialOptions{PingInterval: 50 * time.Millisecond, PongTimeout: 50 * time.Millisecond})
	got := make(chan error, 1)
	go func() {
		_, err := ws2.ReadMessage() // only pongs arrive: handled inside, no message
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("ReadMessage on a live connection ended: %v", err)
	case <-time.After(600 * time.Millisecond): // six read windows
	}
	_ = ws2.WriteMessage(TextMessage, []byte("still here"))
	if err := recv(t, got, "the echo"); err != nil {
		t.Fatal(err)
	}
}

// A server that breaks the framing rules fails the read with an error, not
// a panic: a masked frame (RFC 6455 §5.1) and a 64-bit length with its top
// bit set (which made the draft client's make() panic and kill the process).
func TestDialWebSocketRefusesBadServerFrames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame func(t *testing.T) []byte
		want  error
	}{
		{"masked", func(t *testing.T) []byte {
			var b bytes.Buffer
			_ = writeRawFrame(&b, wsText, true, true, []byte("masked"))
			return b.Bytes()
		}, ErrWebSocketBadMask},
		{"negative length", func(*testing.T) []byte {
			return []byte{0x81, 127, 0x80, 0, 0, 0, 0, 0, 0, 1}
		}, ErrWebSocketPayloadTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := rawDialServer(t, func(c net.Conn, br *bufio.Reader, req *http.Request) {
				_, _ = c.Write(append([]byte(upgradeHead(req, "")), tc.frame(t)...))
				rawSession(c, br)
			})
			ws, _ := mustDial(t, "ws://"+addr+"/", WSDialOptions{})
			if _, err := ws.ReadMessage(); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
}

// An IPv6 literal URL dials (the draft client built "::1:port").
func TestDialWebSocketIPv6(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	s := echoServer(WSOptions{}, nil)
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Shutdown(cancelledContext()) })
	ws, _ := mustDial(t, "ws://"+ln.Addr().String()+"/ws", WSDialOptions{})
	_ = ws.WriteMessage(TextMessage, []byte("v6"))
	readText(t, ws, "v6")
}

// Writers on several goroutines, the reader on its own, and a Close from a
// third while ReadMessage is blocked: every message arrives whole (frames
// never interleave), and the blocked read returns with the server's
// answering close frame (run under -race).
func TestDialWebSocketConcurrentUse(t *testing.T) {
	addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, nil), transports[0])
	ws, _ := mustDial(t, "ws://"+addr+"/ws", WSDialOptions{})

	const writers, each = 4, 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := ws.WriteMessage(TextMessage, []byte(fmt.Sprintf("w%d-%d-%s", w, i, strings.Repeat("x", 100*i)))); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for n := 0; n < writers*each; n++ {
		msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("message %d: %v", n, err)
		}
		// "w<writer>-<i>-" then 100*i x's: a message spliced with another
		// writer's frame would fail to parse or have the wrong tail.
		parts := strings.SplitN(string(msg.Data), "-", 3)
		i, err := strconv.Atoi(parts[min(1, len(parts)-1)])
		if len(parts) != 3 || err != nil || parts[2] != strings.Repeat("x", 100*i) {
			t.Fatalf("garbled message %q", msg.Data[:min(len(msg.Data), 40)])
		}
	}
	wg.Wait()

	got := make(chan *WSMessage, 1)
	go func() {
		msg, _ := ws.ReadMessage()
		got <- msg
	}()
	time.Sleep(settle) // let it block
	if err := ws.Close(1000, ""); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if msg := recv(t, got, "the blocked read"); msg == nil || msg.Type != CloseMessage {
		t.Fatalf("blocked read returned %+v, want the server's CloseMessage", msg)
	}
}
