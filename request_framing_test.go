package rweb

// Tests for request framing (N-036). Where two parsers could disagree about
// where a request's body ends, a proxy in front and rweb may each see a
// different next request (request smuggling). Every ambiguous message is
// therefore refused with 400 (or 501 for a transfer coding rweb does not
// implement) and the connection closed, rather than resolved one way.

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestFramingRejectsAmbiguousRequests(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{}), transports[0])

	const post = "POST /echo HTTP/1.1\r\nHost: test\r\n"
	for _, c := range []struct {
		name, req, status string
	}{
		{"Content-Length and Transfer-Encoding", post + "Content-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n", "400"},
		{"Transfer-Encoding and Content-Length", post + "Transfer-Encoding: chunked\r\nContent-Length: 5\r\n\r\n5\r\nhello\r\n0\r\n\r\n", "400"},
		{"negative Content-Length", post + "Content-Length: -5\r\n\r\nhello", "400"},
		{"signed Content-Length", post + "Content-Length: +5\r\n\r\nhello", "400"},
		{"Content-Length list", post + "Content-Length: 5, 5\r\n\r\nhello", "400"},
		{"empty Content-Length", post + "Content-Length:\r\n\r\nhello", "400"},
		{"hex Content-Length", post + "Content-Length: 0x5\r\n\r\nhello", "400"},
		{"two different Content-Lengths", post + "Content-Length: 4\r\nContent-Length: 5\r\n\r\nhello", "400"},
		{"space before colon", post + "Content-Length : 5\r\n\r\nhello", "400"},
		{"non-token header name", post + "X(y): z\r\nContent-Length: 5\r\n\r\nhello", "400"},
		{"empty header name", post + ": z\r\nContent-Length: 5\r\n\r\nhello", "400"},
		{"obsolete line folding", post + "X-A: one\r\n two\r\nContent-Length: 5\r\n\r\nhello", "400"},
		{"tab folding", post + "X-A: one\r\n\ttwo\r\nContent-Length: 5\r\n\r\nhello", "400"},
		{"line with no colon", post + "NoColonHere\r\nContent-Length: 5\r\n\r\nhello", "400"},
		{"unknown coding before chunked", post + "Transfer-Encoding: gzip, chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n", "501"},
		{"chunked not last", post + "Transfer-Encoding: chunked, gzip\r\n\r\n5\r\nhello\r\n0\r\n\r\n", "400"},
		{"no chunked at all", post + "Transfer-Encoding: gzip\r\n\r\nhello", "400"},
		{"two Transfer-Encoding lines", post + "Transfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n", "400"},
		{"no CRLF after chunk data", post + "Transfer-Encoding: chunked\r\n\r\n5\r\nhelloXX0\r\n\r\n", "400"},
		{"signed chunk size", post + "Transfer-Encoding: chunked\r\n\r\n+5\r\nhello\r\n0\r\n\r\n", "400"},
		{"0x chunk size", post + "Transfer-Encoding: chunked\r\n\r\n0x5\r\nhello\r\n0\r\n\r\n", "400"},
		{"empty chunk size", post + "Transfer-Encoding: chunked\r\n\r\n\r\nhello\r\n0\r\n\r\n", "400"},
		{"chunk size past int64", post + "Transfer-Encoding: chunked\r\n\r\n10000000000000000\r\nhello\r\n0\r\n\r\n", "400"},
		{"chunk size near int64 max", post + "Transfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n7ffffffffffffffe\r\nx\r\n0\r\n\r\n", "413"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialTest(t, addr, transports[0])
			writeAll(t, conn, c.req)
			got, _ := readUntilClosed(conn)
			if !strings.HasPrefix(got, "HTTP/1.1 "+c.status+" ") {
				t.Fatalf("got %q, want %s and a close", firstLine(got), c.status)
			}
		})
	}
}

// With the body cap disabled, a chunk size that cannot be added to the body
// read so far is refused rather than wrapping around.
func TestChunkSizeOverflowWithoutCap(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{MaxRequestBodySize: -1}), transports[0])
	conn := dialTest(t, addr, transports[0])
	writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n"+
		"5\r\nhello\r\n7fffffffffffffff\r\nx\r\n0\r\n\r\n")
	if got, _ := readUntilClosed(conn); !strings.HasPrefix(got, "HTTP/1.1 400 ") {
		t.Fatalf("got %q, want 400", firstLine(got))
	}
}

// Well-formed requests the rules must keep accepting.
func TestFramingAcceptsValidRequests(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{}), transports[0])

	const post = "POST /echo HTTP/1.1\r\nHost: test\r\n"
	for _, c := range []struct {
		name, req, body string
	}{
		{"repeated equal Content-Length", post + "Content-Length: 5\r\nContent-Length: 5\r\n\r\nhello", "hello"},
		{"leading zeros", post + "Content-Length: 005\r\n\r\nhello", "hello"},
		{"Transfer-Encoding case", post + "Transfer-Encoding: Chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n", "hello"},
		{"chunk extensions", post + "Transfer-Encoding: chunked\r\n\r\n5;name=value\r\nhello\r\n6 ; a ; b=\"c\"\r\n world\r\n0;last\r\n\r\n", "hello world"},
		{"upper-case hex", post + "Transfer-Encoding: chunked\r\n\r\nA\r\n0123456789\r\n0\r\n\r\n", "0123456789"},
		{"trailers", post + "Transfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\nX-Checksum: abc\r\nX-Other: d\r\n\r\n", "hello"},
		{"value with odd spacing", post + "Content-Length:5 \r\nX-Tight:v\r\n\r\nhello", "hello"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialTest(t, addr, transports[0])
			br := bufio.NewReader(conn)
			// The same connection must then serve a GET: the body and any
			// trailers were consumed exactly, so the next request line is
			// where the parser expects it.
			writeAll(t, conn, c.req+getReq("/"))
			if code, body := readBody(t, br); code != 200 || body != c.body {
				t.Fatalf("got %d %q, want 200 %q", code, body, c.body)
			}
			if code, body := readBody(t, br); code != 200 || body != "ok" {
				t.Fatalf("follow-up GET: %d %q", code, body)
			}
		})
	}
}

// Chunk extensions are accepted but cannot be used to make the framing
// outweigh the data without bound: one byte of data per chunk, each with a
// long extension, is refused once the excess passes a fixed allowance.
func TestChunkExtensionOverheadBounded(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{}), transports[0])
	conn := dialTest(t, addr, transports[0])
	ext := ";x=" + strings.Repeat("e", 1000)
	go func() {
		_, _ = io.WriteString(conn, "POST /echo HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n"+
			strings.Repeat("1"+ext+"\r\nX\r\n", 100)+"0\r\n\r\n")
	}()
	if got, _ := readUntilClosed(conn); !strings.HasPrefix(got, "HTTP/1.1 400 ") {
		t.Fatalf("got %q, want 400", firstLine(got))
	}
}

// Trailer lines are header lines: they count against MaxHeaderBytes.
func TestChunkTrailersBounded(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{MaxHeaderBytes: 1024}), transports[0])
	conn := dialTest(t, addr, transports[0])
	writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n"+
		strings.Repeat("X-Trailer: "+strings.Repeat("t", 50)+"\r\n", 30)+"\r\n")
	if got, _ := readUntilClosed(conn); !strings.HasPrefix(got, "HTTP/1.1 431 ") {
		t.Fatalf("got %q, want 431", firstLine(got))
	}
}
