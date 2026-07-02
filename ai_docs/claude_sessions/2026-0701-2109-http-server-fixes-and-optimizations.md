# Session: HTTP Server Fixes and Optimizations

**Session ID:** `7a52364f-31f3-4c47-aff9-06020d3c5c0a`
**Date:** 2026-07-01
**Branch:** master (changes uncommitted in working tree at session end, except this doc)

## Ask

User asked for low-hanging fruit in RWeb — performance or correctness. After the
review surfaced 13 findings, user said: "Save these to a plan and Yes, fix all of
these, starting with 1 and 2."

## Review Method

Read the full core request path (Server.go, Context.go, Request.go, Response.go,
send.go, helpers.go, http.go, Group.go, middleware.go), verified suspicions with
greps, and confirmed the headline bug with a live raw-socket repro program in the
scratchpad before reporting.

Plan saved to `ai_docs/PLAN_LOW_HANGING_FRUIT_2026-0701.md` (now marked COMPLETE).

## Findings & Fixes (all 13 implemented + 1 bonus)

### Correctness

1. **[CRASH] Header with empty value panicked the whole process.**
   `handleConnection` sliced header values as `message[colon+2 : len(message)-2]`,
   assuming a space after the colon. `X-Empty:\r\n` inverted the slice bounds →
   panic → process exit (no `recover()` anywhere in the package). Confirmed with a
   live repro: `GET / HTTP/1.1\r\nX-Empty:\r\n\r\n` → `panic: slice bounds out of
   range [9:8]`, process dead.
   Related: `Host:example.com` (no space, legal per RFC 7230) silently lost the
   first byte of the value.
   **Fix:** `value := strings.TrimSpace(message[colon+1:])` — TrimSpace removes
   the trailing CRLF and optional whitespace (OWS) in one shot; no bounds risk.

2. **No panic recovery.**
   **Fix (two levels):** recover around the handler chain in `handleRequest` —
   panic becomes an error routed to the errorHandler (500 + stack logged via
   `runtime/debug.Stack()`); plus a backstop `defer recover()` in
   `handleConnection` (writes canned 500, connection dies, process lives).
   `TestPanic` rewritten: it previously *asserted* that panics propagate; now it
   asserts panic → 500 response.

3. **Form parsing broke on Content-Type with parameters.**
   `parsePostArgs` used `bytes.EqualFold(ct, "application/x-www-form-urlencoded")`
   — failed for `...; charset=UTF-8` (jQuery et al.), so GetPostValue/FormValue
   silently returned "".
   **Fix:** new `hasContentTypePrefix(ct, prefix []byte)` helper in helpers.go
   (case-insensitive prefix via `bytes.EqualFold` on the prefix-length slice).
   Applied in parsePostArgs, ParseMultipartForm, FormValue, and both dispatch
   sites in handleRequest.

4. **`Server.Request()` ignored its `body io.Reader` param.**
   **Fix:** reads body into `ctx.request.body` and seeds `ctx.request.ContentType`
   from the supplied headers (normally set during wire parsing; form-parsing
   paths key off it).

5. **Unbounded request-body allocation (memory DoS).**
   `make([]byte, contentLen)` trusted client Content-Length; chunked totals were
   unbounded; negative chunk sizes panicked in `make`.
   **Fix:** `ServerOptions.MaxRequestBodySize` + `WithMaxRequestBodySize()`.
   0 → default 100 MB (`defaultMaxRequestBodySize`), negative → unlimited.
   Over-cap → canned 413 (`consts.HTTPPayloadTooLarge`) and close. Negative chunk
   size → 400. Also copied in `WithOptions`.

6. **Proxy corrupted multi-value response headers.**
   Joined values with "," — breaks multiple `Set-Cookie`.
   **Fix:** forward one header line per value via `AddHeader`. **`AddHeader` was
   added to the `Response` interface** (concrete type already had it) — breaking
   only for external implementers of the interface.
   Also: URL construction switched `filepath.Join` → `path.Join` in Proxy route
   registration, proxy URL building, and StaticFiles route building (Windows
   would have produced backslashes). Filesystem paths still use `filepath.Join`.

### Performance

7. **`Request.Header()`** allocated `strings.ToLower(key)` per loop iteration and
   missed mixed-case keys. → single `strings.EqualFold` pass; `UserAgent()` now
   delegates to `Header("User-Agent")`.

8. **`QueryParam` re-parsed the query string every call.** The unused `queryArgs`
   field was the intended cache. → lazy `parseQueryArgs()` with `parsedQueryArgs`
   flag; reset in `Clean()` (flag + `queryArgs.Reset()`).

9. **Request bodies allocated twice** (fresh buffer, then append into pooled
   buffer). → `slices.Grow` + `io.ReadFull` directly into `ctx.request.body`
   for both fixed-length and chunked bodies; HEAD/TRACE bodies consumed with
   `io.CopyN(io.Discard, ...)` without buffering.

10. **`StaticFiles` re-sent full files every request.** → `os.Stat` first
    (missing file/dir now 404 instead of 500-via-error), honors
    `If-Modified-Since` with 304 (compare at 1-second grain via
    `modTime.Truncate(time.Second)` vs `http.ParseTime`), serves via
    `FileWithModTime` so `Last-Modified` goes out.

11. **HEAD responses included a body.** → `writeResponse` skips the body write for
    HEAD (Content-Length still reflects what GET would send, per RFC 7231 §4.3.2).

12. **`writeResponse` allocated a fresh `bytes.Buffer` per request.** → `respBuf
    bytes.Buffer` scratch field on the pooled context, `Reset()` before use.

13. **`parseURL` dead condition** `queryPos < len(url)+1` removed.

### Bonus (found during verification)

**HTTP dates were invalid.** `setFileHeaders` formatted `Date`/`Last-Modified`
with `time.RFC1123`, which renders `...UTC` — HTTP requires `...GMT`
(IMF-fixdate). `http.ParseTime` rejects the UTC form, so If-Modified-Since
round-trips could never match (this is why the 304 e2e check initially failed).
**Fix:** `http.TimeFormat` in send.go; `TestFileWithModTime` updated to expect it.

## Verification

- `go test ./...` green (rweb, core/rtr, middleware/stylus); `go vet` clean.
- End-to-end raw-socket program (scratchpad) — all 11 checks pass:
  form+charset, 413 on huge Content-Length, chunked ok, negative chunk → 400,
  HEAD no body, static 200 + Last-Modified, static 304, static 404,
  synthetic request body, cached query params, mixed-case header lookup.
- New regression test `TestHeaderEdgeCases` (raw conn: empty header value → 200;
  no-space-after-colon value preserved).
- `gofmt` applied to touched files (only consts/http.go needed it).

## Files Changed (10 files, +262/−66)

- `Server.go` — header value parse fix, dual recover, body cap + direct-read,
  MaxRequestBodySize option, Request() body, proxy AddHeader loop, path.Join,
  StaticFiles stat/304, HEAD body skip, respBuf reuse
- `Context.go` — respBuf field + bytes import, queryArgs reset in Clean(),
  UserAgent delegation
- `Request.go` — hasContentTypePrefix usage ×3, Header EqualFold, queryArgs cache
- `Response.go` — AddHeader added to Response interface
- `send.go` — http.TimeFormat for Date/Last-Modified
- `helpers.go` — hasContentTypePrefix helper
- `http.go` — parseURL dead-condition cleanup
- `consts/http.go` — HTTPInternalError, HTTPPayloadTooLarge canned responses
- `Server_test.go` — TestPanic rewritten, TestHeaderEdgeCases added
- `send_test.go` — expects http.TimeFormat
- (new) `ai_docs/PLAN_LOW_HANGING_FRUIT_2026-0701.md`

## Behavioral Changes to Note

- Handler panics no longer propagate — they become 500 responses.
- `Response` interface gained `AddHeader` (breaking for external implementers).
- Request bodies now capped at 100 MB by default (`MaxRequestBodySize` to change;
  negative disables).
- Missing static files return 404 (previously 500 via error handler).
- HTTP `Date`/`Last-Modified` headers now use GMT format.

## Not Yet Done / Follow-ups

- Code changes are uncommitted (user has not asked to commit them).
- Larger items deliberately out of scope: streaming static files / proxy
  responses, header count/size limits, `Connection: close` handling.
