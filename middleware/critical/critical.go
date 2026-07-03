// Package critical inlines per-response critical CSS into HTML responses,
// powered by the go-styl compiler (github.com/rohanthewiz/go-styl).
//
// After the handler runs, the middleware scans the rendered HTML for the tag
// names, classes, and IDs it actually uses, prunes the configured .styl
// stylesheet down to the matching rules, and injects the result as a <style>
// block before </head> — zero-render-blocking CSS with no headless browser.
// Pruned output is cached by the page's used-name set (pages sharing a
// layout compile once) and invalidated when the stylesheet or any of its
// @imports change.
//
//	s := rweb.NewServer(rweb.ServerOptions{Address: ":8080"})
//	s.Use(critical.Middleware(stylcrit.Options{
//		Path:     "styles/app.styl", // or FS: an embed.FS
//		Safelist: styl.Used{Classes: []string{"menu--open"}},
//	}))
//
// Handlers just render full HTML pages (element pairs naturally); responses
// whose Content-Type is not text/html, or with a non-2xx status, pass
// through untouched. Names that only appear after client-side script runs
// (toggled classes, htmx swaps) must be listed in Options.Safelist or their
// rules are pruned away.
package critical

import (
	"strings"

	"github.com/rohanthewiz/go-styl/stylcrit"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"
)

// Middleware returns an rweb middleware inlining critical CSS into every
// HTML response (see the package documentation for the options and flow). A
// stylesheet compile error fails the response loudly — go-styl errors are
// positioned (file:line:col), so the fix is in the message.
func Middleware(opts stylcrit.Options) func(rweb.Context) error {
	eng := stylcrit.New(opts)

	return func(ctx rweb.Context) error {
		if err := ctx.Next(); err != nil {
			return err
		}
		res := ctx.Response()
		if st := res.Status(); st < 200 || st > 299 {
			return nil
		}
		if !strings.Contains(res.Header("Content-Type"), "text/html") {
			return nil
		}
		body := res.Body()
		if len(body) == 0 {
			return nil
		}
		out, err := eng.Inline(string(body))
		if err != nil {
			return serr.Wrap(err, "critical CSS middleware")
		}
		res.SetBody([]byte(out))
		return nil
	}
}
