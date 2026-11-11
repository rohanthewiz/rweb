package rweb

// writeResponse sends the header block and a small in-memory body in one
// Write (one syscall on plain TCP, one record on TLS) rather than two. These
// tests watch the writes themselves, through a net.Conn wrapper that logs
// every Write the server makes, since the bytes a client reads are the same
// either way. The cases that must keep the header block on its own (HEAD,
// bodyless statuses, streams, chunked, HTTP/1.0 unframed, a large body) are
// checked here too, for their write pattern; their wire format is covered
// by bodyless_status_test.go, stream_body_test.go and
// connection_close_test.go.

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeLog is the list of Writes made to one connection, each a copy of the
// bytes passed.
type writeLog struct {
	mu     sync.Mutex
	writes [][]byte
}

func (l *writeLog) add(p []byte) {
	l.mu.Lock()
	l.writes = append(l.writes, bytes.Clone(p))
	l.mu.Unlock()
}

func (l *writeLog) snapshot() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.writes...)
}

// recordingConn logs each Write before passing it on. Wrapping the conn
// hides *net.TCPConn, so a sized stream is copied with ordinary Writes
// rather than sendfile, and those Writes are logged too.
type recordingConn struct {
	net.Conn
	log *writeLog
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.log.add(p)
	return c.Conn.Write(p)
}

// serveRecorded serves s through handleConnection, wrapping each accepted
// connection in a recordingConn, and hands the test each connection's log
// in the order they were accepted.
func serveRecorded(t *testing.T, s *Server) (addr string, logs <-chan *writeLog) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	ch := make(chan *writeLog, 64)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			l := &writeLog{}
			ch <- l
			go s.handleConnection(&recordingConn{Conn: c, log: l})
		}
	}()
	return ln.Addr().String(), ch
}

// recordedRoundTrip sends raw on a new connection, reads one response to it with
// net/http's parser (told the method, so a HEAD has no body), and returns
// the bytes that arrived and the server's log of Writes for that
// connection. Once the client has read the whole response, every Write
// that produced it has been logged, since the log is written first.
func recordedRoundTrip(t *testing.T, addr string, logs <-chan *writeLog, method, raw string) ([]byte, *http.Response, [][]byte) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, raw); err != nil {
		t.Fatal(err)
	}

	var l *writeLog
	select {
	case l = <-logs:
	case <-time.After(5 * time.Second):
		t.Fatal("connection not accepted")
	}

	var wire bytes.Buffer
	br := bufio.NewReader(io.TeeReader(c, &wire))
	resp, err := http.ReadResponse(br, &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return wire.Bytes(), resp, l.snapshot()
}

// headerBlockEnd returns the length of the header block at the start of p
// (through the blank line), or -1.
func headerBlockEnd(p []byte) int {
	i := bytes.Index(p, []byte("\r\n\r\n"))
	if i < 0 {
		return -1
	}
	return i + 4
}

func TestResponseCoalescedIntoOneWrite(t *testing.T) {
	atCap := strings.Repeat("a", coalesceBodyMax)
	overCap := strings.Repeat("b", coalesceBodyMax+1)

	routes := func(s *Server) {
		s.Get("/hello", func(ctx Context) error { return ctx.WriteString("hello") })
		s.Get("/json", func(ctx Context) error { return ctx.WriteJSON(map[string]int{"answer": 42}) })
		s.Get("/at-cap", func(ctx Context) error { return ctx.WriteString(atCap) })
		s.Get("/over-cap", func(ctx Context) error { return ctx.WriteString(overCap) })
		s.Get("/empty", func(ctx Context) error { return nil })
	}

	cases := []struct {
		name   string
		opts   ServerOptions
		path   string
		body   string
		writes int // Writes the whole response takes
	}{
		{"small text", ServerOptions{}, "/hello", "hello", 1},
		{"json", ServerOptions{}, "/json", `{"answer":42}`, 1},
		{"body at the cap", ServerOptions{}, "/at-cap", atCap, 1},
		{"no body", ServerOptions{}, "/empty", "", 1},
		// Under a write timeout everything goes through deadlineWriter,
		// which must pass the single write on as one.
		{"small text, write timeout", ServerOptions{WriteTimeout: 5 * time.Second}, "/hello", "hello", 1},
		// Past the cap the copy would cost more than the syscall it saves,
		// so the body keeps its own write.
		{"body over the cap", ServerOptions{}, "/over-cap", overCap, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(tc.opts)
			routes(s)
			addr, logs := serveRecorded(t, s)

			wire, resp, writes := recordedRoundTrip(t, addr, logs, "GET",
				"GET "+tc.path+" HTTP/1.1\r\nHost: test\r\n\r\n")
			if resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if got := string(wire[headerBlockEnd(wire):]); got != tc.body {
				t.Fatalf("body: got %d bytes, want %d", len(got), len(tc.body))
			}
			if !bytes.Equal(bytes.Join(writes, nil), wire) {
				t.Fatalf("the writes logged are not the bytes received")
			}
			if len(writes) != tc.writes {
				t.Fatalf("response took %d writes (sizes %v), want %d", len(writes), writeSizes(writes), tc.writes)
			}
			if tc.writes == 2 && len(writes[0]) != headerBlockEnd(wire) {
				t.Fatalf("first write is not the header block alone (%d bytes)", len(writes[0]))
			}
		})
	}
}

// The responses that are not a header block plus an in-memory body keep
// the header block in a write of its own, as before.
func TestResponseHeaderBlockAloneWhenNotCoalesced(t *testing.T) {
	s := NewServer()
	s.Get("/hello", func(ctx Context) error { return ctx.WriteString("hello") })
	s.Get("/no-content", func(ctx Context) error {
		return ctx.SetStatus(204).WriteString("dropped")
	})
	s.Get("/not-modified", func(ctx Context) error {
		return ctx.SetStatus(304).WriteString("dropped")
	})
	s.Get("/chunked", func(ctx Context) error {
		return Stream(ctx, strings.NewReader("streamed"), -1)
	})
	addr, logs := serveRecorded(t, s)

	cases := []struct {
		name, method, raw, body string
	}{
		{"HEAD", "HEAD", "HEAD /hello HTTP/1.1\r\nHost: t\r\n\r\n", ""},
		{"204", "GET", "GET /no-content HTTP/1.1\r\nHost: t\r\n\r\n", ""},
		{"304", "GET", "GET /not-modified HTTP/1.1\r\nHost: t\r\n\r\n", ""},
		{"chunked stream", "GET", "GET /chunked HTTP/1.1\r\nHost: t\r\n\r\n", "streamed"},
		// HTTP/1.0 cannot read chunked, so the stream goes out unframed and
		// the close ends it.
		{"HTTP/1.0 unframed", "GET", "GET /chunked HTTP/1.0\r\n\r\n", "streamed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, _, writes := recordedRoundTrip(t, addr, logs, tc.method, tc.raw)
			if !bytes.Equal(bytes.Join(writes, nil), wire) {
				t.Fatalf("the writes logged are not the bytes received")
			}
			head := headerBlockEnd(wire)
			if head < 0 || len(writes) == 0 || len(writes[0]) != head {
				t.Fatalf("first write is not the header block alone (sizes %v, head %d)", writeSizes(writes), head)
			}
			if tc.body == "" && len(writes) != 1 {
				t.Fatalf("bodyless response took %d writes (sizes %v)", len(writes), writeSizes(writes))
			}
			if tc.body != "" && !bytes.Contains(wire[head:], []byte(tc.body)) {
				t.Fatalf("body %q missing from %q", tc.body, wire[head:])
			}
		})
	}
}

// A sized stream after an in-memory body: the in-memory part rides with
// the header block, and the stream follows in writes of its own (by
// sendfile, for a file on plain TCP).
func TestResponseSizedStreamAfterCoalescedBody(t *testing.T) {
	s := NewServer()
	s.Get("/mixed", func(ctx Context) error {
		if err := ctx.WriteString("pre-"); err != nil {
			return err
		}
		return Stream(ctx, strings.NewReader("streamed"), int64(len("streamed")))
	})
	addr, logs := serveRecorded(t, s)

	wire, resp, writes := recordedRoundTrip(t, addr, logs, "GET", "GET /mixed HTTP/1.1\r\nHost: t\r\n\r\n")
	if resp.ContentLength != int64(len("pre-streamed")) {
		t.Fatalf("Content-Length %d", resp.ContentLength)
	}
	head := headerBlockEnd(wire)
	if got := string(wire[head:]); got != "pre-streamed" {
		t.Fatalf("body %q", got)
	}
	if !bytes.Equal(bytes.Join(writes, nil), wire) {
		t.Fatalf("the writes logged are not the bytes received")
	}
	if len(writes) < 2 || string(writes[0][head:]) != "pre-" {
		t.Fatalf("want header block + in-memory body, then the stream (sizes %v)", writeSizes(writes))
	}
}

func writeSizes(writes [][]byte) []int {
	n := make([]int, len(writes))
	for i, w := range writes {
		n[i] = len(w)
	}
	return n
}

// A body at the coalescing cap grows respBuf to hold it, and the growth
// must be kept for the next request rather than dropped by Clean (N-041's
// retention cap) and paid for again: coalesceBodyMax, plus room for the
// headers, has to stay under maxRetainedBuf.
func TestCoalescedRespBufKeptByClean(t *testing.T) {
	if coalesceBodyMax+64<<10 > maxRetainedBuf {
		t.Fatalf("coalesceBodyMax %d leaves too little room under maxRetainedBuf %d", coalesceBodyMax, maxRetainedBuf)
	}
	s := NewServer()
	body := bytes.Repeat([]byte("c"), coalesceBodyMax)
	s.Get("/at-cap", func(ctx Context) error { return ctx.Bytes(body) })

	ctx := s.newContext()
	ctx.request.baseCtx = s.baseContext()
	s.handleRequest(ctx, "GET", "/at-cap", io.Discard)
	grown := ctx.respBuf.Cap()
	if grown < coalesceBodyMax {
		t.Fatalf("respBuf capacity %d: the body was not coalesced into it", grown)
	}

	ctx.Clean()

	if c := ctx.respBuf.Cap(); c != grown {
		t.Fatalf("respBuf capacity %d after Clean, want %d kept", c, grown)
	}
}
