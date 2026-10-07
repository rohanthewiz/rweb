package rweb

// Tests for Request().Context() and the disconnect watch behind it
// (conn_reader.go). Everything runs over real loopback connections, plain and
// TLS, because the behaviour under test is about sockets: a close seen by a
// parked read, a read deadline set and cleared, a byte read ahead and put
// back. The long-lived connection types (WebSocket, SSE) are covered here too,
// since both take over the conn from the HTTP loop and so meet the watch.
//
// These are internal tests (package rweb) so they can serve through
// handleConnection on their own listener — the per-connection path Run uses,
// without Run's SIGTERM-based shutdown — and build raw WebSocket frames with
// writeRawFrame.

import (
	"bufio"
	"bytes"
	stdctx "context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	// waitFor bounds anything that should happen promptly (a cancel after a
	// close, an echo); generous so a loaded CI machine does not flake.
	waitFor = 2 * time.Second
	// settle gives the watch goroutine time to reach (or return from) its
	// conn.Read before the test asserts on what that read did.
	settle = 100 * time.Millisecond
)

// transport is a connection flavour every socket-level test runs over.
// TLS matters separately: the watch is stopped with a read deadline, and the
// conn must stay usable afterwards, which for tls.Conn depends on crypto/tls
// treating that deadline error as temporary.
type transport struct {
	name string
	tls  bool
}

var transports = []transport{{"tcp", false}, {"tls", true}}

// serveTest serves s on a loopback listener through handleConnection and
// returns the address. The listener closes when the test ends.
func serveTest(t *testing.T, s *Server, tr transport) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tr.tls {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{testCert(t)}})
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConnection(c)
		}
	}()
	return ln.Addr().String()
}

func dialTest(t *testing.T, addr string, tr transport) net.Conn {
	t.Helper()
	var c net.Conn
	var err error
	if tr.tls {
		c, err = tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	} else {
		c, err = net.Dial("tcp", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	// A safety net: no test should block on a read for this long.
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c
}

// testCert is a throwaway self-signed certificate for the TLS transport.
func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "rweb-request-context-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func getReq(path string) string {
	return "GET " + path + " HTTP/1.1\r\nHost: test\r\n\r\n"
}

func writeAll(t *testing.T, c net.Conn, s string) {
	t.Helper()
	if _, err := io.WriteString(c, s); err != nil {
		t.Fatal(err)
	}
}

// readBody reads one response off br and returns its status and body.
func readBody(t *testing.T, br *bufio.Reader) (int, string) {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// recv waits for one value from ch, failing the test after waitFor.
func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitFor):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// waitDone fails the test unless ctx is cancelled within waitFor.
func waitDone(t *testing.T, ctx stdctx.Context, what string) {
	t.Helper()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), stdctx.Canceled) {
			t.Fatalf("%s: context ended with %v, want context.Canceled", what, ctx.Err())
		}
	case <-time.After(waitFor):
		t.Fatalf("%s: context not cancelled within %v", what, waitFor)
	}
}

// The headline behaviour: a handler blocked on its request context learns
// promptly that the client closed the connection.
func TestRequestContextCancelledOnClientClose(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			entered := make(chan struct{})
			result := make(chan error, 1)
			s.Get("/slow", func(ctx Context) error {
				rc := ctx.Request().Context()
				close(entered)
				select {
				case <-rc.Done():
					result <- rc.Err()
				case <-time.After(5 * time.Second):
					result <- errors.New("not cancelled")
				}
				return nil
			})

			c := dialTest(t, serveTest(t, s, tr), tr)
			writeAll(t, c, getReq("/slow"))
			recv(t, entered, "handler to start")

			start := time.Now()
			_ = c.Close()
			err := recv(t, result, "handler result")
			if !errors.Is(err, stdctx.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}
			t.Logf("cancelled %v after close", time.Since(start))
		})
	}
}

// A context the handler kept is live while the handler runs and cancelled
// once the request has been answered — the client staying connected must not
// leave it running forever.
func TestRequestContextCancelledWhenRequestEnds(t *testing.T) {
	s := NewServer()
	kept := make(chan stdctx.Context, 1)
	s.Get("/", func(ctx Context) error {
		rc := ctx.Request().Context()
		kept <- rc
		return ctx.WriteString(fmt.Sprint(rc.Err()))
	})

	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	writeAll(t, c, getReq("/"))
	if _, body := readBody(t, bufio.NewReader(c)); body != "<nil>" {
		t.Fatalf("context during handler: %s, want <nil>", body)
	}
	waitDone(t, recv(t, kept, "kept context"), "after response")
}

// A synthetic Server.Request has no connection to watch, but the context
// still works and still ends with the request.
func TestRequestContextSynthetic(t *testing.T) {
	s := NewServer()
	var kept stdctx.Context
	s.Get("/", func(ctx Context) error {
		kept = ctx.Request().Context()
		return ctx.WriteString(fmt.Sprint(kept.Err()))
	})
	r := s.Request("GET", "/", nil, nil)
	if string(r.Body()) != "<nil>" {
		t.Fatalf("context during handler: %s, want <nil>", r.Body())
	}
	if !errors.Is(kept.Err(), stdctx.Canceled) {
		t.Fatalf("after Request: %v, want context.Canceled", kept.Err())
	}
}

// One keep-alive connection serving a mix of requests: ones that start the
// watch, one that never asks for a context, and one that starts the watch and
// then panics. Each must leave the conn clean for the next — watch stopped,
// read deadline cleared — or a later request fails to read.
func TestRequestContextKeepAlive(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Get("/ctx", func(ctx Context) error {
				return ctx.WriteString("ctx " + fmt.Sprint(ctx.Request().Context().Err()))
			})
			s.Get("/plain", func(ctx Context) error {
				return ctx.WriteString("plain")
			})
			s.Get("/panic", func(ctx Context) error {
				_ = ctx.Request().Context()
				panic("handler panic with a live watch")
			})

			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)
			steps := []struct {
				path   string
				status int
				body   string // prefix
			}{
				{"/ctx", 200, "ctx <nil>"},
				{"/plain", 200, "plain"},
				{"/ctx", 200, "ctx <nil>"},
				{"/panic", 500, "<h3>500"},
				{"/ctx", 200, "ctx <nil>"},
			}
			for i, st := range steps {
				writeAll(t, c, getReq(st.path))
				status, body := readBody(t, br)
				if status != st.status || !strings.HasPrefix(body, st.body) {
					t.Fatalf("step %d %s: %d %q, want %d %q…", i, st.path, status, body, st.status, st.body)
				}
			}
		})
	}
}

// A client that sends its next request while the current handler is still
// running (pipelining) is not a disconnect: the watch reads the first byte of
// that request, must not cancel, and must hand the byte back so the second
// request parses intact. Without the hand-back the server would see
// "ET /fast…" and refuse the method.
func TestRequestContextPipelinedByteIsKept(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			entered := make(chan struct{})
			proceed := make(chan struct{})
			s.Get("/slow", func(ctx Context) error {
				rc := ctx.Request().Context()
				close(entered)
				<-proceed
				return ctx.WriteString("slow " + fmt.Sprint(rc.Err()))
			})
			s.Get("/fast", func(ctx Context) error {
				return ctx.WriteString("fast " + ctx.Request().QueryParam("x"))
			})

			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)
			writeAll(t, c, getReq("/slow"))
			recv(t, entered, "slow handler to start")

			writeAll(t, c, getReq("/fast?x=1")) // arrives while the watch is parked
			time.Sleep(settle)                  // let the watch read its byte
			close(proceed)

			if _, body := readBody(t, br); body != "slow <nil>" {
				t.Fatalf("pipelined request cancelled the first: %q", body)
			}
			if status, body := readBody(t, br); status != 200 || body != "fast 1" {
				t.Fatalf("second request: %d %q, want 200 \"fast 1\"", status, body)
			}
		})
	}
}

// The other pipelining shape: both requests in one write, so the second is
// already buffered when the first handler asks for its context. No watch is
// started (it could only read past the queued request); both still succeed.
func TestRequestContextPipelinedInOneWrite(t *testing.T) {
	s := NewServer()
	s.Get("/a", func(ctx Context) error {
		return ctx.WriteString("a " + fmt.Sprint(ctx.Request().Context().Err()))
	})
	s.Get("/b", func(ctx Context) error { return ctx.WriteString("b") })

	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	br := bufio.NewReader(c)
	writeAll(t, c, getReq("/a")+getReq("/b"))
	if _, body := readBody(t, br); body != "a <nil>" {
		t.Fatalf("first: %q", body)
	}
	if _, body := readBody(t, br); body != "b" {
		t.Fatalf("second: %q", body)
	}
}

// A pipelining client that half-closes after its last request is still
// reading responses. No watch runs while a request is queued, so the
// half-close cannot be taken for a disconnect and cancel the first request.
func TestRequestContextPipelinedThenHalfClose(t *testing.T) {
	s := NewServer()
	s.Get("/a", func(ctx Context) error {
		rc := ctx.Request().Context()
		time.Sleep(settle) // the half-close lands while this runs
		return ctx.WriteString("a " + fmt.Sprint(rc.Err()))
	})
	s.Get("/b", func(ctx Context) error { return ctx.WriteString("b") })

	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	br := bufio.NewReader(c)
	writeAll(t, c, getReq("/a")+getReq("/b"))
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, body := readBody(t, br); body != "a <nil>" {
		t.Fatalf("first request after half-close: %q, want \"a <nil>\"", body)
	}
	if _, body := readBody(t, br); body != "b" {
		t.Fatalf("second: %q", body)
	}
}

// GetConn hands the conn to the caller, so the watch stops: a close after it
// does not cancel the context mid-handler (the caller owns reads now), but
// the request ending still does.
func TestRequestContextGetConnStopsWatch(t *testing.T) {
	s := NewServer()
	taken := make(chan struct{})
	duringHandler := make(chan error, 1)
	kept := make(chan stdctx.Context, 1)
	s.Get("/", func(ctx Context) error {
		rc := ctx.Request().Context()
		kept <- rc
		_ = ctx.GetConn()
		close(taken)
		time.Sleep(3 * settle) // the client closes during this
		duringHandler <- rc.Err()
		return nil
	})

	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	writeAll(t, c, getReq("/"))
	recv(t, taken, "GetConn")
	_ = c.Close()
	if err := recv(t, duringHandler, "handler result"); err != nil {
		t.Fatalf("context cancelled after GetConn: %v", err)
	}
	waitDone(t, recv(t, kept, "kept context"), "after request")
}

// --- WebSocket ---------------------------------------------------------------

// wsServer is an echo WebSocket server whose middleware asks for the request
// context before the upgrade — so the watch is running when UpgradeWebSocket
// takes the conn over — and reports it on ctxs. If hold is non-nil the
// middleware waits on it after starting the watch.
func wsServer(ctxs chan<- stdctx.Context, hold <-chan struct{}) *Server {
	s := NewServer()
	s.Use(func(ctx Context) error {
		if ctx.Request().Path() == "/ws" {
			ctxs <- ctx.Request().Context()
			if hold != nil {
				<-hold
			}
		}
		return ctx.Next()
	})
	s.WebSocket("/ws", func(ws *WSConn) error {
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				return nil // peer gone; ending the handler ends the request
			}
			if err := ws.WriteMessage(msg.Type, msg.Data); err != nil {
				return nil
			}
		}
	})
	return s
}

const wsUpgradeReq = "GET /ws HTTP/1.1\r\nHost: test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
	"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"

// clientFrame is a masked client text frame, as a browser would send it.
func clientFrame(t *testing.T, text string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeRawFrame(&b, wsText, true, true, []byte(text)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// wsClient reads the 101 off br and returns a client WSConn reading through
// br, which may already hold the first echoed frame.
func wsClient(t *testing.T, c net.Conn, br *bufio.Reader) *WSConn {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: status %d", resp.StatusCode)
	}
	ws := NewWSConn(c, false)
	ws.r = br
	return ws
}

func expectEcho(t *testing.T, ws *WSConn, want string) {
	t.Helper()
	msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("reading echo of %q: %v", want, err)
	}
	if string(msg.Data) != want {
		t.Fatalf("echo %q, want %q", msg.Data, want)
	}
}

// A WebSocket session on a request whose watch was running at upgrade time:
// frames flow both ways, the context stays live for the whole session (the
// watch was stopped, so nothing mistakes the session for a disconnect), and
// it is cancelled once the peer leaves and the WebSocket handler returns.
func TestRequestContextWebSocketSession(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			ctxs := make(chan stdctx.Context, 1)
			c := dialTest(t, serveTest(t, wsServer(ctxs, nil), tr), tr)
			br := bufio.NewReader(c)
			writeAll(t, c, wsUpgradeReq)
			ws := wsClient(t, c, br)
			rc := recv(t, ctxs, "request context")

			for _, m := range []string{"one", "two", "three"} {
				if err := ws.WriteMessage(TextMessage, []byte(m)); err != nil {
					t.Fatal(err)
				}
				expectEcho(t, ws, m)
			}
			time.Sleep(settle)
			if rc.Err() != nil {
				t.Fatalf("context ended during a live WebSocket session: %v", rc.Err())
			}

			_ = c.Close()
			waitDone(t, rc, "after the WebSocket peer left")
		})
	}
}

// A frame sent in the same write as the upgrade request is already in the
// HTTP reader's buffer at upgrade time; it must reach the WebSocket.
func TestRequestContextWebSocketFrameWithUpgrade(t *testing.T) {
	ctxs := make(chan stdctx.Context, 1)
	tr := transports[0]
	c := dialTest(t, serveTest(t, wsServer(ctxs, nil), tr), tr)
	br := bufio.NewReader(c)
	writeAll(t, c, wsUpgradeReq+string(clientFrame(t, "early")))
	expectEcho(t, wsClient(t, c, br), "early")
}

// A frame that arrives while the watch is parked: the watch reads its first
// byte, which is not a disconnect, and that byte must go to the WebSocket at
// upgrade rather than be dropped.
func TestRequestContextWebSocketFrameDuringWatch(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			ctxs := make(chan stdctx.Context, 1)
			hold := make(chan struct{})
			c := dialTest(t, serveTest(t, wsServer(ctxs, hold), tr), tr)
			br := bufio.NewReader(c)
			writeAll(t, c, wsUpgradeReq)
			rc := recv(t, ctxs, "request context") // the watch is running now

			writeAll(t, c, string(clientFrame(t, "early")))
			time.Sleep(settle) // the watch takes the frame's first byte
			if rc.Err() != nil {
				t.Fatalf("a frame byte was taken for a disconnect: %v", rc.Err())
			}
			close(hold)
			expectEcho(t, wsClient(t, c, br), "early")
		})
	}
}

// --- Server-Sent Events ------------------------------------------------------

// sseServer streams "tick N" events from a producer goroutine that runs until
// the request context ends — the pattern Request().Context() enables for SSE.
// It sends at most limit events (0 = unlimited) and then closes the channel,
// which ends the stream from the server side. producerDone closes when the
// producer exits.
func sseServer(limit int, producerDone chan<- struct{}) *Server {
	s := NewServer()
	s.Get("/events", func(ctx Context) error {
		rc := ctx.Request().Context()
		ch := make(chan any)
		go func() {
			defer close(producerDone)
			for i := 0; limit == 0 || i < limit; i++ {
				select {
				case <-rc.Done():
					return
				case ch <- fmt.Sprintf("tick %d", i):
				}
				time.Sleep(10 * time.Millisecond)
			}
			close(ch)
		}()
		return ctx.SetSSE(ch, "tick")
	})
	return s
}

// readEvents reads the SSE response head and then n "data:" lines.
func readEvents(t *testing.T, br *bufio.Reader, n int) {
	t.Helper()
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, " 200 ") {
		t.Fatalf("status line %q, err %v", status, err)
	}
	for seen := 0; seen < n; {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("after %d events: %v", seen, err)
		}
		if strings.HasPrefix(line, "data: tick") {
			seen++
		}
	}
}

// The request context lives through the event stream (the producer keeps
// producing after the handler has returned) and is cancelled as soon as the
// client disconnects, which is what stops the producer.
func TestRequestContextSSEClientLeaves(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			producerDone := make(chan struct{})
			c := dialTest(t, serveTest(t, sseServer(0, producerDone), tr), tr)
			writeAll(t, c, "GET /events HTTP/1.1\r\nHost: test\r\nAccept: text/event-stream\r\n\r\n")
			readEvents(t, bufio.NewReader(c), 5)

			_ = c.Close()
			select {
			case <-producerDone:
			case <-time.After(waitFor):
				t.Fatal("SSE producer still running after the client left")
			}
		})
	}
}

// When the server ends the stream (channel closed), the connection is closed:
// an event stream has no length, so its end is the connection's end.
func TestRequestContextSSEServerEnds(t *testing.T) {
	producerDone := make(chan struct{})
	tr := transports[0]
	c := dialTest(t, serveTest(t, sseServer(3, producerDone), tr), tr)
	writeAll(t, c, "GET /events HTTP/1.1\r\nHost: test\r\nAccept: text/event-stream\r\n\r\n")
	br := bufio.NewReader(c)
	readEvents(t, br, 3)

	_ = c.SetReadDeadline(time.Now().Add(waitFor))
	if _, err := io.ReadAll(br); err != nil {
		t.Fatalf("stream did not end with the connection closing: %v", err)
	}
	recv(t, producerDone, "producer exit")
}
