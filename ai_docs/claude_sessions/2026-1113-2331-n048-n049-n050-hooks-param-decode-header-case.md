# Phase 4 begins: N-048 hooks, N-049 param decoding, N-050 header case

Session id: `7a244f89-d4cb-4e3d-a3cb-fd039205c24d`
Date: 2026-11-13 · Branch: `roh/once-over-fab5.2` · Start `523cd75` → end `b1e4898` (+ this doc)
Plan: `ai_docs/todo/PLAN_ONCE_OVER_2026-1109.md`, Phase 4.

## What was asked

1. "Next up is Phase 4, starting with N-048."
2. "Go with N-049 and don't decode twice" (decision D7: decode path params).
3. "Go ahead with N-050."
4. `/sw`.

Each item was committed on its own, followed by a `next-list` commit. Every
item was checked with `go test -race ./...`, `go vet ./...`,
`GOOS=windows go vet ./...` and staticcheck v0.7.0, the last from the scratchpad
of an earlier session; `~/bin/staticcheck` is still the old build. All passed.

## 1. N-048: error, not-found and 405 hooks; `Routes` (`a2315f6`)

**API** (`Server.go`), each setting a hook on the server:
- `SetErrorHandler(func(Context, error))`
- `SetNotFoundHandler(Handler)`
- `SetMethodNotAllowedHandler(Handler)`

**How they behave:**
- `nil` restores the default for each one.
- Call them before serving: the request path reads the fields without a lock, as it does the routers.
- The 404 and 405 hooks run inside the default dispatch handler, so after `s.Use` middleware.
- The status is set before the hook runs: 404, or 405 with the `Allow` header. A hook that only writes a body is therefore enough. A hook may change the status, and an error it returns goes to the error handler.
- Only unrouted requests reach the not-found hook. A route's own 404 does not: a missing file under `StaticFiles`, or a handler that sets 404 itself.
- The unknown-method 501 still comes before any handler and never reaches the hooks.

**Default error page:**
- It moved out of `NewServer` into `defaultErrorHandler`.
- Its heading now uses `http.StatusText(status)`. `consts.StatusTextFromCode` was not used because it lacks most codes; 413, 418 and 429 are all missing.
- The proxy's pages now read "502 Bad Gateway" and "504 Gateway Timeout". A status with no reason phrase reads "Error".
- Fixing the page heading came from the N-047 note that was folded into N-048.

**Routes:**
- `AddMethod` records `:param` and `*wildcard` routes in `Server.radixRoutes`. Adding the same method and path again replaces the entry, as the tree does.
- Routes are recorded at registration because the radix tree cannot give back its patterns from compressed nodes.
- The new `Routes() []rtr.RouteList` returns hash and radix routes, sorted by path and then by method in `routableMethods` order.
- `ListRoutes` prints `Routes()`, so its output is now complete and in the same order on every run.

**Tests** (`error_hooks_test.go`):
- A custom error handler, a panic routed through it, and restoring the default.
- The status text for 502, 418 and an unknown 599.
- The 404 and 405 hooks, both through `s.Request` and on the wire, including a raw HEAD (the header block alone, with `Content-Length: 12`).
- A hook that changes the status or returns an error.
- The contents and order of `Routes`.

**Docs:** a "Error, not-found and 405 hooks" section in SKILL.md, and a CLAUDE.md bullet.

## 2. N-049: path params percent-decoded exactly once (`6721dfd`)

**Decoding in `request.addParameter`** (Request.go):
- A value containing `%` is decoded with `url.PathUnescape`, so `+` stays `+`.
- A value that does not decode (`50%`, `%zz`) is kept as sent.
- Values with no `%` skip the decode call and its allocation.
- The router still matches the raw path, and `Path()` stays raw. A `%2F` inside a `:param` gives a `/` in the value without changing which route matched.

**Not decoding twice** (the user's instruction):
- The static handler (`staticFiles`, Server.go) no longer calls `url.PathUnescape` on its `*path`.
- Without that change, `x%2520y.txt` would have served `x y.txt`, and `%252e%252e` would have become `..`.
- `addParameter` keeps a bad escape raw. To keep the old 404 for one, the static handler runs `url.PathUnescape` on the raw `Path()` when it contains a `%`.
- The traversal checks (NUL, `..` segments, the `filepath.Rel` containment) run on the decoded value, as before.

**Other `*path` users:** `middleware/stylus` passes `PathParam("path")` to go-styl `stylserve.Engine.Asset`. That function runs `path.Clean` and rejects `..`, so decoded values are safe there.

**Tests:**
- `path_param_decode_test.go` covers the decoding cases, a single decode of `%2520` → `%20`, bad escapes kept raw, `Path()` staying raw, a group route, the HEAD fallback (which re-matches the raw path), a static decode-once check (`x%2520y.txt` serves the file `x%20y.txt`, not `x y.txt`), and one request on the wire.
- `TestStaticFilesPathTraversal` gained `%252e%252e`, `%252E%252E%252F` and `%zz/..` cases.
- The param tests fail on the previous code. The static-file tests pass on both versions: the old handler also decoded once.

**Behaviour change:** an app that decoded params itself now decodes twice and must drop its own `url.PathUnescape`. A decoded value can also contain `/` and `..`. SKILL.md (Route Parameters) says both, and CLAUDE.md's Route Parameters item states the decode-once rule.

## 3. N-050: response header names case-insensitive (`f64493d`)

**Matching** (Response.go):
- `response.Header` and `SetHeader` compare names with `headerKeyEqual`, which checks the lengths and then calls `strings.EqualFold`.
- Stored names are ASCII tokens (`acceptHeaderKey`), so the length check loses nothing.
- It also stops Unicode folding from matching a non-ASCII lookup key: the Kelvin sign U+212A folds to `k`.

**`SetHeader` now replaces every value under a name,** as net/http's `Header.Set` does:
- Duplicates from `AddHeader`, in any letter case, collapse into one entry.
- That entry stays where the first one was and takes the new call's spelling.
- Dropped duplicates are compacted in place and the vacated tail is cleared, so the call still does not allocate.
- A first draft cleared the tail on the path that appends a new header too. That slice would have been out of range, which would panic, so it was fixed before testing.
- `AddHeader` is unchanged.

**Benchmarks:** keep-alive Hello, JSON and Browser, run old and new in turn, five times each. The medians are within noise: 14.4 vs 14.7, 14.3 vs 14.2 and 14.4 vs 14.9 µs/op, new vs old.

**Tests** (`response_header_case_test.go`):
- Lookup and replace in any letter case, and the Kelvin-sign key not matching.
- One Content-Type on the wire, whether it was set before or after `WriteJSON`.
- `SetHeader` collapsing `AddHeader` duplicates, with the kept line still first.
- `Set-Cookie` still repeating.
- Both tests fail on the previous code.

**Docs:** a paragraph in SKILL.md, next to the header-sanitising paragraph.

**Found while doing it: N-073.** A handler-set `Content-Length` is sent alongside the server's own. A throwaway test showed it on the wire: `SetHeader("Content-Length", "999")` + `WriteString("hi")` → `Content-Length: 2` and `Content-Length: 999`. It was logged rather than fixed under N-050. The fix belongs in `writeResponse`, and it must not break an explicit `s.Head` route that sets the GET length.

## Notes

- Phase 4 is now N-051 … N-056. N-051 (charset) is small. N-052 (static Range/ETag/options) is one of the two large items. N-054 needs D8 and N-055 needs D9; the plan recommends an answer for both.
- No pushes were made during the items themselves; `/sw` pushes them all.

## Next

Closed: N-048, N-049, N-050. Declined: None. Raised: N-073.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
