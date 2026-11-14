package rweb_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

// N-054: SSE options. Access-Control-Allow-Origin used to be "*" on every
// event stream, so any website could read a stream through its visitor's
// browser (localhost and intranet servers included); it is now opt-in via
// SSECfg.AllowedOrigins. SSEvent gained ID and Retry, written as id: and
// retry: fields.

// sseOneShot answers with an event stream that ends at once: the handler
// gives each request its own channel holding only SSEClose, so the response
// can be read to EOF and its headers inspected, without a producer.
func sseOneShot(ctx rweb.Context) error {
	ch := make(chan any, 1)
	ch <- rweb.SSEClose
	return ctx.SetSSE(ch, "x")
}

// getSSEHeaders requests path on addr with the given Origin ("" for none)
// and returns the response headers. The body is closed unread: the headers
// are all a CORS check needs, and closing a body the client has not read
// to the end closes the connection, which ends any stream still open.
func getSSEHeaders(t *testing.T, addr, path, origin string) http.Header {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set(consts.HeaderOrigin, origin)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, true, strings.HasPrefix(resp.Header.Get(consts.HeaderContentType), consts.MIMETextEventStream))
	return resp.Header
}

// varyOrigin reports whether the response lists Origin under Vary.
func varyOrigin(h http.Header) bool {
	for _, v := range h.Values(consts.HeaderVary) {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), consts.HeaderOrigin) {
				return true
			}
		}
	}
	return false
}

// With no AllowedOrigins, a cross-origin request gets no
// Access-Control-Allow-Origin (the old code sent "*"), so a browser will not
// hand the stream to another site's script. Same-origin pages need none.
func TestSSECORSOffByDefault(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/events", sseOneShot)
	addr := serveProxyTest(t, s)

	for _, origin := range []string{"", "https://evil.example", "null"} {
		h := getSSEHeaders(t, addr, "/events", origin)
		assert.Equal(t, "", h.Get(consts.HeaderAccessControlAllowOrigin))
		assert.Equal(t, false, varyOrigin(h))
	}
}

// An app that sets its own CORS header (here in middleware, ahead of the
// handler) keeps it when AllowedOrigins is empty: the server only writes the
// header when it has a list of its own to apply.
func TestSSECORSKeepsAppHeaderWhenUnconfigured(t *testing.T) {
	s := rweb.NewServer()
	s.Use(func(ctx rweb.Context) error {
		ctx.Response().SetHeader(consts.HeaderAccessControlAllowOrigin, "https://mine.example")
		return ctx.Next()
	})
	s.Get("/events", sseOneShot)
	addr := serveProxyTest(t, s)

	h := getSSEHeaders(t, addr, "/events", "https://mine.example")
	assert.Equal(t, "https://mine.example", h.Get(consts.HeaderAccessControlAllowOrigin))
}

// The list's forms: "*", an exact origin (case-insensitive, echoed as the
// client sent it), a bare host[:port] under any scheme, and "null". A listed
// origin is echoed with Vary: Origin; an unlisted one gets Vary only, since
// the answer still depended on Origin; "*" needs no Vary.
func TestSSEAllowedOrigins(t *testing.T) {
	cases := []struct {
		name     string
		allowed  []string
		origin   string
		wantACAO string
		wantVary bool
	}{
		{"wildcard", []string{"*"}, "https://any.example", "*", false},
		{"wildcard without Origin", []string{"*"}, "", "*", false},
		{"exact match", []string{"https://app.example.com"}, "https://app.example.com", "https://app.example.com", true},
		{"exact match, trailing slash in list", []string{"https://app.example.com/"}, "https://app.example.com", "https://app.example.com", true},
		{"exact match ignores case", []string{"https://app.example.com"}, "https://APP.example.com", "https://APP.example.com", true},
		{"other scheme is another origin", []string{"https://app.example.com"}, "http://app.example.com", "", true},
		{"other port is another origin", []string{"https://app.example.com"}, "https://app.example.com:8443", "", true},
		{"unlisted", []string{"https://app.example.com"}, "https://evil.example", "", true},
		{"bare host, any scheme", []string{"app.example.com:8443"}, "http://app.example.com:8443", "http://app.example.com:8443", true},
		{"bare host needs the port", []string{"app.example.com"}, "https://app.example.com:8443", "", true},
		{"null listed", []string{"null"}, "null", "null", true},
		{"null not listed", []string{"https://app.example.com"}, "null", "", true},
		{"no Origin, list set", []string{"https://app.example.com"}, "", "", true},
		{"second entry matches", []string{"https://a.example", " https://b.example "}, "https://b.example", "https://b.example", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := rweb.NewServer(rweb.ServerOptions{SSECfg: rweb.SSECfg{AllowedOrigins: tc.allowed}})
			s.Get("/events", sseOneShot)
			addr := serveProxyTest(t, s)

			h := getSSEHeaders(t, addr, "/events", tc.origin)
			assert.Equal(t, tc.wantACAO, h.Get(consts.HeaderAccessControlAllowOrigin))
			assert.Equal(t, tc.wantVary, varyOrigin(h))
			// One value at most: CORS refuses a response with two.
			assert.Equal(t, true, len(h.Values(consts.HeaderAccessControlAllowOrigin)) <= 1)
		})
	}
}

// The functional option sets the same list (and keeps no alias of the
// caller's slice), WithOptions carries it over, and SSEHub routes go through
// the same SetSSE, so they follow it too.
func TestSSEAllowedOriginsOptionForms(t *testing.T) {
	origins := []string{"https://app.example.com"}
	s1 := rweb.NewServerWithOptions(rweb.WithSSEAllowedOrigins(origins...))
	origins[0] = "https://changed.example" // must not reach s1
	s2 := rweb.NewServerWithOptions(rweb.WithOptions(rweb.ServerOptions{
		SSECfg: rweb.SSECfg{AllowedOrigins: []string{"https://app.example.com"}},
	}))

	for _, s := range []*rweb.Server{s1, s2} {
		hub := rweb.NewSSEHub()
		t.Cleanup(hub.Close)
		s.Get("/events", sseOneShot)
		// The hub's stream stays open until the client leaves, which
		// getSSEHeaders does as soon as it has the headers.
		s.Get("/hub", hub.Handler(s))
		addr := serveProxyTest(t, s)

		for _, path := range []string{"/events", "/hub"} {
			h := getSSEHeaders(t, addr, path, "https://app.example.com")
			assert.Equal(t, "https://app.example.com", h.Get(consts.HeaderAccessControlAllowOrigin))
			h = getSSEHeaders(t, addr, path, "https://changed.example")
			assert.Equal(t, "", h.Get(consts.HeaderAccessControlAllowOrigin))
		}
	}
}

// streamBody sends GET path on a raw connection and returns the body:
// everything after the header block, up to the server closing the conn,
// which it does once an event stream ends. It reports failure as an error
// rather than through t, so it can run on a goroutine of its own.
func streamBody(addr, path string) (string, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: a\r\n\r\n"); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	_, body, found := strings.Cut(string(raw), "\r\n\r\n")
	if !found {
		return "", fmt.Errorf("no end of header block in %q", raw)
	}
	return body, nil
}

// readStreamBody is streamBody for the test goroutine.
func readStreamBody(t *testing.T, addr, path string) string {
	t.Helper()
	body, err := streamBody(addr, path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// ID and Retry go out as id: and retry: lines after event:, only when set.
// The id loses CR, LF (a forged field otherwise) and NUL (a browser ignores
// an id containing one); retry is whole milliseconds, rounded up so that a
// sub-millisecond value does not become 0 ("reconnect at once"). A stream
// using neither field is byte for byte what it was before they existed.
func TestSSEEventIDAndRetry(t *testing.T) {
	events := make(chan any, 16)
	for _, e := range []any{
		rweb.SSEvent{Type: "a", Data: "1", ID: "7"},
		rweb.SSEvent{Type: "b", Data: "2", Retry: 1500 * time.Millisecond},
		rweb.SSEvent{Type: "c", Data: "3", ID: "x\r\ny\x00z\n", Retry: time.Microsecond},
		rweb.SSEvent{Type: "d", Data: "4", Retry: -time.Second},
		rweb.SSEvent{Type: "e", Data: "multi\nline", ID: "42", Retry: 2*time.Second + 1},
		"bare",
		rweb.SSEClose,
	} {
		events <- e
	}

	s := rweb.NewServer()
	s.Get("/events", s.SSEHandler(events, "fmt"))
	addr := serveProxyTest(t, s)

	want := "event: a\nid: 7\ndata: 1\n\n" +
		"event: b\nretry: 1500\ndata: 2\n\n" +
		"event: c\nid: xyz\nretry: 1\ndata: 3\n\n" +
		"event: d\ndata: 4\n\n" +
		"event: e\nid: 42\nretry: 2001\ndata: multi\ndata: line\n\n" +
		"event: fmt\ndata: bare\n\n"
	assert.Equal(t, want, readStreamBody(t, addr, "/events"))
}

// SSEHandler serves every client from the one channel it was given, so with
// two clients connected each event reaches one of them, not both (the doc on
// SSEHandler says so and points to SSEHub for fan-out). This pins the
// documented behaviour: the two streams together carry each event once.
func TestSSEHandlerSharedChannelDeliversEachEventOnce(t *testing.T) {
	events := make(chan any)
	s := rweb.NewServer()
	s.Get("/events", s.SSEHandler(events, "n"))
	addr := serveProxyTest(t, s)

	type result struct {
		body string
		err  error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			body, err := streamBody(addr, "/events")
			results <- result{body, err}
		}()
	}

	// The channel is unbuffered, so each send waits for a stream to take
	// it; ten events, then one close sentinel per stream (a stream stops
	// receiving once it has taken one, so the second goes to the other).
	for i := range 10 {
		events <- rweb.SSEvent{Type: "n", Data: i}
	}
	events <- rweb.SSEClose
	events <- rweb.SSEClose

	var got []string
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		for _, ev := range strings.Split(strings.TrimSuffix(r.body, "\n\n"), "\n\n") {
			if ev != "" {
				got = append(got, ev)
			}
		}
	}
	var want []string
	for i := range 10 {
		want = append(want, "event: n\ndata: "+strconv.Itoa(i))
	}
	slices.Sort(got)
	slices.Sort(want)
	assert.Equal(t, strings.Join(want, " | "), strings.Join(got, " | "))
}
