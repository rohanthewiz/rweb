# Add Stylus CSS middleware (middleware/stylus) powered by go-styl

- **Date:** 2026-07-01 17:27
- **Session ID:** `91c0efb6-763f-4dab-8aa4-06abe068654c` (go-styl session; work spanned both repos)
- **Branch:** `feat/stylus-middleware` (local, not yet pushed)
- **Commits:** `ded4423` (middleware + example + docs), `4e59eb4` (bump to tagged go-styl v0.1.0)
- **Companion log:** `~/projs/go/go-styl/ai_docs/claude_sessions/2026-0701-1623-go-styl-m7-m8.md`

## Why

The rweb adapter for the go-styl Stylus compiler originally lived in go-styl as
`stylrweb`, which pulled rweb (+ element, assert) into go-styl's go.mod. Moving
the adapter here flips the dependency arrow: go-styl is back to depending only
on serr, and rweb users find the middleware where framework middleware belongs.
It lives in a **subpackage**, so apps that don't import it never build the
compiler (Go module graph pruning); plain rweb users only see go.sum hash lines.

## What was added

- **`middleware/stylus/stylus.go`** — `stylus.Handler(stylserve.Options) func(rweb.Context) error`.
  Mount on a wildcard route; the wildcard picks the stylesheet:

  ```go
  s.Get("/css/*path", stylus.Handler(stylserve.Options{Dir: "./styles", SourceMaps: true}))
  ```

  Compile-on-first-request with caching (invalidated when the source **or any
  `@import`** changes — go-styl's `Result.Deps` provides the file list); strong
  ETags with `If-None-Match` → 304; `Last-Modified`; optional source maps
  (`<name>.css.map` + `sourceMappingURL` comment). Sources come from a
  directory or any `fs.FS` (embed.FS). Compile errors → 500 with go-styl's
  positioned `file:line:col` message; unknown paths → 404. The heavy lifting
  (path mapping, cache, ETags, traversal safety) is in go-styl's `stylserve`
  engine; this adapter is ~60 lines of rweb plumbing.

- **`middleware/stylus/stylus_test.go`** — in-process `s.Request()` tests over
  an `fstest.MapFS`: compiled output + headers, 304 round-trip, 404 table,
  positioned 500, source-map serving.

- **`examples/stylus_css/`** — runnable demo (`main.go` + `styles/site.styl`):
  wildcard route with source maps and a styled index page. Verified live with
  curl (CSS, map, ETag/304).

- **Docs** — README gained a "Stylus CSS Middleware" section + features bullet;
  CLAUDE.md directory structure lists `middleware/stylus/`.

- **go.mod** — requires `github.com/rohanthewiz/go-styl v0.1.0` (tagged this
  session; initially a pseudo-version until the tag existed).

## Notable: the example caught a go-styl parser bug

The demo stylesheet's `padding gutter (gutter * 2)` compiled to a bogus
`padding:gutter(24px)` — go-styl treated `ident (` **with a space** as a
function call. Per the CSS function-token rule (paren must be glued to the
identifier), fixed in go-styl (`56c1573`, included in v0.1.0):
`padding g (g * 2)` → `12px 24px`, and `margin (10px)` is no longer misparsed
as a mixin call.

## Verification

`go build ./...`, `go vet ./...`, full `go test -count=1 ./...` (root,
`core/rtr`, `middleware/stylus`) all pass. Live server check via the example.

## Next steps

- Push `feat/stylus-middleware` and open a PR (Rohan drives rweb pushes;
  `go_origin` Azure remote is backup-only).
- rweb repo has pre-existing gofmt drift in ~12 files (untouched here).
