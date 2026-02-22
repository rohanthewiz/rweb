package rweb

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// writeRawFrame writes a WebSocket frame with precise control over FIN, opcode,
// and masking. This bypasses WSConn.writeFrame to test frame-level edge cases.
func writeRawFrame(w io.Writer, opcode int, fin bool, masked bool, data []byte) error {
	// First byte: FIN + opcode
	var b0 byte
	if fin {
		b0 = 0x80
	}
	b0 |= byte(opcode)

	// Second byte: mask bit + payload length
	var b1 byte
	if masked {
		b1 = 0x80
	}

	dataLen := len(data)
	var extLen []byte
	if dataLen < 126 {
		b1 |= byte(dataLen)
	} else if dataLen <= 65535 {
		b1 |= 126
		extLen = make([]byte, 2)
		binary.BigEndian.PutUint16(extLen, uint16(dataLen))
	} else {
		b1 |= 127
		extLen = make([]byte, 8)
		binary.BigEndian.PutUint64(extLen, uint64(dataLen))
	}

	if _, err := w.Write([]byte{b0, b1}); err != nil {
		return err
	}
	if extLen != nil {
		if _, err := w.Write(extLen); err != nil {
			return err
		}
	}

	if masked {
		// Use a fixed mask for deterministic tests
		mask := []byte{0x12, 0x34, 0x56, 0x78}
		if _, err := w.Write(mask); err != nil {
			return err
		}
		maskedData := make([]byte, len(data))
		for i := range data {
			maskedData[i] = data[i] ^ mask[i%4]
		}
		_, err := w.Write(maskedData)
		return err
	}

	_, err := w.Write(data)
	return err
}

// newTestPair creates a connected pair of WSConns using net.Pipe().
// The "server" side expects masked frames (from the client), and vice versa.
func newTestPair() (server *WSConn, client *WSConn) {
	serverConn, clientConn := net.Pipe()
	server = NewWSConn(serverConn, true)
	client = NewWSConn(clientConn, false)
	return
}

// --- Phase 1 tests: fragmentation and close handshake ---

func TestWebSocketUnfragmentedMessage(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()

	payload := []byte("hello world")

	// Client writes a single text frame with FIN=1 (masked, since client->server)
	go func() {
		writeRawFrame(client.conn, wsText, true, true, payload)
	}()

	msg, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if msg.Type != TextMessage {
		t.Errorf("expected TextMessage, got %d", msg.Type)
	}
	if string(msg.Data) != string(payload) {
		t.Errorf("expected %q, got %q", string(payload), string(msg.Data))
	}
}

func TestWebSocketFragmentedMessage(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()

	// Client sends 3 fragments: text FIN=0, continuation FIN=0, continuation FIN=1
	// All frames are masked because they're client->server
	go func() {
		writeRawFrame(client.conn, wsText, false, true, []byte("hello "))
		writeRawFrame(client.conn, wsContinuation, false, true, []byte("fragmented "))
		writeRawFrame(client.conn, wsContinuation, true, true, []byte("world"))
	}()

	msg, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if msg.Type != TextMessage {
		t.Errorf("expected TextMessage, got %d", msg.Type)
	}
	expected := "hello fragmented world"
	if string(msg.Data) != expected {
		t.Errorf("expected %q, got %q", expected, string(msg.Data))
	}
}

func TestWebSocketUnexpectedContinuation(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()

	// Send a continuation frame without a preceding text/binary start frame
	go func() {
		writeRawFrame(client.conn, wsContinuation, true, true, []byte("orphan"))
	}()

	_, err := server.ReadMessage()
	if err == nil {
		t.Fatal("expected error for unexpected continuation frame, got nil")
	}
	if err.Error() != "unexpected continuation frame" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWebSocketCloseHandshake(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	// Peer (client) sends a close frame response immediately when it reads one
	go func() {
		// Read the close frame from the server
		_, _, data, err := client.readFrame()
		if err != nil {
			return
		}
		// Echo back the close frame (unmasked since we're reading as "client")
		// But we need to write a masked frame back since the server expects masked
		writeRawFrame(client.conn, wsClose, true, true, data)
	}()

	start := time.Now()
	err := server.Close(wsCloseNormalClosure, "goodbye")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Should complete well under the 2s timeout since the peer responds immediately
	if elapsed > 500*time.Millisecond {
		t.Errorf("Close took %v, expected < 500ms (peer responded immediately)", elapsed)
	}
}

func TestWebSocketCloseHandshakeTimeout(t *testing.T) {
	server, client := newTestPair()

	// Drain the client side so writeFrame doesn't block on net.Pipe(),
	// but never send a close frame back — force the deadline timeout.
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := client.conn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	start := time.Now()
	err := server.Close(wsCloseNormalClosure, "goodbye")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Should wait approximately closeHandshakeTimeout (2s) then return.
	// Allow some tolerance — must be less than 3s but more than 1s.
	if elapsed < 1*time.Second {
		t.Errorf("Close returned too quickly (%v), expected ~2s timeout", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Close took too long (%v), expected ~2s timeout", elapsed)
	}
}

// --- Phase 2 tests: Done channel ---

func TestWebSocketDoneChannelOnClose(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	// Have the client respond to the close frame so Close() returns promptly
	go func() {
		client.readFrame()
		writeRawFrame(client.conn, wsClose, true, true, []byte{0x03, 0xE8}) // 1000
	}()

	// Verify Done() is not closed before we call Close()
	select {
	case <-server.Done():
		t.Fatal("Done() should not be closed before Close()")
	default:
		// expected
	}

	server.Close(wsCloseNormalClosure, "done")

	// Verify Done() is now closed
	select {
	case <-server.Done():
		// expected — channel is closed
	default:
		t.Fatal("Done() should be closed after Close()")
	}
}

func TestWebSocketDoneChannelOnPeerClose(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	// Peer sends a close frame, then drains any response (handleClose writes
	// a close frame back, which blocks on net.Pipe if nobody reads it).
	go func() {
		closeData := make([]byte, 2)
		binary.BigEndian.PutUint16(closeData, uint16(wsCloseNormalClosure))
		writeRawFrame(client.conn, wsClose, true, true, closeData)

		// Drain response so handleClose's writeFrame doesn't block
		buf := make([]byte, 1024)
		for {
			if _, err := client.conn.Read(buf); err != nil {
				return
			}
		}
	}()

	// ReadMessage should return the close message and trigger handleClose
	msg, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if msg.Type != CloseMessage {
		t.Errorf("expected CloseMessage, got %d", msg.Type)
	}

	// Done() should now be closed because handleClose was called
	select {
	case <-server.Done():
		// expected
	default:
		t.Fatal("Done() should be closed after peer sends close frame")
	}
}

func TestWebSocketDoneChannelPingPattern(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	// Continuously drain the client side so ping writes and the close frame
	// don't block on net.Pipe. Track how many ping frames we receive.
	pingReceived := make(chan struct{}, 100)
	go func() {
		for {
			opcode, _, _, err := client.readFrame()
			if err != nil {
				return
			}
			if opcode == wsPing {
				pingReceived <- struct{}{}
			}
			// When we see the close frame, respond with a close
			if opcode == wsClose {
				writeRawFrame(client.conn, wsClose, true, true, []byte{0x03, 0xE8})
				return
			}
		}
	}()

	// Verify the recommended ping pattern using Done() exits cleanly
	pingDone := make(chan struct{})

	go func() {
		ticker := time.NewTicker(10 * time.Millisecond) // fast ticker for testing
		defer ticker.Stop()
		for {
			select {
			case <-server.Done():
				close(pingDone)
				return
			case <-ticker.C:
				server.WritePing([]byte("ping"))
			}
		}
	}()

	// Wait until at least one ping has been received by the client
	select {
	case <-pingReceived:
		// good — at least one ping fired
	case <-time.After(2 * time.Second):
		t.Fatal("no ping received within timeout")
	}

	server.Close(wsCloseNormalClosure, "shutdown")

	// Wait for the ping goroutine to exit
	select {
	case <-pingDone:
		// Ping goroutine exited cleanly via Done()
	case <-time.After(2 * time.Second):
		t.Fatal("ping goroutine did not exit after Done() was closed")
	}
}
