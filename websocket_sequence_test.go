package rweb

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// Message-sequence and UTF-8 rules of ReadMessage (RFC 6455 §5.4, §8.1),
// N-081. The peer here is a test's raw frame writer, so it can send what no
// WSConn would: messages interleaved, continuations with nothing to continue,
// text that is not UTF-8.

// seqFrame is one frame for a raw peer to send.
type seqFrame struct {
	opcode int
	fin    bool
	data   string
	rsv1   bool // permessage-deflate's "compressed" bit
}

// sendFrames writes frames to c as a peer would (masked when the peer is a
// client), stopping at the first write error: a peer that was failed is
// closed, and the frames after the bad one have nowhere to go.
func sendFrames(c net.Conn, masked bool, frames []seqFrame) {
	for _, f := range frames {
		var b bytes.Buffer
		if err := writeRawFrame(&b, f.opcode, f.fin, masked, []byte(f.data)); err != nil {
			return
		}
		frame := b.Bytes()
		if f.rsv1 {
			frame[0] |= 0x40
		}
		if _, err := c.Write(frame); err != nil {
			return
		}
	}
}

// closeCodeFrom reads frames on peer until the first close frame and sends
// its status code, or -1 if the read fails first (no close frame was sent).
// A peer must keep reading while the other side fails the connection: on
// net.Pipe, a close frame nobody reads blocks its writer.
func closeCodeFrom(peer *WSConn) <-chan int {
	code := make(chan int, 1)
	go func() {
		for {
			opcode, _, _, data, err := peer.readFrame()
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
	return code
}

// expectFailed checks the outcome of a ReadMessage that should have failed
// the connection: the error, the close code the peer got, and Done().
func expectFailed(t *testing.T, ws *WSConn, err, wantErr error, code <-chan int, wantCode int) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReadMessage error = %v, want %v", err, wantErr)
	}
	select {
	case c := <-code:
		if c != wantCode {
			t.Fatalf("close code %d, want %d", c, wantCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no close frame sent")
	}
	select {
	case <-ws.Done():
	default:
		t.Fatal("Done() not closed after failing the connection")
	}
}

// Frames out of sequence fail the connection with 1002 and
// ErrWebSocketBadFragment: a new message inside a fragmented one (either
// FIN), and a continuation with no message in progress, including one right
// after a fragmented message completed (the state is reset, not left
// pointing at the finished message). The messages before the bad frame are
// still delivered.
func TestWebSocketBadFragmentSequence(t *testing.T) {
	cases := []struct {
		name   string
		frames []seqFrame
		before []string // messages ReadMessage returns before the error
	}{
		{
			name: "text with FIN inside a fragmented message",
			frames: []seqFrame{
				{wsText, false, "par", false},
				{wsText, true, "intruder", false},
				{wsContinuation, true, "tial", false},
			},
		},
		{
			name: "binary without FIN inside a fragmented message",
			frames: []seqFrame{
				{wsText, false, "par", false},
				{wsBinary, false, "intruder", false},
				{wsContinuation, true, "tial", false},
			},
		},
		{
			name: "new message after a ping inside a fragmented one",
			frames: []seqFrame{
				{wsBinary, false, "par", false},
				{wsPing, true, "", false},
				{wsText, true, "intruder", false},
			},
		},
		{
			name:   "continuation with nothing in progress",
			frames: []seqFrame{{wsContinuation, true, "orphan", false}},
		},
		{
			name: "continuation after a fragmented message completed",
			frames: []seqFrame{
				{wsText, false, "a", false},
				{wsContinuation, true, "b", false},
				{wsContinuation, true, "orphan", false},
			},
			before: []string{"ab"},
		},
		{
			name: "continuation after an unfragmented message",
			frames: []seqFrame{
				{wsText, true, "whole", false},
				{wsContinuation, false, "orphan", false},
			},
			before: []string{"whole"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer client.conn.Close()
			code := closeCodeFrom(client)
			go sendFrames(client.conn, true, tc.frames)

			// Bounded, so a server that accepts the bad frame and waits for
			// another fails the test instead of hanging it.
			_ = server.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			for _, want := range tc.before {
				msg, err := server.ReadMessage()
				if err != nil {
					t.Fatalf("ReadMessage before the bad frame: %v", err)
				}
				if string(msg.Data) != want {
					t.Fatalf("message %q, want %q", msg.Data, want)
				}
			}
			_, err := server.ReadMessage()
			expectFailed(t, server, err, ErrWebSocketBadFragment, code, wsCloseProtocolError)
		})
	}
}

// Fragmented messages that are in sequence are reassembled, empty fragments
// included. An empty first fragment used to leave the partial message nil,
// which then read as "no message in progress", so its continuation was
// refused.
func TestWebSocketFragmentsInSequence(t *testing.T) {
	cases := []struct {
		name   string
		frames []seqFrame
		want   []string
	}{
		{
			name: "all fragments empty",
			frames: []seqFrame{
				{wsText, false, "", false},
				{wsContinuation, false, "", false},
				{wsContinuation, true, "", false},
			},
			want: []string{""},
		},
		{
			name: "empty first fragment",
			frames: []seqFrame{
				{wsBinary, false, "", false},
				{wsContinuation, true, "data", false},
			},
			want: []string{"data"},
		},
		{
			name: "two fragmented messages back to back, pings between fragments",
			frames: []seqFrame{
				{wsText, false, "one ", false},
				{wsPing, true, "p", false},
				{wsContinuation, true, "done", false},
				{wsBinary, false, "two ", false},
				{wsContinuation, false, "", false},
				{wsPing, true, "q", false},
				{wsContinuation, true, "done", false},
				{wsText, true, "three", false},
			},
			want: []string{"one done", "two done", "three"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer server.conn.Close()
			defer client.conn.Close()
			// The pings are answered; something has to read the pongs.
			go func() {
				for {
					if _, _, _, _, err := client.readFrame(); err != nil {
						return
					}
				}
			}()
			go sendFrames(client.conn, true, tc.frames)

			_ = server.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			for _, want := range tc.want {
				msg, err := server.ReadMessage()
				if err != nil {
					t.Fatalf("ReadMessage: %v", err)
				}
				if string(msg.Data) != want {
					t.Fatalf("message %q, want %q", msg.Data, want)
				}
			}
		})
	}
}

// A text message must be valid UTF-8 (RFC 6455 §8.1); one that is not fails
// the connection with 1007 and ErrWebSocketInvalidUTF8. The check is on the
// whole message: a character split across fragments is fine, a message that
// ends mid-character is not. It is made after inflating, and binary messages
// are not checked at all.
func TestWebSocketTextMustBeUTF8(t *testing.T) {
	// "é" is C3 A9; "\xed\xa0\x80" encodes a UTF-16 surrogate, which UTF-8
	// forbids (RFC 3629 §3).
	cases := []struct {
		name    string
		deflate bool
		frames  []seqFrame
		valid   string // the message expected when the frames are valid UTF-8
	}{
		{name: "bad byte", frames: []seqFrame{{wsText, true, "ok\xffno", false}}},
		{name: "surrogate", frames: []seqFrame{{wsText, true, "\xed\xa0\x80", false}}},
		{name: "overlong encoding", frames: []seqFrame{{wsText, true, "\xc0\xaf", false}}},
		{
			name: "fragmented, ends mid-character",
			frames: []seqFrame{
				{wsText, false, "caf", false},
				{wsContinuation, true, "\xc3", false},
			},
		},
		{
			name: "fragmented, bad byte in a middle fragment",
			frames: []seqFrame{
				{wsText, false, "a", false},
				{wsContinuation, false, "\xfe", false},
				{wsContinuation, true, "b", false},
			},
		},
		{
			name:    "compressed, inflates to bad UTF-8",
			deflate: true,
			frames:  []seqFrame{{wsText, true, "", true}}, // data set below
		},
		{
			name: "character split across fragments",
			frames: []seqFrame{
				{wsText, false, "caf\xc3", false},
				{wsContinuation, true, "\xa9", false},
			},
			valid: "café",
		},
		{
			name:   "binary is not checked",
			frames: []seqFrame{{wsBinary, true, "\xff\xfe", false}},
			valid:  "\xff\xfe",
		},
		{
			name:   "valid multi-byte text",
			frames: []seqFrame{{wsText, true, "héllo, 世界 🌍", false}},
			valid:  "héllo, 世界 🌍",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer server.conn.Close()
			defer client.conn.Close()
			frames := tc.frames
			if tc.deflate {
				server.deflate = newServerDeflate(deflateOffer{}, 0)
				frames = []seqFrame{{wsText, true, string(deflateRaw(t, []byte("compressed \xff"))), true}}
			}
			code := closeCodeFrom(client)
			go sendFrames(client.conn, true, frames)

			_ = server.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			msg, err := server.ReadMessage()
			if tc.valid != "" {
				if err != nil {
					t.Fatalf("ReadMessage: %v", err)
				}
				if string(msg.Data) != tc.valid {
					t.Fatalf("message %q, want %q", msg.Data, tc.valid)
				}
				return
			}
			expectFailed(t, server, err, ErrWebSocketInvalidUTF8, code, wsCloseInvalidFramePayloadData)
		})
	}
}

// The client role (DialWebSocket) follows the same rules: a server that
// interleaves messages, or sends text that is not UTF-8, gets a (masked)
// close frame with 1002 or 1007, and the client's ReadMessage the error.
func TestDialWebSocketRefusesBadSequenceAndUTF8(t *testing.T) {
	for _, tc := range []struct {
		name     string
		frames   []seqFrame
		wantErr  error
		wantCode int
	}{
		{
			name: "interleaved",
			frames: []seqFrame{
				{wsText, false, "par", false},
				{wsText, true, "intruder", false},
			},
			wantErr:  ErrWebSocketBadFragment,
			wantCode: wsCloseProtocolError,
		},
		{
			name:     "orphan continuation",
			frames:   []seqFrame{{wsContinuation, true, "orphan", false}},
			wantErr:  ErrWebSocketBadFragment,
			wantCode: wsCloseProtocolError,
		},
		{
			name:     "invalid UTF-8",
			frames:   []seqFrame{{wsText, true, "\xff", false}},
			wantErr:  ErrWebSocketInvalidUTF8,
			wantCode: wsCloseInvalidFramePayloadData,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := make(chan int, 1)
			addr := rawDialServer(t, func(c net.Conn, br *bufio.Reader, req *http.Request) {
				_, _ = c.Write([]byte(upgradeHead(req, "")))
				sendFrames(c, false, tc.frames)
				// Read the client's frames as a server would (masked
				// required), through br: it may already hold some.
				srv := NewWSConn(c, true)
				srv.r = br
				_ = c.SetReadDeadline(time.Now().Add(waitFor))
				code <- <-closeCodeFrom(srv)
			})
			ws, _ := mustDial(t, "ws://"+addr+"/", WSDialOptions{})
			_ = ws.SetReadDeadline(time.Now().Add(waitFor))
			_, err := ws.ReadMessage()
			expectFailed(t, ws, err, tc.wantErr, code, tc.wantCode)
		})
	}
}
