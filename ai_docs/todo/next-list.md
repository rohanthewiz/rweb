# Next list — rweb

The project's open follow-ups, kept as one living list. Sessions edit this file
in place instead of copying a Next section forward from the previous session
doc: an item is added when raised, edited when its premise changes, and moved
(never deleted) when done or declined. A session doc's `## Next` then records
only `Closed: N-… Raised: N-…`.

Seeded 2026-10-03 by `/next-list seed` from all eight session docs
(`2026-0509-2225` … `2026-0919-2229`), with each premise re-checked against the
code at `3169f85`.

## Conventions

- **IDs are permanent** (`N-001`, …) and never reused or renumbered.
- **`raised`** is the stem of the session doc the item first appeared in, even
  if it later lapsed and was re-raised.
- **Age is computed, never stored**: the number of session docs since `raised`,
  the newest doc being age 0. `/next-list` works it out.
- **Value** is the payoff of doing it, not the effort:
  - `high` — something is worked around today, or a second independent
    consumer has arrived
  - `medium` — it blocks one named thing, or it is a visible defect nobody has
    to route around yet
  - `low` — a gap nobody has bumped into, or contingent on something that does
    not exist
- **Sections by intent**: Open is the next build work; Validate is the next
  testing (run, look, measure, or write or repair a test; no product change
  planned); Roadmap is wanted but later; Non-goals is what we are likely never
  to do.
- **Nothing leaves Open, Validate or Roadmap without a line in another
  section** — done goes to Closed, declined goes to Non-goals, duplicates go
  to Closed as `merged into N-xxx`. Moving among Open, Validate and Roadmap is
  fine.
- **Open, Validate and Roadmap stay in ID order.**

**Next ID:** N-024

## Open

- **N-004** · raised `2026-0509-2315-fix-multipart-form-leak` · value medium
  Make multipart parsing fully lazy by dropping the eager pre-parse in
  `handleRequest` (`Server.go:1297`). The pre-parse runs before the handler
  chain, so a multipart POST to *any* path — even an unrouted one — is parsed
  and may spill to temp files, up to the 100 MB body cap. The accessors already
  parse on demand and idempotently (`Request.go:388,422,457`), so removing it
  should be safe; `WithMultipartMaxMemory` only tunes the cost. Also raised in
  `2026-0509-2336`; lapsed after both.

- **N-005** · raised `2026-0509-2315-fix-multipart-form-leak` · value low
  `GetPostValue` and `FormValue` overlap (`FormValue` falls through to
  `GetPostValue`, `Request.go:465`). A cleanup could deprecate `GetPostValue`
  to a thin alias. Lapsed. Candidate for Non-goals.

- **N-006** · raised `2026-0509-2336-fix-multipart-form-state-leaks` · value low
  Remove the `var _ = context.Background` guard at `Request_leak_test.go:516`.
  The raising doc said `context` stays in use through
  `httptrace.WithClientTrace`; it does not — `req.Context()` is a method and
  needs no import, so the guard is the import's only use. Removing it means
  removing the `"context"` import too. Lapsed.

- **N-007** · raised `2026-0509-2336-fix-multipart-form-state-leaks` · value low
  Dead code flagged by `staticcheck ./...`: 13 U1000 hits — unused funcs in
  `args.go` (`visitArgsKey`, `delAllArgsBytes`, `appendArgBytes`,
  `decodeArgAppendNoPlus`, `peekAllArgBytesToDst`, `peekArgsKeys`), unused
  funcs/vars in `bytesconv.go` (`readHexInt`, `writeHexInt`, `lowercaseBytes`,
  `hexIntBufPool`, `errEmptyHexNum`, `errTooLargeHexNum`), and
  `consts/text_bytes.go:4` `defaultContentType` — plus one S1017 at
  `Server.go:512`. Raised as editor lint hints; the `Cookie_test.go`
  `unusedwrite` hint it also named no longer reproduces. Lapsed.

- **N-010** · raised `2026-0701-2109-http-server-fixes-and-optimizations` · value low
  Static files are read fully into memory per request (`os.ReadFile`,
  `Server.go:800`); stream them instead. First written as the non-goal
  "streaming static files", reopened as an open item in `2026-0919-2153`. The
  user excluded it from the `2026-0919-2229` session.

- **N-015** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · value low
  Decide whether a request with **no** Host header should get 400 (RFC 9112
  requires it for HTTP/1.1). Tolerated deliberately today (`Server.go:1103`):
  hand-rolled clients and some raw-socket tests omit it.

- **N-017** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · value low
  `StaticContainSymlinks` leaves a check-to-read race between `EvalSymlinks`
  and `os.ReadFile` (noted at `Server.go:765`). Closing it needs `os.Root`,
  i.e. raising the `go` directive in `go.mod` from `1.23.0` to `1.24`+. Go
  1.23 is out of support, so the bump is cheaper than when this was raised.

- **N-018** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · value low
  Host validation runs on the wire path only; synthetic `s.Request()` calls
  bypass it. `s.Request()` is a test-oriented API. Candidate for Non-goals.

- **N-019** · raised `2026-1006-1954-request-context-cancellation` · value low
  `Server.Proxy` builds its upstream request with `http.NewRequest`, so a
  client leaving mid-proxy does not cancel the upstream call. Pass
  `ctx.Request().Context()` (`http.NewRequestWithContext`). The handler would
  then return `context.Canceled` for every abandoned proxy request, which the
  default error handler logs as an `[ERR]` and answers 500 on a dead conn, so
  skip the error path for `context.Canceled`, as `httputil.ReverseProxy` does.

- **N-020** · raised `2026-1006-1954-request-context-cancellation` · value low
  Shutdown does not cancel request contexts. `Run` closes the listener on
  SIGINT/SIGTERM but does not track connections, so in-flight handlers keep
  their `Request().Context()` live. Needs connection tracking or a
  server-wide base context that `Run` cancels.

- **N-021** · raised `2026-1006-1954-request-context-cancellation` · value low
  The installed skill copy `~/.claude/skills/rweb-light-go-webserver/SKILL.md`
  lags `ai_docs/SKILL.md`. As of 2026-10-10 it already has the TLS
  `TLSAddr`/autocert notes (the raising doc said it predated them); what it
  lacks is the "Compression (permessage-deflate)" and "Request Context
  (v0.2.0+)" sections. Re-sync it (or make it a link) so sessions using the
  skill see `Request().Context()` and `WriteMessages`/deflate. Since then
  `ai_docs/SKILL.md` also gained "404 vs 405" and "Encrypted Cookies"
  (N-001, N-002), which the installed copy lacks too.

- **N-022** · raised `2026-1010-2236-cookie-encryption-and-405` · value medium
  A request with a method the routers have no table for (e.g. `FOO /x`)
  panics in dispatch: `RadixRouter.selectTree` returns nil for it and
  `LookupNoAlloc` dereferences that nil tree (`Server.go` default handler).
  The panic is recovered, so the client gets a 500 and the log an `[ERR]`
  with a stack, per request. Should be `501 Not Implemented` (RFC 9110
  §15.6.2) or 405; `HashRouter.Lookup` already copes (nil map). Found while
  doing N-002; predates it.

- **N-023** · raised `2026-1010-2236-cookie-encryption-and-405` · value low
  HEAD is not implied by GET: a HEAD to a GET-only route gets 405 `Allow: GET`
  (404 before N-002). RFC 9110 §9.3.2 expects HEAD wherever GET is served.
  Could fall back to the GET handler for HEAD (the write path already drops
  the body for HEAD, `Server.go` near `MethodHead`) and add HEAD to `Allow`
  when GET is routed.

## Validate

Items whose remaining work is purely testing: run, look, measure, or write or
repair the test that covers it. No product change is planned unless the check
finds a defect. Same item form as Open.

- **N-016** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · value low
  The keep-alive `ctx.conn` restore was verified through `GetConn()` only. No
  test drives a WebSocket upgrade or an SSE stream as the *second* request on a
  reused connection (checked `websocket_test.go`, `websocket_deflate_test.go`,
  `sse_test.go`, `sse_hub_test.go`). Re-checked 2026-10-10 after `0700738`:
  `request_context_test.go` adds WS and SSE tests, but each upgrades or streams
  on the conn's first request; `TestRequestContextKeepAlive` reuses a conn only
  for plain requests. Still owed. Moved here from Open 2026-10-10 (pure test
  work).

## Roadmap

Wanted, but deliberately not next. Parked, not declined. Same item form as
Open.

_(empty — no session doc marked an item as deferred-but-wanted; sort Open
items here as you see fit)_

## Non-goals

- **N-011** · declined `2026-0701-2109-http-server-fixes-and-optimizations` — Streaming proxy responses. Out of scope for that review and carried as a deliberate non-goal since.
- **N-012** · declined `2026-0701-2109-http-server-fixes-and-optimizations` — Header count/size limits. Same; still none in `Server.go`.
- **N-013** · declined `2026-0701-2109-http-server-fixes-and-optimizations` — `Connection: close` handling. Same; the server does not act on a client's `Connection: close`.

## Closed

Newest first. Closures before this file existed are recorded in the session
docs under `ai_docs/claude_sessions/`; the ones below were found done while
seeding.

- **N-002** · raised `2026-0509-2225-pre-tag-low-hanging-fruit-sweep` · closed 2026-10-10, 2026-1010-2236-cookie-encryption-and-405
  405 Method Not Allowed. Done: on a route miss the default handler probes
  the other methods' tables (`Server.allowedMethods`, miss path only) and
  answers 405 with `Allow` when the path is routed under any of them.
  Tests: `method_not_allowed_test.go`. Raised N-022 and N-023 on the way.
- **N-001** · raised `2026-0509-2225-pre-tag-low-hanging-fruit-sweep` · closed 2026-10-10, 2026-1010-2236-cookie-encryption-and-405
  `CookieConfig.EncryptionKey` was declared but never read. Done with the
  full fix, not the comment: AES-GCM seal/open in `SetCookieWithOptions` and
  `GetCookie`, cookie name as additional data, `ErrCookieDecrypt` on any
  failure, and a bad key fails closed (logged at `NewServer`, returned by
  every cookie set/get). Tests: `Cookie_encrypt_test.go`.
- **N-014** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · closed 2026-10-03
  Tag a release v0.1.31. Done: `v0.1.31` contains `8e70974`; `v0.1.32` is also
  tagged.
- **N-009** · raised `2026-0701-1727-add-stylus-css-middleware` · closed 2026-10-03
  Pre-existing gofmt drift in ~12 files. Done in `2e3202b`; `gofmt -l .` was
  empty after it (`2026-0919-2229`).
- **N-008** · raised `2026-0701-1727-add-stylus-css-middleware` · closed 2026-10-03
  Push `feat/stylus-middleware` and open a PR. Merged: `ded4423` is on master.
- **N-003** · raised `2026-0509-2225-pre-tag-low-hanging-fruit-sweep` · closed 2026-10-03
  Re-enable the `ParseMultipartForm` auto-call in `GetFormFile` (declined in
  the raising doc). Done since: `GetFormFile` and `GetFormFiles` now call it
  (`Request.go:280,314`).
