// Package main demonstrates the critical middleware: each HTML response gets
// the app stylesheet pruned to the rules that page actually uses, inlined as
// a <style> block in <head> — zero-render-blocking CSS with no build step.
//
// Run it, then compare what each page inlines:
//
//	curl http://localhost:8080/       # .card/.btn rules, spinner + keyframes
//	curl http://localhost:8080/about  # only .topbar/.card rules — no .btn, no spinner
//
// Edit styles/app.styl and request again — the cache invalidates
// automatically. Note .menu--open survives on every page via Safelist (a
// script toggles it after load), while .legacy-table appears on none.
package main

import (
	"log"

	"github.com/rohanthewiz/element"
	styl "github.com/rohanthewiz/go-styl"
	"github.com/rohanthewiz/go-styl/stylcrit"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/middleware/critical"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})

	s.Use(critical.Middleware(stylcrit.Options{
		Path:     "styles/app.styl",
		Safelist: styl.Used{Classes: []string{"menu--open"}},
	}))

	s.Get("/", func(ctx rweb.Context) error {
		b := element.AcquireBuilder()
		defer element.ReleaseBuilder(b)
		page(b, "Home", func() {
			b.DivClass("card").R(
				b.H2Class("card__title").T("Welcome"),
				b.AClass("btn", "href", "/about").T("About"),
				b.DivClass("spinner").R(),
			)
		})
		return ctx.WriteHTML(b.String())
	})

	s.Get("/about", func(ctx rweb.Context) error {
		b := element.AcquireBuilder()
		defer element.ReleaseBuilder(b)
		page(b, "About", func() {
			b.DivClass("card").R(
				b.H2Class("card__title").T("About"),
				b.P().T("Rendered by element, styled by go-styl."),
			)
		})
		return ctx.WriteHTML(b.String())
	})

	log.Fatal(s.Run())
}

// page renders the shared layout; body fills in per-page content.
func page(b *element.Builder, title string, body func()) {
	b.Html().R(
		b.Head().R(
			b.Title().T(title),
		),
		b.Body().R(
			b.NavClass("topbar").R(
				b.AClass("brand", "href", "/").T("critical-css demo"),
			),
			b.Wrap(body),
		),
	)
}
