package rweb_test

// import (
// 	"bufio"
// 	"fmt"
// 	"net/http"
// 	"syscall"
// 	"testing"
// 	"time"
//
// 	"github.com/rohanthewiz/assert"
// 	"github.com/rohanthewiz/rweb"
// 	"github.com/rohanthewiz/rweb/consts"
// )
//
// func TestSSE(t *testing.T) {
// 	readyChan := make(chan struct{}, 1)
// 	clientDone := make(chan struct{})
//
// 	// Create event channel with buffer
// 	eventsChan := make(chan any, 8)
//
// 	s := rweb.NewServer(rweb.ServerOptions{
// 		Verbose:   true,
// 		ReadyChan: readyChan,
// 		Address:   "localhost:",
// 	})
//
// 	s.Get("/events", s.SetupSSE(rweb.Context, eventsChan, "test-events"))
//
// 	go func() { // Run our SSE client
// 		defer close(clientDone)
// 		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
//
// 		<-readyChan // wait for server
//
// 		// Create client with timeout
// 		client := &http.Client{
// 			Timeout: 0, // Disable timeout for SSE connection
// 		}
//
// 		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/events", s.GetListenPort()))
// 		assert.Nil(t, err)
// 		assert.Equal(t, resp.Status, consts.OK200)
//
// 		defer func() {
// 			_ = resp.Body.Close()
// 		}()
//
// 		// Verify headers
// 		assert.Equal(t, resp.Header.Get("Content-Type"), "text/event-stream")
// 		assert.Equal(t, resp.Header.Get("Cache-Control"), "no-cache")
// 		assert.Equal(t, resp.Header.Get("Connection"), "keep-alive")
//
// 		// Read events with scanner
// 		scanner := bufio.NewScanner(resp.Body)
// 		eventsReceived := 0
//
// 		// Start a timeout timer
// 		timeout := time.After(10 * time.Second)
//
// 		// Send events in background
// 		go func() {
// 			eventsChan <- "event 1"
// 			eventsChan <- "event 2"
// 			eventsChan <- "event 3"
// 			// No need to wait as closed channel just means we won't add any more events.
// 			// time.Sleep(100 * time.Millisecond)
// 			close(eventsChan)
// 		}()
//
// 		for eventsReceived < 3 {
// 			select {
// 			case <-timeout:
// 				t.Error("Test timed out waiting for events")
// 				return
// 			default:
// 				if !scanner.Scan() {
// 					if err := scanner.Err(); err != nil {
// 						t.Errorf("Scanner error: %v", err)
// 					}
// 					break
// 				}
// 				line := scanner.Text()
// 				if line != "" {
// 					eventsReceived++
// 					expected := fmt.Sprintf("data: event %d", eventsReceived)
// 					t.Logf("Received event: %s", line)
// 					assert.Equal(t, line, expected)
// 				}
// 			}
// 		}
//
// 		assert.Equal(t, eventsReceived, 3)
// 	}()
//
// 	_ = s.Run()
//
// 	// Wait for test completion or timeout
// 	select {
// 	case <-clientDone:
// 		// Test completed normally
// 	case <-time.After(15 * time.Second):
// 		t.Fatal("Test did not complete within timeout")
// 	}
// }
