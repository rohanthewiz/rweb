package rweb_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

func okHandler(ctx rweb.Context) error { return ctx.WriteString("ok") }

// A path routed under other methods answers 405 with an Allow header; a path
// routed under no method still answers 404.
func TestMethodNotAllowed(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/items", okHandler)
	s.Post("/items", okHandler)
	s.Get("/users/:id", okHandler)
	s.Delete("/users/:id", okHandler)
	s.Get("/files/*path", okHandler)
	api := s.Group("/api")
	api.Put("/thing", okHandler)

	cases := []struct {
		method, path string
		status       int
		allow        string
	}{
		{"GET", "/items", 200, ""},
		{"PUT", "/items", 405, "GET, HEAD, POST"},
		{"HEAD", "/items", 200, ""}, // HEAD is served by the GET handler
		{"PATCH", "/users/42", 405, "GET, HEAD, DELETE"},
		{"POST", "/files/a/b.txt", 405, "GET, HEAD"},
		{"GET", "/api/thing", 405, "PUT"},
		{"PUT", "/api/thing", 200, ""},
		{"GET", "/nowhere", 404, ""},
		{"POST", "/users", 404, ""},
		{"DELETE", "/api/other", 404, ""},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			resp := s.Request(c.method, c.path, nil, nil)
			assert.Equal(t, c.status, resp.Status())
			assert.Equal(t, c.allow, resp.Header("Allow"))
		})
	}
}

// The 405 probe must not leave :params from the routes it tried on the
// request, nor run any of their handlers.
func TestMethodNotAllowedProbeHasNoSideEffects(t *testing.T) {
	s := rweb.NewServer()
	ran := false
	s.Get("/users/:id", func(ctx rweb.Context) error {
		ran = true
		return nil
	})
	var seen string
	s.Use(func(ctx rweb.Context) error {
		err := ctx.Next()
		seen = ctx.Request().Param("id")
		return err
	})

	resp := s.Request("POST", "/users/42", nil, nil)
	assert.Equal(t, 405, resp.Status())
	assert.Equal(t, false, ran)
	assert.Equal(t, "", seen)
}

// Over the wire: the 405 carries Allow and an empty body, like a 404.
func TestMethodNotAllowedOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/only-get", okHandler)

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		url := fmt.Sprintf("http://127.0.0.1:%s/only-get", s.GetListenPort())
		resp, err := http.Post(url, "text/plain", strings.NewReader("x"))
		assert.Nil(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "405 Method Not Allowed", resp.Status)
		assert.Equal(t, "GET, HEAD", resp.Header.Get("Allow"))
		assert.Equal(t, "", string(body))
	}()

	_ = s.Run()
}
