# N-073: handler-set Content-Length / Transfer-Encoding no longer sent

Session id: `4dabcca0-2228-4f86-b9e4-f9eb9306bc93`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `2fb5382` → end `b93f857` (+ this doc)
Item: N-073 from `ai_docs/todo/next-list.md` (value medium, raised in the N-050 work).

## What was asked

1. Work N-073: a handler that sets `Content-Length` (or `Transfer-Encoding`)
   got it sent next to the server's own framing header, e.g.
   `SetHeader("Content-Length", "999")` + `WriteString("hi")` →
   `Content-Length: 2` and `Content-Length: 999`. Two framings make a
   response ambiguous to a proxy in front (RFC 9112 §6.3). Mind an explicit
   `s.Head` route that states the GET length for a bodiless HEAD reply.
2. "yes, commit it", then `/sw`.

## Decisions (made on judgement, no user input needed)

- **Skip, not refuse.** Refusing at `SetHeader` would break middleware that
  reads the header back and the HEAD use case. The write path is the one
  place that knows what is actually sent, so the drop happens there.
- **Log level.** A handler length that differs from the one sent (or is not
  a valid length) is a **warning** naming `set` and `sent`: the handler
  believed its body to be a different size, which is a bug worth seeing.
  Everything else (a matching length, the net/http habit; any
  `Transfer-Encoding`; a length on a response that states none) is a debug
  line under `Debug` only, so a harmless habit does not spam the log.
- **HEAD exception.** With no in-memory body and no pending stream, the
  handler's first `Content-Length`, if valid (spaces/tabs trimmed), is sent
  instead of 0. A HEAD served by a GET handler still has a body or sized
  stream to measure, and the measure wins, as for GET. Known edge, accepted:
  a GET handler that sets a length and writes nothing sends 0 on GET (with a
  warning) but the handler's length on HEAD.
- **304.** A handler `Content-Length` on a 304 is now dropped rather than
  passed through. RFC 9110 §8.6 allows it only when it equals the
  representation's length, which rweb cannot check, and caches never take
  Content-Length from a 304 (RFC 9111 §3.2); net/http drops it too.
- **WebSocket 101.** `writeWebSocketUpgradeResponse` copies
  `ctx.response.headers` as well, so it skips framing headers too (RFC 9110
  §8.6 / RFC 9112 §6.1 forbid them on a 1xx). A middleware that sets one on
  every response would otherwise put it on the 101.

## Changes (`ac411b1`)

- `Server.go`
  - `writeResponse`: computes `sentLen` (the Content-Length written, -1
    for none: chunked, HTTP/1.0 unframed, SSE, bodyless status). For a
    bodiless HEAD it uses `response.declaredContentLength()` when valid.
    The header loop skips `isFramingHeader` names and reports each via
    `dropFramingHeader`. Comment block with a before/after diagram.
  - `isFramingHeader(name)`: `headerKeyEqual` against the two names; the
    length check settles almost every other name before any byte compare.
  - `dropFramingHeader(ctx, h, sentLen)`: warn on mismatch/invalid when a
    length was sent, else debug under `Debug`. Runs only when such a header
    is present, so the hot path pays two length compares per header.
  - `writeWebSocketUpgradeResponse`: skips framing headers.
- `Response.go`: `response.declaredContentLength()` — first Content-Length,
  OWS-trimmed, parsed with `parseContentLength` (request_framing.go); -1 if
  absent or invalid. Never copied to the wire, so a value added twice still
  yields one line.
- `response_framing_test.go` (package `rweb`, uses `serveTest`/`dialTest`
  and `captureLogger`):
  - `TestHandlerFramingHeadersNotSent`: one keep-alive conn; each case is
    followed by `GET /ok`, whose status line must start where the framing
    said the body ended. Cases: GET mismatch / match (lower-case, AddHeader)
    / TE on a sized body; HEAD on the GET fallback; HEAD explicit, added
    twice, spaced, invalid (→ 0); 304; 204 with both headers; a length
    beside a chunked stream (TE only); the same stream to HTTP/1.0
    (unframed, neither header). Asserts exactly three warnings with the
    right `set`/`sent`.
  - `TestHandlerFramingHeaderDebugLine`: under `Debug`, a dropped TE gives
    one debug line and no warning.
  - `TestWebSocketUpgradeOmitsFramingHeaders`: middleware-set CL and TE are
    not on the 101; an ordinary header (`X-Kept`) is.
  - All three failed on the old code (reproduced the `2,999` double length
    and the framing headers on the 101).
- Docs: CLAUDE.md gains a "Response framing" bullet; SKILL.md gains a
  paragraph on framing headers (v0.2.1+) and the HEAD-length note.

The assert library's `Equal` takes only comparable values and no message,
so slice comparisons go through `strings.Join` and messages via `t.Errorf`.

## Checks

- `go test -race -count=1 ./...` passes; `GOOS=windows go vet ./...` passes;
  `gofmt -l` clean.
- `BenchmarkKeepAliveHello` / `BenchmarkKeepAliveBrowser`, old vs new test
  binaries interleaved 3×: within loopback noise (≈14–16 µs either way, one
  old outlier at 20.8 µs), no consistent change.

## Commits

- `ac411b1` Response framing: never send a handler-set Content-Length or
  Transfer-Encoding (N-073)
- `b93f857` next-list: close N-073
- This doc, with the session stem added to N-073's Closed entry.

## Next

Closed: N-073. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
