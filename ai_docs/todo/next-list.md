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

**Next ID:** N-059

## Open

- **N-035** · raised `2026-1109-once-over-review` · value high
  No header size limit (a 64 MB header line was accepted) and no read, write or idle timeouts (`Server.go:1231`). Reopens declined N-012. Needs decision D2. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-036** · raised `2026-1109-once-over-review` · value medium
  Request framing accepts ambiguous messages: Content-Length wins over chunked, negative and conflicting Content-Length accepted (`Server.go:1305`). Reproduced. Needs decision D3. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-037** · raised `2026-1109-once-over-review` · value medium
  `Expect: 100-continue` gets no interim response, so curl stalls about a second on large uploads. Reproduced. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-038** · raised `2026-1109-once-over-review` · value low
  `Connection: close` and HTTP/1.0 are ignored; the request-line version is not parsed. Reopens declined N-013. Needs decision D4. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-039** · raised `2026-1109-once-over-review` · value high
  No graceful shutdown: no `Shutdown`/`Serve(listener)`, `Run` never releases SIGINT/SIGTERM (a process with `Run` in a goroutine survived SIGTERM), accept errors retried with no backoff, SSE streams ignore shutdown. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-040** · raised `2026-1109-once-over-review` · value high
  WebSocket hardening: no Origin check, no ping or read deadline, write deadline not applied once expired, `closed` raced, control frames unvalidated, subprotocol echoed blindly, `[ERR]` on normal close. Needs decision D5. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-041** · raised `2026-1109-once-over-review` · value medium
  Pooled request, response and header buffers keep their largest size, so one 100 MB upload pins 100 MB per connection (`Context.go:230`). Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-042** · raised `2026-1109-once-over-review` · value medium
  Headers and body leave in two writes (`Server.go:1675`, `1702`); a prototype measured 3–4 % on plain TCP. Adds the phase's benchmark file. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-043** · raised `2026-1109-once-over-review` · value medium
  WebSocket `readFrame` does three or four unbuffered reads and allocations per frame (`websocket.go:383`). Benchmark first. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-044** · raised `2026-1109-once-over-review` · value low
  One allocation per request line and header line (7 of 8 allocs/op). After N-035; drop if under 5 %. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-045** · raised `2026-1109-once-over-review` · value low
  Static handler splits the prefix and calls `filepath.Abs` twice per request (`Server.go:888`, `940`). Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-046** · raised `2026-1109-once-over-review` · value medium
  Look the route up before reading the body, so an unrouted request does not buffer up to 100 MB first (`Server.go:1352`). Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-047** · raised `2026-1109-once-over-review` · value medium
  `Proxy` uses `http.DefaultClient` (no timeout, follows redirects), forwards hop-by-hop headers and buffers the response (`Server.go:738`). Reopens declined N-011. Needs decision D6. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-048** · raised `2026-1109-once-over-review` · value medium
  No setter for the error handler and no not-found / method-not-allowed hooks; `ListRoutes` omits param routes. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-049** · raised `2026-1109-once-over-review` · value medium
  Path params are not percent-decoded (`/greet/John%20Doe` → `John%20Doe`). Reproduced. Needs decision D7. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-050** · raised `2026-1109-once-over-review` · value low
  Response header names are matched case-sensitively, so two Content-Type lines can be sent (`Response.go:151`). Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-051** · raised `2026-1109-once-over-review` · value low
  `WriteHTML`/`WriteText` send no charset although documented to. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-052** · raised `2026-1109-once-over-review` · value medium
  Static files and `SendFile`: no Range, ETag, Cache-Control or index options; unknown types forced to download; filename encoding. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-053** · raised `2026-1109-once-over-review` · value medium
  Logging goes straight to stdout through about thirty `fmt.Print*` calls, several unconditional; add a pluggable logger. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-054** · raised `2026-1109-once-over-review` · value low
  SSE options: `Access-Control-Allow-Origin: *` is unconditional (`Response.go:277`); no `id:`/`retry:` fields. Needs decision D8. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-055** · raised `2026-1109-once-over-review` · value low
  `ClientIP` trusts `X-Forwarded-For` from any peer; add trusted proxies. Needs decision D9. Plan: `PLAN_ONCE_OVER_2026-1109.md`.

## Validate

Items whose remaining work is purely testing: run, look, measure, or write or
repair the test that covers it. No product change is planned unless the check
finds a defect. Same item form as Open.

_(none)_

## Roadmap

Wanted, but deliberately not next. Parked, not declined. Same item form as
Open.

- **N-056** · raised `2026-1109-once-over-review` · value low
  Helpers and middleware: `rweb.BindJSON`, and `cors`, `reqlog`, `gzip` subpackages. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-057** · raised `2026-1109-once-over-review` · value medium
  Lazy request body (`Request().BodyReader()`), so uploads are not held in memory and a per-route cap becomes possible. Design sketch under N-046 in the plan. Plan: `PLAN_ONCE_OVER_2026-1109.md`.

## Non-goals

- **N-011** · declined `2026-0701-2109-http-server-fixes-and-optimizations` — Streaming proxy responses. Out of scope for that review and carried as a deliberate non-goal since.
- **N-012** · declined `2026-0701-2109-http-server-fixes-and-optimizations` — Header count/size limits. Same; still none in `Server.go`.
- **N-013** · declined `2026-0701-2109-http-server-fixes-and-optimizations` — `Connection: close` handling. Same; the server does not act on a client's `Connection: close`.
- **N-005** · declined `2026-1010-2320-decline-n005-getpostvalue` (raised `2026-0509-2315-fix-multipart-form-leak`) — Deprecate `GetPostValue` in favor of `FormValue`. Not a real overlap: `GetPostValue` is the narrow urlencoded-only read that never triggers a multipart parse, documented as such on the `Request` interface; `FormValue` covers both encodings. Deprecating would only make linters flag existing callers (`examples/cookies`, tests, one outside project) for no defect.
- **N-018** · declined `2026-1011-1337-decline-n018-synthetic-host` (raised `2026-0919-2229-next-list-scheme-host-symlinks`) — Host validation for synthetic `s.Request()` calls. The check (`Server.go:1331`, RFC 9112 §3.2: duplicate or malformed Host → 400) guards the wire, where a proxy in front may disagree with us about which Host line counts. `s.Request()` is a test-oriented API whose headers come from the caller's own code, so there is no hop to disagree with and no untrusted input to refuse.
- **N-015** · declined `2026-1011-1431-decline-n015-missing-host` (raised `2026-0919-2229-next-list-scheme-host-symlinks`) — 400 for a request with no Host header (RFC 9112 §3.2 requires one on HTTP/1.1). Nothing to smuggle without a Host line, and the `"localhost"` fallback grants nothing a client could not get by sending `Host: localhost` (DNS rebinding needs a browser, which always sends one). Enforcing it properly would mean parsing the request-line version, which the server ignores today (`Server.go:1282`), and hostless HTTP/1.0 would keep the fallback anyway. Reasoning recorded at the check (`Server.go:1336`).

## Closed

Newest first. Closures before this file existed are recorded in the session
docs under `ai_docs/claude_sessions/`; the ones below were found done while
seeding.

- **N-058** · raised `2026-1109-once-over-review` · closed 2026-11-09
  `Group.StaticFiles`, `StaticFilesAbs` and `Proxy` registered straight on the server, so group middleware never ran for them: files or an upstream under an authenticated group were served to anyone. Done: `Group.wrap` (split out of `addRoute`) is passed to the server's private `staticFiles` / `staticFilesAbs` / `proxy`, which apply it to the handler before registering. Test: `TestGroupMiddlewareCoversStaticAndProxy` (200 on the old code where 401 is expected). SKILL.md notes it.
- **N-034** · raised `2026-1109-once-over-review` · closed 2026-11-09, `ee2b87c`
  `writeResponse` sends a 1xx, 204 or 304 as the header block alone: no Content-Length or Transfer-Encoding, no body, pending stream closed unread. An SSE stream on such a status is not started. Synthetic `s.Request` is unchanged. Tests: `bodyless_status_test.go`.
- **N-033** · raised `2026-1109-once-over-review` · closed 2026-11-09, `db5bce9`
  Every event goes through `writeSSEEvent`: text per `sseDataText` (string/[]byte as is, error/Stringer by method, basic kinds incl. named types via strconv, else JSON), one `data:` line per payload line, CR/LF stripped from the event name. `rweb.SSEClose` added; bare `"close"` still ends a stream. A payload JSON cannot encode is logged and skipped. The CLAUDE.md "SSE tests need fixing" note was stale (all pass under -race, none skipped) and is removed. Tests: `TestSSEEventFormatting`, `TestSSECloseSentinel`.
- **N-032** · raised `2026-1109-once-over-review` · closed 2026-11-09, `4b7d319`
  `OnDisconnect` runs after the hub lock is released, once per client actually removed, on both the unregister and eviction paths; `Close` uses `sync.Once`. Tests: `TestSSEHubOnDisconnectMayCallHub`, `TestSSEHubCloseConcurrent`.
- **N-031** · raised `2026-1109-once-over-review` · closed 2026-11-09, `2a35db0`
  `SetHeader`/`AddHeader` replace CR, LF and NUL in values with a space and drop a header whose name is not an RFC 9110 token. The drop is logged unconditionally (the response cannot see the Debug option); revisit with N-053. Other control bytes still pass. Tests: `response_header_test.go`, `header_injection_test.go`.
- **N-030** · raised `2026-1109-once-over-review` · closed 2026-11-09, `eeb9004`
  `Group.Group` builds the child slice with `slices.Concat`; `Server.Group` had the same hazard with the caller's variadic slice and now clones it. Tests: `TestSiblingSubGroupsKeepOwnMiddleware`, `TestServerGroupDoesNotAliasCallerSlice`.
- **N-029** · raised `2026-1109-once-over-review` · closed 2026-11-09, `af2afcf`
  Decision D1: stop. The auto-continue branch in `Group.addRoute` is gone; group middleware continues only through `Next()`, as `s.Use` does. No example relied on it (their deny paths were the ones leaking). Test: `TestGroupMiddlewareStopsChain`. SKILL.md and README say so.
- **N-028** · raised `2026-1109-once-over-review` · closed 2026-11-09, `0812f95`
  `parseURL` takes a scheme and host only from an absolute-form target and only before the first `?`; an origin-form target skips the scan. Also fixed on the way: a schemeless target such as `evil.com/admin` is no longer split into host and path, and `http://example.com` (no path) now parses. Tests in `Request_scheme_host_test.go`.
- **N-027** · raised `2026-1011-2314-stream-response-bodies` (from the cats-todo backlog) · closed 2026-10-11, 2026-1011-2314-stream-response-bodies
  A downstream app could not stream a database-dump download: every response
  was held in memory, so it kept dumps over 512 MB on the server and logged
  their path. Done: the static-only pending `response.bodyFile` became
  `response.bodyStream` (any `io.Reader`, sized or until EOF), with public
  `rweb.Stream(ctx, r, size)` (size → Content-Length, negative → chunked),
  `rweb.SendFile` and `rweb.SendFileAs`. StaticFiles now goes through
  `Stream`. A failed or short stream closes the conn (truncated, never a
  complete-looking body); a handler error drops a pending stream. Tests in
  `stream_body_test.go`, tcp and tls.
- **N-016** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · closed 2026-10-11, 2026-1011-2014-n016-keepalive-ws-sse-tests
  No test drove a WebSocket upgrade or an SSE stream as a later request on a
  reused connection, so the keep-alive `ctx.conn` restore (`Server.go`, end of
  the `handleConnection` loop) was covered only through `GetConn()`. Done:
  `request_context_test.go` adds `TestRequestContextWebSocketAfterKeepAlive`
  and `TestRequestContextSSEAfterKeepAlive` (tcp and tls), each serving two
  plain requests before the upgrade or stream. The SSE producer goes quiet
  after its events, so `sendSSE`'s read on `ctx.conn` is the only disconnect
  signal. With the restore line commented out, both fail (the upgrade panics on
  a nil conn; the stream never sees the client leave); restored, both pass,
  and `go test -race ./...` is green.
- **N-007** · raised `2026-0509-2336-fix-multipart-form-state-leaks` · closed 2026-10-11, 2026-1011-1411-n007-staticcheck-dead-code
  Dead code and one simplification flagged by `staticcheck ./...`. Done
  (re-run with `honnef.co/go/tools` v0.8.1, since the installed binary
  predates go1.24): removed the 13 U1000 hits — `args.go` (`visitArgsKey`,
  `delAllArgsBytes`, `appendArgBytes`, `decodeArgAppendNoPlus`,
  `peekAllArgBytesToDst`, `peekArgsKeys`), `bytesconv.go` (`readHexInt`,
  `writeHexInt`, `lowercaseBytes`, `hexIntBufPool`, `errEmptyHexNum`,
  `errTooLargeHexNum`, plus the `bufio`/`io`/`sync` imports only they used),
  `consts/text_bytes.go` `defaultContentType` — and the S1017 in
  `Server.Proxy` (now `strings.TrimPrefix`). Exported `consts.MaxHexIntChars`
  kept. `staticcheck ./...` is now clean.
- **N-026** · raised `2026-1011-0036-os-root-static-containment` · closed 2026-10-11, 2026-1011-0045-next-list-sweep
  `examples/websocket` did not compile. Done: `main.go` calls
  `rweb.NewServerWithOptions` for its functional options. All four example
  modules and `./examples/...` build; the example was run and served `/`
  (200) and a plain GET to `/ws/echo` (426, from N-025).
- **N-025** · raised `2026-1010-2307-head-fallback-and-501-unknown-method` · closed 2026-10-11, 2026-1011-0045-next-list-sweep
  A non-upgrade request to a WebSocket route got 500 and an `[ERR]` line.
  Done: `s.WebSocket` and `s.WebSocketWithOptions` check
  `IsWebSocketUpgrade` first and answer through `rejectWebSocketRequest`
  (`websocket.go`) with nil error: no `Upgrade: websocket` (plain GET, any
  HEAD) → 426 with `Upgrade: websocket` and `Connection: Upgrade`; wrong
  `Sec-WebSocket-Version` → 426 with `Sec-WebSocket-Version: 13` (RFC 6455
  §4.4); missing key or `Connection: upgrade` → 400. Added
  `consts.StatusUpgradeRequired`. Also made the `Upgrade` token
  case-insensitive in `IsWebSocketUpgrade` and `performHandshake` (RFC 6455
  §4.2.1), since the reject path compares it that way. A manual
  `ctx.UpgradeWebSocket()` in a user handler still returns
  `ErrWebSocketNotUpgraded`. Tests: `websocket_reject_test.go`.
- **N-024** · raised `2026-1010-2246-lazy-multipart-parse` · closed 2026-10-11, 2026-1011-0045-next-list-sweep
  Synthetic `s.Request()` left a spilled multipart form's temp files
  behind, because it cannot run the per-request `Clean` (that would zero
  the `Response` it returns). Done: after `handleRequest` it calls
  `ctx.request.CleanupMultipartForm()`, which touches only the request
  half. Test: `TestMultipartParsedOnlyOnUse` now asserts the temp dir is
  empty after the synthetic requests (2 files left on the old code).
- **N-020** · raised `2026-1006-1954-request-context-cancellation` · closed 2026-10-11, 2026-1011-0045-next-list-sweep
  Shutdown did not cancel request contexts. Done with a server-wide base
  context, not connection tracking: `Server.baseCtx` is the parent of every
  `Request().Context()`, each connection snapshots it when it starts
  (`request.baseCtx`, also set by synthetic `s.Request`), and `Run` cancels
  it after closing the listener. A keep-alive connection now closes after
  the response it is working on once the base is cancelled; one idle in its
  read is not woken and serves at most one more request. `Run` renews a
  cancelled base first, so a server can run again. `Run` still does not
  wait for in-flight handlers. Tests: `shutdown_test.go` (calls
  `cancelBaseContext`, the step `Run` takes, instead of signalling the test
  process).
- **N-019** · raised `2026-1006-1954-request-context-cancellation` · closed 2026-10-11, 2026-1011-0045-next-list-sweep
  `Server.Proxy` did not cancel the upstream call when the client left.
  Done: the upstream request is built with `http.NewRequestWithContext`
  and `ctx.Request().Context()`. When `Do` or the body read fails and the
  request context is done, the handler records 502 and returns nil
  (`clientGone`), so no `[ERR]` line and no 500 page for a dead conn; a
  genuine upstream failure still reaches the error handler. The upstream
  body is now closed on the read-error path too. Tests:
  `proxy_cancel_test.go` (TCP and TLS; times out on the old code).
- **N-017** · raised `2026-0919-2229-next-list-scheme-host-symlinks` · closed 2026-10-11, 2026-1011-0036-os-root-static-containment
  `StaticContainSymlinks` had a check-to-read race between `EvalSymlinks`
  and the open. Done: `go` directive raised to `1.24.0` (root and the four
  example modules; `toolchain go1.23.4` dropped), and with the option on the
  static handler opens through `os.Root` (`openContained`,
  `static_contain.go`), so check and open are one step. `os.Root` refuses
  absolute symlinks even when they point inside, so a refused open falls
  back to resolving with `EvalSymlinks` and reopening the link-free real
  path through the same Root: still contained, and in-root absolute links
  keep serving. Tests: `TestStaticFilesContainRelativeSymlinks`,
  `TestStaticFilesContainSymlinksSwapRace` (rename-swaps a dir link among
  inside / relative-outside / absolute-outside; the old code served the
  outside file within ~70–250 requests, the new code never does in 5000).
- **N-010** · raised `2026-0701-2109-http-server-fixes-and-optimizations` · closed 2026-10-11, 2026-1011-0017-stream-static-files
  Static files were read fully into memory per request. Done: the static
  handler opens the file, Stats the open file (size, mod time and dir check
  describe the bytes sent), and attaches it to the response as a pending
  body file; `writeResponse` sends it with `io.CopyN` (sendfile on plain
  TCP), closing the connection if the copy comes up short of the
  Content-Length already sent. HEAD and 304 never read the file. Anything
  that touches the body (`Body`, `Write`, `SetBody`, `ctx.Bytes`,
  `WriteString`) loads it first, so body-rewriting middleware such as
  `critical` behaves as before; synthetic `Request()` loads it too. Tests:
  `static_stream_test.go` (TCP and TLS).
- **N-006** · raised `2026-0509-2336-fix-multipart-form-state-leaks` · closed 2026-10-10, 2026-1011-0017-stream-static-files
  The `var _ = context.Background` guard in `Request_leak_test.go` was the
  `"context"` import's only use (`req.Context()` is a method and needs no
  import). Done: removed the guard, its comment, and the import; `go vet` and
  `go test ./...` pass.
- **N-023** · raised `2026-1010-2236-cookie-encryption-and-405` · closed 2026-10-10, 2026-1010-2307-head-fallback-and-501-unknown-method
  HEAD is not implied by GET. Done: a HEAD that matches no HEAD route falls
  back to the GET handler (`lookupGetForHead`, which first drops any :params
  the failed HEAD radix lookup recorded), and `Allow` lists HEAD wherever GET
  is routed. Two GET-route kinds needed care: a HEAD to an SSE route sends
  the headers only (no stream, SSE cleanup still runs), and
  `IsWebSocketUpgrade` is false for HEAD so a WebSocket route never upgrades
  on it. Tests: `head_and_unknown_method_test.go`.
- **N-022** · raised `2026-1010-2236-cookie-encryption-and-405` · closed 2026-10-10, 2026-1010-2307-head-fallback-and-501-unknown-method
  Unknown method panicked in dispatch. Premise was half right: the panic
  happened only for synthetic `s.Request(...)`; on the wire
  `isValidRequestMethod` already rejected the method, but answered with
  `consts.HTTPBadMethod`, a request line (`BAD-METHOD / HTTP/1.1`), not a
  response (`TestBadRequestMethod` sent it and expected it echoed back). Also
  found: both routers' GET fast path was `method[0] == 'G'`, so `GOT /x` was
  served by the GET route. Done: wire and synthetic paths answer
  `501 Not Implemented` (`consts.HTTPNotImplemented`); `RadixRouter`
  lookups treat a method with no tree as no route; the GET fast path in
  both routers is a full compare (no benchmark change).
- **N-021** · raised `2026-1006-1954-request-context-cancellation` · closed 2026-10-10, 2026-1010-2307-head-fallback-and-501-unknown-method
  The installed skill copy lagged `ai_docs/SKILL.md` (missing 404 vs 405,
  Encrypted Cookies, symlink containment, WebSocket compression and
  `WriteMessages`, `Scheme`/`Host` notes, Request Context). Done by making
  `~/.claude/skills/rweb-light-go-webserver/SKILL.md` a symlink to
  `ai_docs/SKILL.md`, so it can no longer drift; the repo copy was a strict
  superset, so nothing was lost. This is machine-local (outside the repo):
  another checkout or machine needs the same `ln -s`.
- **N-004** · raised `2026-0509-2315-fix-multipart-form-leak` · closed 2026-10-10, 2026-1010-2246-lazy-multipart-parse
  Multipart parsing is now fully lazy: the eager `ParseMultipartForm` in
  `handleRequest` is gone, so a multipart body is parsed (and may spill to
  temp files) only when a handler calls `GetFormFile`, `GetFormFiles` or
  `FormValue`. Test `multipart_lazy_test.go` fails on the old code. Raised
  N-024 on the way.
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
