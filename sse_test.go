package rweb_test

import (
	"bufio"
	"fmt"
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
