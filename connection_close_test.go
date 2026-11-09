package rweb

// Tests for persistence (N-038): which connections stay open after a
// response. HTTP/1.1 is persistent unless either side says
// "Connection: close"; HTTP/1.0 is not, unless the client asks with
// "Connection: keep-alive" (RFC 9112 §9.3). A client that is told nothing,
// or told the wrong thing, waits on a connection the server keeps open, or
// reads a body it cannot delimit.

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

func keepAliveServer() *Server {
	s := NewServer()
	s.Get("/", func(ctx Context) error { return ctx.WriteString("ok") })
	s.Get("/stream", func(ctx Context) error {
		return Stream(ctx, strings.NewReader("streamed body"), -1) // unknown size: chunked on 1.1
	})
	s.Get("/bye", func(ctx Context) error {
		ctx.Response().SetHeader("Connection", "close")
		return ctx.WriteString("bye")
	})
	return s
}

// closedAfterOne sends req and reads until the server closes, failing if it
// does not close promptly (the default IdleTimeout is minutes). It returns
// the response head and body.
func closedAfterOne(t *testing.T, addr, req string) (head, body string) {
	t.Helper()
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, req)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	raw, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("connection not closed after the response: %v (read %q)", err, raw)
	}
	head, body, _ = strings.Cut(string(raw), "\r\n\r\n")
	return head, body
}

// headerLine reports whether head carries the header line want (name
// case-insensitively).
func headerLine(head, want string) bool {
	name, value, _ := strings.Cut(want, ": ")
	for _, line := range strings.Split(head, "\r\n")[1:] {
		n, v, _ := strings.Cut(line, ": ")
		if strings.EqualFold(n, name) && v == value {
			return true
		}
	}
	return false
}

// Connection options are whole tokens in a comma-separated list, in any case.
func TestHasToken(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{
		{"close", true},
		{"Close", true},
		{"keep-alive, close", true},
		{" TE ,close ", true},
		{"x-close-later", false},
		{"closed", false},
		{"", false},
	} {
		if got := hasToken(c.v, "close"); got != c.want {
			t.Errorf("hasToken(%q, close) = %v, want %v", c.v, got, c.want)
		}
	}
}

// The connection closes after the response, which says so, when the
// request asks for it or is HTTP/1.0 without keep-alive.
func TestConnectionCloseHonoured(t *testing.T) {
	addr := serveTest(t, keepAliveServer(), transports[0])
	for _, c := range []struct{ name, req string }{
		{"1.1 close", "GET / HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n"},
		{"1.1 close in a list", "GET / HTTP/1.1\r\nHost: test\r\nConnection: TE, Close\r\n\r\n"},
		{"1.0 default", "GET / HTTP/1.0\r\nHost: test\r\n\r\n"},
		{"1.0 keep-alive and close", "GET / HTTP/1.0\r\nHost: test\r\nConnection: keep-alive\r\nConnection: close\r\n\r\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			head, body := closedAfterOne(t, addr, c.req)
			if !strings.HasPrefix(head, "HTTP/1.1 200 ") || body != "ok" {
				t.Fatalf("response %q / %q", head, body)
			}
			if !headerLine(head, "Connection: close") {
				t.Fatalf("no Connection: close in\n%s", head)
			}
		})
	}
}

// A handler that sets Connection: close gets the connection closed too
// (RFC 9112 §9.6: a server sending close MUST close after the response).
func TestHandlerConnectionCloseHonoured(t *testing.T) {
	addr := serveTest(t, keepAliveServer(), transports[0])
	head, body := closedAfterOne(t, addr, "GET /bye HTTP/1.1\r\nHost: test\r\n\r\n")
	if body != "bye" || strings.Count(strings.ToLower(head), "\r\nconnection:") != 1 {
		t.Fatalf("response %q / %q, want one Connection header", head, body)
	}
}

// Persistent connections stay open: HTTP/1.1 by default (with no
// Connection header added), and HTTP/1.0 when the client asks, in which
// case the response confirms it with Connection: keep-alive, without which
// a 1.0 client assumes the server will close.
func TestKeepAliveKept(t *testing.T) {
	addr := serveTest(t, keepAliveServer(), transports[0])
	for _, c := range []struct{ name, req, conn string }{
		{"1.1 default", "GET / HTTP/1.1\r\nHost: test\r\n\r\n", ""},
		{"1.1 keep-alive", "GET / HTTP/1.1\r\nHost: test\r\nConnection: keep-alive\r\n\r\n", ""},
		{"1.0 keep-alive", "GET / HTTP/1.0\r\nHost: test\r\nConnection: Keep-Alive\r\n\r\n", "keep-alive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialTest(t, addr, transports[0])
			br := bufio.NewReader(conn)
			for i := 0; i < 2; i++ {
				writeAll(t, conn, c.req)
				head, ok := readHead(t, br)
				if !ok {
					t.FailNow()
				}
				got := ""
				for _, line := range strings.Split(head, "\r\n") {
					if n, v, _ := strings.Cut(line, ": "); strings.EqualFold(n, "Connection") {
						got = v
					}
				}
				if got != c.conn {
					t.Fatalf("request %d: Connection %q, want %q", i, got, c.conn)
				}
				body := make([]byte, 2)
				if _, err := io.ReadFull(br, body); err != nil || string(body) != "ok" {
					t.Fatalf("request %d: body %q, %v", i, body, err)
				}
			}
		})
	}
}

// An HTTP/1.0 client cannot read chunked encoding. A body of unknown length
// goes to it unframed, with neither Content-Length nor Transfer-Encoding,
// and the close of the connection ends it, even if the client asked to keep
// the connection.
func TestHTTP10StreamUnframed(t *testing.T) {
	addr := serveTest(t, keepAliveServer(), transports[0])
	for _, c := range []struct{ name, req string }{
		{"1.0", "GET /stream HTTP/1.0\r\nHost: test\r\n\r\n"},
		{"1.0 keep-alive", "GET /stream HTTP/1.0\r\nHost: test\r\nConnection: keep-alive\r\n\r\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			head, body := closedAfterOne(t, addr, c.req)
			lower := strings.ToLower(head)
			if strings.Contains(lower, "transfer-encoding") || strings.Contains(lower, "content-length") {
				t.Fatalf("1.0 response carries framing:\n%s", head)
			}
			if !headerLine(head, "Connection: close") {
				t.Fatalf("no Connection: close in\n%s", head)
			}
			if body != "streamed body" {
				t.Fatalf("body %q", body)
			}
		})
	}

	// HTTP/1.1 still gets it chunked, on a connection that stays open.
	conn := dialTest(t, addr, transports[0])
	br := bufio.NewReader(conn)
	writeAll(t, conn, getReq("/stream")+getReq("/"))
	if code, body := readBody(t, br); code != 200 || body != "streamed body" {
		t.Fatalf("1.1 stream: %d %q", code, body)
	}
	if code, body := readBody(t, br); code != 200 || body != "ok" {
		t.Fatalf("1.1 follow-up: %d %q", code, body)
	}
}
