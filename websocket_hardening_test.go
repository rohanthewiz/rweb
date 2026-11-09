package rweb

// WebSocket hardening (N-040): concurrent close, control-frame limits, write
// and read deadlines, server pings, subprotocol selection and the logging of
// a WebSocket handler's error. Internal tests, so they can build connections
// with newTestPair and speak raw frames with writeRawFrame.

import (
	"bytes"
	"encoding/binary"
	"errors"
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
