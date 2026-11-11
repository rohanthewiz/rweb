package rweb

// WebSocket read-side concurrency (N-043): Close from another goroutine while
// the handler is blocked in ReadMessage must not put a second reader on the
// connection, and the callback setters (SetPingHandler, SetPongHandler,
// OnClose) must be safe while frames are being read. Run with -race.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// msgResult is what one ReadMessage call returned.
type msgResult struct {
	msg *WSMessage
	err error
}

// readOnce runs one ReadMessage on its own goroutine — the connection's
// handler, blocked waiting for the peer — and reports what it returned.
func readOnce(ws *WSConn) <-chan msgResult {
	res := make(chan msgResult, 1)
	go func() {
		msg, err := ws.ReadMessage()
		res <- msgResult{msg, err}
	}()
	return res
}

// waitReading waits until a ReadMessage holds ws's read side, so a Close
// made next meets the reader-active case rather than racing it to start.
func waitReading(t *testing.T, ws *WSConn) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for len(ws.readSem) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ReadMessage never took the read side")
		}
		time.Sleep(time.Millisecond)
	}
}

// answerClose plays the peer: it reads frames off client until the server's
// close frame, answers it with a close frame of its own (masked, as a client
// sends), and then drains until the connection closes, so no write on the
// server side blocks on net.Pipe.
func answerClose(client *WSConn) {
	for {
		opcode, _, _, data, err := client.readFrame()
		if err != nil {
			return
		}
		if opcode == wsClose {
			_ = writeRawFrame(client.conn, wsClose, true, true, data)
			drain(client.conn)
			return
		}
	}
}

// The case found during N-040: a hub (another goroutine) closes a client
// whose handler is blocked in ReadMessage. Close used to read the peer's
// close frame itself, a second reader alongside ReadMessage: the two split
// the reply's bytes between them, so the handler got garbage or nothing and
// Close waited out its timeout. Now Close waits for the handler's read:
// ReadMessage returns the peer's close frame, and Close returns as soon as
// it has.
func TestWebSocketCloseWhileReadingPeerAnswers(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()
	go answerClose(client)

	read := readOnce(server)
	waitReading(t, server)

	start := time.Now()
	if err := server.Close(wsCloseNormalClosure, "hub closing"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > closeHandshakeTimeout/2 {
		t.Errorf("Close took %v with a peer that answered at once", elapsed)
	}

	r := recv(t, read, "ReadMessage to return")
	if r.err != nil {
		t.Fatalf("ReadMessage: %v; want the peer's close frame", r.err)
	}
	if r.msg.Type != CloseMessage || binary.BigEndian.Uint16(r.msg.Data) != wsCloseNormalClosure {
		t.Fatalf("ReadMessage returned type %d data %x; want the peer's close frame (1000)", r.msg.Type, r.msg.Data)
	}
}

// The same with a peer that never answers: both the handler's read and
// Close end on the close-handshake timeout, and nothing reads the
// connection twice (under -race, two readers would race on the shared read
// state).
func TestWebSocketCloseWhileReadingPeerSilent(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()
	go drain(client.conn) // reads the close frame, never answers

	read := readOnce(server)
	waitReading(t, server)

	start := time.Now()
	if err := server.Close(wsCloseNormalClosure, "hub closing"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < closeHandshakeTimeout/2 || elapsed > closeHandshakeTimeout+time.Second {
		t.Errorf("Close took %v; want about the close-handshake timeout (%v)", elapsed, closeHandshakeTimeout)
	}

	r := recv(t, read, "ReadMessage to fail")
	if r.err == nil {
		t.Fatalf("ReadMessage returned type %d with a silent peer; want an error", r.msg.Type)
	}
}

// The same through the server's own upgrade path, on a real socket, with
// the liveness reader and its deadlines in the read chain.
func TestWebSocketCloseWhileReadingOnServer(t *testing.T) {
	s := NewServer()
	wsCh := make(chan *WSConn, 1)
	read := make(chan msgResult, 1)
	s.WebSocket("/ws", func(ws *WSConn) error {
		wsCh <- ws
		msg, err := ws.ReadMessage()
		read <- msgResult{msg, err}
		return nil
	})
	c, br := upgradeOn(t, s, "")
	ws := recv(t, wsCh, "the WebSocket handler")

	client := NewWSConn(c, false)
	client.r = br
	go answerClose(client)
	waitReading(t, ws)

	start := time.Now()
	if err := ws.Close(wsCloseGoingAway, "bye"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > closeHandshakeTimeout/2 {
		t.Errorf("Close took %v with a peer that answered at once", elapsed)
	}
	r := recv(t, read, "ReadMessage to return")
	if r.err != nil || r.msg.Type != CloseMessage {
		t.Fatalf("ReadMessage = %v, %v; want the peer's close frame", r.msg, r.err)
	}
}

// A Close made from inside a ping handler — on the reading goroutine itself
// — must not wait for the read side its own ReadMessage holds: the read
// side is handed back while the handler runs, so Close reads the peer's
// answer and returns at once, and ReadMessage then fails on the closed
// connection rather than hanging.
func TestWebSocketCloseFromPingHandler(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	closeErr := make(chan error, 1)
	var closeTook time.Duration
	server.SetPingHandler(func([]byte) error {
		start := time.Now()
		err := server.Close(wsCloseNormalClosure, "from ping handler")
		closeTook = time.Since(start)
		closeErr <- err
		return nil
	})

	go func() {
		_ = writeRawFrame(client.conn, wsPing, true, true, []byte("hi"))
		answerClose(client)
	}()

	_, err := server.ReadMessage()
	if err == nil {
		t.Fatal("ReadMessage after Close: want an error")
	}
	if err := recv(t, closeErr, "Close in the ping handler"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closeTook > closeHandshakeTimeout/2 {
		t.Errorf("Close from the ping handler took %v; it waited on its own read", closeTook)
	}
}

// SetPingHandler, SetPongHandler and OnClose from another goroutine while
// ReadMessage handles pings, pongs and finally a close frame: no data race
// (run with -race), and every frame reaches some handler.
func TestWebSocketHandlerSettersDuringReads(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()
	go drain(client.conn) // pong replies and the close reply

	var pings, pongs, closes atomic.Int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			server.SetPingHandler(func([]byte) error { pings.Add(1); return nil })
			server.SetPongHandler(func([]byte) error { pongs.Add(1); return nil })
			server.OnClose(func(int, string) { closes.Add(1) })
		}
	}()

	const n = 200
	go func() {
		for i := 0; i < n; i++ {
			_ = writeRawFrame(client.conn, wsPing, true, true, []byte("p"))
			_ = writeRawFrame(client.conn, wsPong, true, true, []byte("q"))
		}
		_ = writeRawFrame(client.conn, wsClose, true, true, []byte{0x03, 0xE8})
	}()

	// The setters run from the start, but the first ping may still meet the
	// defaults; wait until they are in place so the counts below are exact.
	for {
		server.handlersMu.Lock()
		set := len(server.closeHandlers) > 0
		server.handlersMu.Unlock()
		if set {
			break
		}
		time.Sleep(time.Millisecond)
	}

	msg, err := server.ReadMessage()
	close(stop)
	wg.Wait()
	if err != nil || msg.Type != CloseMessage {
		t.Fatalf("ReadMessage = %v, %v; want the close frame after the pings", msg, err)
	}
	if pings.Load() != n || pongs.Load() != n {
		t.Errorf("handled %d pings and %d pongs, want %d of each", pings.Load(), pongs.Load(), n)
	}
	if closes.Load() == 0 {
		t.Error("no close handler ran")
	}
}

// A 64-bit payload length with its top bit set is invalid (RFC 6455 §5.2);
// as an int64 it is negative, which used to pass the size check and panic
// in make. It is refused as too large.
func TestWebSocketPayloadLengthTopBitSet(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	server := NewWSConn(serverConn, true)

	var frame bytes.Buffer
	frame.Write([]byte{0x82, 0x80 | 127}) // FIN binary, masked, 64-bit length
	_ = binary.Write(&frame, binary.BigEndian, uint64(1)<<63|5)
	frame.Write([]byte{1, 2, 3, 4}) // mask key
	go func() { _, _ = clientConn.Write(frame.Bytes()) }()

	if _, err := server.ReadMessage(); !errors.Is(err, ErrWebSocketPayloadTooLarge) {
		t.Fatalf("ReadMessage = %v, want ErrWebSocketPayloadTooLarge", err)
	}
}

// Two goroutines calling ReadMessage take turns instead of splitting
// frames: every message arrives whole, at one of them.
func TestWebSocketConcurrentReadMessage(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	const n = 100
	go func() {
		w := bufio.NewWriter(client.conn)
		for i := 0; i < n; i++ {
			_ = writeRawFrame(w, wsText, true, true, bytes.Repeat([]byte{'x'}, 300))
		}
		_ = w.Flush()
	}()

	var got atomic.Int32
	var wg sync.WaitGroup
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for got.Load() < n {
				msg, err := server.ReadMessage()
				if err != nil {
					return
				}
				if len(msg.Data) != 300 || msg.Data[0] != 'x' || msg.Data[299] != 'x' {
					t.Errorf("message of %d bytes arrived mangled", len(msg.Data))
					return
				}
				if got.Add(1) == n {
					_ = server.conn.Close() // ends the other reader's wait
				}
			}
		}()
	}
	wg.Wait()
	if got.Load() != n {
		t.Fatalf("%d of %d messages read", got.Load(), n)
	}
}

// A Close made inside an OnClose handler answers the peer's close frame with
// the app's own code and returns at once. It used to deadlock: the handlers
// ran under closeMutex, which Close takes first.
func TestWebSocketCloseFromOnCloseHandler(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	closeErr := make(chan error, 1)
	server.OnClose(func(code int, text string) {
		closeErr <- server.Close(wsCloseGoingAway, "on close")
	})

	reply := make(chan []byte, 1)
	go func() {
		_ = writeRawFrame(client.conn, wsClose, true, true, []byte{0x03, 0xE8})
		_, _, _, data, err := client.readFrame()
		if err == nil {
			reply <- data
		}
		drain(client.conn)
	}()

	read := readOnce(server)
	if err := recv(t, closeErr, "Close in the OnClose handler"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r := recv(t, read, "ReadMessage to return")
	if r.err != nil || r.msg.Type != CloseMessage {
		t.Fatalf("ReadMessage = %v, %v; want the peer's close frame", r.msg, r.err)
	}
	// One close frame answers the peer's: the handler's, with its code.
	if data := recv(t, reply, "the server's close frame"); binary.BigEndian.Uint16(data) != wsCloseGoingAway {
		t.Fatalf("server answered with code %d, want the handler's %d", binary.BigEndian.Uint16(data), wsCloseGoingAway)
	}
}
