package rweb_test

import (
	"bufio"
	"fmt"
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

