package rweb

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

func TestParseDeflateOffer(t *testing.T) {
	cases := []struct {
		name, header string
		ok           bool
		response     string
	}{
		{"what browsers send", "permessage-deflate; client_max_window_bits", true,
			"permessage-deflate; client_no_context_takeover"},
		{"no parameters", "permessage-deflate", true, "permessage-deflate; client_no_context_takeover"},
		{"the client wants no server context", "permessage-deflate; server_no_context_takeover; client_no_context_takeover", true,
			"permessage-deflate; client_no_context_takeover; server_no_context_takeover"},
		{"full server window is fine", `permessage-deflate; server_max_window_bits="15"`, true,
			"permessage-deflate; client_no_context_takeover"},
		{"a smaller server window cannot be honoured", "permessage-deflate; server_max_window_bits=10", false, ""},
		{"falls through to an offer that can be", "permessage-deflate; server_max_window_bits=9, permessage-deflate", true,
			"permessage-deflate; client_no_context_takeover"},
		{"an unknown parameter declines the offer", "permessage-deflate; mystery=1", false, ""},
		{"another extension entirely", "x-webkit-deflate-frame", false, ""},
		{"nothing offered", "", false, ""},
	}
	for _, c := range cases {
		o, ok := parseDeflateOffer(c.header)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if ok && o.response() != c.response {
			t.Errorf("%s: response %q, want %q", c.name, o.response(), c.response)
		}
	}
}

// deflatePair is newTestPair with permessage-deflate negotiated the way the
// server negotiates it: server → client with context takeover, client → server
// without.
func deflatePair() (server, client *WSConn) {
	server, client = newTestPair()
	server.deflate = newServerDeflate(deflateOffer{}, 0)
	client.deflate = &wsDeflate{level: flate.BestSpeed, writeNoContext: true, readNoContext: false}
	return
}

// countingConn counts writes, to hold the framing to one write per frame.
type countingConn struct {
	net.Conn
	mu     sync.Mutex
	writes int
	bytes  int
}

func (c *countingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	c.bytes += len(p)
	c.mu.Unlock()
	return c.Conn.Write(p)
}

// Messages survive the round trip in both directions, and a stream of
// similar messages gets cheap: with context takeover the server's window
// already holds what the next message repeats.
func TestDeflateRoundTripWithContextTakeover(t *testing.T) {
	server, client := deflatePair()
	counter := &countingConn{Conn: server.conn}
	server.conn = counter

	msgs := make([]string, 40)
	for i := range msgs {
		msgs[i] = fmt.Sprintf(`{"t":"pane_diff","pane":3,"cur":{"x":%d,"y":7,"vis":true,"shape":2},"cells":[{"i":%d,"s":"%c"}]}`,
			i%80, 1000+i, 'a'+i%26)
	}
	errc := make(chan error, 1)
	go func() {
		for _, m := range msgs {
			if err := server.WriteMessage(TextMessage, []byte(m)); err != nil {
				errc <- err
				return
			}
		}
		errc <- nil
	}()
	for i, want := range msgs {
		got, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got.Type != TextMessage || string(got.Data) != want {
			t.Fatalf("message %d = %q, want %q", i, got.Data, want)
		}
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	raw := 0
	for _, m := range msgs {
		raw += len(m)
	}
	if counter.writes != len(msgs) {
		t.Errorf("%d writes for %d messages, want one each", counter.writes, len(msgs))
	}
	if counter.bytes*3 > raw {
		t.Errorf("%d bytes on the wire for %d bytes of messages — context takeover is not working", counter.bytes, raw)
	}
	t.Logf("%d messages: %d bytes raw, %d on the wire", len(msgs), raw, counter.bytes)

	// And the other way: the client's messages are independent streams.
	go func() {
		for _, m := range []string{"hello", strings.Repeat("abc", 1000), ""} {
			if err := client.WriteMessage(TextMessage, []byte(m)); err != nil {
				errc <- err
				return
			}
		}
		errc <- nil
	}()
	for _, want := range []string{"hello", strings.Repeat("abc", 1000), ""} {
		got, err := server.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Data) != want {
			t.Fatalf("client message = %q, want %q", got.Data, want)
		}
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

// A peer that asked for server_no_context_takeover gets every message as its
// own stream, readable by an inflater that keeps no window.
func TestDeflateServerNoContextTakeover(t *testing.T) {
	server, client := newTestPair()
	server.deflate = newServerDeflate(deflateOffer{serverNoContext: true}, 0)
	client.deflate = &wsDeflate{level: flate.BestSpeed, writeNoContext: true, readNoContext: true}
	msg := []byte(strings.Repeat("the same line again\n", 50))
	go func() {
		for range 3 {
			_ = server.WriteMessage(TextMessage, msg)
		}
	}()
	for i := range 3 {
		got, err := client.ReadMessage()
		if err != nil || !bytes.Equal(got.Data, msg) {
			t.Fatalf("message %d: %v, %d bytes", i, err, len(got.Data))
		}
	}
}

// writeRawRSVFrame is writeRawFrame with RSV1 set.
func writeRawRSVFrame(c net.Conn, opcode int, fin bool, data []byte) error {
	var b bytes.Buffer
	if err := writeRawFrame(&b, opcode, fin, true, data); err != nil {
		return err
	}
	frame := b.Bytes()
	frame[0] |= 0x40
	_, err := c.Write(frame)
	return err
}

func deflateRaw(t *testing.T, p []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.BestCompression)
	w.Write(p)
	w.Flush()
	return bytes.TrimSuffix(b.Bytes(), flushMarker)
}

// A compressed message split across frames: RSV1 on the first frame only, the
// whole message inflated once reassembled.
func TestDeflateFragmentedMessage(t *testing.T) {
	server, client := newTestPair()
	server.deflate = newServerDeflate(deflateOffer{}, 0)
	want := []byte(strings.Repeat("fragmented and compressed ", 40))
	comp := deflateRaw(t, want)
	half := len(comp) / 2
	go func() {
		_ = writeRawRSVFrame(client.conn, wsText, false, comp[:half])
		_ = writeRawFrame(client.conn, wsContinuation, true, true, comp[half:])
	}()
	got, err := server.ReadMessage()
	if err != nil || !bytes.Equal(got.Data, want) {
		t.Fatalf("got %q, %v", got.Data, err)
	}
}

// RSV1 means nothing without the extension, and never means anything on a
// control frame: both are protocol errors, not data.
func TestDeflateRSVRules(t *testing.T) {
	server, client := newTestPair()
	go func() { _ = writeRawRSVFrame(client.conn, wsText, true, []byte("x")) }()
	if _, err := server.ReadMessage(); !errors.Is(err, ErrWebSocketBadRSV) {
		t.Fatalf("RSV1 without the extension: %v", err)
	}

	server, client = deflatePair()
	go func() { _ = writeRawRSVFrame(client.conn, wsPing, true, nil) }()
	if _, err := server.ReadMessage(); !errors.Is(err, ErrWebSocketBadRSV) {
		t.Fatalf("RSV1 on a ping: %v", err)
	}
}

// The size limit applies to what a message inflates to, not to what it
// weighed on the wire.
func TestDeflateBombIsRefused(t *testing.T) {
	server, client := newTestPair()
	server.deflate = newServerDeflate(deflateOffer{}, 0)
	server.SetMaxMessageSize(1 << 16)
	bomb := deflateRaw(t, make([]byte, 1<<20)) // a megabyte of zeros: about a kilobyte compressed
	if int64(len(bomb)) >= 1<<16 {
		t.Fatalf("test bomb is %d bytes compressed", len(bomb))
	}
	go func() { _ = writeRawRSVFrame(client.conn, wsBinary, true, bomb) }()
	if _, err := server.ReadMessage(); !errors.Is(err, ErrWebSocketPayloadTooLarge) {
		t.Fatalf("a message inflating past the limit: %v", err)
	}
}

// WriteMessages sends a batch in one write, each still its own message.
func TestWriteMessagesBatchesIntoOneWrite(t *testing.T) {
	for _, compress := range []bool{false, true} {
		server, client := newTestPair()
		if compress {
			server, client = deflatePair()
		}
		counter := &countingConn{Conn: server.conn}
		server.conn = counter
		batch := [][]byte{[]byte("one"), []byte(strings.Repeat("two", 100)), []byte("three")}
		errc := make(chan error, 1)
		go func() { errc <- server.WriteMessages(TextMessage, batch...) }()
		for i, want := range batch {
			got, err := client.ReadMessage()
			if err != nil || !bytes.Equal(got.Data, want) {
				t.Fatalf("compress=%v message %d: %q, %v", compress, i, got.Data, err)
			}
		}
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
		if counter.writes != 1 {
			t.Fatalf("compress=%v: %d writes for a batch of %d", compress, counter.writes, len(batch))
		}
	}
}

// Every frame size class — 7-bit, 16-bit and 64-bit lengths — in one write,
// from both ends (the client masks).
func TestFramesGoOutInOneWrite(t *testing.T) {
	for _, size := range []int{0, 125, 126, 65535, 65536, 200000} {
		server, client := newTestPair()
		sc := &countingConn{Conn: server.conn}
		server.conn = sc
		cc := &countingConn{Conn: client.conn}
		client.conn = cc
		msg := bytes.Repeat([]byte{'z'}, size)
		go func() { _ = server.WriteMessage(BinaryMessage, msg) }()
		if got, err := client.ReadMessage(); err != nil || !bytes.Equal(got.Data, msg) {
			t.Fatalf("size %d server→client: %v", size, err)
		}
		go func() { _ = client.WriteMessage(BinaryMessage, msg) }()
		if got, err := server.ReadMessage(); err != nil || !bytes.Equal(got.Data, msg) {
			t.Fatalf("size %d client→server: %v", size, err)
		}
		if sc.writes != 1 || cc.writes != 1 {
			t.Fatalf("size %d: server %d writes, client %d writes; want one each", size, sc.writes, cc.writes)
		}
	}
}
