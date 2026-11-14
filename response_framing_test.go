package rweb

import (
	"bufio"
	"io"
	"net/http/httputil"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
)

// Body framing belongs to writeResponse alone (N-073). A Content-Length or
// Transfer-Encoding the handler set used to be copied to the wire next to
// the server's own, so SetHeader("Content-Length", "999") + "hi" went out as
// "Content-Length: 2" and "Content-Length: 999": two framings, which a proxy
// in front and the client behind it may read differently (RFC 9112 §6.3).
//
// rawHead is one response's status line and header lines as sent.
type rawHead struct {
	status string
	lines  []string // "Name: value", in wire order
}

// values returns every value sent under name, in any case.
func (h rawHead) values(name string) []string {
	var out []string
	for _, l := range h.lines {
		k, v, _ := strings.Cut(l, ":")
		if strings.EqualFold(k, name) {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

// readHeadLines reads a status line and headers off br up to the blank line.
func readHeadLines(t *testing.T, br *bufio.Reader) rawHead {
	t.Helper()
	var h rawHead
	for i := 0; ; i++ {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading response head: %v (so far %q)", err, h)
		}
		line = strings.TrimSuffix(line, "\r\n")
		if line == "" {
			return h
		}
		if i == 0 {
			h.status = line
		} else {
			h.lines = append(h.lines, line)
		}
	}
}

// readLengthBody reads the body a single Content-Length announces.
func readLengthBody(t *testing.T, br *bufio.Reader, h rawHead) string {
	t.Helper()
	cl := h.values("Content-Length")
	if len(cl) != 1 {
		t.Fatalf("want one Content-Length, got %q in %q", cl, h.lines)
	}
	n, err := strconv.Atoi(cl[0])
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(br, b); err != nil {
		t.Fatalf("reading %d body bytes: %v", n, err)
	}
	return string(b)
}

// TestHandlerFramingHeadersNotSent runs every case on one keep-alive
// connection, each followed (where it has no body to read) by a GET of /ok,
// whose status line must come right after; that shows the framing the
// client was given matches the bytes sent.
func TestHandlerFramingHeadersNotSent(t *testing.T) {
	logs := &captureLogger{}
	s := NewServer(ServerOptions{Logger: logs})
	s.Get("/ok", func(ctx Context) error { return ctx.WriteString("ok") })

	// The reproduced case: a length the body does not have.
	s.Get("/cl-mismatch", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "999")
		return ctx.WriteString("hi")
	})
	// The net/http habit: the right length, set by hand (any case, added).
	s.Get("/cl-match", func(ctx Context) error {
		ctx.Response().AddHeader("content-length", "2")
		return ctx.WriteString("hi")
	})
	// Chunked asked for on a body of known length: the length wins.
	s.Get("/te", func(ctx Context) error {
		ctx.Response().SetHeader("Transfer-Encoding", "chunked")
		return ctx.WriteString("hi")
	})
	// A length set beside a stream of unknown size: the stream is chunked,
	// and a length beside chunked is the classic smuggling pair.
	s.Get("/stream", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "5")
		return Stream(ctx, strings.NewReader("hello"), -1)
	})
	// An explicit HEAD route that skips the body work and states GET's
	// length: that length is the one sent.
	s.Head("/head", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "1234")
		return nil
	})
	s.Head("/head-twice", func(ctx Context) error {
		ctx.Response().AddHeader("Content-Length", "1234")
		ctx.Response().AddHeader("Content-Length", "1234")
		return nil
	})
	s.Head("/head-spaces", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", " 1234\t")
		return nil
	})
	s.Head("/head-invalid", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "12x")
		return nil
	})
	// Bodyless statuses state no length at all, the handler's included.
	s.Get("/304", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "1234")
		ctx.SetStatus(304)
		return nil
	})
	s.Get("/204", func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "5")
		ctx.Response().SetHeader("Transfer-Encoding", "chunked")
		return ctx.NoContent()
	})

	addr := serveTest(t, s, transport{name: "tcp"})
	c := dialTest(t, addr, transport{name: "tcp"})
	br := bufio.NewReader(c)

	// expectNext checks that the next response on the connection starts
	// right where the previous one's framing said it would.
	expectNext := func(name string) {
		t.Helper()
		writeAll(t, c, getReq("/ok"))
		h := readHeadLines(t, br)
		if !strings.HasPrefix(h.status, "HTTP/1.1 200") {
			t.Fatalf("%s: the next response began %q; the framing was off", name, h.status)
		}
		assert.Equal(t, "ok", readLengthBody(t, br, h))
	}

	for _, tc := range []struct {
		method, path string
		wantCL       []string // every Content-Length line expected, nil for none
		wantBody     string   // read by the Content-Length; "" with none to read
	}{
		{"GET", "/cl-mismatch", []string{"2"}, "hi"},
		{"GET", "/cl-match", []string{"2"}, "hi"},
		{"GET", "/te", []string{"2"}, "hi"},
		{"HEAD", "/cl-mismatch", []string{"2"}, ""}, // GET's handler: the body's length wins
		{"HEAD", "/head", []string{"1234"}, ""},
		{"HEAD", "/head-twice", []string{"1234"}, ""},
		{"HEAD", "/head-spaces", []string{"1234"}, ""},
		{"HEAD", "/head-invalid", []string{"0"}, ""},
		{"GET", "/304", nil, ""},
		{"GET", "/204", nil, ""},
	} {
		name := tc.method + " " + tc.path
		writeAll(t, c, tc.method+" "+tc.path+" HTTP/1.1\r\nHost: test\r\n\r\n")
		h := readHeadLines(t, br)
		if got, want := strings.Join(h.values("Content-Length"), ","), strings.Join(tc.wantCL, ","); got != want {
			t.Errorf("%s: Content-Length lines %q, want %q\n%q", name, got, want, h.lines)
		}
		if te := h.values("Transfer-Encoding"); len(te) != 0 {
			t.Errorf("%s: Transfer-Encoding sent: %q", name, te)
		}
		if tc.wantBody != "" {
			if got := readLengthBody(t, br, h); got != tc.wantBody {
				t.Errorf("%s: body %q, want %q", name, got, tc.wantBody)
			}
		}
		expectNext(name)
	}

	// The unknown-size stream is chunked, with no length beside it.
	writeAll(t, c, getReq("/stream"))
	h := readHeadLines(t, br)
	assert.Equal(t, 0, len(h.values("Content-Length")))
	assert.Equal(t, "chunked", strings.Join(h.values("Transfer-Encoding"), ","))
	body, err := io.ReadAll(httputil.NewChunkedReader(br))
	assert.Nil(t, err)
	assert.Equal(t, "hello", string(body))
	// NewChunkedReader stops at the last chunk; the CRLF that ends the
	// (empty) trailer section is still to be read.
	crlf, err := br.ReadString('\n')
	assert.Nil(t, err)
	assert.Equal(t, "\r\n", crlf)
	expectNext("GET /stream")

	// To HTTP/1.0 the same stream goes unframed, closed by the connection's
	// end: still no length.
	writeAll(t, c, "GET /stream HTTP/1.0\r\nHost: test\r\n\r\n")
	h = readHeadLines(t, br)
	assert.Equal(t, 0, len(h.values("Content-Length")))
	assert.Equal(t, 0, len(h.values("Transfer-Encoding")))
	rest, err := io.ReadAll(br)
	assert.Nil(t, err)
	assert.Equal(t, "hello", string(rest))

	// A length the body does not have is a warning naming both values; a
	// matching one, a Transfer-Encoding and a bodyless status's length are
	// not (Debug is off, so they leave no line at all), and neither is the
	// HEAD length that was used.
	warns := 0
	logs.mu.Lock()
	for _, l := range logs.lines {
		if l.level != "warn" {
			continue
		}
		warns++
		switch l.kv["path"] {
		case "/cl-mismatch":
			assert.Equal(t, "999", l.kv["set"])
			assert.Equal(t, "2", l.kv["sent"])
		case "/head-invalid":
			assert.Equal(t, "12x", l.kv["set"])
			assert.Equal(t, "0", l.kv["sent"])
		default:
			t.Errorf("unexpected warning: %s %v", l.msg, l.kv)
		}
	}
	logs.mu.Unlock()
	// GET and HEAD of /cl-mismatch, and /head-invalid.
	if warns != 3 {
		t.Errorf("%d warnings, want 3:\n%s", warns, logs.String())
	}
}

// Under Debug, a dropped framing header that is not worth a warning is
// still reported, once per header.
func TestHandlerFramingHeaderDebugLine(t *testing.T) {
	logs := &captureLogger{}
	s := NewServer(ServerOptions{Logger: logs, Debug: true})
	s.Get("/te", func(ctx Context) error {
		ctx.Response().SetHeader("Transfer-Encoding", "chunked")
		return ctx.WriteString("hi")
	})
	resp := s.Request("GET", "/te", nil, nil)
	assert.Equal(t, "hi", string(resp.Body()))

	l, found := logs.find("debug", "framing header set by the handler")
	if !found {
		t.Fatalf("no debug line for the dropped header:\n%s", logs.String())
	}
	assert.Equal(t, "Transfer-Encoding", l.kv["name"])
	assert.Equal(t, "chunked", l.kv["value"])
	assert.Equal(t, 0, logs.levels()["warn"])
}

// A WebSocket's 101 carries no framing header either, even one a
// middleware set on every response before the route upgraded.
func TestWebSocketUpgradeOmitsFramingHeaders(t *testing.T) {
	s := NewServer()
	s.Use(func(ctx Context) error {
		ctx.Response().SetHeader("Content-Length", "0")
		ctx.Response().SetHeader("Transfer-Encoding", "chunked")
		ctx.Response().SetHeader("X-Kept", "yes")
		return ctx.Next()
	})
	s.WebSocket("/ws", func(ws *WSConn) error { return ws.Close(wsCloseNormalClosure, "") })

	addr := serveTest(t, s, transport{name: "tcp"})
	c := dialTest(t, addr, transport{name: "tcp"})
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c)

	var req strings.Builder
	req.WriteString("GET /ws HTTP/1.1\r\n")
	for _, h := range handshakeHeaders("") {
		req.WriteString(h.Key + ": " + h.Value + "\r\n")
	}
	req.WriteString("\r\n")
	writeAll(t, c, req.String())

	h := readHeadLines(t, br)
	if !strings.HasPrefix(h.status, "HTTP/1.1 101") {
		t.Fatalf("status line %q, want a 101", h.status)
	}
	if len(h.values("Content-Length")) != 0 || len(h.values("Transfer-Encoding")) != 0 {
		t.Errorf("the 101 carries a framing header: %q", h.lines)
	}
	assert.Equal(t, "yes", strings.Join(h.values("X-Kept"), ","))
}
