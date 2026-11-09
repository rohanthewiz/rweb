package rweb_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestSSEHandler(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	// Create event channel with buffer
	eventsChan := make(chan any, 8)

	s := rweb.NewServer(rweb.ServerOptions{
		Verbose:   true,
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	s.Get("/events", s.SSEHandler(eventsChan, "test-events"))

	go func() {
		defer close(clientDone)
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		client := &http.Client{
			Timeout: 0, // Disable timeout for SSE connection
		}

		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/events", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		defer func() {
			_ = resp.Body.Close()
		}()

		// Verify SSE headers
		assert.Equal(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"), true)
		assert.Equal(t, resp.Header.Get("Cache-Control"), "no-cache")
		assert.Equal(t, resp.Header.Get("Connection"), "keep-alive")

		// Send events in background
		go func() {
			eventsChan <- "event 1"
			eventsChan <- "event 2"
			eventsChan <- "event 3"
			close(eventsChan)
		}()

		// Read events with scanner
		scanner := bufio.NewScanner(resp.Body)
		eventsReceived := 0
		timeout := time.After(10 * time.Second)

		for eventsReceived < 3 {
			select {
			case <-timeout:
				t.Error("Test timed out waiting for events")
				return
			default:
				if !scanner.Scan() {
					if err := scanner.Err(); err != nil {
						t.Errorf("Scanner error: %v", err)
					}
					return
				}
				line := scanner.Text()
				// SSE format: "event: test-events\ndata: event N\n\n"
				if strings.HasPrefix(line, "data: ") {
					eventsReceived++
					expected := fmt.Sprintf("data: event %d", eventsReceived)
					assert.Equal(t, line, expected)
				}
			}
		}

		assert.Equal(t, eventsReceived, 3)
	}()

	_ = s.Run()

	select {
	case <-clientDone:
		// Test completed normally
	case <-time.After(15 * time.Second):
		t.Fatal("Test did not complete within timeout")
	}
}

// TestSSESenderGoroutineExitsOnChannelClose is a regression guard for a goroutine
// leak in sendSSE: the disconnect-detector goroutine (`go func(){ conn.Read(...) }`)
// only unblocks when the *client* closes its side of the conn. If sendSSE returns
// through the events-channel-closed path instead, that goroutine would otherwise
// stay parked forever. The fix sets a past-instant ReadDeadline in a defer so the
// goroutine unblocks deterministically. We assert by counting goroutines: the
// post-test count must settle back to the pre-test baseline.
func TestSSESenderGoroutineExitsOnChannelClose(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	sseDone := make(chan struct{})

	// Server closes this channel itself to trigger the events-closed exit path
	// (NOT the connGone path). The client must therefore stay connected for
	// the test to actually exercise the leak scenario.
	eventsChan := make(chan any, 1)

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	s.Get("/events", func(ctx rweb.Context) error {
		defer close(sseDone)
		return ctx.SetSSE(eventsChan, "test-events")
	})

	serverDone := make(chan struct{})
	go func() {
		_ = s.Run()
		close(serverDone)
	}()
	defer func() {
		syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-serverDone
	}()

	<-readyChan

	// Capture baseline AFTER server has spun up its accept loop, BEFORE we
	// open the SSE conn (which forks the disconnect-detector goroutine).
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	base := runtime.NumGoroutine()

	addr := fmt.Sprintf("127.0.0.1:%s", s.GetListenPort())
	conn, err := net.Dial("tcp", addr)
	assert.Nil(t, err)
	defer conn.Close() // client stays connected through the channel-close path

	reqStr := fmt.Sprintf("GET /events HTTP/1.1\r\nHost: %s\r\nAccept: text/event-stream\r\n\r\n", addr)
	_, err = conn.Write([]byte(reqStr))
	assert.Nil(t, err)

	// Wait for the response status line so we know sendSSE has started
	// (and the inner Read goroutine has been spawned).
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	assert.Nil(t, err)
	assert.Equal(t, strings.Contains(statusLine, "200"), true)

	// Trigger the channel-closed exit path, then wait for sendSSE to return.
	close(eventsChan)
	select {
	case <-sseDone:
	case <-time.After(5 * time.Second):
		t.Fatal("sendSSE did not return after events channel was closed")
	}

	// Give the deferred SetReadDeadline + WaitGroup.Wait a moment to retire
	// the inner reader goroutine and let the runtime account for it.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	// Goroutine *count* alone is too coarse here — the test client's still-open
	// conn keeps handleConnection parked, hiding the +1 from a leaked reader.
	// Instead, dump every goroutine's stack and look for one whose frames
	// include the reader closure inside sendSSE. That frame is unique to the
	// leak: no other code path produces a goroutine sitting in `sendSSE.funcN`.
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])
	for _, gstack := range strings.Split(stacks, "\n\n") {
		if strings.Contains(gstack, "rweb.(*Server).sendSSE.func") &&
			(strings.Contains(gstack, "internal/poll") ||
				strings.Contains(gstack, "net.(*conn).Read")) {
			t.Logf("leaked reader goroutine stack:\n%s", gstack)
			t.Errorf("sendSSE reader goroutine still parked after channel-close exit (baseline=%d, now=%d)",
				base, runtime.NumGoroutine())
			return
		}
	}
}

// TestSSESenderGoroutineExitsOnCloseSentinel is a second exit-path guard for
// the sendSSE goroutine cleanup: when the events channel sends the literal
// string "close", sendSSE returns *without* the channel being closed and
// without the client disconnecting. Under the bug, the inner Read goroutine
// would still be parked. Under the fix, the deferred SetReadDeadline retires
// it on every return path including this one.
func TestSSESenderGoroutineExitsOnCloseSentinel(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	sseDone := make(chan struct{})

	eventsChan := make(chan any, 1)

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	s.Get("/events", func(ctx rweb.Context) error {
		defer close(sseDone)
		return ctx.SetSSE(eventsChan, "test-events")
	})

	serverDone := make(chan struct{})
	go func() {
		_ = s.Run()
		close(serverDone)
	}()
	defer func() {
		syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-serverDone
	}()

	<-readyChan

	addr := fmt.Sprintf("127.0.0.1:%s", s.GetListenPort())
	conn, err := net.Dial("tcp", addr)
	assert.Nil(t, err)
	defer conn.Close()

	reqStr := fmt.Sprintf("GET /events HTTP/1.1\r\nHost: %s\r\nAccept: text/event-stream\r\n\r\n", addr)
	_, err = conn.Write([]byte(reqStr))
	assert.Nil(t, err)

	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	assert.Nil(t, err)
	assert.Equal(t, strings.Contains(statusLine, "200"), true)

	// Trigger the close-sentinel exit (NOT a close(eventsChan)). The handler
	// recognizes the literal string "close" and returns from the event loop.
	eventsChan <- "close"

	select {
	case <-sseDone:
	case <-time.After(5 * time.Second):
		t.Fatal("sendSSE did not return after close-sentinel event")
	}

	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])
	for _, gstack := range strings.Split(stacks, "\n\n") {
		if strings.Contains(gstack, "rweb.(*Server).sendSSE.func") &&
			(strings.Contains(gstack, "internal/poll") ||
				strings.Contains(gstack, "net.(*conn).Read")) {
			t.Logf("leaked reader goroutine stack:\n%s", gstack)
			t.Errorf("sendSSE reader goroutine still parked after close-sentinel exit")
			return
		}
	}
}

// TestSSEClientDisconnect verifies that sendSSE returns promptly (sub-second)
// when the client closes its end of the connection, rather than waiting for
// the next heartbeat or write to discover the broken pipe.
func TestSSEClientDisconnect(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	sseDone := make(chan struct{}) // closed when sendSSE returns

	// Buffered channel that we never close — the only exit path should be connGone
	eventsChan := make(chan any, 8)

	s := rweb.NewServer(rweb.ServerOptions{
		Verbose:   true,
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	s.Get("/events", func(ctx rweb.Context) error {
		defer close(sseDone)
		return ctx.SetSSE(eventsChan, "test-events")
	})

	serverDone := make(chan struct{})
	go func() {
		_ = s.Run()
		close(serverDone)
	}()

	<-readyChan

	// Open a raw TCP connection so we can close it explicitly
	addr := fmt.Sprintf("127.0.0.1:%s", s.GetListenPort())
	conn, err := net.Dial("tcp", addr)
	assert.Nil(t, err)

	// Send an HTTP GET request for SSE
	reqStr := fmt.Sprintf("GET /events HTTP/1.1\r\nHost: %s\r\nAccept: text/event-stream\r\n\r\n", addr)
	_, err = conn.Write([]byte(reqStr))
	assert.Nil(t, err)

	// Read until we see the SSE headers (status line)
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	assert.Nil(t, err)
	assert.Equal(t, strings.Contains(statusLine, "200"), true)

	// Now close the client side
	start := time.Now()
	_ = conn.Close()

	// sendSSE should detect the closed connection and return quickly
	select {
	case <-sseDone:
		elapsed := time.Since(start)
		if elapsed > 2*time.Second {
			t.Errorf("sendSSE took %v to detect client disconnect, expected < 2s", elapsed)
		}
		t.Logf("sendSSE detected client disconnect in %v", elapsed)
	case <-time.After(5 * time.Second):
		t.Fatal("sendSSE did not return within 5s after client disconnect")
	}

	// Shutdown the server
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shut down")
	}
}

// TestSSEHeadersCarryNoContentEncoding pins the one thing about an SSE response
// that no Go client can detect from the events themselves.
//
// SetSSEHeaders used to send `Content-Encoding: text/plain` — a media type in a
// slot that holds a content *coding*. Go's http client ignores Content-Encoding
// unless it is "gzip", so every Go test kept passing while browsers and curl,
// which discard a body whose coding they cannot decode, showed a connected
// stream that never delivered a byte. The only way to catch that from Go is to
// read the header, so this test asserts on the header and not on the stream:
// rweb compresses nothing, so an event stream must arrive with no
// Content-Encoding at all.
func TestSSEHeadersCarryNoContentEncoding(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	eventsChan := make(chan any, 1)

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})
	s.Get("/events", s.SSEHandler(eventsChan, "test-events"))

	serverDone := make(chan struct{})
	go func() {
		_ = s.Run()
		close(serverDone)
	}()
	defer func() {
		// Run installs its own SIGTERM handler and returns when one arrives;
		// wait for that return so the listener is closed before the test exits.
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-serverDone
	}()

	<-readyChan

	// A plain client, with no timeout: the response headers arrive as soon as
	// the stream opens, long before any event is sent, which is all we need.
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/events", s.GetListenPort()))
	assert.Nil(t, err)
	defer func() {
		close(eventsChan) // lets the sender return, closing the response body
		_ = resp.Body.Close()
	}()

	assert.Equal(t, resp.Status, consts.OK200)
	assert.Equal(t, strings.HasPrefix(resp.Header.Get(consts.HeaderContentType), consts.MIMETextEventStream), true)

	// The assertion this test exists for. Go leaves a Content-Encoding it does
	// not understand on the response untouched, so an empty value here means
	// the wire really carried none.
	assert.Equal(t, resp.Header.Get(consts.HeaderContentEncoding), "")
}

// sseLevel is a named string type with no String method, as an app might
// declare for an enum; it must go out as its text, not as a JSON string.
type sseLevel string

// sseOutcome is a plain struct; anything without a text form goes out as JSON.
type sseOutcome struct {
	ID   int
	Name string
}

// readSSEBody sends GET path on a raw connection and returns the response
// body: everything after the header block, up to the server closing the
// conn, which it does once an event stream ends.
func readSSEBody(t *testing.T, s *rweb.Server, path string) string {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
	assert.Nil(t, err)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	_, _ = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: a\r\n\r\n")
	raw, err := io.ReadAll(conn)
	assert.Nil(t, err)
	_, body, found := strings.Cut(string(raw), "\r\n\r\n")
	assert.Equal(t, true, found)
	return body
}

// TestSSEEventFormatting pins the bytes of each kind of event on the wire.
//
// sendSSE used to format every payload with %s (%+v for bare non-strings), so
// an int went out as "%!s(int=42)" and a map as "map[a:%!s(int=1)]", and it
// wrote the payload as a single data: line, so a browser kept only the text
// up to the first newline (a following line without a field name is dropped).
// Now each payload has a text form (strings and []byte as they are, error and
// fmt.Stringer through their method, numbers and bools via strconv, the rest
// as JSON), each line of it is its own data: field, and line breaks are
// removed from the event name, where they would start a forged field.
//
// The events are queued before the client connects and the stream ends with
// the "close" string sentinel, so the whole body can be read to EOF and
// compared byte for byte.
func TestSSEEventFormatting(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	events := make(chan any, 32)
	for _, e := range []any{
		rweb.SSEvent{Type: "n", Data: 42},
		map[string]int{"a": 1},
		"line1\nline2",
		[]byte("raw bytes"),
		rweb.SSEvent{Type: "m", Data: "a\r\nb\rc\n"},
		rweb.SSEvent{Type: "evil\r\nname", Data: "x"},
		rweb.SSEvent{Type: "e", Data: errors.New("boom")},
		rweb.SSEvent{Type: "d", Data: 1500 * time.Millisecond},
		rweb.SSEvent{Type: "b", Data: true},
		rweb.SSEvent{Type: "f", Data: 2.5},
		rweb.SSEvent{Type: "s", Data: sseOutcome{ID: 7, Name: "x"}},
		rweb.SSEvent{Type: "l", Data: sseLevel("warn")},
		rweb.SSEvent{Type: "c", Data: "close"}, // an event whose text is "close" does not end the stream
		7,
		"close",
		"never sent",
	} {
		events <- e
	}

	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/events", s.SSEHandler(events, "fmt"))

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		body := readSSEBody(t, s, "/events")
		want := "event: n\ndata: 42\n\n" +
			"event: fmt\ndata: {\"a\":1}\n\n" +
			"event: fmt\ndata: line1\ndata: line2\n\n" +
			"event: fmt\ndata: raw bytes\n\n" +
			// The trailing newline is kept: an empty last data line makes the
			// browser's data end in "\n", as the payload did.
			"event: m\ndata: a\ndata: b\ndata: c\ndata: \n\n" +
			"event: evilname\ndata: x\n\n" +
			"event: e\ndata: boom\n\n" +
			"event: d\ndata: 1.5s\n\n" +
			"event: b\ndata: true\n\n" +
			"event: f\ndata: 2.5\n\n" +
			"event: s\ndata: {\"ID\":7,\"Name\":\"x\"}\n\n" +
			"event: l\ndata: warn\n\n" +
			"event: c\ndata: close\n\n" +
			"event: fmt\ndata: 7\n\n"
		assert.Equal(t, want, body)
		assert.Equal(t, 1, len(events)) // the stream ended at "close"
	}()

	_ = s.Run()
}

// TestSSECloseSentinel: rweb.SSEClose on the channel ends the stream without
// sending anything for itself, like the "close" string, but cannot be confused
// with an event whose text happens to be "close".
func TestSSECloseSentinel(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	events := make(chan any, 4)
	events <- "close" + "d" // text that merely starts like the string sentinel
	events <- rweb.SSEClose
	events <- "never sent"

	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/events", s.SSEHandler(events, "x"))

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		assert.Equal(t, "event: x\ndata: closed\n\n", readSSEBody(t, s, "/events"))
		assert.Equal(t, 1, len(events))
	}()

	_ = s.Run()
}
