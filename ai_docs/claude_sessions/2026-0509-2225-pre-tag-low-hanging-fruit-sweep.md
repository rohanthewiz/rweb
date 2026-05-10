# Pre-tag low-hanging-fruit sweep — rweb

**Date:** 2026-05-09 22:25
**Session ID:** f0448dd7-19b1-42e7-8605-c00cfc789aba
**Branch:** master
**Starting commit:** 3e8d8f0 (Merge PR #29 — additional MIME types)
**Last tag:** v0.1.26

## Goal

User intended to push a new tag for the recent MIME-type fixes and asked to first sweep for low-hanging fruit (bugs or features) in rweb that could ship in the same tag.

## Approach

1. Three Explore agents in parallel:
   - Bugs / TODOs / dead code audit
   - Missing-feature gap analysis vs Echo/Gin/Fiber
   - SSE / WebSocket / security audit
2. Verified each finding by reading source directly (agents over-claimed in places — for example a "race condition on `listener.Close()`" turned out to be a benign redundant close; "proxy Host header forwarding" is intentional, not a bug).
3. Asked user to scope the in-scope items via AskUserQuestion. User selected 7 of 8 candidates; declined re-enabling the `ParseMultipartForm` auto-call in `GetFormFile`.
4. Wrote plan at `~/.claude/plans/snappy-coalescing-bear.md`, exited plan mode, implemented.

## Items shipped

### Bugs / hardening
1. **Path traversal in `StaticFiles`** (`Server.go`)
   - Wildcard param was joined into the FS path with no `Clean`/`Rel` guard. `GET /static/../../etc/passwd` would escape the configured `targetDir` since `filepath.Join` *normalizes* `..` rather than rejecting.
   - Fix: percent-unescape the wildcard, reject `..` segments and NUL bytes, then verify candidate is under root via `filepath.Abs` + `filepath.Rel`. Returns 404 (not 403) so off-root existence isn't probeable.
2. **SSE disconnect-detector goroutine leak** (`Server.go::sendSSE`)
   - The `go func(){ ctx.conn.Read(buf) }()` only unblocks on *client* close. On every other exit path (events channel close, `"close"` sentinel string, write error) the goroutine stayed parked on a conn the framework had stopped tracking.
   - Fix: deferred `ctx.conn.SetReadDeadline(time.Unix(1,0))` + `sync.WaitGroup.Wait()` ensures the inner goroutine retires before `sendSSE` returns. Defer ordering: registered after the existing `sseCleanup` defer, so it runs *before* cleanup (LIFO), guaranteeing the reader is gone before any cleanup callbacks fire.
3. **`X-Content-Type-Options: nosniff` on file responses** (`send.go::setFileHeaders`)
   - One-line addition. Defense-in-depth for user-controlled static content.

### Features added to the public API
4. **`Context.NoContent() error`** — 204 helper.
5. **`Request.GetFormFiles(key) ([]*multipart.FileHeader, error)`** — multi-file uploads. Existing `GetFormFile` only returned the first file under a key.
6. **`Context.ClientIP() string`** — `X-Forwarded-For` (first entry) → `X-Real-IP` → `RemoteAddr` host portion. Documented in interface as untrusted-by-default.
7. **`Context.BasicAuth() (user, pass string, ok bool)`** — RFC 7617 parsing. Case-insensitive `Basic` prefix; preserves colons in password; only returns `ok` when header is present, well-formed, base64-decodes, and contains `:`.

## Verification methodology that proved valuable

- **Adversarial test for SSE leak:** wrote `TestSSESenderGoroutineExitsOnChannelClose` first using `runtime.NumGoroutine()`, but the test passed even with the fix reverted because the test client's still-open conn parks `handleConnection` separately — the +1 from the leaked reader was hidden in the noise. Switched to scanning every goroutine's stack trace with `runtime.Stack(buf, true)` and matching on `rweb.(*Server).sendSSE.func` + an in-flight Read frame. Verified by temporarily reverting the fix — test correctly identified the leaked goroutine parked at `Server.go:1057`. Then restored the fix; test passes again.

## Coverage gap audit (round 2)

User asked for an honest gap audit. Identified five gaps; closed four, deferred one with rationale:

| Gap | Resolution |
|---|---|
| `GetFormFiles` had **zero** test coverage | Added `TestGetFormFiles` (real-HTTP multipart upload: 3 files under same key + decoy under different key, asserts count/names/contents) and `TestGetFormFilesNotPresent` (missing-key error path). The synthetic `s.Request()` doesn't pipe the body parameter through to the multipart parser, so this had to be a real-server test. |
| SSE leak fix only tested via channel-close exit | Added `TestSSESenderGoroutineExitsOnCloseSentinel` for the `"close"` string sentinel exit path. |
| Path traversal table missed NUL byte / double-slash / absolute-path-injection | Added `/static/allowed.txt%00.evil`, `/static//etc/passwd`, `/static/%2fetc/passwd` to the existing table. |
| `ClientIP` `RemoteAddr` fallback untested (synthetic `Request` has no conn) | Added `TestClientIPRemoteAddrFallback` against real listener; asserts loopback IP returned without port. |
| Symlink-following in `StaticFiles` | Deferred. Defending requires `EvalSymlinks` per request — perf cost users should opt into. Out of scope for tag-prep. |

Two other items deliberately *not* added:
- SSE write-error exit path test — would require whitebox + faulty `respWriter` injection. The deferred cleanup covers all return paths uniformly; the two we tested exercise the same `defer`.
- `NoContent` body-already-written test — my impl mirrors Echo/Gin (no body clearing). API-design choice, not a bug.

## Final state

- 9 files modified, 727 insertions, 4 deletions.
- `go vet ./...` clean.
- `go test ./...` and `go test -race ./...` green.
- Ready to tag (suggested `v0.1.27`).

## Files touched

- `Server.go` — path-traversal guard in `StaticFiles`; SSE goroutine cleanup in `sendSSE`. Added `slices`, `time` imports.
- `Request.go` — `GetFormFiles` impl and `ItfRequest` interface entry.
- `Context.go` — `NoContent`, `ClientIP`, `BasicAuth` (interface + impl). Added `encoding/base64`, `consts` imports.
- `send.go` — `X-Content-Type-Options: nosniff` in `setFileHeaders`.
- `Server_test.go` — `TestStaticFilesPathTraversal` (8 traversal variants + happy path).
- `Request_test.go` — `TestGetFormFiles`, `TestGetFormFilesNotPresent`.
- `Context_test.go` — `TestNoContent`, `TestClientIP` (table-driven), `TestClientIPRemoteAddrFallback`, `TestBasicAuth` (9 sub-cases).
- `sse_test.go` — `TestSSESenderGoroutineExitsOnChannelClose`, `TestSSESenderGoroutineExitsOnCloseSentinel`.
- `send_test.go` — added nosniff header assertion in `TestFileHeaders`.

## Items rejected from scope (worth recording so we don't re-investigate)

- **405 Method-Not-Allowed**: requires router to track methods-per-path. Not low-hanging; defer.
- **`listener.Close()` "race"** (one of the agents flagged it): `defer listener.Close()` + explicit close in `Run()` is just redundant. `net.Listener.Close` is idempotent. No race.
- **Proxy `Host` header forwarding**: intentional for transparent-proxy use case. Not a bug.
- **`CookieConfig.EncryptionKey` is declared but unused**: real gap, but full HMAC/AES wiring is bigger than tag-prep. Defer.
- **Re-enabling `ParseMultipartForm` auto-call in `GetFormFile`** (lines 214-216 of `Request.go`): currently commented out, forcing callers to parse manually. User declined to re-enable in this pass.

## Plan file

`~/.claude/plans/snappy-coalescing-bear.md` — final approved plan with implementation specifics for each in-scope item.
