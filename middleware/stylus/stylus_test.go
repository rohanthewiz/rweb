package stylus

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rohanthewiz/go-styl/stylserve"
	"github.com/rohanthewiz/rweb"
)

var testFS = fstest.MapFS{
	"app.styl":    &fstest.MapFile{Data: []byte("@import \"theme\"\nbody\n  color fg\n")},
	"theme.styl":  &fstest.MapFile{Data: []byte("fg = #333\n")},
	"broken.styl": &fstest.MapFile{Data: []byte(".x\n  nope()\n")},
}

func newServer(opts stylserve.Options) *rweb.Server {
	if opts.Dir == "" && opts.FS == nil {
		opts.FS = testFS
	}
	s := rweb.NewServer(rweb.ServerOptions{Address: ":0"})
	s.Get("/css/*path", Handler(opts))
	return s
}

func TestServesCompiledCSS(t *testing.T) {
	s := newServer(stylserve.Options{})
	res := s.Request(http.MethodGet, "/css/app.css", nil, nil)

	if res.Status() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Status(), res.Body())
	}
	if ct := res.Header("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(string(res.Body()), "color:#333") {
		t.Errorf("body = %q", res.Body())
	}
	if res.Header("ETag") == "" {
		t.Error("missing ETag")
	}
}

func TestConditionalRequest304(t *testing.T) {
	s := newServer(stylserve.Options{})
	first := s.Request(http.MethodGet, "/css/app.css", nil, nil)
	etag := first.Header("ETag")

	res := s.Request(http.MethodGet, "/css/app.css",
		[]rweb.Header{{Key: "If-None-Match", Value: etag}}, nil)
	if res.Status() != http.StatusNotModified {
		t.Errorf("status = %d, want 304", res.Status())
	}
	if len(res.Body()) != 0 {
		t.Errorf("304 body = %q, want empty", res.Body())
	}
}

func TestNotFound(t *testing.T) {
	s := newServer(stylserve.Options{})
	for _, p := range []string{"/css/missing.css", "/css/app.styl"} {
		if res := s.Request(http.MethodGet, p, nil, nil); res.Status() != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", p, res.Status())
		}
	}
}

func TestCompileError500(t *testing.T) {
	s := newServer(stylserve.Options{})
	res := s.Request(http.MethodGet, "/css/broken.css", nil, nil)
	if res.Status() != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.Status())
	}
	if !strings.Contains(string(res.Body()), "broken.styl:2:3") {
		t.Errorf("body = %q, want positioned error", res.Body())
	}
}

func TestSourceMapServed(t *testing.T) {
	s := newServer(stylserve.Options{SourceMaps: true})

	css := s.Request(http.MethodGet, "/css/app.css", nil, nil)
	if !strings.Contains(string(css.Body()), "sourceMappingURL=app.css.map") {
		t.Errorf("css missing sourceMappingURL:\n%s", css.Body())
	}

	res := s.Request(http.MethodGet, "/css/app.css.map", nil, nil)
	if res.Status() != http.StatusOK || !strings.Contains(string(res.Body()), `"version":3`) {
		t.Errorf("map status = %d body = %q", res.Status(), res.Body())
	}
}
