package rweb_test

import (
	"errors"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestBytes(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Bytes([]byte("Hello"))
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Hello")
}

func TestString(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello")
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Hello")
}

func TestError(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Status(401).Error("Not logged in")
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 401)
	// We are return some message to the user now
	// assert.Equal(t, string(response.Body()), "")
}

func TestErrorMultiple(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Status(401).Error("Not logged in", errors.New("Missing auth token"))
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 401)
	// We are return some message to the user now
	// assert.Equal(t, string(response.Body()), "")
}

func TestRedirect(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Redirect(301, "/target")
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 301)
	assert.Equal(t, response.Header("Location"), "/target")
}
