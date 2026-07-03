package critical

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rohanthewiz/element"
	styl "github.com/rohanthewiz/go-styl"
	"github.com/rohanthewiz/go-styl/stylcrit"
	"github.com/rohanthewiz/rweb"
)

var testFS = fstest.MapFS{
	"app.styl": &fstest.MapFile{Data: []byte(`@import "theme"

.card
  background primary

.card__title
  font-weight bold

.unused-widget
  color red

.menu--open
  display block
`)},
	"theme.styl":  &fstest.MapFile{Data: []byte("primary = #0af\n")},
	"broken.styl": &fstest.MapFile{Data: []byte(".x\n  nope()\n")},
}

// renderPage builds the test page the way a real rweb app would — with
// element.
func renderPage() string {
	b := element.NewBuilder()
	b.Html().R(
		b.Head().R(
			b.Title().T("Critical"),
		),
		b.Body().R(
			b.DivClass("card").R(
				b.H2Class("card__title").T("Hello"),
			),
		),
	)
	return b.String()
}

func newServer(opts stylcrit.Options) *rweb.Server {
	if opts.Path == "" {
		opts.Path = "app.styl"
	}
	if opts.FS == nil {
		opts.FS = testFS
	}
	s := rweb.NewServer(rweb.ServerOptions{Address: ":0"})
	s.Use(Middleware(opts))
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML(renderPage())
	})
	s.Get("/api", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]string{"class": "card"})
	})
	s.Get("/away", func(ctx rweb.Context) error {
		return ctx.Redirect(http.StatusFound, "/")
	})
	return s
}

func TestInlinesCriticalCSS(t *testing.T) {
	s := newServer(stylcrit.Options{})
	res := s.Request(http.MethodGet, "/", nil, nil)

	if res.Status() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Status(), res.Body())
	}
	body := string(res.Body())
	if !strings.Contains(body, "<style>") || !strings.Contains(body, "</style></head>") {
		t.Fatalf("style block not injected before </head>:\n%s", body)
	}
	for _, want := range []string{".card{background:#0af}", ".card__title{font-weight:bold}"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	for _, gone := range []string{".unused-widget", ".menu--open"} {
		if strings.Contains(body, gone) {
			t.Errorf("unused rule %q survived:\n%s", gone, body)
		}
	}
}

func TestSafelistSurvives(t *testing.T) {
	s := newServer(stylcrit.Options{
		Safelist: styl.Used{Classes: []string{"menu--open"}},
	})
	body := string(s.Request(http.MethodGet, "/", nil, nil).Body())
	if !strings.Contains(body, ".menu--open{display:block}") {
		t.Errorf("safelisted rule pruned:\n%s", body)
	}
}

func TestNonHTMLUntouched(t *testing.T) {
	s := newServer(stylcrit.Options{})
	res := s.Request(http.MethodGet, "/api", nil, nil)
	if body := string(res.Body()); strings.Contains(body, "<style>") {
		t.Errorf("JSON response modified:\n%s", body)
	}
}

func TestRedirectUntouched(t *testing.T) {
	s := newServer(stylcrit.Options{})
	res := s.Request(http.MethodGet, "/away", nil, nil)
	if res.Status() != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.Status())
	}
	if strings.Contains(string(res.Body()), "<style>") {
		t.Errorf("redirect body modified: %q", res.Body())
	}
}

func TestCompileErrorFailsLoudly(t *testing.T) {
	s := newServer(stylcrit.Options{Path: "broken.styl"})
	res := s.Request(http.MethodGet, "/", nil, nil)
	if res.Status() != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body %q)", res.Status(), res.Body())
	}
}
