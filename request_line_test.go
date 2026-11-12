package rweb

// Request-line parsing (N-063). A request line may end in CRLF or, as RFC
// 9112 §2.2 lets a recipient accept, a bare LF; and it may have no version
// field at all ("GET /path", served as HTTP/1.1). The target used to be cut
// out by assuming a two-byte CRLF whenever the version was missing, so a
// bare-LF line lost the target's last byte ("GET /xy\n" was routed to /x)
// and "GET \n" sliced [4:3] and panicked. An empty target is now a 400.

import (
	"bufio"
	"strings"
	"testing"
)

func requestLineServer() *Server {
	s := NewServer()
	s.Get("/x", func(ctx Context) error { return ctx.WriteString("x") })
	s.Get("/xy", func(ctx Context) error { return ctx.WriteString("xy") })
	return s
}

// Well-formed request lines, with and without a version, ending in CRLF or
// a bare LF, all reach the full target.
func TestRequestLineTargetKeptWhole(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, requestLineServer(), tr)
			for _, c := range []struct{ name, req string }{
				{"no version, CRLF", "GET /xy\r\n\r\n"},
				{"no version, bare LF", "GET /xy\n\r\n"},
				{"version, CRLF", "GET /xy HTTP/1.1\r\nHost: t\r\n\r\n"},
				{"version, bare LF", "GET /xy HTTP/1.1\nHost: t\r\n\r\n"},
				{"HTTP/1.0, bare LF", "GET /xy HTTP/1.0\n\r\n"},
			} {
				t.Run(c.name, func(t *testing.T) {
					conn := dialTest(t, addr, tr)
					writeAll(t, conn, c.req)
					status, body := readBody(t, bufio.NewReader(conn))
					if status != 200 || body != "xy" {
						t.Fatalf("got %d %q, want 200 \"xy\"", status, body)
					}
				})
			}
		})
	}
}

// A request line whose target is empty is refused with 400 and a close,
// rather than routed (CRLF) or panicking (bare LF).
func TestRequestLineEmptyTarget(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, requestLineServer(), tr)
			for _, c := range []struct{ name, req string }{
				{"no version, bare LF", "GET \n\r\n"},
				{"no version, CRLF", "GET \r\n\r\n"},
				{"version", "GET  HTTP/1.1\r\nHost: t\r\n\r\n"},
				{"version, bare LF", "GET  HTTP/1.1\nHost: t\r\n\r\n"},
			} {
				t.Run(c.name, func(t *testing.T) {
					conn := dialTest(t, addr, tr)
					writeAll(t, conn, c.req)
					got, _ := readUntilClosed(conn)
					if !strings.HasPrefix(got, "HTTP/1.1 400 ") {
						t.Fatalf("got %q, want 400 and a close", firstLine(got))
					}
				})
			}
		})
	}
}
