package rweb

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// A *slog.Logger has exactly the Logger method set, so it plugs in with no
// adapter. Checked at compile time.
var _ Logger = (*slog.Logger)(nil)

// logLine is one call a captureLogger received.
type logLine struct {
	level string // "debug", "info", "warn", "error"
	msg   string
	kv    map[string]string // values as %v renders them
}

// captureLogger is a Logger that records every line for a test to assert
// on. Safe for concurrent use: server goroutines log while the test reads.
type captureLogger struct {
	mu    sync.Mutex
	lines []logLine
}

func (c *captureLogger) add(level, msg string, keyVals []any) {
	kv := make(map[string]string, len(keyVals)/2)
	for i := 0; i+1 < len(keyVals); i += 2 {
		kv[fmt.Sprint(keyVals[i])] = fmt.Sprint(keyVals[i+1])
	}
	c.mu.Lock()
	c.lines = append(c.lines, logLine{level: level, msg: msg, kv: kv})
	c.mu.Unlock()
}

func (c *captureLogger) Debug(msg string, keyVals ...any) { c.add("debug", msg, keyVals) }
func (c *captureLogger) Info(msg string, keyVals ...any)  { c.add("info", msg, keyVals) }
func (c *captureLogger) Warn(msg string, keyVals ...any)  { c.add("warn", msg, keyVals) }
func (c *captureLogger) Error(msg string, keyVals ...any) { c.add("error", msg, keyVals) }

// String renders every line received, one per line, for failure messages
// and for tests that only look for a substring.
func (c *captureLogger) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, l := range c.lines {
		fmt.Fprintf(&b, "%s %s %v\n", l.level, l.msg, l.kv)
	}
	return b.String()
}

// find returns the first line at level whose message contains msgPart.
func (c *captureLogger) find(level, msgPart string) (logLine, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		if l.level == level && strings.Contains(l.msg, msgPart) {
			return l, true
		}
	}
	return logLine{}, false
}

// levels counts the lines received at each level.
func (c *captureLogger) levels() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := map[string]int{}
	for _, l := range c.lines {
		n[l.level]++
	}
	return n
}

// mustFind fails the test unless a line at level containing msgPart has
// arrived. Used after a round trip whose response the line precedes, so no
// waiting is needed.
func (c *captureLogger) mustFind(t *testing.T, level, msgPart string) logLine {
	t.Helper()
	l, ok := c.find(level, msgPart)
	if !ok {
		t.Fatalf("no %s line containing %q; got:\n%s", level, msgPart, c.String())
	}
	return l
}

// A nil Logger selects the rohanthewiz/logger adapter; a set one is used
// as given, whichever way the options arrive, and reaches the pooled
// contexts' responses (which log dropped header names).
func TestLoggerOptionResolution(t *testing.T) {
	c := &captureLogger{}

	if got := NewServer().log; got != defaultLogger {
		t.Errorf("no Logger option: got %#v, want the default", got)
	}
	if _, ok := defaultLogger.(rohanLogger); !ok {
		t.Errorf("default logger is %T, want the rohanthewiz/logger adapter", defaultLogger)
	}
	for name, s := range map[string]*Server{
		"ServerOptions": NewServer(ServerOptions{Logger: c}),
		"WithLogger":    NewServerWithOptions(WithLogger(c)),
		"WithOptions":   NewServerWithOptions(WithOptions(ServerOptions{Logger: c})),
	} {
		if s.log != c {
			t.Errorf("%s: server logger is %#v, want the one passed", name, s.log)
		}
		if got := s.newContext().response.logger(); got != c {
			t.Errorf("%s: response logger is %#v, want the server's", name, got)
		}
	}

	// Built outside NewServer: no server, so the fallbacks apply.
	var res response
	if res.logger() != defaultLogger {
		t.Error("a response with no server should log to the default logger")
	}
	if ctxLogger(&context{}) != defaultLogger {
		t.Error("a context with no server should log to the default logger")
	}
}

// The default logger writes through rohanthewiz/logger, at the matching
// level. Its LogChan hook hands each formatted line to a channel, which
// stands in for the app's log output here.
func TestDefaultLoggerWritesThroughRohanLogger(t *testing.T) {
	ch := make(chan string, 16)
	logger.InitLog(logger.LogConfig{
		Formatter:  "text",
		LogLevel:   "debug",
		LogChanCfg: logger.LogChanCfg{Enabled: true, Ch: ch, LogLevel: "debug"},
	})
	// InitLog's settings are process-wide. Put logrus back at its own
	// default level, so rweb's debug lines from later tests stay quiet. The
	// hook stays registered (logrus cannot remove one) but sends without
	// blocking, so once nothing reads ch it only drops.
	t.Cleanup(func() { logger.SetLogLevel("info") })

	defaultLogger.Debug("rweb test debug line", "k1", "v1")
	defaultLogger.Warn("rweb test warn line", "k2", 42)
	// An error with no serr context: a plain error line.
	defaultLogger.Error("rweb test plain error", "error", errors.New("plain boom"))
	// A serr error: its attributes become fields (LogErr), the error's text
	// is the message and the server's message goes under "event".
	serrErr := serr.Wrap(errors.New("serr boom"), "userId", "42")
	defaultLogger.Error("rweb test serr error", "code", "C1", "error", serrErr)
	// Wrapped around a serr error: fields still unpacked, full text kept.
	defaultLogger.Error("rweb test wrapped error", "error", fmt.Errorf("outer: %w", serrErr))

	next := func() string {
		t.Helper()
		select {
		case l := <-ch:
			return l
		case <-time.After(waitFor):
			t.Fatal("no line from rohanthewiz/logger")
			return ""
		}
	}
	for _, want := range [][]string{
		{"level=debug", "rweb test debug line", "k1=v1"},
		{"level=warning", "rweb test warn line", "k2=42"},
		{"level=error", `msg="rweb test plain error"`, `error="plain boom"`},
		{"level=error", `msg="serr boom"`, `event="rweb test serr error"`, "code=C1", "userId=42"},
		{"level=error", `msg="serr boom"`, `event="rweb test wrapped error"`, "userId=42",
			`error="outer: serr boom"`},
	} {
		got := next()
		for _, part := range want {
			if !strings.Contains(got, part) {
				t.Errorf("line %q lacks %q", got, part)
			}
		}
	}
}

// A *slog.Logger set as the Logger receives the server's lines. A dropped
// header name is one of the lines that is always logged.
func TestSlogLoggerPlugsIn(t *testing.T) {
	var buf bytes.Buffer
	s := NewServer(ServerOptions{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	s.Get("/x", func(ctx Context) error {
		ctx.Response().SetHeader("Bad Key", "v")
		return ctx.WriteString("ok")
	})
	s.Request("GET", "/x", nil, nil)

	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "dropping response header") ||
		!strings.Contains(out, "Bad Key") {
		t.Fatalf("slog output lacks the dropped-header warning: %q", out)
	}
}

// A header name that is not a token is dropped with a warning through the
// server's logger (no longer the standard log package), whatever Debug
// says. The name is quoted, so CR or LF in it cannot start a log line of
// its own.
func TestDroppedHeaderNameWarnsThroughServerLogger(t *testing.T) {
	c := &captureLogger{}
	s := NewServer(ServerOptions{Logger: c})
	s.Get("/x", func(ctx Context) error {
		ctx.Response().AddHeader("X-Evil\r\nSet-Cookie", "a=b")
		return ctx.WriteString("ok")
	})
	resp := s.Request("GET", "/x", nil, nil)
	if resp.Header("Set-Cookie") != "" {
		t.Fatal("the bad header reached the response")
	}

	l := c.mustFind(t, "warn", "dropping response header")
	if want := `"X-Evil\r\nSet-Cookie"`; l.kv["name"] != want {
		t.Errorf("name = %s, want %s", l.kv["name"], want)
	}
}

// The default error handler logs at error level under the same code the
// page shows, so a user's report can be matched to the line.
func TestDefaultErrorHandlerLogsCode(t *testing.T) {
	c := &captureLogger{}
	s := NewServer(ServerOptions{Logger: c})
	s.Get("/fail", func(ctx Context) error { return errors.New("boom") })

	resp := s.Request("GET", "/fail", nil, nil)
	m := regexp.MustCompile(`Error code: ([\w-]+)`).FindSubmatch(resp.Body())
	if m == nil {
		t.Fatalf("no error code in page %q", resp.Body())
	}

	l := c.mustFind(t, "error", "request failed")
	if l.kv["code"] != string(m[1]) {
		t.Errorf("logged code %q, page code %q", l.kv["code"], m[1])
	}
	if l.kv["path"] != "/fail" || l.kv["method"] != "GET" || l.kv["error"] != "boom" {
		t.Errorf("line fields = %v", l.kv)
	}
}

// RequestInfo logs one info line per request through the server's logger,
// whether or not Verbose is set (adding the middleware asks for it).
func TestRequestInfoLogsThroughServerLogger(t *testing.T) {
	c := &captureLogger{}
	s := NewServer(ServerOptions{Logger: c})
	s.Use(RequestInfo)
	s.Get("/x", func(ctx Context) error { return ctx.WriteString("ok") })
	s.Request("GET", "/x", nil, nil)

	l := c.mustFind(t, "info", "request served")
	if l.kv["method"] != "GET" || l.kv["path"] != "/x" || l.kv["status"] != "200" || l.kv["duration"] == "" {
		t.Errorf("line fields = %v", l.kv)
	}
}

// SSEHub has no server, so it takes its own Logger; an event it cannot
// encode is logged there at error level.
func TestSSEHubLogger(t *testing.T) {
	c := &captureLogger{}
	hub := NewSSEHub(SSEHubOptions{Logger: c})
	defer hub.Close()

	hub.Broadcast(SSEvent{Type: "t", Data: make(chan int)}) // JSON cannot encode a chan
	l := c.mustFind(t, "error", "SSEHub broadcast event not sent")
	if l.kv["type"] != "t" {
		t.Errorf("line fields = %v", l.kv)
	}
}

// loggingServer serves the requests TestServerLogLevelsFollowOptions makes:
// a 404, a body, SSE streams ended each way the server can end one, and an
// event that cannot be encoded.
func loggingServer(opts ServerOptions) *Server {
	s := NewServer(opts)
	// The handler asks for the body: the Debug "request body" line is
	// written when a body is buffered (N-057), not for one nobody reads.
	s.Post("/echo", func(ctx Context) error {
		_ = ctx.Request().Body()
		return ctx.WriteString("ok")
	})
	s.Get("/sse-close", func(ctx Context) error {
		ch := make(chan any, 4)
		ch <- "a"
		ch <- make(chan int) // no text for it: skipped with a warning
		ch <- "b"
		ch <- SSEClose
		return ctx.SetSSE(ch, "tick")
	})
	s.Get("/sse-closed", func(ctx Context) error {
		ch := make(chan any, 1)
		ch <- "a"
		close(ch)
		return ctx.SetSSE(ch, "tick")
	})
	return s
}

// exchange sends req on a new connection and reads until the server closes
// it (each request asks for that, and an SSE stream ends that way), so any
// line the server logs while serving req has been logged on return.
func exchange(t *testing.T, addr, req string) string {
	t.Helper()
	c := dialTest(t, addr, transport{name: "tcp"})
	writeAll(t, c, req)
	b, err := io.ReadAll(bufio.NewReader(c))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Errors and warnings are logged always; info lines only with Verbose and
// debug lines only with Debug, so a server is quiet by default whatever
// level its logger lets through. The SSE stream-end lines were printed on
// every stream before; they are info lines now, beside their siblings.
func TestServerLogLevelsFollowOptions(t *testing.T) {
	cases := []struct {
		name  string
		opts  ServerOptions
		info  bool // info lines expected
		debug bool // debug lines expected
	}{
		{name: "default", opts: ServerOptions{}},
		{name: "verbose", opts: ServerOptions{Verbose: true}, info: true},
		{name: "debug", opts: ServerOptions{Debug: true}, debug: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &captureLogger{}
			tc.opts.Logger = c
			addr := serveTest(t, loggingServer(tc.opts), transport{name: "tcp"})

			exchange(t, addr, "GET /nowhere HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
			exchange(t, addr, "POST /echo HTTP/1.1\r\nHost: test\r\nConnection: close\r\n"+
				"Content-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello")
			if out := exchange(t, addr, "GET /sse-close HTTP/1.1\r\nHost: test\r\n\r\n"); !strings.Contains(out, "data: b") {
				t.Fatalf("stream did not go on past the bad event: %q", out)
			}
			exchange(t, addr, "GET /sse-closed HTTP/1.1\r\nHost: test\r\n\r\n")

			// Always: the event with no text is a producer bug.
			c.mustFind(t, "warn", "SSE event not sent")

			n := c.levels()
			if tc.info {
				c.mustFind(t, "info", "SSE stream started")
				c.mustFind(t, "info", "SSE stream ended by close event")
				c.mustFind(t, "info", "SSE stream ended, channel closed")
			} else if n["info"] > 0 {
				t.Errorf("info lines without Verbose:\n%s", c.String())
			}
			if tc.debug {
				c.mustFind(t, "debug", "routing")
				c.mustFind(t, "debug", "no route, returning 404")
				if l := c.mustFind(t, "debug", "request body"); l.kv["body"] != "hello" {
					t.Errorf("body line fields = %v", l.kv)
				}
			} else if n["debug"] > 0 {
				t.Errorf("debug lines without Debug:\n%s", c.String())
			}
			if n["error"] > 0 {
				t.Errorf("error lines for requests that all went as meant:\n%s", c.String())
			}
		})
	}
}

// An unbuffered ReadyChan is a misconfiguration (the ready signal is sent
// without blocking, so it can be missed): warned about whatever Verbose
// says. A buffered one is not.
func TestReadyChanWithoutBufferWarns(t *testing.T) {
	c := &captureLogger{}
	NewServer(ServerOptions{Logger: c, ReadyChan: make(chan struct{})})
	c.mustFind(t, "warn", "ReadyChan has no buffer")

	c2 := &captureLogger{}
	NewServer(ServerOptions{Logger: c2, ReadyChan: make(chan struct{}, 1)})
	if out := c2.String(); out != "" {
		t.Errorf("buffered ReadyChan logged: %q", out)
	}
}

// The accept-retry warning is not gated by Debug: an accept error stops
// new clients being served. scriptedListener (graceful_shutdown_test.go)
// fails one Accept, then blocks until closed.
func TestAcceptErrorWarns(t *testing.T) {
	c := &captureLogger{}
	s := NewServer(ServerOptions{Logger: c})
	ln := newScriptedListener(nil)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()

	deadline := time.Now().Add(waitFor)
	for {
		if l, ok := c.find("warn", "accepting connection failed"); ok {
			if l.kv["error"] != errAcceptFailed.Error() || l.kv["retryIn"] == "" {
				t.Errorf("line fields = %v", l.kv)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no accept warning; got:\n%s", c.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = s.Shutdown(t.Context())
	recv(t, done, "Serve to return")
}
