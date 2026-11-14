package rweb_test

// Tests for N-048: SetErrorHandler, SetNotFoundHandler,
// SetMethodNotAllowedHandler, the default error page's reason phrase, and
// Routes / ListRoutes covering :param and *wildcard routes.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// A custom error handler sees the handler's error and writes the response;
// nil puts the default back.
func TestSetErrorHandler(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/fail", func(ctx rweb.Context) error {
		return errors.New("boom")
	})
	s.Get("/panic", func(ctx rweb.Context) error {
		panic("kaboom")
	})

	var seen []error
	s.SetErrorHandler(func(ctx rweb.Context, err error) {
		seen = append(seen, err)
		ctx.SetStatus(503)
		_ = ctx.WriteString("custom: " + err.Error())
	})

	resp := s.Request("GET", "/fail", nil, nil)
	assert.Equal(t, 503, resp.Status())
	assert.Equal(t, "custom: boom", string(resp.Body()))

	// A panic is recovered and routed through the same hook.
	resp = s.Request("GET", "/panic", nil, nil)
	assert.Equal(t, 503, resp.Status())
	assert.Equal(t, 2, len(seen))
	assert.True(t, strings.Contains(seen[1].Error(), "kaboom"))

	// nil restores the default page.
	s.SetErrorHandler(nil)
	resp = s.Request("GET", "/fail", nil, nil)
	assert.Equal(t, 500, resp.Status())
	assert.True(t, strings.Contains(string(resp.Body()), "500 Internal Server Error"))
	assert.Equal(t, 2, len(seen))
}

// The default error page names the status the handler chose, not always
// "Internal Server Error".
func TestDefaultErrorPageStatusText(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/gw", func(ctx rweb.Context) error {
		ctx.SetStatus(502)
		return errors.New("upstream down")
	})
	s.Get("/teapot", func(ctx rweb.Context) error {
		ctx.SetStatus(418)
		return errors.New("short and stout")
	})
	s.Get("/odd", func(ctx rweb.Context) error {
		ctx.SetStatus(599) // no reason phrase known for it
		return errors.New("odd")
	})

	cases := []struct {
		path, heading string
		status        int
	}{
		{"/gw", "<h3>502 Bad Gateway</h3>", 502},
		{"/teapot", "<h3>418 I'm a teapot</h3>", 418},
		{"/odd", "<h3>599 Error</h3>", 599},
	}
	for _, c := range cases {
		resp := s.Request("GET", c.path, nil, nil)
		assert.Equal(t, c.status, resp.Status())
		body := string(resp.Body())
		assert.Contains(t, body, c.heading)
		assert.NotContains(t, body, "Internal Server Error")
	}
}

// The not-found hook runs for unrouted paths only, after middleware, with
// the status already 404; a route's own 404 does not reach it.
func TestSetNotFoundHandler(t *testing.T) {
	s := rweb.NewServer()
	var mwRan bool
	s.Use(func(ctx rweb.Context) error {
		mwRan = true
		return ctx.Next()
	})
	s.Get("/here", okHandler)
	s.Get("/gone", func(ctx rweb.Context) error {
		ctx.SetStatus(404)
		return ctx.WriteString("route's own 404")
	})

	// Default: 404, empty body.
	resp := s.Request("GET", "/nowhere", nil, nil)
	assert.Equal(t, 404, resp.Status())
	assert.Equal(t, "", string(resp.Body()))

	calls := 0
	s.SetNotFoundHandler(func(ctx rweb.Context) error {
		calls++
		// Status is already 404, so writing a body is all a hook needs.
		assert.Equal(t, 404, ctx.Response().Status())
		return ctx.WriteHTML("<h1>Nothing at " + ctx.Request().Path() + "</h1>")
	})

	mwRan = false
	resp = s.Request("GET", "/nowhere", nil, nil)
	assert.Equal(t, 404, resp.Status())
	assert.Equal(t, "<h1>Nothing at /nowhere</h1>", string(resp.Body()))
	assert.True(t, strings.HasPrefix(resp.Header("Content-Type"), "text/html"))
	assert.True(t, mwRan)
	assert.Equal(t, 1, calls)

	// Matched routes, including one that answers 404 itself, skip the hook.
	resp = s.Request("GET", "/here", nil, nil)
	assert.Equal(t, 200, resp.Status())
	resp = s.Request("GET", "/gone", nil, nil)
	assert.Equal(t, "route's own 404", string(resp.Body()))
	assert.Equal(t, 1, calls)

	// A 405 is not a 404: the not-found hook stays out of it.
	resp = s.Request("POST", "/here", nil, nil)
	assert.Equal(t, 405, resp.Status())
	assert.Equal(t, 1, calls)

	// nil restores the default.
	s.SetNotFoundHandler(nil)
	resp = s.Request("GET", "/nowhere", nil, nil)
	assert.Equal(t, 404, resp.Status())
	assert.Equal(t, "", string(resp.Body()))
}

// A hook may change the status, and an error it returns goes to the error
// handler.
func TestNotFoundHandlerStatusAndError(t *testing.T) {
	s := rweb.NewServer()
	s.SetNotFoundHandler(func(ctx rweb.Context) error {
		if ctx.Request().Path() == "/moved" {
			return ctx.Redirect(301, "/new")
		}
		return errors.New("hook failed")
	})
	var handled error
	s.SetErrorHandler(func(ctx rweb.Context, err error) {
		handled = err
		_ = ctx.WriteString("err page")
	})

	resp := s.Request("GET", "/moved", nil, nil)
	assert.Equal(t, 301, resp.Status())
	assert.Equal(t, "/new", resp.Header("Location"))

	resp = s.Request("GET", "/other", nil, nil)
	assert.NotNil(t, handled)
	assert.Equal(t, "hook failed", handled.Error())
	// The error handler keeps the 404 the hook ran under.
	assert.Equal(t, 404, resp.Status())
	assert.Equal(t, "err page", string(resp.Body()))
}

// The 405 hook runs with the status and Allow header already set, for exact
// and radix routes alike.
func TestSetMethodNotAllowedHandler(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/items", okHandler)
	s.Post("/items", okHandler)
	s.Get("/users/:id", okHandler)

	notFoundCalls := 0
	s.SetNotFoundHandler(func(ctx rweb.Context) error {
		notFoundCalls++
		return nil
	})
	s.SetMethodNotAllowedHandler(func(ctx rweb.Context) error {
		assert.Equal(t, 405, ctx.Response().Status())
		return ctx.WriteJSON(map[string]string{
			"error": "method not allowed",
			"allow": ctx.Response().Header("Allow"),
		})
	})

	resp := s.Request("PUT", "/items", nil, nil)
	assert.Equal(t, 405, resp.Status())
	assert.Equal(t, "GET, HEAD, POST", resp.Header("Allow"))
	assert.Equal(t, `{"allow":"GET, HEAD, POST","error":"method not allowed"}`,
		strings.TrimSpace(string(resp.Body())))

	resp = s.Request("DELETE", "/users/7", nil, nil)
	assert.Equal(t, 405, resp.Status())
	assert.Equal(t, "GET, HEAD", resp.Header("Allow"))
	assert.True(t, strings.Contains(string(resp.Body()), `"allow":"GET, HEAD"`))
	assert.Equal(t, 0, notFoundCalls)

	// nil restores the default empty-bodied 405.
	s.SetMethodNotAllowedHandler(nil)
	resp = s.Request("PUT", "/items", nil, nil)
	assert.Equal(t, 405, resp.Status())
	assert.Equal(t, "", string(resp.Body()))
}

// On the wire: hook bodies are framed and sent, and a HEAD to an unrouted
// path gets the hook's headers with no body.
func TestNotFoundHandlerOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/only-get", okHandler)
	s.SetNotFoundHandler(func(ctx rweb.Context) error {
		return ctx.WriteString("no such page")
	})
	s.SetMethodNotAllowedHandler(func(ctx rweb.Context) error {
		return ctx.WriteString("use " + ctx.Response().Header("Allow"))
	})

	go func() {
		defer stopRun(s)
		<-readyChan
		base := fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort())

		resp, err := http.Get(base + "/missing")
		assert.Nil(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, "404 Not Found", resp.Status)
		assert.Equal(t, "no such page", string(body))

		resp, err = http.Post(base+"/only-get", "text/plain", strings.NewReader("x"))
		assert.Nil(t, err)
		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, "405 Method Not Allowed", resp.Status)
		assert.Equal(t, "GET, HEAD", resp.Header.Get("Allow"))
		assert.Equal(t, "use GET, HEAD", string(body))

		// HEAD: raw, so the absence of a body is visible rather than
		// hidden by the client.
		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		_, _ = conn.Write([]byte("HEAD /missing HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
		raw, _ := io.ReadAll(conn)
		assert.True(t, strings.HasPrefix(string(raw), "HTTP/1.1 404 Not Found\r\n"))
		assert.Contains(t, string(raw), "Content-Length: 12\r\n")
		assert.True(t, strings.HasSuffix(string(raw), "\r\n\r\n"))
	}()

	_ = s.Run()
}

// Routes lists exact and :param / *wildcard routes, group routes under
// their full path, in a stable order; re-adding a radix route replaces it.
func TestRoutesIncludesRadix(t *testing.T) {
	s := rweb.NewServer()
	s.Post("/users", okHandler)
	s.Get("/users", okHandler)
	s.Get("/users/:id", okHandler)
	s.Delete("/users/:id", okHandler)
	s.Get("/files/*path", okHandler)
	s.Get("/users/:id", okHandler) // again: still one entry
	api := s.Group("/api")
	api.Put("/items/:id", okHandler)

	var got []string
	for _, r := range s.Routes() {
		got = append(got, r.Method+" "+r.Path)
		assert.True(t, r.HandlerRef != "")
	}
	assert.DeepEqual(t, []string{
		"PUT /api/items/:id",
		"GET /files/*path",
		"GET /users",
		"POST /users",
		"GET /users/:id",
		"DELETE /users/:id",
	}, got)
}
