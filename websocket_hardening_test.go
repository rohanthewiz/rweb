package rweb

// WebSocket hardening (N-040): concurrent close, control-frame limits, write
// and read deadlines, server pings, subprotocol selection and the logging of
// a WebSocket handler's error. Internal tests, so they can build connections
// with newTestPair and speak raw frames with writeRawFrame.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Close and WriteMessage from different goroutines — a hub writing to a
// client while the client's handler closes it — must not race on the closed
// flag (run with -race), and no data frame may follow the close frame on the
// wire: once the close frame is out, the connection is closed for writing.
func TestWebSocketConcurrentCloseAndWrite(t *testing.T) {
	for i := 0; i < 20; i++ {
		server, client := newTestPair()

		// The peer: drain frames, answer the close frame, and report any data
		// frame that arrives after it.
		afterClose := make(chan int, 1)
		go func() {
			seenClose, late := false, 0
			for {
				opcode, _, _, _, err := client.readFrame()
				if err != nil {
					afterClose <- late
					return
				}
				switch {
				case opcode == wsClose && !seenClose:
					seenClose = true
					_ = writeRawFrame(client.conn, wsClose, true, true, []byte{0x03, 0xE8})
				case seenClose:
					late++
				}
			}
		}()

		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					if err := server.WriteMessage(TextMessage, []byte("tick")); err != nil {
						return
					}
				}
			}()
		}
		time.Sleep(time.Millisecond)
		if err := server.Close(wsCloseNormalClosure, "bye"); err != nil {
			t.Fatalf("Close: %v", err)
		}
		wg.Wait()
		_ = client.conn.Close()
		if late := <-afterClose; late != 0 {
			t.Fatalf("round %d: %d data frame(s) after the close frame", i, late)
		}
	}
}

// A second Close while the first waits for the peer's close frame returns at
// once: the connection is already closed for writing, and waiting out the
// first call's close-handshake timeout behind a lock gains nothing.
func TestWebSocketSecondCloseDoesNotWait(t *testing.T) {
	server, client := newTestPair()

	// The peer reads but never answers the close frame.
	gotClose := make(chan struct{})
	go func() {
		for {
			opcode, _, _, _, err := client.readFrame()
			if err != nil {
				return
			}
			if opcode == wsClose {
				close(gotClose)
			}
		}
	}()

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_ = server.Close(wsCloseNormalClosure, "first")
	}()
	<-gotClose

	start := time.Now()
	if err := server.Close(wsCloseGoingAway, "second"); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > closeHandshakeTimeout/4 {
		t.Errorf("second Close took %v; it waited behind the first", elapsed)
	}

	// The peer leaving ends the first Close's wait.
	_ = client.conn.Close()
	<-firstDone
}

// Control frames carry at most 125 bytes and are never fragmented (RFC 6455
// §5.5). A peer that sends one that is longer, or without FIN, is failed with
// close code 1002, before its payload is read: an "oversized ping" is
// otherwise a way to make the server buffer and echo up to the message limit.
func TestWebSocketBadControlFrames(t *testing.T) {
	cases := []struct {
		name   string
		opcode int
		fin    bool
		size   int
	}{
		{"ping over 125 bytes", wsPing, true, 126},
		{"pong over 125 bytes", wsPong, true, 1000},
		{"close over 125 bytes", wsClose, true, 200},
		{"fragmented ping", wsPing, false, 4},
		{"fragmented close", wsClose, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer client.conn.Close()

			// The peer sends the bad frame, then reports the close code of the
			// first close frame it gets back. The frame header goes first and
			// the payload only if the server is still reading: a server that
			// fails the frame on its header never reads the payload, and
			// net.Pipe would block that write forever.
			code := make(chan int, 1)
			go func() {
				payload := make([]byte, tc.size)
				if tc.opcode == wsClose {
					payload[0], payload[1] = 0x03, 0xE8
				}
				go func() { _ = writeRawFrame(client.conn, tc.opcode, tc.fin, true, payload) }()
				for {
					opcode, _, _, data, err := client.readFrame()
					if err != nil {
						code <- -1
						return
					}
					if opcode == wsClose && len(data) >= 2 {
						code <- int(binary.BigEndian.Uint16(data))
						return
					}
				}
			}()

			// Bounded, so a server that accepts the frame and waits for the
			// next one fails the test instead of hanging it.
			_ = server.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err := server.ReadMessage()
			if !errors.Is(err, ErrWebSocketBadControlFrame) {
				t.Fatalf("ReadMessage error = %v, want ErrWebSocketBadControlFrame", err)
			}
			select {
			case c := <-code:
				if c != wsCloseProtocolError {
					t.Fatalf("close code %d, want %d", c, wsCloseProtocolError)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no close frame sent for the bad control frame")
			}
			select {
			case <-server.Done():
			default:
				t.Fatal("Done() not closed after failing the connection")
			}
		})
	}
}

// A ping of exactly 125 bytes is legal and is answered with a pong carrying
// the same payload.
func TestWebSocketMaxSizePingIsAnswered(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()

	payload := bytes.Repeat([]byte("p"), 125)
	go func() {
		_ = writeRawFrame(client.conn, wsPing, true, true, payload)
		_ = writeRawFrame(client.conn, wsText, true, true, []byte("after"))
	}()
	pong := make(chan []byte, 1)
	go func() {
		opcode, _, _, data, err := client.readFrame()
		if err == nil && opcode == wsPong {
			pong <- data
		}
		close(pong)
	}()

	msg, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(msg.Data) != "after" {
		t.Fatalf("message %q, want %q", msg.Data, "after")
	}
	if got := <-pong; !bytes.Equal(got, payload) {
		t.Fatalf("pong payload %d bytes, want the 125-byte ping payload", len(got))
	}
}

// --- write deadlines ---------------------------------------------------------

// drain reads and discards everything the peer side receives, until it is
// closed.
func drain(c net.Conn) {
	buf := make([]byte, 4096)
	for {
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
}

// upgradeOn dials s, completes the opening handshake on /ws (with extra
// request headers, if any) and returns the conn and a reader positioned
// after the 101.
func upgradeOn(t *testing.T, s *Server, extraHeaders string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, br, _ := upgradeResp(t, s, extraHeaders)
	return c, br
}

// upgradeResp is upgradeOn that also returns the 101 response, for its
// headers.
func upgradeResp(t *testing.T, s *Server, extraHeaders string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	t.Cleanup(func() { _ = c.Close() })
	writeAll(t, c, "GET /ws HTTP/1.1\r\nHost: test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"+extraHeaders+"\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: status %d, want 101", resp.StatusCode)
	}
	return c, br, resp
}

// A peer that stops reading must not block a writer forever: with
// WSOptions.WriteTimeout, the write that finds the socket buffers full fails
// with a timeout once the timeout passes.
func TestWebSocketWriteTimeoutOption(t *testing.T) {
	s := NewServer()
	errc := make(chan error, 1)
	s.WebSocketWithOptions("/ws", WSOptions{WriteTimeout: 50 * time.Millisecond}, func(ws *WSConn) error {
		msg := make([]byte, 64<<10)
		for {
			if err := ws.WriteMessage(BinaryMessage, msg); err != nil {
				errc <- err
				return nil
			}
		}
	})
	upgradeOn(t, s, "") // and never read

	select {
	case err := <-errc:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("write error = %v, want a deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writes to a peer that does not read never timed out")
	}
}

// The route options resolve to the documented defaults: a 10 s write timeout
// unless WriteTimeout says otherwise, none when it is negative.
func TestWebSocketWriteTimeoutDefaults(t *testing.T) {
	cases := []struct {
		name string
		opts *WSOptions // nil: s.WebSocket
		want time.Duration
	}{
		{"WebSocket", nil, defaultWriteTimeout},
		{"zero options", &WSOptions{}, defaultWriteTimeout},
		{"explicit", &WSOptions{WriteTimeout: time.Second}, time.Second},
		{"negative disables", &WSOptions{WriteTimeout: -1}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			got := make(chan time.Duration, 1)
			h := func(ws *WSConn) error {
				got <- ws.writeTimeout
				return nil
			}
			if tc.opts == nil {
				s.WebSocket("/ws", h)
			} else {
				s.WebSocketWithOptions("/ws", *tc.opts, h)
			}
			upgradeOn(t, s, "")
			if d := recv(t, got, "handler"); d != tc.want {
				t.Fatalf("write timeout %v, want %v", d, tc.want)
			}
		})
	}
}

// SetWriteDeadline behaves as on a net.Conn: a deadline already in the past
// fails the next write (it used to be skipped silently), and the zero time
// clears it.
func TestWebSocketSetWriteDeadlineExpired(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()
	go drain(client.conn)

	if err := server.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := server.WriteMessage(TextMessage, []byte("late")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write past the deadline: err = %v, want a deadline error", err)
	}
	if err := server.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := server.WriteMessage(TextMessage, []byte("on time")); err != nil {
		t.Fatalf("write after clearing the deadline: %v", err)
	}
}

// As on a net.Conn, a deadline set while a write is blocked applies to that
// write.
func TestWebSocketSetWriteDeadlineUnblocksWrite(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()

	errc := make(chan error, 1)
	go func() { errc <- server.WriteMessage(TextMessage, []byte("nobody reads this")) }()
	time.Sleep(20 * time.Millisecond) // the write is now blocked on the pipe
	_ = server.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))

	select {
	case err := <-errc:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("blocked write: err = %v, want a deadline error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetWriteDeadline did not reach a write already blocked")
	}
}

// partialConn accepts half of the first write and then fails it, as a
// socket does when a write deadline expires mid-frame.
type partialConn struct {
	net.Conn
	writes int
}

func (p *partialConn) Write(b []byte) (int, error) {
	p.writes++
	if p.writes == 1 {
		return len(b) / 2, os.ErrDeadlineExceeded
	}
	return len(b), nil
}

// After a write that put part of a frame on the wire, the stream is out of
// step with the peer's parser: every later write fails with the same error
// rather than sending frames the peer would misread.
func TestWebSocketPartialWriteIsSticky(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()
	pc := &partialConn{Conn: server.conn}
	server.conn = pc

	if err := server.WriteMessage(TextMessage, []byte("half of me")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("first write: err = %v, want the deadline error", err)
	}
	if err := server.WriteMessage(TextMessage, []byte("next")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write after a partial frame: err = %v, want the same error", err)
	}
	if pc.writes != 1 {
		t.Fatalf("%d writes reached the socket, want 1", pc.writes)
	}
}

// --- server pings and the read deadline --------------------------------------

// fastPings keeps the ping tests quick: a peer has 100 ms of silence before
// it is considered gone.
var fastPings = WSOptions{PingInterval: 50 * time.Millisecond, PongTimeout: 50 * time.Millisecond}

// readResult is what a test handler's ReadMessage loop ended with.
type readResult struct {
	err     error
	elapsed time.Duration
	ws      *WSConn
}

// readUntilError registers a WebSocket route whose handler reads (echoing
// text) until ReadMessage fails, then reports how and when.
func readUntilError(s *Server, opts WSOptions, before func(ws *WSConn)) <-chan readResult {
	res := make(chan readResult, 1)
	s.WebSocketWithOptions("/ws", opts, func(ws *WSConn) error {
		start := time.Now()
		if before != nil {
			before(ws)
		}
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				res <- readResult{err, time.Since(start), ws}
				return nil
			}
			if err := ws.WriteMessage(msg.Type, msg.Data); err != nil {
				res <- readResult{err, time.Since(start), ws}
				return nil
			}
		}
	})
	return res
}

// A peer that vanishes without closing (cable pulled, laptop asleep) sends
// nothing and answers no pings. ReadMessage must not block forever: once
// the ping interval plus the pong timeout pass in silence, it fails with a
// timeout, the handler returns, and Done() closes, which is what stops the
// ping goroutine.
func TestWebSocketDeadPeerIsDropped(t *testing.T) {
	s := NewServer()
	res := readUntilError(s, fastPings, nil)
	upgradeOn(t, s, "") // then silence: no frames, no pongs

	r := recv(t, res, "ReadMessage to fail for a silent peer")
	if !errors.Is(r.err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadMessage error = %v, want a deadline error", r.err)
	}
	if r.elapsed > time.Second {
		t.Fatalf("dead peer detected after %v, want about 100ms", r.elapsed)
	}
	select {
	case <-r.ws.Done():
	case <-time.After(waitFor):
		t.Fatal("Done() not closed after the WebSocket handler returned")
	}
}

// A peer that is idle but alive answers the server's pings (every client
// does, automatically); each pong extends the read deadline, so the idle
// connection is kept, for many ping intervals, and still works afterwards.
func TestWebSocketIdlePeerAnsweringPingsIsKept(t *testing.T) {
	s := NewServer()
	res := readUntilError(s, fastPings, nil)
	c, br := upgradeOn(t, s, "")
	client := NewWSConn(c, false)
	client.r = br

	pings := make(chan struct{}, 100)
	client.SetPingHandler(func(data []byte) error {
		pings <- struct{}{}
		return client.writePong(data)
	})
	echoes := make(chan string, 1)
	go func() {
		for {
			msg, err := client.ReadMessage() // answers pings while waiting
			if err != nil {
				return
			}
			echoes <- string(msg.Data)
		}
	}()

	time.Sleep(400 * time.Millisecond) // four times the silence allowance
	select {
	case r := <-res:
		t.Fatalf("idle peer answering pings was dropped: %v", r.err)
	default:
	}
	if n := len(pings); n < 3 {
		t.Fatalf("%d pings in 400ms at a 50ms interval", n)
	}
	if err := client.WriteMessage(TextMessage, []byte("still here")); err != nil {
		t.Fatal(err)
	}
	if got := recv(t, echoes, "echo"); got != "still here" {
		t.Fatalf("echo %q", got)
	}
}

// The ping options resolve to the documented defaults, and a negative
// PingInterval turns pings and the read deadline off entirely.
func TestWebSocketPingDefaults(t *testing.T) {
	cases := []struct {
		name               string
		opts               *WSOptions // nil: s.WebSocket
		interval, deadline time.Duration
	}{
		{"WebSocket", nil, defaultPingInterval, defaultPingInterval + defaultPongTimeout},
		{"zero options", &WSOptions{}, defaultPingInterval, defaultPingInterval + defaultPongTimeout},
		{"explicit", &WSOptions{PingInterval: time.Second, PongTimeout: 2 * time.Second}, time.Second, 3 * time.Second},
		{"pings without a read deadline", &WSOptions{PongTimeout: -1}, defaultPingInterval, 0},
		{"negative disables", &WSOptions{PingInterval: -1}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			got := make(chan [2]time.Duration, 1)
			h := func(ws *WSConn) error {
				got <- [2]time.Duration{ws.pingInterval, ws.readWindow}
				return nil
			}
			if tc.opts == nil {
				s.WebSocket("/ws", h)
			} else {
				s.WebSocketWithOptions("/ws", *tc.opts, h)
			}
			upgradeOn(t, s, "")
			v := recv(t, got, "handler")
			if v[0] != tc.interval || v[1] != tc.deadline {
				t.Fatalf("ping interval %v, read window %v; want %v, %v", v[0], v[1], tc.interval, tc.deadline)
			}
		})
	}
}

// With pings disabled no ping is sent and a silent peer is not dropped.
func TestWebSocketPingsDisabled(t *testing.T) {
	s := NewServer()
	res := readUntilError(s, WSOptions{PingInterval: -1}, nil)
	c, br := upgradeOn(t, s, "")

	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if b, err := br.ReadByte(); err == nil {
		t.Fatalf("got a frame (first byte %#x) with pings disabled", b)
	}
	select {
	case r := <-res:
		t.Fatalf("silent peer dropped with pings disabled: %v", r.err)
	default:
	}
}

// An app's own read deadline still works with pings on: whichever of the
// two is earlier ends the read. An early app deadline fires on time...
func TestWebSocketAppReadDeadlineEarlierWins(t *testing.T) {
	s := NewServer()
	opts := WSOptions{PingInterval: time.Second, PongTimeout: time.Second}
	res := readUntilError(s, opts, func(ws *WSConn) {
		_ = ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	})
	upgradeOn(t, s, "")

	r := recv(t, res, "ReadMessage to hit the app's deadline")
	if !errors.Is(r.err, os.ErrDeadlineExceeded) || r.elapsed > time.Second {
		t.Fatalf("read ended after %v with %v, want a deadline error at about 100ms", r.elapsed, r.err)
	}
}

// ...and a late one does not switch off dead-peer detection.
func TestWebSocketAppReadDeadlineLaterKeepsLiveness(t *testing.T) {
	s := NewServer()
	res := readUntilError(s, fastPings, func(ws *WSConn) {
		_ = ws.SetReadDeadline(time.Now().Add(time.Hour))
	})
	upgradeOn(t, s, "")

	r := recv(t, res, "ReadMessage to fail for a silent peer")
	if !errors.Is(r.err, os.ErrDeadlineExceeded) || r.elapsed > time.Second {
		t.Fatalf("read ended after %v with %v, want a deadline error at about 100ms", r.elapsed, r.err)
	}
}

// Clearing the app deadline (zero time) hands the read back to the ping
// window, which a peer answering pings keeps extending.
func TestWebSocketAppReadDeadlineCleared(t *testing.T) {
	s := NewServer()
	res := readUntilError(s, fastPings, func(ws *WSConn) {
		_ = ws.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		_ = ws.SetReadDeadline(time.Time{})
	})
	c, br := upgradeOn(t, s, "")
	client := NewWSConn(c, false)
	client.r = br
	go func() {
		for {
			if _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	select {
	case r := <-res:
		t.Fatalf("read ended after the app cleared its deadline: %v", r.err)
	default:
	}
}

// --- the WebSocket handler's error -------------------------------------------

// syncBuffer is a bytes.Buffer safe for the log package writing from server
// goroutines while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// captureLog sends the standard logger's output to a buffer for the test.
func captureLog(t *testing.T) *syncBuffer {
	buf := &syncBuffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })
	return buf
}

// After the upgrade the connection speaks WebSocket, so the error handler's
// HTML page could never be sent; and the error a read loop most often
// returns is the peer leaving. Neither reaches the error handler, and only
// an error other than a close or EOF is logged.
func TestWebSocketHandlerErrorAfterUpgrade(t *testing.T) {
	cases := []struct {
		name    string
		peer    func(t *testing.T, c net.Conn, client *WSConn) // how the peer ends the session
		handler func(ws *WSConn) error
		logged  string // "" means nothing may be logged
	}{
		{
			name: "peer sends a close frame",
			peer: func(t *testing.T, c net.Conn, client *WSConn) {
				_ = writeRawFrame(c, wsClose, true, true, []byte{0x03, 0xE8})
				_, _ = client.ReadMessage() // the close reply
			},
			handler: readLoop,
		},
		{
			name:    "peer drops the connection",
			peer:    func(t *testing.T, c net.Conn, _ *WSConn) { _ = c.Close() },
			handler: readLoop,
		},
		{
			name: "handler's own error",
			peer: func(t *testing.T, c net.Conn, client *WSConn) {},
			handler: func(ws *WSConn) error {
				return errors.New("boom: the app failed")
			},
			logged: "boom: the app failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLog(t)
			s := NewServer()
			handlerErrs := make(chan error, 1)
			s.errorHandler = func(_ Context, err error) { handlerErrs <- err }
			returned := make(chan error, 1)
			s.WebSocket("/ws", func(ws *WSConn) error {
				err := tc.handler(ws)
				returned <- err
				return err
			})

			c, br := upgradeOn(t, s, "")
			client := NewWSConn(c, false)
			client.r = br
			tc.peer(t, c, client)
			recv(t, returned, "the WebSocket handler to return")

			if tc.logged != "" {
				deadline := time.Now().Add(waitFor)
				for !strings.Contains(logs.String(), tc.logged) {
					if time.Now().After(deadline) {
						t.Fatalf("handler error not logged; log: %q", logs.String())
					}
					time.Sleep(5 * time.Millisecond)
				}
			} else {
				// handleRequest logs right after the handler returns; give it
				// time to, were it going to.
				time.Sleep(settle)
				if out := logs.String(); out != "" {
					t.Fatalf("a normal end of the session was logged: %q", out)
				}
			}
			select {
			case err := <-handlerErrs:
				t.Fatalf("error handler called after the upgrade: %v", err)
			default:
			}
		})
	}
}

// readLoop is a WebSocket handler that returns ReadMessage's error, as
// most real handlers do.
func readLoop(ws *WSConn) error {
	for {
		if _, err := ws.ReadMessage(); err != nil {
			return err
		}
	}
}

// --- subprotocols ------------------------------------------------------------

// The server picks the first subprotocol the client offers that the route
// lists (the client orders its offer by preference), and answers with no
// Sec-WebSocket-Protocol at all when nothing matches or the route lists
// none: echoing an offer back unchecked claimed a protocol the handler may
// not speak.
func TestWebSocketSubprotocolSelection(t *testing.T) {
	cases := []struct {
		name       string
		configured []string // nil and opts==nil: s.WebSocket
		useOpts    bool
		offered    []string // one Sec-WebSocket-Protocol line each
		want       string
	}{
		{"s.WebSocket lists none", nil, false, []string{"chat, superchat"}, ""},
		{"options list none", nil, true, []string{"chat"}, ""},
		{"first offered that is listed", []string{"superchat", "chat"}, true, []string{"chat, superchat"}, "chat"},
		{"skips unlisted offers", []string{"v2"}, true, []string{"v1, v2"}, "v2"},
		{"no match", []string{"v2"}, true, []string{"v1"}, ""},
		{"nothing offered", []string{"chat"}, true, nil, ""},
		{"offer split over header lines", []string{"b"}, true, []string{"a", "b"}, "b"},
		{"case-sensitive", []string{"Chat"}, true, []string{"chat"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			got := make(chan string, 1)
			h := func(ws *WSConn) error {
				got <- ws.Subprotocol()
				return nil
			}
			if tc.useOpts {
				s.WebSocketWithOptions("/ws", WSOptions{Subprotocols: tc.configured}, h)
			} else {
				s.WebSocket("/ws", h)
			}
			extra := ""
			for _, o := range tc.offered {
				extra += "Sec-WebSocket-Protocol: " + o + "\r\n"
			}
			_, _, resp := upgradeResp(t, s, extra)

			hdr, present := resp.Header["Sec-Websocket-Protocol"]
			if tc.want == "" && present {
				t.Fatalf("Sec-WebSocket-Protocol %q sent, want none", hdr)
			}
			if tc.want != "" && (len(hdr) != 1 || hdr[0] != tc.want) {
				t.Fatalf("Sec-WebSocket-Protocol %q, want %q", hdr, tc.want)
			}
			if p := recv(t, got, "handler"); p != tc.want {
				t.Fatalf("ws.Subprotocol() = %q, want %q", p, tc.want)
			}
		})
	}
}
