# Fix multipart/urlencoded form state leaks across keep-alive requests

- **Date:** 2026-05-09 23:36
- **Session ID:** `e3332204-9a24-4bc0-b357-58fee397652f`
- **Branch:** `roh/fix-some-low-hanging-fruit`
- **Supersedes:** `2026-0509-2315-fix-multipart-form-leak.md` (earlier snapshot of the same session)

## Trigger

User selected lines 218–220 of `~/projs/go/pers/rweb/Request.go`:

```go
// if err := req.ParseMultipartForm(); err != nil {
// 	return nil, nil, err
// }
```

…and asked: "I don't remember why I commented out lines 219–221, but I seem to remember I was not happy with the way multipart form was handled in the request functions. Please take a deep dive into what is happening between multipartForm and regular form handling or anything not kosher here."

## Why those lines were commented out

`Server.handleRequest` already eagerly pre-parses multipart bodies before any handler runs:

```go
// Server.go (~927)
if bytes.HasPrefix(ctx.ContentType, consts.BytMultipartFormData) {
    if err := ctx.request.ParseMultipartForm(); err != nil { ... }
}
```

So calling `ParseMultipartForm` again inside `GetFormFile` looked redundant. But it was load-bearing: any pre-parse failure was logged with `fmt.Printf` and silently swallowed, and the handler then saw `req.multipartForm == nil` and got back a generic `"no multipart form data"` error — indistinguishable from a body-less request.

## Bugs found

The deep dive turned up **five** real bugs, all in the same shape: state on the request struct survived `Context.Clean()` and leaked into the next request on the same keep-alive connection (since contexts are pooled per-connection in `Server.handleConnection`).

### 1. `multipartForm` not nil-ed in CleanupMultipartForm

`form.RemoveAll()` only deletes temp files for `File` parts. `Form.Value` (a plain string-map) was preserved. Combined with `FormValue`'s pre-fix dispatch logic (`if req.multipartForm != nil`), request N+1 could read string values from request N's multipart form.

### 2. `postArgs` not reset in Context.Clean

`Clean()` reset `parsedPostArgs = false` but never reset `req.postArgs`. `parsePostArgs` early-returns when Content-Type isn't urlencoded, so a follow-up request with a non-form Content-Type read back the previous urlencoded POST's args via `GetPostValue → PostArgs() → returns &req.postArgs`.

### 3. `ContentType []byte` cache not reset (discovered during negative validation)

`req.ContentType` is populated when the `Content-Type` header is read. But `Clean()` didn't reset it. So a follow-up request that sent NO Content-Type header (e.g. a GET) inherited the previous value, silently steering `parsePostArgs` / `FormValue` / `ParseMultipartForm` down the wrong path.

This bug **masked** the postArgs test from #2: with the leaked Content-Type still saying urlencoded, parsePostArgs re-entered the parse path against the empty body, which cleared postArgs as a side effect.

### 4. Multipart parse errors silently swallowed

`Server.go:929–935` logged parse errors via `Printf` and continued. The handler couldn't distinguish a parse failure from a missing body.

### 5. Hard-coded 32 MB ceiling, dead `multipartFormBoundary` field, inverted/misleading comments

- `reader.ReadForm(32 << 20)` — magic number, no `ServerOptions` knob.
- `multipartFormBoundary` declared, never written or read.
- `GetPostValue` interface comment said "non-multipart" where it should have said "multipart".
- `FormValue` interface comment claimed multipart-only when it actually handles both content types.

## Fixes applied

### `Request.go`

- Removed dead `multipartFormBoundary` field.
- Added `multipartParseErr error` — caches parse failures; subsequent calls (from `GetFormFile` / `GetFormFiles` / `FormValue`) get the same real error rather than a generic sentinel.
- Added `multipartMaxMem int64` — per-request memory cap, configurable via `ServerOptions.MultipartMaxMemory`.
- Added `defaultMultipartMaxMemory int64 = 32 << 20` const.
- `ParseMultipartForm` now properly idempotent: caches both success (`multipartForm`) and failure (`multipartParseErr`).
- `GetFormFile`: uncommented the defensive `ParseMultipartForm` call.
- `GetFormFiles`: same defensive call added.
- `FormValue`: rewritten to dispatch on the **current** request's Content-Type rather than `multipartForm != nil`. This is part of the layered fix for #1.
- `CleanupMultipartForm`: now nils out `multipartForm` and clears `multipartParseErr` (the load-bearing change for #1).
- Fixed inverted `GetPostValue` interface doc; clarified `FormValue` interface doc.

### `Context.go`

- `Clean()` now also calls `ctx.request.postArgs.Reset()` (fix for #2).
- `Clean()` now also sets `ctx.request.ContentType = nil` (fix for #3).

### `Server.go`

- Added `ServerOptions.MultipartMaxMemory int64` (0 = use 32 MB default).
- Added `WithMultipartMaxMemory(n int64) ServerOption` functional option.
- Updated `WithOptions` to copy the new field.
- `newContext` seeds `request.multipartMaxMem` from `s.options.MultipartMaxMemory` once at pool creation (config, not request state — never reset by Cleanup).

## Regression tests added (`Request_leak_test.go`)

User asked for edge-case coverage. The existing test suite had zero coverage for the cross-request leaks — without regression tests these bugs could silently return.

All five tests use a shared `*http.Transport` with `MaxConnsPerHost: 1` to force connection reuse, and **assert reuse via `httptrace.GotConn{Reused: true}`** — without that assertion, a test that accidentally landed on a fresh connection would silently pass.

| Test | Target fix | Negative-validation result |
|---|---|---|
| `TestMultipartDoesNotLeakIntoFollowingGet` | FormValue Content-Type dispatch + Cleanup nil-out (defense in depth) | passes with both fixes |
| `TestMultipartDoesNotLeakIntoFollowingMultipart` | `multipartForm = nil` in CleanupMultipartForm | reverting fix → `FormValue('leaktest') on request 2 = "request-one" (want empty)` |
| `TestPostArgsDoNotLeakIntoFollowingNonFormPost` | `postArgs.Reset()` in Clean | reverting fix → `GetPostValue('leaktest') on JSON POST = "request-one" (want empty)` |
| `TestMalformedMultipartSurfacesParseError` | Uncomment + error caching in GetFormFile | reverting fix → `GetFormFile masked the real parse error with the generic sentinel: "no multipart form data"` |
| `TestContentTypeDoesNotLeakAcrossRequests` | `ContentType = nil` in Clean (the bug discovered DURING test validation) | reverting fix → `ContentType leaked: GetFormFile saw "multipart: NextPart: EOF"` |

### Test design notes worth keeping

- **Test 3 sends a JSON POST as request 2, not a GET.** Initial draft used a GET, but with the ContentType leak (#3) still present, parsePostArgs would re-enter the parse path against the empty body and clear postArgs as a side-effect, hiding the bug. A JSON POST has Content-Type `application/json` which forces parsePostArgs to early-return cleanly, exposing the underlying postArgs leak.
- **Test 4 uses raw `net.Conn`.** Go's `net/http` auto-adds a boundary parameter to multipart Content-Types, which would prevent us from constructing the malformed-Content-Type case.
- **`t.Fatal` doesn't work from goroutines** (calls `runtime.Goexit` only on the calling goroutine). All tests use `t.Errorf` + `return` instead.

## Verification

```
go test ./...
ok  	github.com/rohanthewiz/rweb	5.000s
ok  	github.com/rohanthewiz/rweb/core/rtr	(cached)
```

All tests pass with all fixes restored. Each regression test was independently verified to FAIL when its target fix was reverted, then the fix was restored.

## Files changed

- `~/projs/go/pers/rweb/Request.go`
- `~/projs/go/pers/rweb/Context.go`
- `~/projs/go/pers/rweb/Server.go`
- `~/projs/go/pers/rweb/Request_leak_test.go` *(new)*

## Lessons / takeaways

1. **Negative-validation is non-optional for regression tests.** Three of the four originally-passing tests had subtle issues that only surfaced when each fix was reverted. One of them (test 3) found an entirely *new* bug (#3, the ContentType leak).

2. **Per-connection context pooling means every cached field on the request struct is a potential cross-request leak.** Audit checklist: when adding a cached field on `request`, also add a reset in `Context.Clean()`. Worth grep-auditing the rest of the struct (e.g. `query`, `path`, `method`, `host`, `scheme` are reset elsewhere; `params` is reset; `headers` and `body` are reset).

3. **The eager multipart pre-parse in `Server.go` is still 32 MB per request even when handlers don't read the form.** `WithMultipartMaxMemory` lets users tune it down per-server. A future improvement would be to make multipart parsing fully lazy on first `GetFormFile`/`FormValue` call (the body is already buffered, so this is feasible) — that would also let unrelated handlers skip the cost entirely.

## Notes for next time

- Consider also resetting `multipartMaxMem` in `Clean()`? No — it's config, not request state, and the comment in `newContext` documents that.
- The `_ = context.Background` compile-time guard at the bottom of `Request_leak_test.go` is a vestige from an earlier draft when `context` was imported but unused after a refactor; it can be removed if `httptrace.WithClientTrace` calls keep `context` in active use (they do).
- `Cookie_test.go` and `args.go` / `bytesconv.go` carry pre-existing lint hints (`unusedwrite`, `unusedfunc`, `efaceany`) that are unrelated to this work.
