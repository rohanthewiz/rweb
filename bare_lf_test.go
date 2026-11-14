package rweb

// Bare-LF empty lines (N-077). RFC 9112 §2.2 lets a recipient take a lone LF
// as a line ending, which rweb did for every line but the empty one: a head
// written with LF throughout was a 400, and a chunked body whose trailers
// ended in a bare LF stayed open, so the next pipelined request was read as
// trailers and never answered. isBlankLine now takes "\n" as well as "\r\n".

import (
	"bufio"
	"testing"
)

func TestBareLFEndsHead(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, requestLineServer(), tr)
			conn := dialTest(t, addr, tr)
			// Two pipelined requests, LF only: both must be read and framed.
			writeAll(t, conn, "GET /xy HTTP/1.1\nHost: t\n\nGET /x HTTP/1.1\nHost: t\n\n")
			br := bufio.NewReader(conn)
			for _, want := range []string{"xy", "x"} {
				if status, body := readBody(t, br); status != 200 || body != want {
					t.Fatalf("got %d %q, want 200 %q", status, body, want)
				}
			}
		})
	}
}

// The trailers of a chunked body end at a bare-LF empty line too, so the
// request after it is answered rather than swallowed as trailer lines.
func TestBareLFEndsChunkedTrailers(t *testing.T) {
	s := limitsServer(ServerOptions{})
	addr := serveTest(t, s, transports[0])
	conn := dialTest(t, addr, transports[0])
	writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: t\r\nTransfer-Encoding: chunked\r\n\r\n"+
		"5\r\nhello\r\n0\r\nX-Trailer: 1\n\n"+
		"GET / HTTP/1.1\r\nHost: t\r\n\r\n")
	br := bufio.NewReader(conn)
	if status, body := readBody(t, br); status != 200 || body != "hello" {
		t.Fatalf("chunked POST: got %d %q, want 200 \"hello\"", status, body)
	}
	if status, body := readBody(t, br); status != 200 || body != "ok" {
		t.Fatalf("pipelined GET after bare-LF trailers: got %d %q, want 200 \"ok\"", status, body)
	}
}
