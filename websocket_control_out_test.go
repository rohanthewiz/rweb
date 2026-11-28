package rweb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// Control frames this side sends (N-084): Close repairs what it is given so
// the close frame is always one a peer accepts (reason cut to 123 bytes at a
// character boundary and made UTF-8, an unsendable code sent as no code),
// and WritePing / WriteMessage with a control type refuse a payload over 125
// bytes before writing anything.

func TestCloseReason(t *testing.T) {
	long := strings.Repeat("a", 200)
	for _, tc := range []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"short", "bye", "bye"},
		{"exactly 123", long[:123], long[:123]},
		{"124 cut to 123", long[:124], long[:123]},
		{"long cut to 123", long, long[:123]},
		// é is 2 bytes: at 122+2 it straddles the limit and goes whole.
		{"2-byte char straddling", long[:122] + "é", long[:122]},
		{"2-byte char ending at 123", long[:121] + "é" + "zz", long[:121] + "é"},
		// 🙂 is 4 bytes: 121+4 straddles, cut back to 121.
		{"4-byte char straddling", long[:121] + "🙂", long[:121]},
		{"invalid byte", "a\xffb", "a�b"},
		{"surrogate", "x\xed\xa0\x80y", "x�y"},
		// Replacing first, then cutting: the U+FFFD (3 bytes) at 121
		// straddles 123 and goes whole.
		{"invalid byte at the limit", long[:121] + "\xff" + long[:10], long[:121]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := closeReason(tc.in)
			if got != tc.want {
				t.Fatalf("closeReason(%q) = %q (%d bytes), want %q (%d bytes)", tc.in, got, len(got), tc.want, len(tc.want))
			}
			if len(got) > maxCloseReason || !utf8.ValidString(got) {
				t.Fatalf("closeReason(%q) = %q: %d bytes, valid UTF-8 %v", tc.in, got, len(got), utf8.ValidString(got))
			}
		})
	}
}

func TestCloseFramePayloadCodes(t *testing.T) {
	// A code that may not be sent gives an empty body, reason dropped.
	for _, code := range []int{-1, 0, 999, 1004, 1005, 1006, 1015, 1016, 2999, 5000, 65535, 70000} {
		if got := closeFramePayload(code, "why"); got != nil {
			t.Errorf("closeFramePayload(%d) = %v, want an empty body", code, got)
		}
	}
	for _, code := range []int{1000, 1001, 1003, 1007, 1011, 1014, 3000, 4999} {
		got := closeFramePayload(code, "why")
		if len(got) != 5 || int(binary.BigEndian.Uint16(got)) != code || string(got[2:]) != "why" {
			t.Errorf("closeFramePayload(%d) = %v, want the code and the reason", code, got)
		}
	}
	// The whole body stays within a control frame.
	if got := closeFramePayload(1011, strings.Repeat("é", 100)); len(got) > 125 {
		t.Errorf("body of %d bytes", len(got))
	}
}

// closeSeen is what the peer's ReadMessage and OnClose handler report for
// the close frame it received.
type closeSeen struct {
	err    error
	typ    MessageType
	code   int
	reason string
}

// closeOverPipe calls Close(code, reason) on one side of a net.Pipe pair
// (the server role, or the client role when fromClient is set, so masking is
// covered too) while the other side reads, and reports what the reader saw.
func closeOverPipe(t *testing.T, fromClient bool, code int, reason string) closeSeen {
	t.Helper()
	server, client := newTestPair()
	closer, peer := server, client
	if fromClient {
		closer, peer = client, server
	}
	var seen closeSeen
	var mu sync.Mutex
	peer.OnClose(func(c int, r string) {
		mu.Lock()
		seen.code, seen.reason = c, r
		mu.Unlock()
	})
	read := make(chan struct{})
	go func() {
		defer close(read)
		msg, err := peer.ReadMessage()
		mu.Lock()
		seen.err = err
		if msg != nil {
			seen.typ = msg.Type
		}
		mu.Unlock()
	}()
	if err := closer.Close(code, reason); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-read:
	case <-time.After(3 * time.Second):
		t.Fatal("peer's ReadMessage did not return")
	}
	mu.Lock()
	defer mu.Unlock()
	return seen
}

// A long reason reaches the peer cut, as a close frame it accepts, with the
// code intact; before N-084 the peer refused the frame (over 125 bytes) and
// the code and reason were lost.
func TestCloseLongReasonArrives(t *testing.T) {
	reason := strings.Repeat("database connection failed: ", 10) // 280 bytes
	for _, fromClient := range []bool{false, true} {
		seen := closeOverPipe(t, fromClient, wsCloseInternalServerErr, reason)
		if seen.err != nil || seen.typ != CloseMessage {
			t.Fatalf("fromClient=%v: peer ReadMessage = %v, %v; want the close frame", fromClient, seen.typ, seen.err)
		}
		if seen.code != wsCloseInternalServerErr || seen.reason != reason[:maxCloseReason] {
			t.Fatalf("fromClient=%v: OnClose(%d, %q), want 1011 and the first 123 bytes", fromClient, seen.code, seen.reason)
		}
	}
}

// A reason that is not UTF-8 arrives with U+FFFD in place of the bad bytes;
// the peer would otherwise fail the connection with 1007 (N-082).
func TestCloseInvalidUTF8ReasonArrives(t *testing.T) {
	seen := closeOverPipe(t, false, wsCloseNormalClosure, "bye\xff")
	if seen.err != nil || seen.code != wsCloseNormalClosure || seen.reason != "bye�" {
		t.Fatalf("peer saw err=%v OnClose(%d, %q)", seen.err, seen.code, seen.reason)
	}
}

// A code that may not be sent goes out as no code: the peer reports 1005
// and does not fail the connection with 1002 (as it did since N-082).
func TestCloseUnsendableCodeSendsNoCode(t *testing.T) {
	for _, code := range []int{0, 1006, 1015, 2000, 5000} {
		seen := closeOverPipe(t, false, code, "reason dropped")
		if seen.err != nil || seen.typ != CloseMessage {
			t.Fatalf("Close(%d): peer ReadMessage = %v, %v; want the close frame", code, seen.typ, seen.err)
		}
		if seen.code != wsCloseNoStatusReceived || seen.reason != "" {
			t.Fatalf("Close(%d): OnClose(%d, %q), want 1005 and no reason", code, seen.code, seen.reason)
		}
	}
}

// A ping or pong (WritePing, or PingMessage / PongMessage through
// WriteMessage / WriteMessages) over 125 bytes is refused before anything is written, and the connection
// carries on: the next frame the peer sees is the message sent after.
func TestControlFrameOver125Refused(t *testing.T) {
	big := bytes.Repeat([]byte("p"), 126)
	for _, tc := range []struct {
		name  string
		write func(ws *WSConn) error
	}{
		{"WritePing", func(ws *WSConn) error { return ws.WritePing(big) }},
		{"WriteMessage ping", func(ws *WSConn) error { return ws.WriteMessage(PingMessage, big) }},
		{"WriteMessage pong", func(ws *WSConn) error { return ws.WriteMessage(PongMessage, big) }},
		// WriteMessage(CloseMessage, …) is refused for its type since N-085,
		// whatever the size (TestWriteMessageRefusesType).
		// The small pong ahead of the big one is not sent either: the batch
		// is refused whole, before the one write.
		{"WriteMessages", func(ws *WSConn) error { return ws.WriteMessages(PongMessage, []byte("ok"), big) }},
	} {
		for _, isClient := range []bool{false, true} {
			server, client := newTestPair()
			writer, peer := server, client
			if isClient {
				writer, peer = client, server
			}
			// No reader on the pipe yet: a write would block, so an error
			// that comes back at once means nothing was written.
			errc := make(chan error, 1)
			go func() { errc <- tc.write(writer) }()
			select {
			case err := <-errc:
				if !errors.Is(err, ErrWebSocketBadControlFrame) {
					t.Fatalf("%s (client %v): err = %v, want ErrWebSocketBadControlFrame", tc.name, isClient, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s (client %v): blocked writing a frame that should have been refused", tc.name, isClient)
			}
			select {
			case <-writer.Done():
				t.Fatalf("%s (client %v): the refusal closed the connection", tc.name, isClient)
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
				t.Fatalf("%s (client %v): WriteMessage after the refusal: %v", tc.name, isClient, err)
			}
			if op := <-first; op != wsText {
				t.Fatalf("%s (client %v): peer's first frame has opcode %d, want text", tc.name, isClient, op)
			}
			server.conn.Close()
			client.conn.Close()
		}
	}
}

// 125 bytes is the limit, not over it: a ping that size arrives whole.
func TestPing125Arrives(t *testing.T) {
	server, client := newTestPair()
	defer server.conn.Close()
	defer client.conn.Close()
	payload := bytes.Repeat([]byte("p"), 125)
	got := make(chan []byte, 1)
	client.SetPingHandler(func(data []byte) error {
		got <- data
		return nil
	})
	go func() { _, _ = client.ReadMessage() }()
	if err := server.WritePing(payload); err != nil {
		t.Fatalf("WritePing(125 bytes): %v", err)
	}
	select {
	case data := <-got:
		if !bytes.Equal(data, payload) {
			t.Fatalf("ping of %d bytes arrived, want 125", len(data))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ping did not arrive")
	}
}
