package rweb

import (
	"bufio"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Close frames from the peer (RFC 6455 §5.5.1, §7.4), N-082: an empty close
// frame is answered with an empty one (1005 used to be echoed onto the
// wire), and a close frame that breaks the rules (1-byte body, a code that
// may not be sent, a reason that is not UTF-8) fails the connection with
// 1002 or 1007 instead of being echoed and handed to OnClose handlers.

// closeReply is the first close frame a peer read: got is false when the
// read failed before one arrived (no close frame was sent).
type closeReply struct {
	got  bool
	body []byte
}

// closeFrameFrom reads frames on peer until the first close frame and sends
// its body. Unlike closeCodeFrom it also reports a close frame with no body,
// which is the answer an empty close frame must get. On net.Pipe the peer
// must keep reading while the other side writes, or that write blocks.
func closeFrameFrom(peer *WSConn) <-chan closeReply {
	reply := make(chan closeReply, 1)
	go func() {
		for {
			opcode, _, _, data, err := peer.readFrame()
			if err != nil {
				reply <- closeReply{}
				return
			}
			if opcode == wsClose {
				reply <- closeReply{got: true, body: data}
				return
			}
		}
	}()
	return reply
}

// recvClose waits for a peer's close frame and fails the test without one.
func recvClose(t *testing.T, reply <-chan closeReply) []byte {
	t.Helper()
	select {
	case r := <-reply:
		if !r.got {
			t.Fatal("the peer got no close frame (the connection ended without one)")
		}
		return r.body
	case <-time.After(waitFor):
		t.Fatal("no close frame within the wait")
	}
	return nil
}

// closeBody is a close frame body: the code, then the reason.
func closeBody(code int, reason string) []byte {
	b := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(b, uint16(code))
	copy(b[2:], reason)
	return b
}

// A close frame with no body, as a browser's ws.close() with no arguments
// sends, is returned as a CloseMessage with no data, reported to OnClose as
// 1005 with no reason, and answered with a close frame with no body. The
// answer used to carry 1005, which RFC 6455 §7.4.1 forbids on the wire.
// An OnClose handler that answers through Close with the code it was given
// (1005) sends an empty close frame too.
func TestWebSocketEmptyCloseAnsweredEmpty(t *testing.T) {
	for _, tc := range []struct {
		name        string
		answerInHdl bool // the OnClose handler answers with Close(code, text)
	}{
		{name: "automatic answer"},
		{name: "OnClose handler answers with the code it got", answerInHdl: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer server.conn.Close()
			defer client.conn.Close()

			type got struct {
				code int
				text string
			}
			seen := make(chan got, 1)
			server.OnClose(func(code int, text string) {
				seen <- got{code, text}
				if tc.answerInHdl {
					_ = server.Close(code, text)
				}
			})
			reply := closeFrameFrom(client)
			go func() { _ = writeRawFrame(client.conn, wsClose, true, true, nil) }()

			_ = server.conn.SetReadDeadline(time.Now().Add(waitFor))
			msg, err := server.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			if msg.Type != CloseMessage || len(msg.Data) != 0 {
				t.Fatalf("got %v %q, want a CloseMessage with no data", msg.Type, msg.Data)
			}
			if g := <-seen; g.code != wsCloseNoStatusReceived || g.text != "" {
				t.Fatalf("OnClose got (%d, %q), want (1005, \"\")", g.code, g.text)
			}
			if body := recvClose(t, reply); len(body) != 0 {
				t.Fatalf("answer has body % x, want none (1005 must not be sent)", body)
			}
		})
	}
}

// Close(1005, reason) sends a close frame with no body: 1005 may not be
// sent, and a reason cannot go without a code.
func TestWebSocketCloseNoStatusSendsEmptyFrame(t *testing.T) {
	for _, reason := range []string{"", "dropped"} {
		server, client := newTestPair()
		reply := closeFrameFrom(client)
		go func() { _ = server.Close(wsCloseNoStatusReceived, reason) }()
		if body := recvClose(t, reply); len(body) != 0 {
			t.Fatalf("Close(1005, %q) sent body % x, want none", reason, body)
		}
		server.conn.Close()
		client.conn.Close()
	}
}

// Every code that may be sent is accepted, with or without a reason: the
// handler gets the code and reason, ReadMessage the frame, and the answer
// echoes the code. 1012–1014 are not in RFC 6455 but are registered with
// IANA (service restart, try again later, bad gateway).
func TestWebSocketCloseValidCodesAccepted(t *testing.T) {
	codes := []int{1000, 1001, 1002, 1003, 1007, 1008, 1009, 1010, 1011, 1012, 1013, 1014, 3000, 3999, 4000, 4999}
	reasons := []string{"", "bye", "héllo, 世界", strings.Repeat("é", 61) + "x"} // the last is 123 bytes, the most a close frame holds
	for _, code := range codes {
		for _, reason := range reasons {
			server, client := newTestPair()
			var gotCode atomic.Int64
			var gotText atomic.Value
			server.OnClose(func(c int, text string) {
				gotCode.Store(int64(c))
				gotText.Store(text)
			})
			reply := closeFrameFrom(client)
			go func() { _ = writeRawFrame(client.conn, wsClose, true, true, closeBody(code, reason)) }()

			_ = server.conn.SetReadDeadline(time.Now().Add(waitFor))
			msg, err := server.ReadMessage()
			if err != nil {
				t.Fatalf("code %d reason %q: ReadMessage: %v", code, reason, err)
			}
			if msg.Type != CloseMessage || string(msg.Data) != string(closeBody(code, reason)) {
				t.Fatalf("code %d reason %q: got %v % x", code, reason, msg.Type, msg.Data)
			}
			if int(gotCode.Load()) != code || gotText.Load() != reason {
				t.Fatalf("code %d reason %q: OnClose got (%d, %q)", code, reason, gotCode.Load(), gotText.Load())
			}
			body := recvClose(t, reply)
			if len(body) != 2 || int(binary.BigEndian.Uint16(body)) != code {
				t.Fatalf("code %d: answer body % x, want the code echoed", code, body)
			}
			server.conn.Close()
			client.conn.Close()
		}
	}
}

// A close frame that breaks RFC 6455 §5.5.1 or §7.4 fails the connection:
// ReadMessage returns ErrWebSocketBadClose, the peer gets 1002 (1007 for a
// reason that is not UTF-8) rather than its own frame echoed, Done() is
// closed, no OnClose handler runs, and a later ReadMessage returns
// ErrWebSocketAlreadyClosed at once (the peer's close frame did arrive).
func TestWebSocketBadCloseFrameFails(t *testing.T) {
	type badClose struct {
		name     string
		body     []byte
		wantCode int
	}
	cases := []badClose{{"1-byte body", []byte{0x03}, wsCloseProtocolError}}
	for _, code := range []int{0, 999, 1004, 1005, 1006, 1015, 1016, 1100, 2000, 2999, 5000, 65535} {
		cases = append(cases, badClose{"code " + strconv.Itoa(code), closeBody(code, ""), wsCloseProtocolError})
	}
	cases = append(cases,
		badClose{"unsendable code with a reason", closeBody(1006, "gone"), wsCloseProtocolError},
		badClose{"reason with a bad byte", closeBody(1000, "bye\xff"), wsCloseInvalidFramePayloadData},
		badClose{"reason with a surrogate", closeBody(1001, "\xed\xa0\x80"), wsCloseInvalidFramePayloadData},
		badClose{"reason ends mid-character", closeBody(1000, "caf\xc3"), wsCloseInvalidFramePayloadData},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer client.conn.Close()
			var handlerRan atomic.Bool
			server.OnClose(func(int, string) { handlerRan.Store(true) })
			code := closeCodeFrom(client)
			go func() { _ = writeRawFrame(client.conn, wsClose, true, true, tc.body) }()

			_ = server.conn.SetReadDeadline(time.Now().Add(waitFor))
			_, err := server.ReadMessage()
			expectFailed(t, server, err, ErrWebSocketBadClose, code, tc.wantCode)
			if handlerRan.Load() {
				t.Fatal("OnClose ran for a malformed close frame")
			}
			if _, err := server.ReadMessage(); !errors.Is(err, ErrWebSocketAlreadyClosed) {
				t.Fatalf("ReadMessage after the bad close frame: %v, want ErrWebSocketAlreadyClosed", err)
			}
		})
	}
}

// Close waiting for the peer's answer (case 2 of Close: the handler is in
// ReadMessage) is not held up by a malformed answer: ReadMessage returns
// ErrWebSocketBadClose, and Close returns at once instead of reading on
// until the 2 s close-handshake deadline.
func TestWebSocketBadCloseAnswerEndsClose(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	readErr := make(chan error, 1)
	go func() {
		_, err := server.ReadMessage()
		readErr <- err
	}()
	// The peer reads the server's close frame and answers it with 1005,
	// which may not be sent.
	go func() {
		if r := <-closeFrameFrom(client); r.got {
			_ = writeRawFrame(client.conn, wsClose, true, true, closeBody(1005, ""))
		}
	}()
	time.Sleep(20 * time.Millisecond) // let ReadMessage take the read side

	start := time.Now()
	if err := server.Close(1000, "bye"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close took %v; the peer answered at once", d)
	}
	if err := <-readErr; !errors.Is(err, ErrWebSocketBadClose) {
		t.Fatalf("ReadMessage: %v, want ErrWebSocketBadClose", err)
	}
}

// Over the wire, through an rweb server's handler loop (tcp and tls): a
// client's empty close frame, as a browser sends it, gets an empty close
// frame back and the handler a CloseMessage; a close frame carrying 1005
// gets 1002 and the handler ErrWebSocketBadClose.
func TestWebSocketCloseFramesOverTheWire(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			t.Run("empty close", func(t *testing.T) {
				ended := make(chan *WSMessage, 1)
				addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, ended), tr)
				ws, _ := mustDial(t, dialURL(addr, tr, "/ws"), dialOpts(tr))
				_ = ws.conn.SetDeadline(time.Now().Add(waitFor))
				if err := writeRawFrame(ws.conn, wsClose, true, true, nil); err != nil {
					t.Fatal(err)
				}
				if body := recvClose(t, closeFrameFrom(ws)); len(body) != 0 {
					t.Fatalf("server answered with body % x, want none", body)
				}
				got := recv(t, ended, "server handler's end")
				if got == nil || got.Type != CloseMessage || len(got.Data) != 0 {
					t.Fatalf("handler saw %+v, want an empty CloseMessage", got)
				}
			})
			t.Run("1005 on the wire", func(t *testing.T) {
				ended := make(chan *WSMessage, 1)
				addr, _ := serveShutdownTest(t, echoServer(WSOptions{}, ended), tr)
				ws, _ := mustDial(t, dialURL(addr, tr, "/ws"), dialOpts(tr))
				_ = ws.conn.SetDeadline(time.Now().Add(waitFor))
				if err := writeRawFrame(ws.conn, wsClose, true, true, closeBody(1005, "")); err != nil {
					t.Fatal(err)
				}
				body := recvClose(t, closeFrameFrom(ws))
				if len(body) != 2 || binary.BigEndian.Uint16(body) != wsCloseProtocolError {
					t.Fatalf("server answered with body % x, want 1002", body)
				}
				if got := recv(t, ended, "server handler's end"); got != nil {
					t.Fatalf("handler got %+v, want an error", got)
				}
			})
		})
	}
}

// The client role (DialWebSocket) follows the same rules: a server's empty
// close frame is answered with an empty (masked) one, and a malformed one
// fails the connection with 1002 or 1007.
func TestDialWebSocketCloseFrames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     []byte
		wantErr  error // nil: a CloseMessage, answered with an empty close frame
		wantCode int
	}{
		{name: "empty close", body: nil},
		{name: "1-byte body", body: []byte{0x03}, wantErr: ErrWebSocketBadClose, wantCode: wsCloseProtocolError},
		{name: "code 1006", body: closeBody(1006, ""), wantErr: ErrWebSocketBadClose, wantCode: wsCloseProtocolError},
		{name: "reason not UTF-8", body: closeBody(1000, "\xff"), wantErr: ErrWebSocketBadClose, wantCode: wsCloseInvalidFramePayloadData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := make(chan closeReply, 1)
			addr := rawDialServer(t, func(c net.Conn, br *bufio.Reader, req *http.Request) {
				_, _ = c.Write([]byte(upgradeHead(req, "")))
				_ = writeRawFrame(c, wsClose, true, false, tc.body)
				// Read the client's frames as a server would (masked
				// required), through br: it may already hold some.
				srv := NewWSConn(c, true)
				srv.r = br
				_ = c.SetReadDeadline(time.Now().Add(waitFor))
				reply <- <-closeFrameFrom(srv)
			})
			ws, _ := mustDial(t, "ws://"+addr+"/", WSDialOptions{})
			_ = ws.SetReadDeadline(time.Now().Add(waitFor))
			msg, err := ws.ReadMessage()
			body := recvClose(t, reply)
			if tc.wantErr == nil {
				if err != nil || msg.Type != CloseMessage || len(msg.Data) != 0 {
					t.Fatalf("ReadMessage = %+v, %v; want an empty CloseMessage", msg, err)
				}
				if len(body) != 0 {
					t.Fatalf("client answered with body % x, want none", body)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ReadMessage error = %v, want %v", err, tc.wantErr)
			}
			if len(body) != 2 || int(binary.BigEndian.Uint16(body)) != tc.wantCode {
				t.Fatalf("client answered with body % x, want %d", body, tc.wantCode)
			}
		})
	}
}
