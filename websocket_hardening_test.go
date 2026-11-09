package rweb

// WebSocket hardening (N-040): concurrent close, control-frame limits, write
// and read deadlines, server pings, subprotocol selection and the logging of
// a WebSocket handler's error. Internal tests, so they can build connections
// with newTestPair and speak raw frames with writeRawFrame.

import (
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
