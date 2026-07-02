# Plan: Low-Hanging Fruit — Correctness & Performance (2026-07-01)

**STATUS: COMPLETE (2026-07-01).** All 13 items implemented and verified:
full test suite green, plus end-to-end raw-socket checks for the crash fix,
panic recovery, 413 cap, chunked bodies, HEAD, static 304, form charset,
synthetic request bodies, cached query args, and mixed-case header lookup.
Bonus fix found during verification: HTTP dates were emitted as RFC1123
"UTC" instead of the required "GMT" (`http.TimeFormat`), which broke
If-Modified-Since round-trips (send.go). Regression tests added:
`TestPanic` (rewritten — panic now yields 500, not a crash) and
`TestHeaderEdgeCases` (empty header value / no-space-after-colon).

Findings from a review of the core request path (Server.go, Context.go, Request.go,
Response.go, send.go, Group.go). Ordered by severity. Items 1–2 first (remotely
triggerable full-process crash).

## Correctness

1. **[CRASH — confirmed by repro] Header with empty value panics the whole server.**
   `handleConnection` slices header values as `message[colon+2 : len(message)-2]`,
   assuming a space after the colon. A request line `X-Empty:\r\n` inverts the slice
   bounds → panic → process exit (no recover anywhere).
   Also: a legal header with no space after the colon (`Host:example.com`) silently
   loses the first byte of its value.
   **Fix:** slice `[colon+1 : len(message)-2]` with a bounds guard; `TrimSpace` the
   value (RFC 7230 allows optional whitespace).

2. **No panic recovery.** Any panic in a user handler (or parser bug) kills the
   process, not just the connection.
   **Fix:** deferred `recover()` in `handleConnection` (process safety backstop) +
   recover around the handler chain in `handleRequest` so a handler panic becomes a
   500 via the error handler.

3. **Form parsing breaks when Content-Type carries a charset param.**
   `parsePostArgs` uses `bytes.EqualFold(ct, "application/x-www-form-urlencoded")`,
   which fails for `...; charset=UTF-8` (jQuery et al.) → `GetPostValue`/`FormValue`
   silently return "".
   **Fix:** case-insensitive *prefix* match; apply consistently to the multipart
   checks too (those are HasPrefix but case-sensitive).

4. **`Server.Request()` ignores its `body io.Reader` param.** Synthetic test
   requests with bodies silently test empty bodies.
   **Fix:** read body into `ctx.request.body`; also seed `ctx.request.ContentType`
   from the passed headers so form parsing works.

5. **Unbounded request-body allocation (memory DoS).**
   `make([]byte, contentLen)` trusts client Content-Length; chunked total unbounded.
   **Fix:** `ServerOptions.MaxRequestBodySize` (+functional option). 0 → default
   (100 MB), -1 → unlimited. Over limit → 413 + close. Enforce for both fixed-length
   and chunked bodies.

6. **Proxy mangles multi-value response headers.** Joins values with "," — corrupts
   multiple `Set-Cookie`. Also `filepath.Join` used to build URL routes (breaks on
   Windows).
   **Fix:** `AddHeader` per value (add `AddHeader` to the `Response` interface);
   `path.Join` for URL construction (keep `filepath.Join` for filesystem paths).

## Performance

7. **`Request.Header()` allocates `strings.ToLower(key)` per loop iteration** and
   only matches exactly-lowercase keys — misses `Content-type:`.
   **Fix:** single `strings.EqualFold` pass. `UserAgent()` then delegates to it.

8. **`QueryParam` re-parses the whole query string on every call.** The unused
   `queryArgs` field was clearly intended as the cache.
   **Fix:** lazy parse-once into `queryArgs` (+ `parsedQueryArgs` flag, reset in
   `Clean()`).

9. **Request bodies allocated twice** (fresh buffer + append into pooled buffer),
   even for HEAD/TRACE where the body is discarded.
   **Fix:** grow `ctx.request.body` and `ReadFull` directly into it; for HEAD/TRACE
   discard with `io.CopyN(io.Discard, ...)`.

10. **`StaticFiles` sends full file every time.** No Last-Modified / 304 handling
    although `FileWithModTime` already exists.
    **Fix:** `os.Stat` → `FileWithModTime`; honor `If-Modified-Since` with 304.

## Smaller notes

11. **HEAD responses include a body.** `writeResponse` writes the body regardless of
    method. Fix: skip body for HEAD (keep Content-Length).
12. **`writeResponse` allocates a fresh `bytes.Buffer` per request.** Reuse a scratch
    buffer on the pooled context.
13. **`parseURL` dead condition** `queryPos < len(url)+1` is always true — remove.

## Verification

- `go test ./...` after each group of changes.
- Re-run the crash repro (raw `X-Empty:\r\n` header) — must get a 400/normal
  response, not a process exit.
- Panic-in-handler test: handler that panics → 500, server keeps serving.
