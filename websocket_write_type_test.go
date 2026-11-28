package rweb

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// WriteMessage / WriteMessages send only the types a caller may send as a
// single frame (N-085): text, binary, ping, pong. CloseMessage goes through
// Close, and a value that is no message type would put a continuation or
// reserved-opcode frame on the wire.

// badWriteTypes are refused by both write calls: CloseMessage, the
// continuation opcode, reserved data and control opcodes, and values that do
// not fit the 4-bit opcode field at all.
var badWriteTypes = []MessageType{
	CloseMessage,
	MessageType(wsContinuation),
	3, 7, // reserved data opcodes
	11, 15, // reserved control opcodes
	16, 0x81, -1, // past the opcode field
}

// Each refused type returns ErrWebSocketBadMessageType at once, with nothing
// written, the connection still open and not marked closed: the next frame
// the peer sees is the text message sent after the refusal.
func TestWriteMessageRefusesType(t *testing.T) {
	for _, mt := range badWriteTypes {
		for _, call := range []struct {
			name  string
			write func(ws *WSConn) error
		}{
			{"WriteMessage", func(ws *WSConn) error { return ws.WriteMessage(mt, []byte("x")) }},
			{"WriteMessages", func(ws *WSConn) error { return ws.WriteMessages(mt, []byte("x"), []byte("y")) }},
		} {
			for _, isClient := range []bool{false, true} {
				server, client := newTestPair()
				writer, peer := server, client
				if isClient {
					writer, peer = client, server
				}
				// No reader on the pipe yet: a write would block, so an
				// error that comes back at once means nothing was written.
				errc := make(chan error, 1)
				go func() { errc <- call.write(writer) }()
				select {
				case err := <-errc:
					if !errors.Is(err, ErrWebSocketBadMessageType) {
						t.Fatalf("%s(%d) (client %v): err = %v, want ErrWebSocketBadMessageType", call.name, mt, isClient, err)
					}
					if mt == CloseMessage && !strings.Contains(err.Error(), "Close(code, reason)") {
						t.Errorf("%s(CloseMessage): error %q does not point to Close", call.name, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatalf("%s(%d) (client %v): blocked writing a frame that should have been refused", call.name, mt, isClient)
				}
				if writer.closed.Load() {
					t.Fatalf("%s(%d) (client %v): the refusal marked the connection closed", call.name, mt, isClient)
				}
				select {
				case <-writer.Done():
					t.Fatalf("%s(%d) (client %v): the refusal closed the connection", call.name, mt, isClient)
				default:
				}

				first := make(chan int, 1)
				go func() {
					opcode, _, _, _, err := peer.readFrame()
					if err != nil {
						opcode = -1
					}
					first <- opcode
				}()
				if err := writer.WriteMessage(TextMessage, []byte("next")); err != nil {
					t.Fatalf("%s(%d) (client %v): WriteMessage after the refusal: %v", call.name, mt, isClient, err)
				}
				if op := <-first; op != wsText {
					t.Fatalf("%s(%d) (client %v): peer's first frame has opcode %d, want text", call.name, mt, isClient, op)
				}
				server.conn.Close()
				client.conn.Close()
			}
		}
	}
}

// The four types that are sent go out with their own opcode, through both
// calls and in both roles.
func TestWriteMessageSendsAllowedTypes(t *testing.T) {
	for _, tc := range []struct {
		mt     MessageType
		opcode int
	}{
		{TextMessage, wsText},
		{BinaryMessage, wsBinary},
		{PingMessage, wsPing},
		{PongMessage, wsPong},
	} {
		for _, batch := range []bool{false, true} {
			for _, isClient := range []bool{false, true} {
				server, client := newTestPair()
				writer, peer := server, client
				if isClient {
					writer, peer = client, server
				}
				got := make(chan []int, 1)
				go func() {
					var ops []int
					n := 1
					if batch {
						n = 2
					}
					for i := 0; i < n; i++ {
						opcode, _, _, payload, err := peer.readFrame()
						if err != nil || string(payload) != "hi" {
							opcode = -1
						}
						ops = append(ops, opcode)
					}
					got <- ops
				}()
				var err error
				if batch {
					err = writer.WriteMessages(tc.mt, []byte("hi"), []byte("hi"))
				} else {
					err = writer.WriteMessage(tc.mt, []byte("hi"))
				}
				if err != nil {
					t.Fatalf("type %d (batch %v, client %v): %v", tc.mt, batch, isClient, err)
				}
				for _, op := range <-got {
					if op != tc.opcode {
						t.Fatalf("type %d (batch %v, client %v): peer read opcode %d, want %d", tc.mt, batch, isClient, op, tc.opcode)
					}
				}
				server.conn.Close()
				client.conn.Close()
			}
		}
	}
}

// An echo loop that hands ReadMessage's result straight back,
// WriteMessage(msg.Type, msg.Data), gets ErrWebSocketAlreadyClosed for the
// peer's close, as before the type check (README's example ignores the
// error and loops into ReadMessage, which then fails and ends it): the
// closed check comes first, and ReadMessage has answered the close by then.
func TestWriteMessageCloseAfterPeerCloseIsAlreadyClosed(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()

	go func() {
		// The client's Close waits for the answer, read here by its own
		// Close (nobody else reads the client side).
		_ = client.Close(wsCloseNormalClosure, "bye")
	}()
	msg, err := server.ReadMessage()
	if err != nil || msg.Type != CloseMessage {
		t.Fatalf("ReadMessage = %v, %v; want the peer's CloseMessage", msg, err)
	}
	for _, write := range []func() error{
		func() error { return server.WriteMessage(msg.Type, msg.Data) },
		func() error { return server.WriteMessages(msg.Type, msg.Data) },
	} {
		if err := write(); !errors.Is(err, ErrWebSocketAlreadyClosed) {
			t.Fatalf("echoing the CloseMessage: err = %v, want ErrWebSocketAlreadyClosed", err)
		}
	}
}
