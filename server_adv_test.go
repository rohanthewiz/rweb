package rweb_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"syscall"
	"testing"
	"time"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestProxy(t *testing.T) {
	// US Server
	usTgtReadyChan := make(chan struct{}, 1)
	usTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: usTgtReadyChan, Address: "localhost:"})
	usTgt.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from US server root")
	})

	usTgt.Get("/us/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the US server landing!</h1>")
	})

	usTgt.Post("/us/proxy-incoming/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"status": "success",
		}
		return ctx.WriteJSON(data)
	})

	// Euro Server
	euTgtReadyChan := make(chan struct{}, 1)
	euTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: euTgtReadyChan, Address: "localhost:"})
	euTgt.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from EU server root")
	})

	euTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the EU server landing!</h1>")
	})

	euTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().GetPostValue("def"))
	})

	go func() {
		_ = usTgt.Run() // run with high-order port
	}()

	go func() {
		_ = euTgt.Run()
	}()

	<-usTgtReadyChan // wait for US server
	<-euTgtReadyChan // wait for EUR server

	// Proxy
	pxyReadyChan := make(chan struct{}, 1)
	pxy := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: pxyReadyChan, Address: "localhost:"})

	pxy.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from proxy server root")
	})

	// e.g. curl http://localhost:8080/via-proxy/us/status
	// 		- This will proxy to http://localhost:8081/usa/proxy-incoming/status
	// e.g. curl http://localhost:8080/via-proxy/us
	err := pxy.Proxy("/via-proxy/us",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", usTgt.GetListenPort()), 1)
	if err != nil {
		log.Fatal(err)
	}

	// e.g. curl http://localhost:8080/via-proxy/eu/status
	err = pxy.Proxy("/via-proxy/eu",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", euTgt.GetListenPort()), 2)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		defer func() {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()

		<-pxyReadyChan // wait for proxy

		// Proxy to US server status
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/us/status", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		respMap := map[string]string{}
		_ = json.Unmarshal(body, &respMap)

		if st, ok := respMap["status"]; ok {
			assert.Equal(t, st, "success")
		} else {
			t.Error("Response map does not contain 'status'")
		}

		// Proxy to US server root
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/us", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "<h1>Welcome to the US server landing!</h1>")

		// Proxy to EU
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/eu/status", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "456")
	}()

	_ = pxy.Run()
}

func TestSSE(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	// Create event channel with buffer
	eventsChan := make(chan any, 8)

	s := rweb.NewServer(rweb.ServerOptions{
		Verbose:   true,
		ReadyChan: readyChan,
		Address:   "localhost:",
	})
	s.Get("/events", s.SSEHandler(eventsChan))

	go func() { // Run our SSE client
		defer close(clientDone)
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		// Create client with timeout
		client := &http.Client{
			Timeout: 0, // Disable timeout for SSE connection
		}

		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/events", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		defer func() {
			_ = resp.Body.Close()
		}()

		// Verify headers
		assert.Equal(t, resp.Header.Get("Content-Type"), "text/event-stream")
		assert.Equal(t, resp.Header.Get("Cache-Control"), "no-cache")
		assert.Equal(t, resp.Header.Get("Connection"), "keep-alive")

		// Read events with scanner
		scanner := bufio.NewScanner(resp.Body)
		eventsReceived := 0

		// Start a timeout timer
		timeout := time.After(10 * time.Second)

		// Send events in background
		go func() {
			eventsChan <- "event 1"
			eventsChan <- "event 2"
			eventsChan <- "event 3"
			// No need to wait as closed channel just means we won't add any more events.
			// time.Sleep(100 * time.Millisecond)
			close(eventsChan)
		}()

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
					break
				}
				line := scanner.Text()
				if line != "" {
					eventsReceived++
					expected := fmt.Sprintf("data: event %d", eventsReceived)
					t.Logf("Received event: %s", line)
					assert.Equal(t, line, expected)
				}
			}
		}

		assert.Equal(t, eventsReceived, 3)
	}()

	_ = s.Run()

	// Wait for test completion or timeout
	select {
	case <-clientDone:
		// Test completed normally
	case <-time.After(15 * time.Second):
		t.Fatal("Test did not complete within timeout")
	}
}
