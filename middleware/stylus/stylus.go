// Package stylus serves compiled Stylus (.styl) stylesheets from an rweb
// server, powered by the go-styl compiler (github.com/rohanthewiz/go-styl).
//
// Stylesheets are compiled on first request and cached; the cache is
// invalidated when the source or any of its @imports change. Responses carry
// strong ETags (with If-None-Match handled for 304s) and Last-Modified.
//
// Register the handler on a wildcard route; the wildcard segment selects the
// stylesheet ("/css/app.css" compiles "<root>/app.styl"):
//
//	s := rweb.NewServer(rweb.ServerOptions{Address: ":8080"})
//	s.Get("/css/*path", stylus.Handler(stylserve.Options{Dir: "./styles"}))
//
// With go:embed the stylesheets ship inside the binary:
//
//	//go:embed styles/*.styl
//	var styles embed.FS
//	sub, _ := fs.Sub(styles, "styles")
//	s.Get("/css/*path", stylus.Handler(stylserve.Options{FS: sub}))
//
// With Options.SourceMaps set, "<name>.css.map" is served alongside and the
// CSS gains a sourceMappingURL comment for DevTools.
package stylus

import (
	"errors"
	"io/fs"
	"net/http"

	"github.com/rohanthewiz/go-styl/stylserve"
	"github.com/rohanthewiz/rweb"
)

// Handler returns an rweb handler serving compiled CSS from the source root
// described by opts (stylserve.Options{Dir | FS, IncludePaths, Pretty,
// MergeDuplicates, SourceMaps}). Mount it on a route whose final segment is
// the "*path" wildcard.
func Handler(opts stylserve.Options) func(rweb.Context) error {
	eng := stylserve.New(opts)

	return func(ctx rweb.Context) error {
		asset, err := eng.Asset(ctx.Request().PathParam("path"))
		if errors.Is(err, fs.ErrNotExist) {
			return ctx.SetStatus(http.StatusNotFound).WriteString("not found")
		}
		if err != nil {
			// go-styl compile errors are positioned (file:line:col) — surface them.
			return ctx.SetStatus(http.StatusInternalServerError).WriteString(err.Error())
		}

		res := ctx.Response()
		res.SetHeader("Content-Type", asset.ContentType)
		res.SetHeader("ETag", asset.ETag)
		if !asset.ModTime.IsZero() {
			res.SetHeader("Last-Modified", asset.ModTime.UTC().Format(http.TimeFormat))
		}
		if ctx.Request().Header("If-None-Match") == asset.ETag {
			ctx.SetStatus(http.StatusNotModified)
			return nil
		}
		return ctx.Bytes(asset.Body)
	}
}
