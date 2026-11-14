package rweb_test

// Tests for N-049: path params are percent-decoded once, in
// request.addParameter, and StaticFiles no longer decodes its *path again.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

func echoParam(name string) rweb.Handler {
	return func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().Param(name))
	}
}

// :param and *wildcard values come back decoded; Path() stays raw.
func TestPathParamsDecoded(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/greet/:name", echoParam("name"))
	s.Get("/files/*path", echoParam("path"))
	s.Get("/raw/:x", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().Path())
	})
	s.Group("/api").Get("/users/:id", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().PathParam("id"))
	})

	cases := []struct{ path, want string }{
		{"/greet/John", "John"},                   // no escapes: unchanged
		{"/greet/John%20Doe", "John Doe"},         // the reported case
		{"/greet/Jos%C3%A9", "José"},              // UTF-8
		{"/greet/a+b", "a+b"},                     // "+" is a space only in queries
		{"/greet/a%2Fb", "a/b"},                   // %2F decodes, route still matched
		{"/greet/100%25", "100%"},                 // literal percent
		{"/greet/%2520", "%20"},                   // decoded once, not twice
		{"/greet/50%", "50%"},                     // stray "%": kept raw
		{"/greet/%zz", "%zz"},                     // bad escape: kept raw
		{"/files/a%20b/c%20d.txt", "a b/c d.txt"}, // wildcard
		{"/raw/x%20y", "/raw/x%20y"},              // Path() is not decoded
		{"/api/users/u%3A1", "u:1"},               // group route
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			resp := s.Request("GET", c.path, nil, nil)
			assert.Equal(t, 200, resp.Status())
			assert.Equal(t, c.want, string(resp.Body()))
		})
	}
}

// A HEAD served by the GET route re-matches the raw path, so its params are
// decoded once too.
func TestPathParamsDecodedForHeadFallback(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/greet/:name", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("X-Name", ctx.Request().Param("name"))
		return nil
	})
	resp := s.Request("HEAD", "/greet/%2541%20B", nil, nil)
	assert.Equal(t, 200, resp.Status())
	assert.Equal(t, "%41 B", resp.Header("X-Name"))
}

// StaticFiles serves a file by its once-decoded name: "x%2520y.txt" names
// the file "x%20y.txt", not "x y.txt".
func TestStaticFilesDecodeOnce(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		assert.Nil(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	write("x y.txt", "spaced")
	write("x%20y.txt", "literal-escape")
	write("100%.txt", "percent")

	s := rweb.NewServer()
	s.StaticFilesAbs("/static/", root, 1)

	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/static/x%20y.txt", 200, "spaced"},
		{"/static/x%2520y.txt", 200, "literal-escape"}, // decoding twice would give "spaced"
		{"/static/100%25.txt", 200, "percent"},
		{"/static/100%.txt", 404, ""}, // invalid escape is refused, as before
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			resp := s.Request("GET", c.path, nil, nil)
			assert.Equal(t, c.status, resp.Status())
			if c.status == 200 {
				assert.Equal(t, c.body, string(resp.Body()))
			}
		})
	}
}

// On the wire, through the real request parser.
func TestPathParamsDecodedOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/greet/:name", echoParam("name"))

	go func() {
		defer stopRun(s)
		<-readyChan

		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/greet/John%%20Doe", s.GetListenPort()))
		assert.Nil(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "John Doe", string(body))
	}()

	_ = s.Run()
}
