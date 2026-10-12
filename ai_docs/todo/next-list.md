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

**Next ID:** N-028

## Open

_(none)_

## Validate

Items whose remaining work is purely testing: run, look, measure, or write or
repair the test that covers it. No product change is planned unless the check
finds a defect. Same item form as Open.

_(none)_

## Roadmap

Wanted, but deliberately not next. Parked, not declined. Same item form as
Open.

_(empty — no session doc marked an item as deferred-but-wanted; sort Open
items here as you see fit)_

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
