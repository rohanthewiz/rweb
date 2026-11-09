package rweb

// Tests for Expect: 100-continue (N-037). A client that sends it waits for
// "HTTP/1.1 100 Continue" before sending the body (curl does for uploads
// over 1 MB, giving up after about a second). The point of the mechanism is
// that a request the server will refuse anyway never has its body sent.

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

const continueLine = "HTTP/1.1 100 Continue\r\n\r\n"

// expectInterim reads the 100 Continue off br, failing if it does not come
// within a short wait: the client below has not sent its body yet, so a
// server that waits for the body before answering never sends anything.
func expectInterim(t *testing.T, c interface{ SetReadDeadline(time.Time) error }, br *bufio.Reader) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	got := make([]byte, len(continueLine))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("no 100 Continue before the body was sent: %v (got %q)", err, got)
	}
	if string(got) != continueLine {
		t.Fatalf("interim response %q, want %q", got, continueLine)
	}
	_ = c.SetReadDeadline(time.Now().Add(waitFor))
}

// The interim response comes after the head and before the body is read,
// for a Content-Length body and a chunked one; the final response follows
// on the same connection as usual.
func TestExpectContinueSendsInterim(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{}), transports[0])
	for _, c := range []struct {
		name, head, body string
	}{
		{"content-length", "Content-Length: 5\r\nExpect: 100-continue\r\n", "hello"},
		{"chunked", "Transfer-Encoding: chunked\r\nExpect: 100-continue\r\n", "5\r\nhello\r\n0\r\n\r\n"},
		{"token case", "Content-Length: 5\r\nExpect: 100-Continue\r\n", "hello"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialTest(t, addr, transports[0])
			br := bufio.NewReader(conn)
			writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: test\r\n"+c.head+"\r\n")
			expectInterim(t, conn, br)
			writeAll(t, conn, c.body)
			if code, body := readBody(t, br); code != 200 || body != "hello" {
				t.Fatalf("final response: %d %q", code, body)
			}
		})
	}
}

// A body over the cap is refused with 413 straight away, with no 100 in
// front of it, so the client never sends the body.
func TestExpectContinueOverCapIs413(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{MaxRequestBodySize: 10}), transports[0])
	conn := dialTest(t, addr, transports[0])
	writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: test\r\nContent-Length: 100\r\nExpect: 100-continue\r\n\r\n")
	got, _ := readUntilClosed(conn)
	if !strings.HasPrefix(got, "HTTP/1.1 413 ") {
		t.Fatalf("got %q, want a 413 (and no 100 before it)", firstLine(got))
	}
}

// Any expectation other than 100-continue cannot be met: 417, and close.
func TestUnknownExpectationIs417(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{}), transports[0])
	conn := dialTest(t, addr, transports[0])
	writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: test\r\nContent-Length: 5\r\nExpect: something-else\r\n\r\nhello")
	got, _ := readUntilClosed(conn)
	if !strings.HasPrefix(got, "HTTP/1.1 417 ") {
		t.Fatalf("got %q, want 417", firstLine(got))
	}
}

// No interim response where none is due: a request without a body, and an
// HTTP/1.0 request (a 1.0 client cannot know a 1xx, and RFC 9110 §10.1.1
// says a server MUST ignore 100-continue from one).
func TestExpectContinueNotSentWithoutCause(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{}), transports[0])
	for _, c := range []struct{ name, req, want string }{
		{"no body", "GET / HTTP/1.1\r\nHost: test\r\nExpect: 100-continue\r\n\r\n", "ok"},
		{"HTTP/1.0", "POST /echo HTTP/1.0\r\nHost: test\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\nhello", "hello"},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialTest(t, addr, transports[0])
			writeAll(t, conn, c.req)
			br := bufio.NewReader(conn)
			status, err := br.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(status, "HTTP/1.1 200 ") {
				t.Fatalf("first status line %q, want the final 200", status)
			}
			if _, rest, _ := strings.Cut(readAllFrom(t, br), "\r\n\r\n"); rest != c.want {
				t.Fatalf("body %q, want %q", rest, c.want)
			}
		})
	}
}

// readAllFrom reads the rest of one small response: its headers and body.
// Content-Length is honoured so a keep-alive connection does not block.
func readAllFrom(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	n := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		sb.WriteString(line)
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			for _, ch := range strings.TrimSpace(v) {
				n = n*10 + int(ch-'0')
			}
		}
		if line == "\r\n" {
			break
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		t.Fatal(err)
	}
	sb.Write(body)
	return "\r\n" + sb.String()
}
