package rweb

// Control bytes in request header values (N-060). RFC 9110 §5.5 lets a
// recipient reject a value holding CTLs or (for CR, LF, NUL) replace them;
// rweb rejects with 400 and a close, as net/http does, so a handler never
// sees a NUL, ESC or bare CR and a proxy in front that strips them cannot
// read a different head from ours. HTAB and obs-text (bytes ≥ 0x80) stay.

import (
	"bufio"
	"strings"
	"testing"
)

func headerValueServer() *Server {
	s := NewServer()
	s.Get("/h", func(ctx Context) error { return ctx.WriteString("[" + ctx.Request().Header("X-A") + "]") })
	return s
}

func TestHeaderValueControlBytesRefused(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, headerValueServer(), tr)
			for _, c := range []struct{ name, value string }{
				{"NUL", "a\x00b"},
				{"SOH", "a\x01b"},
				{"ESC", "a\x1b[31mred"},
				{"DEL", "a\x7fb"},
				{"bare CR inside", "a\rb"},
				{"VT at the end", "a\v"},
				{"FF at the start", "\fa"},
				{"stray CR before CRLF", "a\r"},
			} {
				t.Run(c.name, func(t *testing.T) {
					conn := dialTest(t, addr, tr)
					writeAll(t, conn, "GET /h HTTP/1.1\r\nHost: t\r\nX-A: "+c.value+"\r\n\r\n")
					got, _ := readUntilClosed(conn)
					if !strings.HasPrefix(got, "HTTP/1.1 400 ") {
						t.Fatalf("got %q, want 400 and a close", firstLine(got))
					}
				})
			}
		})
	}
}

// HTAB inside a value and obs-text are kept; the optional whitespace around
// the value (SP and HTAB) is trimmed; a bare-LF line ending is still fine.
func TestHeaderValueAcceptedForms(t *testing.T) {
	addr := serveTest(t, headerValueServer(), transports[0])
	for _, c := range []struct{ name, line, want string }{
		{"tab inside", "X-A: a\tb\r\n", "[a\tb]"},
		{"OWS around", "X-A: \t a b \t\r\n", "[a b]"},
		{"UTF-8", "X-A: café\r\n", "[café]"},
		{"no space after colon", "X-A:v\r\n", "[v]"},
		{"empty", "X-A:\r\n", "[]"},
		{"bare LF ending", "X-A: v\n", "[v]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialTest(t, addr, transports[0])
			writeAll(t, conn, "GET /h HTTP/1.1\r\nHost: t\r\n"+c.line+"\r\n")
			status, body := readBody(t, bufio.NewReader(conn))
			if status != 200 || body != c.want {
				t.Fatalf("got %d %q, want 200 %q", status, body, c.want)
			}
		})
	}
}
