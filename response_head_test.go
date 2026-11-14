package rweb

// Response head conformance (N-074). The status line always has the space
// after the code and a reason from consts.StatusTextFromCode or, for the
// many codes that table lacks, http.StatusText; a 429 used to go out as
// "HTTP/1.1 429\r\n". Every response carries a Date header (RFC 9110
// §6.6.1), formatted once per second (appendDateLine), unless the handler
// set its own.

import (
	"bufio"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// headLines sends a GET for path to s and returns the response head as sent,
// status line first, without the blank line that ends it.
func headLines(t *testing.T, s *Server, path string) []string {
	t.Helper()
	conn := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	writeAll(t, conn, "GET "+path+" HTTP/1.1\r\nHost: t\r\nConnection: close\r\n\r\n")
	br := bufio.NewReader(conn)
	var lines []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading head: %v (so far %q)", err, lines)
		}
		if line == "\r\n" {
			return lines
		}
		lines = append(lines, line)
	}
}

func TestStatusLineAlwaysHasReasonSlot(t *testing.T) {
	s := NewServer()
	for _, code := range []int{200, 413, 416, 422, 429, 299} {
		s.Get("/s/"+strconv.Itoa(code), func(code int) Handler {
			return func(ctx Context) error { return ctx.SetStatus(code).WriteString("body") }
		}(code))
	}
	for _, c := range []struct {
		code int
		want string
	}{
		{200, "HTTP/1.1 200 OK\r\n"},
		{413, "HTTP/1.1 413 Request Entity Too Large\r\n"},
		{416, "HTTP/1.1 416 Range Not Satisfiable\r\n"}, // RFC 9110 name, from consts
		{422, "HTTP/1.1 422 Unprocessable Entity\r\n"},
		{429, "HTTP/1.1 429 Too Many Requests\r\n"},
		{299, "HTTP/1.1 299 \r\n"}, // no reason known: the space stays
	} {
		head := headLines(t, s, "/s/"+strconv.Itoa(c.code))
		if head[0] != c.want {
			t.Errorf("%d: status line %q, want %q", c.code, head[0], c.want)
		}
	}
}

func TestDateHeaderOnEveryResponse(t *testing.T) {
	s := NewServer()
	s.Get("/plain", func(ctx Context) error { return ctx.WriteString("hi") })
	s.Get("/own", func(ctx Context) error {
		ctx.Response().SetHeader("Date", "Sun, 06 Nov 1994 08:49:37 GMT")
		return ctx.WriteString("hi")
	})
	s.Get("/err", func(ctx Context) error { return ctx.SetStatus(404).WriteString("nope") })

	dates := func(head []string) []string {
		var d []string
		for _, l := range head[1:] {
			if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(k, "Date") {
				d = append(d, strings.TrimSpace(v))
			}
		}
		return d
	}
	for _, path := range []string{"/plain", "/err"} {
		got := dates(headLines(t, s, path))
		if len(got) != 1 {
			t.Fatalf("%s: Date lines %q, want exactly one", path, got)
		}
		when, err := http.ParseTime(got[0])
		if err != nil {
			t.Fatalf("%s: Date %q does not parse: %v", path, got[0], err)
		}
		if d := time.Since(when); d < -2*time.Second || d > 5*time.Second {
			t.Errorf("%s: Date %q is %v off the clock", path, got[0], d)
		}
	}
	if got := dates(headLines(t, s, "/own")); len(got) != 1 || got[0] != "Sun, 06 Nov 1994 08:49:37 GMT" {
		t.Errorf("handler-set Date: got %q, want only the handler's", got)
	}
}

// Within one second the same line is reused (no formatting, no allocation);
// a new second formats a new one.
func TestAppendDateLineCachesPerSecond(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 23, 30, 5, 100, time.UTC)
	a := string(appendDateLine(nil, t0))
	if a != "Date: Sat, 14 Nov 2026 23:30:05 GMT\r\n" {
		t.Fatalf("line %q", a)
	}
	later := t0.Add(800 * time.Millisecond) // same second
	if allocs := testing.AllocsPerRun(100, func() {
		buf := make([]byte, 0, 64)
		_ = appendDateLine(buf, later)
	}); allocs > 1 { // the 64-byte buffer above; the line itself is cached
		t.Errorf("%v allocations per call within one second, want only the caller's buffer", allocs)
	}
	if b := string(appendDateLine(nil, t0.Add(time.Second))); b != "Date: Sat, 14 Nov 2026 23:30:06 GMT\r\n" {
		t.Errorf("next second: %q", b)
	}
	// Any zone in, GMT out.
	est := time.FixedZone("EST", -5*3600)
	if c := string(appendDateLine(nil, t0.Add(2*time.Second).In(est))); c != "Date: Sat, 14 Nov 2026 23:30:07 GMT\r\n" {
		t.Errorf("non-UTC time: %q", c)
	}
}
