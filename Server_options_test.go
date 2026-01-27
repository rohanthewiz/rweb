package rweb_test

import (
	"fmt"
	"io"
	"net/http"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

// TestWithAddress tests the WithAddress functional option
func TestWithAddress(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	s := rweb.NewServer(
		rweb.WithAddress("localhost:"),
		rweb.WithVerbose(),
		rweb.WithReadyChan(readyChan),
	)

	const msg = "Hello from functional options"
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString(msg)
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), msg)
	}()

	_ = s.Run()
}

// TestWithDebug tests the WithDebug functional option
func TestWithDebug(t *testing.T) {
	s := rweb.NewServer(
		rweb.WithDebug(),
		rweb.WithVerbose(),
	)

	s.Get("/test", func(ctx rweb.Context) error {
		return ctx.WriteString("test")
	})

	resp := s.Request(consts.MethodGet, "/test", nil, nil)
	assert.Equal(t, resp.Status(), consts.StatusOK)
}

// TestWithCookie tests the WithCookie functional option
func TestWithCookie(t *testing.T) {
	cookieConfig := rweb.CookieConfig{
		HttpOnly: true,
		SameSite: rweb.SameSiteLaxMode,
		Path:     "/",
	}

	s := rweb.NewServer(
		rweb.WithCookie(cookieConfig),
	)

	s.Get("/", func(ctx rweb.Context) error {
		ctx.SetCookie("test", "value")
		return ctx.WriteString("ok")
	})

	resp := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, resp.Status(), consts.StatusOK)
}

// TestWithKeepTrailingSlashes tests the WithKeepTrailingSlashes functional option
func TestWithKeepTrailingSlashes(t *testing.T) {
	s := rweb.NewServer(
		rweb.WithKeepTrailingSlashes(),
	)

	s.Get("/test/", func(ctx rweb.Context) error {
		return ctx.WriteString("with trailing slash")
	})

	resp := s.Request(consts.MethodGet, "/test/", nil, nil)
	assert.Equal(t, resp.Status(), consts.StatusOK)
}

// TestWithSSESendConnectedEvent tests the WithSSESendConnectedEvent functional option
func TestWithSSESendConnectedEvent(t *testing.T) {
	s := rweb.NewServer(
		rweb.WithSSESendConnectedEvent(),
	)

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("ok")
	})

	resp := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, resp.Status(), consts.StatusOK)
}

// TestMultipleOptions tests combining multiple functional options
func TestMultipleOptions(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	s := rweb.NewServer(
		rweb.WithAddress(":0"),
		rweb.WithVerbose(),
		rweb.WithDebug(),
		rweb.WithDebugRequestContext(),
		rweb.WithKeepTrailingSlashes(),
		rweb.WithReadyChan(readyChan),
		rweb.WithCookie(rweb.CookieConfig{
			HttpOnly: true,
			Path:     "/",
		}),
	)

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("multiple options work")
	})

	resp := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, resp.Status(), consts.StatusOK)
	assert.Equal(t, string(resp.Body()), "multiple options work")
}

// TestWithOptions tests backwards compatibility with ServerOptions struct
func TestWithOptions(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	// Old style configuration wrapped in WithOptions
	s := rweb.NewServer(
		rweb.WithOptions(rweb.ServerOptions{
			Address:   "localhost:",
			Verbose:   true,
			Debug:     true,
			ReadyChan: readyChan,
		}),
	)

	const msg = "Backwards compatibility works"
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString(msg)
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), msg)
	}()

	_ = s.Run()
}

// TestNoOptions tests server creation with no options (defaults)
func TestNoOptions(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("default server")
	})

	resp := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, resp.Status(), consts.StatusOK)
	assert.Equal(t, string(resp.Body()), "default server")
}
