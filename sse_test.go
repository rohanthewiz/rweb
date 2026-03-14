package rweb_test

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
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

