package rweb

// ReadMessage after the closing handshake (N-076). Once the peer's close
// frame has been read, a further ReadMessage returns
// ErrWebSocketAlreadyClosed at once. Before, after the server's own close
// (Shutdown's 1001 via goingAway, which leaves the conn open for the
// handler's loop to close), a handler that read on past the CloseMessage
// blocked until the 2 s closeHandshakeTimeout deadline, and Shutdown waited
// with it. Found in the N-062 browser run.

import (
	"bufio"
	stdctx "context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A handler that ignores the CloseMessage answering Shutdown's 1001 and reads
// again gets ErrWebSocketAlreadyClosed straight away, so Shutdown is not held
// for the handshake timeout.
func TestWebSocketReadAfterShutdownCloseFailsAtOnce(t *testing.T) {
	s := NewServer()
	gotClose := make(chan struct{}, 1)
	ended := make(chan error, 1)
	s.WebSocket("/ws", func(ws *WSConn) error {
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				ended <- err
				return nil
			}
			if msg.Type == CloseMessage {
				gotClose <- struct{}{} // and read on, as the N-062 harness did
			}
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()

	c := dialTest(t, ln.Addr().String(), transports[0])
	writeAll(t, c, "GET /ws HTTP/1.1\r\nHost: test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(c)
	if resp, err := http.ReadResponse(br, nil); err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	// Play the browser by hand: answer the server's close frame with a
	// (masked) close frame of our own, and then keep the TCP connection
	// open. RFC 6455 §7.1.1 has the server close TCP first, so a browser
	// waits for that; rweb's own client WSConn closes at once instead, which
	// would end the server's read with EOF and hide the wait.
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		var frame [4]byte // 0x88, len 2, 1001 (unmasked, from the server)
		if _, err := io.ReadFull(br, frame[:]); err != nil {
			return
		}
		mask := [4]byte{1, 2, 3, 4}
		reply := []byte{0x88, 0x80 | 2, mask[0], mask[1], mask[2], mask[3], frame[2] ^ mask[0], frame[3] ^ mask[1]}
		_, _ = c.Write(reply)
	}()

	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	took := time.Since(start)
	recv(t, answered, "the client to answer the 1001")
	recv(t, gotClose, "the handler to see the client's close reply")
	if err := recv(t, ended, "the handler's next read to fail"); !errors.Is(err, ErrWebSocketAlreadyClosed) {
		t.Errorf("read after the close handshake: %v, want ErrWebSocketAlreadyClosed", err)
	}
	if took >= closeHandshakeTimeout {
		t.Errorf("Shutdown took %v, at least the %v handshake timeout", took, closeHandshakeTimeout)
	}
}

// After the peer starts the close (its frame read and answered), a further
// ReadMessage reports ErrWebSocketAlreadyClosed, not a network error from
// the conn closed underneath it.
func TestWebSocketReadAfterPeerCloseIsAlreadyClosed(t *testing.T) {
	s := NewServer()
	second := make(chan error, 1)
	s.WebSocket("/ws", func(ws *WSConn) error {
		msg, err := ws.ReadMessage()
		if err != nil || msg.Type != CloseMessage {
			second <- errors.New("first read was not the CloseMessage")
			return nil
		}
		_, err = ws.ReadMessage()
		second <- err
		return nil
	})
	c, br := upgradeOn(t, s, "")
	client := NewWSConn(c, false)
	client.r = br
	_ = client.Close(1000, "bye")
	if err := recv(t, second, "the second read"); !errors.Is(err, ErrWebSocketAlreadyClosed) {
		t.Fatalf("second read: %v, want ErrWebSocketAlreadyClosed", err)
	}
}
