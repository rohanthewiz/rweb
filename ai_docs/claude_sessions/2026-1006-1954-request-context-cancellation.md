# Session: Request().Context() — cancel a handler when the client leaves

**Session ID:** `08db3691-72c2-4b1f-afc8-bc4d8dbb119d`
**Date:** 2026-10-06
**Branch:** master
**Commit:** `0700738` (code, tests, docs); tagged `v0.2.0`

## Ask

The session started in roman (`~/projs/go/roman`) on its next-list item
N-006: the web UI cannot cancel a batch, because roman's `/batch` handler
runs on `context.Background()` — rweb never cancelled a handler's context when
the browser left (it had no request context at all). While roman-side
workarounds were being designed (run IDs, a `/batch/cancel` endpoint, a
`pagehide` beacon, a cancel-before-register tombstone), the user asked:

1. "Would it be better to support cancelling a handler's context when the
   browser leaves in RWeb?" — answered yes, with the design and its limits.
2. "start on rweb first, but be sure that long-lived connections like WS and
   SSE still properly work"
3. "commit both and wrap the session (/sess-wrap) on rweb. Let's do a minor
   version bump there to be safe" — hence `v0.2.0`, not `v0.1.33`: the change
   adds a method to the exported `ItfRequest` interface.

No roman code changed; roman's N-006 is the next step there (see Next).

## Why rweb, not roman

rweb serves each connection in one goroutine: read a request, run the handler
synchronously, write the buffered response, loop. Nothing reads the socket
while the handler runs, so a client closing is invisible until a write fails —
and rweb writes nothing until the handler returns. With a cancelling request
context in rweb, roman's cancel becomes `AbortController.abort()` on the
`/batch` fetch (an aborted HTTP/1.1 fetch closes its socket), and closing,
reloading or crashing the tab cancels too, with no endpoint, IDs or beacon.

## Design

Modeled on net/http's `connReader` (`startBackgroundRead` /
`abortPendingRead`), which is how `http.Request.Context()` learns a client has
gone.

```
conn ──► connReader ──► bufio.Reader (ctx.reader) ──► request parsing
           │
           └─ watch goroutine, only while a request context is live:
                conn.Read(1 byte) returns
                  error / EOF           → cancel the request context
                  1 byte (pipelining)   → keep it (next Read returns it first),
                                          stop watching, do not cancel
                  deadline (stopWatch)  → expected, nothing happened
```

- **API:** `ctx.Request().Context() context.Context` on `ItfRequest` (mirrors
  net/http's `r.Context()`). Rejected: making `rweb.Context` itself implement
  `context.Context` (gin-style) — the rweb context is pooled and reused, so a
  goroutine holding it past the handler would see the next request.
- **Lazy:** the `context.WithCancel` and the watch are created on the first
  `Context()` call. A keep-alive benchmark (loopback, old HEAD vs new) showed
  handlers that never ask unchanged: 11 allocs, ~25 µs/op both. Asking costs
  ~+5 allocs, +340 B, ~+5–8 µs (goroutine, two `SetReadDeadline` syscalls).
- **Why under bufio:** the watch's byte is the first byte of the next request;
  returning it from `connReader.Read` restores stream order without touching
  bufio internals. `handleConnection` now does
  `ctx.request.cr.reset(conn); ctx.reader.Reset(&ctx.request.cr)`.
- **Stopping:** `stopWatch` sets a past read deadline, waits for the goroutine,
  then clears the deadline so the conn is reusable. Works for `tls.Conn` too
  (crypto/tls treats the deadline error as temporary) — verified by the TLS
  keep-alive and WS tests.
- **Not watched:** a request with the next one already buffered (pipelined —
  the client has committed to reading responses, so a later half-close must not
  cancel), after `UpgradeWebSocket` or `GetConn` (their reads now), synthetic
  `Server.Request` (no conn). A half-close otherwise counts as gone, as in
  net/http.

### Lifecycle in `handleRequest`

1. `defer ctx.request.finishContext()` — stops any watch and cancels. Deferred
   so a panic escaping to the connection backstop cannot leave a watch
   goroutine reading into a context that has gone back to the pool.
   `Clean()` calls it again as a backstop.
2. Handler chain runs; `Context()` may start the watch.
3. `ctx.request.cr.stopWatch()` before `writeResponse` — `sendSSE` starts its
   own reader there; the context stays live.
4. `writeResponse` (for SSE: the whole stream), then the deferred cancel.

### WebSocket and SSE

- **WebSocket:** `upgradeWebSocket` calls `releaseConn()` (stop the watch, no
  restart) after a successful handshake and before writing the 101. The
  context stays live for the session and ends when the WS handler returns; a
  peer leaving shows as a `ReadMessage` error. `WSConn` gained an `r io.Reader`
  (default `conn`); `readFrame` reads from it. On upgrade, bytes already read
  off the conn — `ctx.reader`'s buffer, then a byte the watch took — are
  copied and fed first via `io.MultiReader`. This also fixes a pre-existing
  bug: frames arriving in the same write as the upgrade request were dropped.
- **SSE:** the context lives through the stream, so a producer goroutine can
  stop on `rc.Done()`; it is cancelled when `sendSSE` returns (client gone,
  channel closed, "close" sentinel). `handleConnection` now returns after an
  SSE response explicitly — the stream has no Content-Length, so its end is
  the connection's end. Before, that relied on the past read deadline
  `sendSSE` leaves behind; a byte read ahead by the watch could have let the
  next read start succeeding.
- **GetConn:** now calls `releaseConn()`, since a caller reading the raw conn
  would race the watch. Docs point to `ClientIP` for the address.

## Tests — `request_context_test.go` (internal package)

Real loopback connections, plain TCP and TLS, served through
`handleConnection` on a per-test listener (no `Run`/SIGTERM), so they are
isolated. 12 tests:

| Test | Pins |
|---|---|
| CancelledOnClientClose (tcp, tls) | close → `context.Canceled` (~0.1 ms) |
| CancelledWhenRequestEnds | live in handler, cancelled after response |
| Synthetic | `s.Request` works, cancelled after |
| KeepAlive (tcp, tls) | ctx / plain / panic-with-watch / ctx on one conn |
| PipelinedByteIsKept (tcp, tls) | 2nd request sent mid-handler: no cancel, parses intact |
| PipelinedInOneWrite | both requests buffered: no watch, both succeed |
| PipelinedThenHalfClose | `CloseWrite` after pipelining does not cancel request 1 |
| GetConnStopsWatch | close after GetConn: no cancel mid-handler; cancel at end |
| WebSocketSession (tcp, tls) | echo ×3, ctx live during session, cancelled after peer leaves |
| WebSocketFrameWithUpgrade | frame in same write as upgrade reaches WS |
| WebSocketFrameDuringWatch (tcp, tls) | frame byte taken by watch goes to WS |
| SSEClientLeaves (tcp, tls) | producer runs through stream, stops on client close |
| SSEServerEnds | channel close → server closes the conn |

**Mutation check:** each mechanism was disabled on purpose and the suite
re-run; every mutation was caught — no watch; byte not handed back; deadline
not cleared (also broke WS and SSE tests); no stop at WS upgrade; no
readAhead; GetConn keeping the watch; no end-of-request cancel; watching with a
pipelined request buffered (initially *not* caught — which exposed a wrong
justification in the doc comment; fixed the comment and added
PipelinedThenHalfClose, which catches it).

Full suite: `go test -race -count=3 ./...` green; `gofmt -l .` empty;
`go vet ./...` clean.

## Files Changed

- `conn_reader.go` — new: `connReader` and the disconnect watch
- `Request.go` — `ItfRequest.Context()`; request fields `cr`, `reqCtx`,
  `reqCancel`, `noWatch`; `Context`, `releaseConn`, `readAhead`,
  `finishContext`
- `Server.go` — reader through `connReader`; explicit close after SSE;
  `handleRequest` lifecycle (defer finish, stop before `writeResponse`)
- `Context.go` — `Clean` finishes the context; WS upgrade releases the conn
  and feeds read-ahead bytes; `GetConn` releases the conn
- `websocket.go` — `WSConn.r`; frame reads use it
- `request_context_test.go` — new
- `README.md`, `ai_docs/SKILL.md`, `CLAUDE.md` — Request Context docs (v0.2.0+)
- `ai_docs/todo/next-list.md` — N-019–N-021 raised; line refs updated in
  N-004, N-005, N-015
- `ai_docs/claude_sessions/2026-1006-1954-request-context-cancellation.md` — this doc

## Next

Closed: None. Declined: None. Raised: N-019, N-020, N-021.
Deferred: None. Promoted: None.
Updated: N-004, N-005, N-015 (line references shifted by `0700738`).
Full list: `ai_docs/todo/next-list.md`.

Outside this repo: roman's N-006 is now unblocked — bump roman's rweb from
v0.1.30 to v0.2.0 (also brings v0.1.31/32, incl. 400 on a malformed or
duplicate Host header), pass `ctx.Request().Context()` to `Runner.Run` in
`/batch`, and add a Cancel button that aborts the fetch with `AbortController`.
