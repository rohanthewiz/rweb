package rweb_test

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// trackedReader is a body stream that records whether it was read and closed,
// so a test can tell a dropped stream from one that was sent.
type trackedReader struct {
	r      io.Reader
	read   atomic.Bool
	closed atomic.Bool
}

func (tr *trackedReader) Read(p []byte) (int, error) { tr.read.Store(true); return tr.r.Read(p) }
func (tr *trackedReader) Close() error               { tr.closed.Store(true); return nil }

// readRawHead reads one response's status line and headers off br, up to and
// including the blank line, and returns them without the final CRLF pair.
// It runs on the client goroutine, where t.Fatal is not allowed, so a read
// error is reported with t.Errorf and ok=false for the caller to stop on.
func readRawHead(t *testing.T, br *bufio.Reader) (head string, ok bool) {
	t.Helper()
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Errorf("reading response head: %v (so far %q)", err, sb.String())
			return sb.String(), false
		}
		if line == "\r\n" {
			return sb.String(), true
		}
		sb.WriteString(line)
	}
}

// TestBodylessStatusesOverWire pins the framing of 1xx, 204 and 304
// responses. RFC 9110 §8.6 forbids Content-Length on a 1xx or 204, and on a
// 304 it may only state the representation's length, which rweb does not
// know there, so `Content-Length: 0` was wrong on all three. None of them
// has a body either (RFC 9112 §6.3), so a body the handler wrote, or a
// pending stream, must not be sent: a client reads the next response
// straight after the header block, and stray body bytes would be read as
// that response's status line.
//
// Every case runs on one keep-alive connection and is followed by a GET of
// /ok, whose status line must come right after the bodyless response's
// head; that is what shows no body bytes were sent.
func TestBodylessStatusesOverWire(t *testing.T) {
	dir := t.TempDir()
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	file := filepath.Join(dir, "page.txt")
	assert.Nil(t, os.WriteFile(file, []byte("cached page"), 0o644))
	assert.Nil(t, os.Chtimes(file, modTime, modTime))
	ims := modTime.Format(http.TimeFormat)

	stream204 := &trackedReader{r: strings.NewReader("stream body")}
	chunked204 := &trackedReader{r: strings.NewReader("chunked body")}

	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/ok", okHandler)
	s.Get("/no-content", func(ctx rweb.Context) error { return ctx.NoContent() })
	s.Get("/204-with-body", func(ctx rweb.Context) error {
		ctx.SetStatus(204)
		return ctx.WriteString("must not be sent")
	})
	s.Get("/204-with-stream", func(ctx rweb.Context) error {
		ctx.SetStatus(204)
		return rweb.Stream(ctx, stream204, int64(len("stream body")))
	})
	s.Get("/204-with-chunked-stream", func(ctx rweb.Context) error {
		ctx.SetStatus(204)
		return rweb.Stream(ctx, chunked204, -1)
	})
	s.Get("/304-with-body", func(ctx rweb.Context) error {
		ctx.SetStatus(304)
		return ctx.WriteString("must not be sent")
	})
	s.Get("/1xx", func(ctx rweb.Context) error {
		ctx.SetStatus(199) // a 1xx a handler might set by mistake as its final status
		return ctx.WriteString("must not be sent")
	})
	s.Get("/send", func(ctx rweb.Context) error { return rweb.SendFile(ctx, file) })
	s.StaticFilesAbs("/static/", dir, 1) // same 304 path as StaticFiles; the temp dir is absolute

	go func() {
		defer stopRun(s)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		br := bufio.NewReader(conn)

		for _, c := range []struct {
			method, path, extra, status string
		}{
			{"GET", "/no-content", "", "204"},
			{"GET", "/204-with-body", "", "204"},
			{"GET", "/204-with-stream", "", "204"},
			{"GET", "/204-with-chunked-stream", "", "204"},
			{"GET", "/304-with-body", "", "304"},
			{"GET", "/1xx", "", "199"},
			{"GET", "/send", "If-Modified-Since: " + ims + "\r\n", "304"},
			{"GET", "/static/page.txt", "If-Modified-Since: " + ims + "\r\n", "304"},
			{"HEAD", "/no-content", "", "204"},
		} {
			_, _ = io.WriteString(conn, c.method+" "+c.path+" HTTP/1.1\r\nHost: a\r\n"+c.extra+"\r\n")
			head, ok := readRawHead(t, br)
			if !ok {
				return
			}
			name := c.method + " " + c.path
			if !strings.HasPrefix(head, "HTTP/1.1 "+c.status) {
				t.Errorf("%s: status line %q, want %s", name, strings.SplitN(head, "\r\n", 2)[0], c.status)
			}
			lower := strings.ToLower(head)
			if strings.Contains(lower, "\r\ncontent-length:") {
				t.Errorf("%s: response carries Content-Length:\n%s", name, head)
			}
			if strings.Contains(lower, "\r\ntransfer-encoding:") {
				t.Errorf("%s: response carries Transfer-Encoding:\n%s", name, head)
			}

			// The next response must start right here; leaked body bytes
			// would sit in front of its status line.
			_, _ = io.WriteString(conn, "GET /ok HTTP/1.1\r\nHost: a\r\n\r\n")
			next, ok := readRawHead(t, br)
			if !ok {
				return
			}
			if !strings.HasPrefix(next, "HTTP/1.1 200") {
				// The connection is out of step now; later cases would only
				// report the same leak.
				t.Errorf("%s: a body leaked; the next response began %q", name, next)
				return
			}
			okBody := make([]byte, len("ok"))
			_, err := io.ReadFull(br, okBody)
			assert.Nil(t, err)
			assert.Equal(t, "ok", string(okBody))
		}

		// The pending streams were dropped unread, and released.
		assert.Equal(t, false, stream204.read.Load())
		assert.Equal(t, true, stream204.closed.Load())
		assert.Equal(t, false, chunked204.read.Load())
		assert.Equal(t, true, chunked204.closed.Load())

		// Other statuses keep their framing: a 200 and its HEAD still state
		// the length.
		_, _ = io.WriteString(conn, "HEAD /ok HTTP/1.1\r\nHost: a\r\n\r\n")
		head, ok := readRawHead(t, br)
		assert.Equal(t, true, ok)
		assert.Equal(t, true, strings.Contains(head, "\r\nContent-Length: 2\r\n"))
	}()

	_ = s.Run()
}

// An event stream set up on a 204 (EventSource treats a 204 as "stop
// reconnecting", so a handler may set one after SetSSE) is not streamed: the
// stream would be a body. The headers are the whole response, the SSE cleanup
// runs, no event is consumed, and the connection closes, as for a HEAD.
func TestSSEOnBodylessStatusSendsNoStream(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	events := make(chan any, 1)
	events <- "should-not-be-sent"

	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/events", func(ctx rweb.Context) error {
		if err := ctx.SetSSE(events, "update"); err != nil {
			return err
		}
		ctx.SetStatus(204)
		return nil
	})

	go func() {
		defer stopRun(s)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

		_, _ = io.WriteString(conn, "GET /events HTTP/1.1\r\nHost: a\r\n\r\n")
		raw, err := io.ReadAll(conn) // returns only once the server closes
		assert.Nil(t, err)
		head, rest, found := strings.Cut(string(raw), "\r\n\r\n")
		assert.Equal(t, true, found)
		assert.Equal(t, true, strings.HasPrefix(head, "HTTP/1.1 204"))
		assert.Equal(t, "", rest)
		assert.Equal(t, 1, len(events))
	}()

	_ = s.Run()
}
