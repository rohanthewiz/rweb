// Package main demonstrates the stylus middleware: Stylus (.styl) stylesheets
// compiled to CSS on request, cached, and invalidated when sources change.
//
// Run it, then:
//
//	curl -i http://localhost:8080/css/site.css      # compiled CSS (+ ETag)
//	curl -i http://localhost:8080/css/site.css.map  # source map (DevTools)
//
// Edit styles/site.styl and request again — it recompiles automatically.
package main

import (
	"log"

	"github.com/rohanthewiz/go-styl/stylserve"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/middleware/stylus"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})

	// GET /css/<name>.css compiles ./styles/<name>.styl.
	// SourceMaps also serves <name>.css.map for browser DevTools.
	s.Get("/css/*path", stylus.Handler(stylserve.Options{
		Dir:        "styles",
		SourceMaps: true,
	}))

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML(`<!doctype html>
<link rel="stylesheet" href="/css/site.css">
<div class="banner">Styled by Stylus, compiled by go-styl</div>`)
	})

	log.Fatal(s.Run())
}
