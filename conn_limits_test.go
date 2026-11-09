package rweb

// Tests for the connection limits of N-035: the request-head byte budget
// (MaxHeaderBytes), the read deadlines (ReadHeaderTimeout, IdleTimeout,
// ReadTimeout) and the write deadlines (WriteTimeout, SSECfg.WriteTimeout).
//
// They are wire tests on loopback through serveTest/dialTest
// (request_context_test.go), with timeouts of tens to a few hundred
// milliseconds set through the options so the suite stays fast. Each asserts
// an upper bound on how long the server took to act, with generous slack, and
// the timeouts are chosen so that the old behaviour (no deadline at all) fails
// by hitting that bound.

import (
	"bufio"
	stdctx "context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// limitsServer is a server with the routes the limit tests share.
func limitsServer(opts ServerOptions) *Server {
	s := NewServer(opts)
	s.Get("/", func(ctx Context) error { return ctx.WriteString("ok") })
	s.Post("/echo", func(ctx Context) error { return ctx.WriteString(string(ctx.Request().Body())) })
	return s
}

// readUntilClosed reads c until the server closes it (or the dialTest safety
// deadline fires) and returns what arrived and how long that took.
func readUntilClosed(c net.Conn) (string, time.Duration) {
	start := time.Now()
	b, _ := io.ReadAll(c)
	return string(b), time.Since(start)
}

// zeroReader is an endless source of zero bytes, for large bodies that cost
// no memory.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// --- options -----------------------------------------------------------------

// Zero selects the default, negative disables, positive is taken as given.
func TestConnLimitsResolve(t *testing.T) {
	def := resolveConnLimits(ServerOptions{})
	if def.maxHeaderBytes != 1<<20 || def.readHeader != 30*time.Second || def.idle != 120*time.Second ||
		def.read != 0 || def.write != 0 || def.sseWrite != 30*time.Second {
		t.Fatalf("defaults: %+v", def)
	}

	off := resolveConnLimits(ServerOptions{
		MaxHeaderBytes: -1, ReadHeaderTimeout: -1, IdleTimeout: -1, ReadTimeout: -1, WriteTimeout: -1,
		SSECfg: SSECfg{WriteTimeout: -1},
	})
	if off != (connLimits{}) {
		t.Fatalf("all disabled: %+v", off)
	}

	set := resolveConnLimits(ServerOptions{
		MaxHeaderBytes: 2048, ReadHeaderTimeout: time.Second, IdleTimeout: 2 * time.Second,
		ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second, SSECfg: SSECfg{WriteTimeout: 5 * time.Second},
	})
	want := connLimits{maxHeaderBytes: 2048, readHeader: time.Second, idle: 2 * time.Second,
		read: 3 * time.Second, write: 4 * time.Second, sseWrite: 5 * time.Second}
	if set != want {
		t.Fatalf("explicit: %+v, want %+v", set, want)
	}
}

// Each With… option sets its field, and WithOptions carries them all over.
func TestConnLimitOptions(t *testing.T) {
	var o ServerOptions
	for _, opt := range []ServerOption{
		WithMaxHeaderBytes(4096),
		WithReadHeaderTimeout(time.Second),
		WithIdleTimeout(2 * time.Second),
		WithReadTimeout(3 * time.Second),
		WithWriteTimeout(4 * time.Second),
		WithSSEWriteTimeout(5 * time.Second),
	} {
		opt(&o)
	}
	if o.MaxHeaderBytes != 4096 || o.ReadHeaderTimeout != time.Second || o.IdleTimeout != 2*time.Second ||
		o.ReadTimeout != 3*time.Second || o.WriteTimeout != 4*time.Second || o.SSECfg.WriteTimeout != 5*time.Second {
		t.Fatalf("With… options: %+v", o)
	}

	var copied ServerOptions
	WithOptions(o)(&copied)
	if copied.MaxHeaderBytes != o.MaxHeaderBytes || copied.ReadHeaderTimeout != o.ReadHeaderTimeout ||
		copied.IdleTimeout != o.IdleTimeout || copied.ReadTimeout != o.ReadTimeout ||
		copied.WriteTimeout != o.WriteTimeout || copied.SSECfg.WriteTimeout != o.SSECfg.WriteTimeout {
		t.Fatalf("WithOptions dropped a limit: %+v", copied)
	}
}

// --- header budget -----------------------------------------------------------

// A single 2 MB header line used to be buffered whole and answered normally.
// With the default 1 MB budget it is refused with 431 and the connection
// closed, after reading little more than the budget.
func TestHeaderOverDefaultBudgetIs431(t *testing.T) {
	c := dialTest(t, serveTest(t, limitsServer(ServerOptions{}), transports[0]), transports[0])
	go func() {
		// The server stops reading part-way; the write error that follows is
		// expected and ignored.
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: test\r\nX-Big: "+strings.Repeat("a", 2<<20)+"\r\n\r\n")
	}()
	got, _ := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 431 ") {
		t.Fatalf("response %q, want a 431", firstLine(got))
	}
}

// The budget covers the request line and every header line together, not
// each line on its own.
func TestHeaderBudgetCoversAllLines(t *testing.T) {
	s := limitsServer(ServerOptions{MaxHeaderBytes: 1024})
	addr := serveTest(t, s, transports[0])

	line := "X-Filler: " + strings.Repeat("f", 38) + "\r\n" // 50 bytes

	// About 800 bytes in all: within the budget.
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "GET / HTTP/1.1\r\nHost: test\r\n"+strings.Repeat(line, 15)+"\r\n")
	if code, body := readBody(t, bufio.NewReader(c)); code != 200 || body != "ok" {
		t.Fatalf("within budget: %d %q", code, body)
	}

	// About 1500 bytes, no line over 50: over the budget.
	c = dialTest(t, addr, transports[0])
	writeAll(t, c, "GET / HTTP/1.1\r\nHost: test\r\n"+strings.Repeat(line, 30)+"\r\n")
	if got, _ := readUntilClosed(c); !strings.HasPrefix(got, "HTTP/1.1 431 ") {
		t.Fatalf("over budget: %q, want a 431", firstLine(got))
	}
}

// A negative MaxHeaderBytes disables the budget.
func TestHeaderBudgetDisabled(t *testing.T) {
	c := dialTest(t, serveTest(t, limitsServer(ServerOptions{MaxHeaderBytes: -1}), transports[0]), transports[0])
	go func() {
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: test\r\nX-Big: "+strings.Repeat("a", 3<<19)+"\r\n\r\n")
	}()
	if code, body := readBody(t, bufio.NewReader(c)); code != 200 || body != "ok" {
		t.Fatalf("budget disabled: %d %q", code, body)
	}
}

// --- read deadlines ----------------------------------------------------------

// A connection that never sends a byte is closed after ReadHeaderTimeout,
// silently: there is no request to answer. Over TLS the handshake runs inside
// that first read, so the same deadline bounds a client that never finishes
// (here: never starts) its handshake.
func TestSilentConnectionClosed(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, limitsServer(ServerOptions{ReadHeaderTimeout: 100 * time.Millisecond}), tr)
			c, err := net.Dial("tcp", addr) // raw TCP even for TLS: no handshake is sent
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			got, took := readUntilClosed(c)
			if got != "" || took > time.Second {
				t.Fatalf("got %q after %v; want a silent close after about 100ms", got, took)
			}
		})
	}
}

// Requests spaced inside IdleTimeout are all served on the one connection
// (the deadline is renewed per request); once the client goes quiet for
// longer, the server closes the connection without a response.
func TestIdleTimeoutKeepAlive(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, limitsServer(ServerOptions{IdleTimeout: 80 * time.Millisecond}), tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)
			for i := 0; i < 3; i++ {
				if i > 0 {
					time.Sleep(50 * time.Millisecond) // each gap within the idle window, together past it
				}
				writeAll(t, c, getReq("/"))
				if code, body := readBody(t, br); code != 200 || body != "ok" {
					t.Fatalf("request %d: %d %q", i, code, body)
				}
			}
			start := time.Now()
			rest, err := io.ReadAll(br)
			if took := time.Since(start); err != nil || len(rest) != 0 || took > time.Second {
				t.Fatalf("idle close: %q, %v after %v; want a silent close after about 80ms", rest, err, took)
			}
		})
	}
}

// Half a request head and then silence: 408 once ReadHeaderTimeout runs out.
func TestPartialHeadIs408(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{ReadHeaderTimeout: 100 * time.Millisecond}), transports[0])
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "GET / HTTP/1.1\r\nHost: test\r\nX-Part")
	got, took := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 408 ") || took > time.Second {
		t.Fatalf("got %q after %v; want a 408 after about 100ms", firstLine(got), took)
	}
}

// ReadHeaderTimeout bounds the whole head, not each line: a client trickling
// one header line at a time (slowloris) still runs into it.
func TestSlowHeadIs408(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{ReadHeaderTimeout: 200 * time.Millisecond}), transports[0])
	c := dialTest(t, addr, transports[0])
	go func() {
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: test\r\n")
		for i := 0; i < 20; i++ {
			time.Sleep(50 * time.Millisecond)
			if _, err := io.WriteString(c, "X-Drip: 1\r\n"); err != nil {
				return
			}
		}
	}()
	got, took := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 408 ") || took > time.Second {
		t.Fatalf("got %q after %v; want a 408 after about 200ms", firstLine(got), took)
	}
}

// The head deadline does not carry over to the body: with ReadTimeout off
// (the default) a body may take longer than ReadHeaderTimeout to arrive.
func TestHeadDeadlineNotAppliedToBody(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{ReadHeaderTimeout: 50 * time.Millisecond}), transports[0])
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "POST /echo HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\n01234")
	time.Sleep(150 * time.Millisecond)
	writeAll(t, c, "56789")
	if code, body := readBody(t, bufio.NewReader(c)); code != 200 || body != "0123456789" {
		t.Fatalf("slow body: %d %q", code, body)
	}
}

// ReadTimeout bounds the body: a body that stops short gets 408.
func TestReadTimeoutBoundsBody(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{ReadTimeout: 100 * time.Millisecond}), transports[0])
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, "POST /echo HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\n012")
	got, took := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 408 ") || took > time.Second {
		t.Fatalf("got %q after %v; want a 408 after about 100ms", firstLine(got), took)
	}
}

// The read deadline is cleared before the handler runs. A handler slower than
// every read timeout must not find its request context cancelled: the
// disconnect watch reads the conn while the handler runs, and a deadline left
// over from the head would end that read as if the client had gone.
func TestReadDeadlineClearedForHandler(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer(ServerOptions{ReadHeaderTimeout: 30 * time.Millisecond, IdleTimeout: 30 * time.Millisecond})
			s.Get("/slow", func(ctx Context) error {
				rc := ctx.Request().Context() // starts the watch
				time.Sleep(100 * time.Millisecond)
				if rc.Err() != nil {
					return ctx.WriteString("cancelled")
				}
				return ctx.WriteString("live")
			})
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)
			for i := 0; i < 2; i++ { // the second request follows an idle-deadline wait
				writeAll(t, c, getReq("/slow"))
				if code, body := readBody(t, br); code != 200 || body != "live" {
					t.Fatalf("request %d: %d %q", i, code, body)
				}
			}
		})
	}
}

// Nor may a read deadline reach a WebSocket session: frames sent long after
// the head and idle timeouts have passed still echo.
func TestReadDeadlineClearedForWebSocket(t *testing.T) {
	s := NewServer(ServerOptions{ReadHeaderTimeout: 30 * time.Millisecond, IdleTimeout: 30 * time.Millisecond})
	s.WebSocket("/ws", func(ws *WSConn) error {
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				return nil
			}
			if err := ws.WriteMessage(msg.Type, msg.Data); err != nil {
				return nil
			}
		}
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	writeAll(t, c, wsUpgradeReq)
	ws := wsClient(t, c, br)
	time.Sleep(100 * time.Millisecond)
	if err := ws.WriteMessage(TextMessage, []byte("late")); err != nil {
		t.Fatal(err)
	}
	expectEcho(t, ws, "late")
}

// --- write deadlines ---------------------------------------------------------

// A client that stops reading frees the connection's goroutine within
// WriteTimeout, whatever kind of body is being sent. Without a write deadline
// the write blocks for as long as the client keeps the connection open.
func TestWriteTimeoutFreesStalledResponse(t *testing.T) {
	const big = 64 << 20 // far past any loopback socket buffer
	for _, c := range []struct {
		name string
		body func(ctx Context) error
	}{
		{"in-memory", func(ctx Context) error { return ctx.Bytes(make([]byte, big)) }},
		{"sized stream", func(ctx Context) error { return Stream(ctx, io.LimitReader(zeroReader{}, big), big) }},
		{"chunked stream", func(ctx Context) error { return Stream(ctx, io.LimitReader(zeroReader{}, big), -1) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctxs := make(chan stdctx.Context, 1)
			s := NewServer(ServerOptions{WriteTimeout: 100 * time.Millisecond})
			s.Get("/big", func(ctx Context) error {
				ctxs <- ctx.Request().Context() // cancelled once the response is written, or abandoned
				return c.body(ctx)
			})
			conn := dialTest(t, serveTest(t, s, transports[0]), transports[0])
			writeAll(t, conn, getReq("/big")) // and never read
			waitDone(t, recv(t, ctxs, "request context"), "after the write stalled")

			// The response is broken, so the connection must be closed rather
			// than left waiting for a next request until IdleTimeout. Reading
			// now drains what was sent and then ends (EOF or a reset).
			_ = conn.SetReadDeadline(time.Now().Add(waitFor))
			if _, err := io.Copy(io.Discard, conn); isTimeout(err) {
				t.Fatal("connection left open after the write timed out")
			}
		})
	}
}

// WriteTimeout bounds each stalled write, not the whole response: a download
// that takes several times WriteTimeout in all, but keeps moving, completes.
// The file goes out in slices, each with its own deadline, so sendfile is
// still used on plain TCP.
func TestWriteTimeoutAllowsSlowLargeDownload(t *testing.T) {
	const size = 6 << 20
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(ServerOptions{WriteTimeout: 100 * time.Millisecond})
	s.Get("/file", func(ctx Context) error { return SendFile(ctx, path) })
	s.Get("/mem", func(ctx Context) error { return ctx.Bytes(data) })
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	for _, route := range []string{"/file", "/mem"} {
		writeAll(t, c, getReq(route))
		head, ok := readHead(t, br)
		if !ok || !strings.HasPrefix(head, "HTTP/1.1 200") {
			t.Fatalf("%s: head %q", route, head)
		}
		// 1 MB every 35 ms: about 210 ms in all, twice WriteTimeout, while
		// a 1 MB slice drains in one step.
		got := make([]byte, 0, size)
		piece := make([]byte, 1<<20)
		for len(got) < size {
			n, err := io.ReadFull(br, piece[:min(len(piece), size-len(got))])
			got = append(got, piece[:n]...)
			if err != nil {
				t.Fatalf("%s: read after %d bytes: %v", route, len(got), err)
			}
			time.Sleep(35 * time.Millisecond)
		}
		if string(got) != string(data) {
			t.Fatalf("%s: body differs", route)
		}
	}
}

// The write deadline is cleared once a response is written. A WebSocket
// upgrade later on the same keep-alive connection writes its 101 straight to
// the conn; a deadline left over from the previous response would fail that
// write.
func TestWriteDeadlineClearedAfterResponse(t *testing.T) {
	s := NewServer(ServerOptions{WriteTimeout: 50 * time.Millisecond})
	s.Get("/", func(ctx Context) error { return ctx.WriteString("ok") })
	s.WebSocket("/ws", func(ws *WSConn) error {
		msg, err := ws.ReadMessage()
		if err != nil {
			return nil
		}
		return ws.WriteMessage(msg.Type, msg.Data)
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	writeAll(t, c, getReq("/"))
	if code, body := readBody(t, br); code != 200 || body != "ok" {
		t.Fatalf("first request: %d %q", code, body)
	}
	time.Sleep(150 * time.Millisecond)
	writeAll(t, c, wsUpgradeReq)
	ws := wsClient(t, c, br)
	if err := ws.WriteMessage(TextMessage, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	expectEcho(t, ws, "hi")
}

// An SSE client that stays connected but stops reading is let go once a
// flush stalls for SSECfg.WriteTimeout, which applies even with WriteTimeout
// off: the stream ends, so its cleanup runs and its request context ends.
func TestSSEWriteTimeoutFreesStalledStream(t *testing.T) {
	ctxs := make(chan stdctx.Context, 1)
	s := NewServer(ServerOptions{SSECfg: SSECfg{WriteTimeout: 100 * time.Millisecond}})
	s.Get("/events", func(ctx Context) error {
		rc := ctx.Request().Context()
		ctxs <- rc
		events := make(chan any)
		payload := strings.Repeat("e", 64<<10)
		go func() {
			for {
				select {
				case events <- payload:
				case <-rc.Done():
					return
				}
			}
		}()
		return ctx.SetSSE(events, "fill")
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	writeAll(t, c, getReq("/events")) // and never read
	waitDone(t, recv(t, ctxs, "request context"), "after the SSE flush stalled")
}

// --- helpers -----------------------------------------------------------------

// firstLine is the status line of a raw response, for failure messages.
func firstLine(raw string) string {
	line, _, _ := strings.Cut(raw, "\r\n")
	return line
}

// readHead reads a response's status line and headers off br, through the
// blank line.
func readHead(t *testing.T, br *bufio.Reader) (string, bool) {
	t.Helper()
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Errorf("reading response head: %v (so far %q)", err, sb.String())
			return sb.String(), false
		}
		if line == consts.CRLF {
			return sb.String(), true
		}
		sb.WriteString(line)
	}
}
