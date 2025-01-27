package rweb_test

import (
	"fmt"
	"testing"

	"git.akyoto.dev/go/assert"
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
