package rweb_test

import (
	"fmt"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestRequest(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/request", func(ctx rweb.Context) error {
		req := ctx.Request()
		method := req.Method()
		scheme := req.Scheme()
		host := req.Host()
		path := req.Path()
		return ctx.WriteString(fmt.Sprintf("%s %s %s %s", method, scheme, host, path))
	})

	response := s.Request(consts.MethodGet, "http://example.com/request?x=1", []rweb.Header{{"Accept", "*/*"}}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "GET http example.com /request")
}

func TestRequestHeader(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		accept := ctx.Request().Header("Accept")
		empty := ctx.Request().Header("")
		return ctx.WriteString(accept + empty)
	})

	response := s.Request(consts.MethodGet, "/", []rweb.Header{{"Accept", "*/*"}}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "*/*")
}

func TestRequestParam(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/blog/:article", func(ctx rweb.Context) error {
		article := ctx.Request().Param("article")
		empty := ctx.Request().Param("")
		return ctx.WriteString(article + empty)
	})

	response := s.Request(consts.MethodGet, "/blog/my-article", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "my-article")
}

func TestUserAgent(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		userAgent := ctx.UserAgent()
		return ctx.WriteString(userAgent)
	})

	// Test with standard User-Agent header
	response := s.Request(consts.MethodGet, "/", []rweb.Header{{"User-Agent", "Mozilla/5.0"}}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Mozilla/5.0")

	// Test with lowercase user-agent header (case-insensitive matching)
	response2 := s.Request(consts.MethodGet, "/", []rweb.Header{{"user-agent", "Chrome/100.0"}}, nil)
	assert.Equal(t, response2.Status(), 200)
	assert.Equal(t, string(response2.Body()), "Chrome/100.0")

	// Test with User-Agent header absent (should return empty string)
	response3 := s.Request(consts.MethodGet, "/", []rweb.Header{}, nil)
	assert.Equal(t, response3.Status(), 200)
	assert.Equal(t, string(response3.Body()), "")
}
