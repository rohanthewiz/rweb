package rweb

// WebSocket frame reads (N-043): the buffered reader, the header parsed from
// WSConn.hdr, and the word-wise unmask. Every length encoding, odd lengths
// and mask offsets, frames split at every byte, and lead bytes that end in
// the middle of a frame header.

import (
	"bufio"
	"bytes"
	stdctx "context"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// maskRef is the plain definition of masking: b[i] ^= key[(pos+i)%4].
func maskRef(key [4]byte, pos int, b []byte) {
	for i := range b {
		b[i] ^= key[(pos+i)%4]
	}
}

// maskBytes against the definition, for every length up to a few words
// (the tail of 0-7 bytes after the 8-byte chunks, each length mod 8 more
// than once) and every starting offset in the key.
func TestMaskBytesMatchesDefinition(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	key := [4]byte{0x12, 0x34, 0x56, 0x78}
	for n := 0; n <= 70; n++ {
		for pos := 0; pos < 8; pos++ { // offsets past 3 must wrap
			src := make([]byte, n)
			rng.Read(src)

			want := append([]byte(nil), src...)
			maskRef(key, pos, want)
			got := append([]byte(nil), src...)
			next := maskBytes(key, pos, got)

			if !bytes.Equal(got, want) {
				t.Fatalf("len %d pos %d: got %x, want %x", n, pos, got, want)
			}
			if next != (pos+n)%4 {
				t.Fatalf("len %d pos %d: returned offset %d, want %d", n, pos, next, (pos+n)%4)
			}
		}
	}
}

// Masking a payload in pieces, each starting at the offset the previous
// piece returned, gives the same bytes as masking it whole, wherever the
// cuts fall — including cuts that leave the next piece unaligned to the key.
func TestMaskBytesInPieces(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for round := 0; round < 200; round++ {
		var key [4]byte
		rng.Read(key[:])
		src := make([]byte, rng.Intn(300))
		rng.Read(src)

		whole := append([]byte(nil), src...)
		maskBytes(key, 0, whole)

		pieces := append([]byte(nil), src...)
		pos, rest := 0, pieces
		for len(rest) > 0 {
			cut := 1 + rng.Intn(len(rest))
			pos = maskBytes(key, pos, rest[:cut])
			rest = rest[cut:]
		}
		if !bytes.Equal(pieces, whole) {
			t.Fatalf("round %d (len %d): masked in pieces differs from masked whole", round, len(src))
		}
		// And masking twice restores the payload.
		maskBytes(key, 0, whole)
		if !bytes.Equal(whole, src) {
			t.Fatalf("round %d: masking twice did not restore the payload", round)
		}
	}
}

// frameSizes covers each length encoding, its edges, and odd lengths in
// each, so every unmask tail length is crossed with every header size.
var frameSizes = []int{0, 1, 3, 7, 8, 9, 15, 17, 125, 126, 127, 1000, 4093, 4096, 4099, 65535, 65536, 70001}

// Masked frames of every size class, sent back to back in one write, are
// read whole and unmasked by the server side.
func TestReadFrameAllLengths(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()

	payloads := make([][]byte, len(frameSizes))
	var stream bytes.Buffer
	rng := rand.New(rand.NewSource(3))
	for i, n := range frameSizes {
		payloads[i] = make([]byte, n)
		rng.Read(payloads[i])
		if err := writeRawFrame(&stream, wsBinary, true, true, payloads[i]); err != nil {
			t.Fatal(err)
		}
	}
	go func() { _, _ = client.conn.Write(stream.Bytes()) }()

	for i, want := range payloads {
		msg, err := server.ReadMessage()
		if err != nil {
			t.Fatalf("frame %d (%d bytes): %v", i, len(want), err)
		}
		if !bytes.Equal(msg.Data, want) {
			t.Fatalf("frame %d (%d bytes): payload differs", i, len(want))
		}
	}
}

// oneByteConn delivers what is written to it one byte per Read, so every
// header field and payload is split across reads at every position.
type oneByteConn struct{ net.Conn }

func (c oneByteConn) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return c.Conn.Read(p)
}

// A frame trickling in one byte per socket read still parses: the header
// steps and the payload are io.ReadFull through the buffer, which waits for
// all the bytes each needs.
func TestReadFrameSplitAtEveryByte(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	server := NewWSConn(oneByteConn{serverConn}, true)

	var stream bytes.Buffer
	for _, n := range []int{5, 126, 300} {
		_ = writeRawFrame(&stream, wsText, true, true, bytes.Repeat([]byte{'a' + byte(n%26)}, n))
	}
	go func() { _, _ = clientConn.Write(stream.Bytes()) }()

	for _, n := range []int{5, 126, 300} {
		msg, err := server.ReadMessage()
		if err != nil {
			t.Fatalf("%d-byte frame: %v", n, err)
		}
		if !bytes.Equal(msg.Data, bytes.Repeat([]byte{'a' + byte(n%26)}, n)) {
			t.Fatalf("%d-byte frame: payload differs", n)
		}
	}
}

// readCountingConn counts socket reads and read-deadline updates.
type readCountingConn struct {
	net.Conn
	reads, deadlines atomic.Int32
}

func (c *readCountingConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(p)
}

func (c *readCountingConn) SetReadDeadline(t time.Time) error {
	c.deadlines.Add(1)
	return c.Conn.SetReadDeadline(t)
}

// A burst of small frames that arrived together is read with one socket
// read and, with server pings on, one read-deadline update — not three of
// each per frame (header, mask, payload) as before the frame buffer.
func TestReadFrameBurstIsOneSocketRead(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	cc := &readCountingConn{Conn: serverConn}
	server := NewWSConn(cc, true)
	server.applyOptions(WSOptions{PingInterval: time.Hour}) // liveness reader, no ping in the test's lifetime
	server.startKeepalive()
	defer server.finish()
	armed := cc.deadlines.Load() // the deadline set when the keepalive starts

	const n = 50
	var stream bytes.Buffer
	for i := 0; i < n; i++ {
		_ = writeRawFrame(&stream, wsText, true, true, []byte("sixteen bytes!!!"))
	}
	if stream.Len() > wsReadBufferSize {
		t.Fatalf("test burst of %d bytes does not fit one buffer", stream.Len())
	}
	go func() { _, _ = clientConn.Write(stream.Bytes()) }()

	for i := 0; i < n; i++ {
		if _, err := server.ReadMessage(); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	if r := cc.reads.Load(); r != 1 {
		t.Errorf("%d socket reads for %d buffered frames, want 1", r, n)
	}
	if d := cc.deadlines.Load() - armed; d != 1 {
		t.Errorf("%d read-deadline updates for %d buffered frames, want 1", d, n)
	}
}

// Bytes HTTP had already read past the upgrade request reach the WebSocket
// ahead of the socket, through the frame buffer, even when they end inside
// a frame header: here the upgrade request, one whole frame and the first
// three bytes of the next arrive in one write, and the rest of the second
// frame later.
func TestWebSocketLeadBytesEndMidHeader(t *testing.T) {
	ctxs := make(chan stdctx.Context, 1)
	tr := transports[0]
	c := dialTest(t, serveTest(t, wsServer(ctxs, nil), tr), tr)
	br := bufio.NewReader(c)

	second := clientFrame(t, "second, split after its first three bytes")
	writeAll(t, c, wsUpgradeReq+string(clientFrame(t, "first"))+string(second[:3]))
	ws := wsClient(t, c, br)
	expectEcho(t, ws, "first")

	time.Sleep(settle) // the server is now waiting mid-header
	writeAll(t, c, string(second[3:]))
	expectEcho(t, ws, "second, split after its first three bytes")
}

// Two connections read frames on their own goroutines at once: the frame
// buffer and header array are per connection (run with -race).
func TestReadFrameBuffersArePerConnection(t *testing.T) {
	var wg sync.WaitGroup
	for c := 0; c < 4; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server, client := newTestPair()
			defer client.conn.Close()
			go func() {
				w := bufio.NewWriter(client.conn)
				for i := 0; i < 100; i++ {
					_ = writeRawFrame(w, wsText, true, true, bytes.Repeat([]byte{'z'}, i))
				}
				_ = w.Flush()
			}()
			for i := 0; i < 100; i++ {
				msg, err := server.ReadMessage()
				if err != nil {
					t.Errorf("message %d: %v", i, err)
					return
				}
				if len(msg.Data) != i {
					t.Errorf("message %d: %d bytes, want %d", i, len(msg.Data), i)
					return
				}
			}
		}()
	}
	wg.Wait()
}
