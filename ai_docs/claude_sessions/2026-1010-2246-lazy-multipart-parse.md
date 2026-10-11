# Session: lazy multipart parsing (N-004)

**Session ID:** `84cf0cbb-23e3-4bf1-9e76-04961f3b5d6b` (continues
`2026-1010-2236-cookie-encryption-and-405`)
**Date:** 2026-10-10
**Branch:** master
**Base commit:** `39136f0`

## Ask

"When completely done here do a /sess-wrap, then do N-004 next." The wrap
produced `2026-1010-2236-cookie-encryption-and-405` (`39136f0`, pushed); this
doc covers N-004.

## Problem

`handleRequest` parsed every `multipart/form-data` body before the handler
chain ran, so before routing. A multipart POST to any path, even an unrouted
one or one whose handler never reads the form, was parsed, and `ReadForm`
spilled parts past `MultipartMaxMemory` to temp files: up to the 100 MB body
cap of disk churn nobody asked for. `WithMultipartMaxMemory` only tuned that
cost.

## Change

- **`Server.go` (`handleRequest`)**: removed the eager `ParseMultipartForm`
  branch. The comment there now explains why multipart is lazy and why
  urlencoded is still parsed eagerly (an in-memory scan of a body already
  read; no disk cost; Debug prints the args).
- **Why removal is safe**: every reader of `request.multipartForm`
  (`GetFormFile`, `GetFormFiles`, `FormValue`) already calls
  `ParseMultipartForm`, which is idempotent and caches both the form and the
  error. Nothing else touches the field except `CleanupMultipartForm`, which
  is nil-safe. Confirmed by grep before removing.
- **Comments updated** to stop describing an up-front parse:
  `ParseMultipartForm`, `GetFormFile`, `GetFormFiles`, `FormValue`
  (`Request.go`); `TestMalformedMultipartSurfacesParseError`
  (`Request_leak_test.go`); `ServerOptions.MultipartMaxMemory` now says the
  form is parsed on first read.
- Lost with the eager branch: the `Error parsing multipart form` stdout print
  and the Verbose `Parsed Multipart Form` print. Parse errors still reach the
  handler through `GetFormFile`/`GetFormFiles`.

## Tests (`multipart_lazy_test.go`, new)

Uses a 1 KB `MultipartMaxMemory` and a 64 KB file part, with `TMPDIR` pointed
at `t.TempDir()`, so the temp dir shows whether a parse ran.

- `TestMultipartParsedOnlyOnUse` (synthetic `s.Request`): a middleware counts
  temp entries after the handler. Unrouted path and a form-ignoring handler →
  0; `FormValue` handler → spilled and returns the value; `GetFormFile`
  handler → returns the filename. **Fails on the old code** (verified by
  stashing `Server.go`), passes now.
- `TestMultipartLazyParseCleanedUp` (real connection): a handler that calls
  `GetFormFile` sees a spill file; after the response the temp dir empties
  (polled up to 2 s, since `Clean` runs just after the response is written).
  The count is an `atomic.Int64` because only the socket orders the server
  and client goroutines.

## Found along the way (raised)

- **N-024 (low)**: synthetic `s.Request()` never runs the per-request `Clean`
  (it returns the context's own `Response`, which `Clean` would zero), so a
  spilled multipart form leaves temp files. Only the test-oriented API is
  affected; the wire path cleans after every request (`Server.go`, after the
  SSE check in the keep-alive loop, and in `handleConnection`'s defer).

## Verification

`go test -race ./...` passes; `gofmt -l .` empty; `go vet .` clean;
`staticcheck .` shows only the pre-existing N-007 hits.

## Next

Closed: N-004. Declined: None. Raised: N-024.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
