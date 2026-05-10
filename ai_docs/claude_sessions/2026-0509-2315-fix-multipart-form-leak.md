# Fix multipart form handling — cross-request data leaks and dead state

- **Date:** 2026-05-09 23:15
- **Session ID:** `e3332204-9a24-4bc0-b357-58fee397652f`
- **Branch:** `roh/fix-some-low-hanging-fruit`

## Trigger

User selected lines 218–220 of `~/projs/go/pers/rweb/Request.go`:

```go
// if err := req.ParseMultipartForm(); err != nil {
// 	return nil, nil, err
// }
```

…and asked: "I don't remember why I commented out lines 219–221, but I seem to remember I was not happy with the way multipart form was handled in the request functions. Please take a deep dive into what is happening between multipartForm and regular form handling or anything not kosher here."

## Why those lines were commented out

`Server.handleRequest` already pre-parses multipart bodies eagerly at the top of request handling:

```go
// Server.go (~927)
if bytes.HasPrefix(ctx.ContentType, consts.BytMultipartFormData) {
    if err := ctx.request.ParseMultipartForm(); err != nil { ... }
}
```

So calling `ParseMultipartForm` again inside `GetFormFile` was redundant on the happy path — that's why it got commented out. But it left the function fragile: any pre-parse failure was logged with `fmt.Printf` and silently swallowed. The handler then saw `req.multipartForm == nil` and got back a generic `"no multipart form data"` error, indistinguishable from a body-less request.

## Deep-dive findings

### 1. Cross-request data leak via `multipartForm` (real bug)

`CleanupMultipartForm` called `form.RemoveAll()` (which only deletes temp files for `File` parts) but **never set `req.multipartForm = nil`**. With HTTP keep-alive the same `*context` is reused (Server.go:913 `ctx.Clean()`), so `req.multipartForm` survived across requests. `Form.Value` is a plain string-map that `RemoveAll` does not clear — so on a non-multipart follow-up request:

```go
func (req *request) FormValue(key string) string {
    if req.multipartForm != nil {                       // <-- still non-nil!
        if values := req.multipartForm.Value[key]; ... { // <-- prior request's data
            return values[0]
        }
    }
    return req.GetPostValue(key)
}
```

`FormValue("foo")` on request N could return a value submitted in request N-1.

### 2. Cross-request data leak via `postArgs` (real bug, mirror of #1)

`Context.Clean()` reset `parsedPostArgs = false` but never reset `req.postArgs`. Then `parsePostArgs` early-returns when Content-Type isn't `application/x-www-form-urlencoded` — so a GET on the same connection reads back the previous urlencoded POST's args via `GetPostValue → PostArgs() → returns &req.postArgs`.

### 3. Dead `multipartFormBoundary` field

Declared at Request.go:67, never assigned, never read. `ParseMultipartForm` recomputes the boundary locally each call.

### 4. Inverted `GetPostValue` interface comment

```go
// GetPostValue retrieves the value of POST param - cannot be used for non-multipart forms
// use FormValue for multipart form values.
```

Actually `GetPostValue` is for **urlencoded** forms and **cannot** be used for multipart. The "non-" inverted the meaning.

### 5. Multipart parse errors silently dropped

`Server.go:929–935` logs parse errors via `Printf` and continues. The handler can't distinguish a parse failure from a missing body.

### 6. Hard-coded 32 MB allocation

`reader.ReadForm(32 << 20)` was a magic number with no `ServerOptions` knob.

### 7. Asymmetric API surface and misleading `FormValue` doc

Interface comment said `FormValue` retrieves multipart form parameter values, but the implementation also falls back to `GetPostValue` for urlencoded — handles both content types.

## Fixes applied

User said "fix all the issues."

### `Request.go`

- Removed dead `multipartFormBoundary` field.
- Added `multipartParseErr error` field — caches parse failure so all accessors surface the same real error.
- Added `multipartMaxMem int64` field — per-request memory cap, configurable.
- Added `defaultMultipartMaxMemory int64 = 32 << 20` constant.
- `ParseMultipartForm`: now properly idempotent. Caches both success (`multipartForm`) and failure (`multipartParseErr`). Uses `multipartMaxMem`, falling back to default when zero.
- `GetFormFile`: uncommented the defensive `ParseMultipartForm` call (no-op on pre-parsed happy path, surfaces real error on failure).
- `GetFormFiles`: same defensive call added.
- `FormValue`: rewritten to dispatch on the **current** request's Content-Type rather than `multipartForm != nil` — this is the actual fix for the leak in #1.
- `CleanupMultipartForm`: now nils out `multipartForm` and clears `multipartParseErr`. This is the load-bearing change.
- Fixed inverted `GetPostValue` interface doc; clarified `FormValue` interface doc.

### `Context.go`

- `Clean()` now calls `ctx.request.postArgs.Reset()` in addition to clearing the `parsedPostArgs` flag — fix for #2.

### `Server.go`

- Added `ServerOptions.MultipartMaxMemory int64` (0 = use 32 MB default).
- Added `WithMultipartMaxMemory(n int64) ServerOption` functional option.
- Updated `WithOptions` to copy the new field.
- `newContext` seeds `request.multipartMaxMem` from `s.options.MultipartMaxMemory` once at pool creation (config, not request state — never reset by Cleanup).

## Verification

```
go build ./...
go test ./...
ok  	github.com/rohanthewiz/rweb	3.104s
ok  	github.com/rohanthewiz/rweb/core/rtr	(cached)
```

All tests pass.

## Files changed

- `~/projs/go/pers/rweb/Request.go`
- `~/projs/go/pers/rweb/Context.go`
- `~/projs/go/pers/rweb/Server.go`

## Notes for future work

- Consider making multipart parsing fully **lazy** (drop the eager pre-parse in `handleRequest`). With error caching now in place, lazy would avoid the up-to-32MB allocation for handlers that don't read the form. Skipped this round to preserve backward compatibility.
- `GetPostValue` interface doc is now correct, but the API still has overlapping responsibilities between `GetPostValue` and `FormValue`. A future cleanup could deprecate `GetPostValue` to a thin alias.
