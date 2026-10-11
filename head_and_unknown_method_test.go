package rweb_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// An unknown method through the synthetic s.Request path is a 501, not a
// recovered nil-tree panic (500), and a G-method that is not GET does not
// reach GET routes.
func TestUnknownMethodIs501(t *testing.T) {
	s := rweb.NewServer()
	ran := false
	s.Get("/x", func(ctx rweb.Context) error { ran = true; return nil })
	s.Get("/p/:id", func(ctx rweb.Context) error { ran = true; return nil })

	for _, c := range []struct{ method, path string }{
		{"FOO", "/p/1"}, // radix route: used to panic in selectTree's nil tree
		{"FOO", "/x"},   // hash route
		{"GOT", "/x"},   // used to be served by GET (method[0] == 'G')
		{"GOT", "/p/1"},
		{"FOO", "/nowhere"},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			resp := s.Request(c.method, c.path, nil, nil)
			assert.Equal(t, 501, resp.Status())
			assert.Equal(t, "", resp.Header("Allow"))
		})
	}
	assert.Equal(t, false, ran)
}

// Over the wire an unknown method gets a well-formed 501 that a real client
// parses, and the connection is closed after it.
func TestUnknownMethodOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/x", okHandler)

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		req, _ := http.NewRequest("PROPFIND", fmt.Sprintf("http://127.0.0.1:%s/x", s.GetListenPort()), nil)
		resp, err := http.DefaultClient.Do(req)
		assert.Nil(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 501, resp.StatusCode)
		assert.Equal(t, "", string(body))
		assert.Equal(t, true, resp.Close)
	}()

	_ = s.Run()
}

// HEAD with no HEAD route runs the GET handler, which sees Method() "HEAD"
// and its own :params; an explicit HEAD route still wins.
func TestHeadFallsBackToGet(t *testing.T) {
	s := rweb.NewServer()
	var method, id string
	s.Get("/items", func(ctx rweb.Context) error {
		method = ctx.Request().Method()
		return ctx.WriteString("items")
	})
	s.Get("/users/:id", func(ctx rweb.Context) error {
		id = ctx.Request().Param("id")
		return ctx.WriteString("user")
	})
	s.Get("/both", okHandler)
	s.Head("/both", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("X-Head", "explicit")
		return nil
	})
	s.Post("/post-only", okHandler)

	resp := s.Request("HEAD", "/items", nil, nil)
	assert.Equal(t, 200, resp.Status())
	assert.Equal(t, "HEAD", method)

	resp = s.Request("HEAD", "/users/42", nil, nil)
	assert.Equal(t, 200, resp.Status())
	assert.Equal(t, "42", id)

	resp = s.Request("HEAD", "/both", nil, nil)
	assert.Equal(t, "explicit", resp.Header("X-Head"))

	// HEAD falls back to GET only: a POST-only path is still a 405
	resp = s.Request("HEAD", "/post-only", nil, nil)
	assert.Equal(t, 405, resp.Status())
	assert.Equal(t, "POST", resp.Header("Allow"))

	resp = s.Request("HEAD", "/nowhere", nil, nil)
	assert.Equal(t, 404, resp.Status())
}

// A HEAD radix route that matches part of the path records its :param before
// failing; the GET handler the request falls back to must not see it.
func TestHeadFallbackDropsFailedHeadParams(t *testing.T) {
	s := rweb.NewServer()
	s.Head("/a/:y/b", okHandler)
	var x, y string
	s.Get("/a/:x/c", func(ctx rweb.Context) error {
		x = ctx.Request().Param("x")
		y = ctx.Request().Param("y")
		return nil
	})

	resp := s.Request("HEAD", "/a/1/c", nil, nil)
	assert.Equal(t, 200, resp.Status())
	assert.Equal(t, "1", x)
	assert.Equal(t, "", y)
}

// Over the wire: a HEAD to a GET route carries the GET's Content-Length and
// no body, and the keep-alive connection is still usable afterwards.
func TestHeadOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/hello", func(ctx rweb.Context) error { return ctx.WriteString("hello world") })

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		br := bufio.NewReader(conn)

		// HEAD then GET on one connection: if the HEAD leaked a body, the
		// GET's status line would not be where the reader expects it.
		_, _ = io.WriteString(conn, "HEAD /hello HTTP/1.1\r\nHost: a\r\n\r\n")
		resp, err := http.ReadResponse(br, &http.Request{Method: "HEAD"})
		assert.Nil(t, err)
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, int64(len("hello world")), resp.ContentLength)
		_ = resp.Body.Close()

		_, _ = io.WriteString(conn, "GET /hello HTTP/1.1\r\nHost: a\r\n\r\n")
		resp, err = http.ReadResponse(br, &http.Request{Method: "GET"})
		assert.Nil(t, err)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "hello world", string(body))
	}()

	_ = s.Run()
}

// A HEAD to an SSE route gets the stream's headers and then the connection
// closes: no events are sent and the response does not hang.
func TestHeadToSSERouteSendsNoStream(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	events := make(chan any, 1)
	events <- "should-not-be-sent"
	s.Get("/events", s.SSEHandler(events, "update"))

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

		_, _ = io.WriteString(conn, "HEAD /events HTTP/1.1\r\nHost: a\r\n\r\n")
		raw, err := io.ReadAll(conn) // returns only once the server closes
		assert.Nil(t, err)
		head, rest, found := strings.Cut(string(raw), "\r\n\r\n")
		assert.Equal(t, true, found)
		assert.Equal(t, true, strings.HasPrefix(head, "HTTP/1.1 200"))
		assert.Equal(t, true, strings.Contains(head, "text/event-stream"))
		assert.Equal(t, "", rest)
		assert.Equal(t, 1, len(events)) // the event was never consumed
	}()

	_ = s.Run()
}

// A HEAD carrying WebSocket upgrade headers to a WebSocket route is not
// upgraded: the handshake is a GET.
func TestHeadToWebSocketRouteDoesNotUpgrade(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	upgraded := false
	s.WebSocket("/ws", func(ws *rweb.WSConn) error {
		upgraded = true
		return nil
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

		_, _ = io.WriteString(conn, "HEAD /ws HTTP/1.1\r\nHost: a\r\nUpgrade: websocket\r\n"+
			"Connection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
		resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "HEAD"})
		assert.Nil(t, err)
		assert.Equal(t, true, resp.StatusCode != 101)
		assert.Equal(t, false, upgraded)
	}()

	_ = s.Run()
}
