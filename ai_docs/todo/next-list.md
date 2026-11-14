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

**Next ID:** N-074

## Open

- **N-052** · raised `2026-1109-1806-once-over-review-phases-1-2` · value medium
  Static files and `SendFile`: no Range, ETag, Cache-Control or index options; unknown types forced to download; filename encoding. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-053** · raised `2026-1109-1806-once-over-review-phases-1-2` · value medium
  Logging goes straight to stdout through about thirty `fmt.Print*` calls, several unconditional; add a pluggable logger. Includes the unconditional `log.Printf` for a dropped response header name added by N-031 (`Response.go`, `acceptHeaderKey`). Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-054** · raised `2026-1109-1806-once-over-review-phases-1-2` · value low
  SSE options: `Access-Control-Allow-Origin: *` is unconditional (`Response.go:277`); no `id:`/`retry:` fields. Needs decision D8. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-055** · raised `2026-1109-1806-once-over-review-phases-1-2` · value low
  `ClientIP` trusts `X-Forwarded-For` from any peer; add trusted proxies. Needs decision D9. Plan: `PLAN_ONCE_OVER_2026-1109.md`. Also (since N-047): `Proxy` overwrites `X-Forwarded-Proto` and `-Host` with this hop's values (`proxy.go`, `setForwardedHeaders`), so behind a TLS-terminating load balancer the upstream sees `http`; with a trusted-proxies list the incoming values from a trusted peer could pass through.
- **N-059** · raised `2026-1109-1806-once-over-review-phases-1-2` · value low
  No lingering close after an early 431 or 413: the server answers and closes while the client may still be sending, so on Linux the client can get a reset before it reads the response (reliable on macOS loopback). Read a bounded amount, or half-close and wait briefly, before closing (`Server.go`, `headFailed` and the 413 paths). From the N-035 report; not reproduced.
- **N-060** · raised `2026-1109-1806-once-over-review-phases-1-2` · value low
  Request header values containing control characters (other than the line's own CRLF) are accepted and handed to handlers. RFC 9110 §5.5 allows refusing them; decide between 400 and stripping. From the N-036 report.
- **N-064** · raised `2026-1111-1143-phase-3-performance` · value low
  No read timeout on a streamed body once its headers are sent: an upstream behind `Proxy` (or any slow `rweb.Stream` producer) that stalls mid-body holds the client connection until shutdown, and since the disconnect watch is stopped before `writeResponse`, a client that leaves during the stall is not noticed until the next write. nginx's `proxy_read_timeout` bounds each read; a per-read idle timer around `resp.Body` would do it for the proxy. From the N-047 report.
- **N-065** · raised `2026-1111-1143-phase-3-performance` · value low
  `Proxy` does not rewrite an upstream `Location` (or cookie `Path`) under the proxy prefix, so an upstream redirect to `/new` sends the client to the proxy's root rather than under the prefix (no `ProxyPassReverse` equivalent). From the N-047 report.
- **N-066** · raised `2026-1111-1143-phase-3-performance` · value low
  WebSocket upgrades cannot go through `Proxy`: `Upgrade` is hop-by-hop and stripped since N-047, so the upstream answers plainly (before N-047 the request hung). Proxying one needs the client conn taken over and bytes copied both ways. From the N-047 report.
- **N-068** · raised `2026-1111-1143-phase-3-performance` · value low
  The hub in `examples/websocket/main.go` and the chat example in `ai_docs/SKILL.md` call `client.Close` while holding the hub's lock. For a client that is gone, `Close` waits up to 2 s for the close reply, stalling every broadcast meanwhile. Collect under the lock, close outside it. From the N-043 report.
- **N-070** · raised `2026-1111-1143-phase-3-performance` · value low
  Small allocation savings, unmeasured: `strconv.Itoa(int(ctx.status))` allocates on every response (`writeResponse`, `writeWebSocketUpgradeResponse`; one of hello's 4 allocs/op; `strconv.AppendInt` into `respBuf` avoids it), and `writeChunkedBody` allocates a 32 KB buffer per chunked response (pool it). Benchmark with `bench_test.go`; drop if under 5 %. From the N-042 report.
- **N-072** · raised `2026-1111-1814-n063-n069-n067-n071-hardening` · value low
  The request line is split only at its first and last space (`serveConn`, `Server.go`), so extra spaces and a space inside the target are accepted: `GET /a b HTTP/1.1` reaches a `/a b` route (200), and `GET  /x HTTP/1.1` looks up ` /x` (404, not 400). RFC 9112 §3 says a recipient SHOULD NOT autocorrect an invalid request line and then process it, since such a line may be crafted to get past a filter in front, and §3.2 allows no whitespace in the target. A 400 would match the framing rules of N-036. Decide together with N-060 (control characters in header values). Found while fixing N-063; both cases checked on the wire with a throwaway test.
- **N-073** · raised `2026-1113-2331-n048-n049-n050-hooks-param-decode-header-case` · value medium
  A handler that sets `Content-Length` (or `Transfer-Encoding`) itself gets it sent alongside the server's own framing header: `SetHeader("Content-Length", "999")` + `WriteString("hi")` sends `Content-Length: 2` and `Content-Length: 999` (reproduced). Two differing lengths make the response ambiguous to a proxy in front (RFC 9112 §6.3). `writeResponse` frames every body itself, so it should skip (or refuse) handler-set framing headers, minding an explicit `s.Head` route that sets the GET length for a bodiless HEAD reply. From the N-050 work.

## Validate

Items whose remaining work is purely testing: run, look, measure, or write or
repair the test that covers it. No product change is planned unless the check
finds a defect. Same item form as Open.

- **N-062** · raised `2026-1109-1806-once-over-review-phases-1-2` · value medium
  The WebSocket changes of N-040 (origin check, 30 s pings, write timeout, 1001 at shutdown) are covered by Go-client tests only. Check with a real browser and `wscat`, including one run behind a reverse proxy that rewrites `Host` (expected: 403 until `AllowedOrigins` lists the public origin).

## Roadmap

Wanted, but deliberately not next. Parked, not declined. Same item form as
Open.

- **N-056** · raised `2026-1109-1806-once-over-review-phases-1-2` · value low
  Helpers and middleware: `rweb.BindJSON`, and `cors`, `reqlog`, `gzip` subpackages. Plan: `PLAN_ONCE_OVER_2026-1109.md`.
- **N-057** · raised `2026-1109-1806-once-over-review-phases-1-2` · value medium
  Lazy request body (`Request().BodyReader()`), so uploads are not held in memory and a per-route cap becomes possible. Design sketch under N-046 in the plan. Plan: `PLAN_ONCE_OVER_2026-1109.md`. Absorbs N-046 (route before reading the body), decided 2026-11-11: global middleware (`s.Use`) runs before routing and may answer an unrouted request itself, so skipping the body on a route miss would hand it an empty `Body()`, and any routed POST already accepts bodies up to the server-wide cap, so the miss path alone protects little. With a lazy body, an unrouted request whose body nobody reads is drained or closed after the chain, with no change to what middleware sees. Also unlocks streaming uploads through `Proxy` (N-047 left the request body buffered).

## Non-goals

- **N-044** · declined `2026-1111-1143-phase-3-performance` (raised `2026-1109-1806-once-over-review-phases-1-2`) — One allocation per request line and header line. Built and tested: the head gathered in a pooled `request.headBuf` and converted to one string, keys and values as substrings, every framing check still per line. Allocations fell 12 → 2 per browser-like request (14 → 4 on TLS) and 4 → 2 for hello, but time did not move: single connection −1.4 % to +3.9 % ns/op and +2.3 % to +2.5 % cpu-ns/op over two interleaved runs, and 64 connections in parallel 4625 → 4630 ns/op with CPU per request unchanged (GC is not a factor at ~650 B/op). Under the plan's 5 % bar, so not committed. Revisit only if a profile under real load shows GC from request heads.
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

- **N-051** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-14, 2026-1114-0017-n051-text-charset-utf8 (`9d1acfd`)
  `WriteHTML`/`WriteText` (and the `…Bytes` forms) send `text/html; charset=utf-8` / `text/plain; charset=utf-8`; `rweb.HTML`, `Text`, `CSS`, `JS` and also `CSV` (as `setFileHeaders` already did for `.csv`) send their types with `charset=utf-8`. New constants `consts.CharsetUTF8`, `MIMEHTMLUTF8`, `MIMETextPlainUTF8`, `MIMECSSUTF8`, `MIMEJavaScriptUTF8`, `MIMECSVUTF8` (plus bare `MIMECSS`, `MIMEJavaScript`, `MIMECSV`, now used by `setFileHeaders`); the bare constants keep their values. JSON and XML stay bare by design (RFC 8259 has no charset for JSON; on XML it would override the document's encoding declaration). The default error page gains the charset via `WriteHTML`. Behaviour change: the header now outranks a page's `<meta charset>`; noted in SKILL.md. Tests: `text_charset_test.go` (on the wire, via `mime.ParseMediaType`), `TestContentTypes`, `TestWriteHTMLBytes`/`TestWriteTextBytes`.
- **N-050** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-13, 2026-1113-2331-n048-n049-n050-hooks-param-decode-header-case (`f64493d`)
  `response.Header` and `SetHeader` match names with `headerKeyEqual` (length check, then `strings.EqualFold`), so `SetHeader("content-type", …)` + `WriteJSON` sends one Content-Type. `SetHeader` also replaces every value under the name, `AddHeader` duplicates of any case included (net/http `Header.Set` semantics), in place and without allocating; the kept entry stays at the first one's position with the new spelling. Benchmarks within noise. Tests: `response_header_case_test.go`. Raised N-073 (handler-set `Content-Length` duplicates the server's).
- **N-049** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-13, 2026-1113-2331-n048-n049-n050-hooks-param-decode-header-case (`6721dfd`)
  Decision D7: yes, decode, and (user) never twice. `request.addParameter` decodes a value containing `%` with `url.PathUnescape` (`+` stays `+`); a bad escape is kept raw. Routing still uses the raw path and `Path()` stays raw. `StaticFiles` no longer decodes its `*path`; it 404s a bad escape by checking the raw path (only when it holds `%`) and runs its traversal checks on the decoded value as before. `%252e%252e` and a `x%2520y.txt` vs `x y.txt` case prove a single decode. Stylus `*path` is safe (go-styl cleans and rejects `..`). Behaviour change for apps that decoded params themselves; noted in SKILL.md. Tests: `path_param_decode_test.go`, `TestStaticFilesPathTraversal`.
- **N-048** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-13, 2026-1113-2331-n048-n049-n050-hooks-param-decode-header-case (`a2315f6`)
  `SetErrorHandler`, `SetNotFoundHandler`, `SetMethodNotAllowedHandler`; nil restores each default. The 404/405 hooks run in the default dispatch handler (after middleware) with the status, and `Allow` for 405, already set; a hook's error goes to the error handler; a route's own 404 (StaticFiles) does not reach the hook. The default error page moved to `defaultErrorHandler` and uses `http.StatusText` (not `consts.StatusTextFromCode`, which lacks most codes), so the proxy's pages read "502 Bad Gateway" / "504 Gateway Timeout"; unknown codes read "Error". `AddMethod` records radix routes (`Server.radixRoutes`, re-add replaces); new `Routes()` returns all routes sorted by path then method, and `ListRoutes` prints it. Tests: `error_hooks_test.go`. Documented in SKILL.md ("Error, not-found and 405 hooks") and CLAUDE.md.
- **N-071** · raised `2026-1111-1143-phase-3-performance` · closed 2026-11-13, 2026-1113-2302-n071-stoprun-windows-tests (`d746e23`, `9bd8452`)
  The package's tests now build for Windows. The note named only `graceful_shutdown_test.go`, but 42 more calls in 13 test files stopped a `Run` server with `syscall.Kill(…SIGTERM)` (the earlier count of 43 was off by one); `go vet` reports only the first failing file, which hid them. The user chose option (a). `d746e23` moved Run's genuine signal tests to `graceful_shutdown_signal_test.go` under `//go:build unix` and moved `TestClientIPRemoteAddrFallback` to `Serve`/`Shutdown`. `9bd8452` replaced the 42 signals with `stopRun(s, ...)` (`run_stop_test.go`), which calls `Shutdown` on each server; the proxy test passes its two upstreams too. One change in timing: `Run` now returns once the listener closes, and `stopRun` finishes the draining. CLAUDE.md says to stop `Run` this way. Checks: `go vet` passes for darwin, linux and windows; `GOOS=windows go test -c` builds; `-race` passes, and the root package passed 5 repeated runs.
- **N-063** · raised `2026-1111-1143-phase-3-performance` · closed 2026-11-11, 2026-1111-1814-n063-n069-n067-n071-hardening (`5cb62b0`)
  `serveConn` trims the request line's ending once (`TrimRight "\r\n"`, as the version field already was) and cuts the target and version from the trimmed line. A bare-LF line now keeps its whole target (`GET /xy\n` → `/xy`, was `/x`), and `GET \n` no longer panics. An empty target (`GET `, `GET  HTTP/1.1`) gets 400 and a close. Tests: `request_line_test.go`, tcp and tls; both cases failed before the change. Raised N-072 on the way.
- **N-069** · raised `2026-1111-1143-phase-3-performance` · closed 2026-11-11, 2026-1111-1814-n063-n069-n067-n071-hardening (`15beae1`)
  `Clean` drops a request or response header list whose capacity exceeds `maxRetainedHeaders` (256 entries, 8 KB) back to `newContext`'s 8, as N-041 does for the body buffers. It also clears the entries before truncating, so a kept array no longer holds the previous request's strings past `len`. Allocations unchanged (hello 4, browser-like 12). `cpu-ns/op` medians, interleaved on a noisy machine: hello 13.2 → 12.7 µs, browser-like 13.7 → 13.5 µs. Tests: `TestCleanReleasesOversizedHeaderLists` and `TestCleanKeepsSmallHeaderListsZeroed`; both failed with the change off.
- **N-067** · raised `2026-1111-1143-phase-3-performance` · closed 2026-11-11, 2026-1111-1814-n063-n069-n067-n071-hardening (`bf71d71`)
  `WSConn.maxMessageSize` is an `atomic.Int64`, so `SetMaxMessageSize` is safe from any goroutine. Test: `TestWebSocketSetMaxMessageSizeDuringReads`; before the change, `-race` flagged the setter against `readFrame`.
- **N-042** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-11, 2026-1111-1143-phase-3-performance (`7ba428b`)
  `writeResponse` appends an in-memory body of at most `coalesceBodyMax` (8 KB) to the header block and writes once; HEAD, 1xx/204/304, SSE, chunked, HTTP/1.0 unframed and larger bodies keep the header block alone, and a sized stream after the in-memory part still goes by sendfile. An empty body no longer costs a zero-length Write. The plan said 64 KB; a size sweep showed coalescing lose 6–25 % on plain TCP from 16 KB up (TLS flat), so 8 KB. Results (interleaved, medians): hello −6.1 % ns/op (TLS −6.6 %), JSON −8.3 % (TLS −4.3 %), browser-like −8.4 % (TLS −4.1 %), 8 KB body −4.9 % (TLS −0.5 %). Adds the phase's benchmark file `bench_test.go` (keep-alive hello, JSON, browser-like and body-at-cap, plain and TLS, with a `cpu-ns/op` metric from getrusage; `BenchmarkKeepAliveHello` keeps its name). Phase 3 as a whole: `BenchmarkKeepAliveHello` 14.5 → 13.8 µs/op (−4.8 %) from `371ce8c` to the end of the phase. Tests: `write_coalesce_test.go`.
- **N-043** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-11, 2026-1111-1143-phase-3-performance (`5fb94b0`, `0f71fda`)
  Concurrency: frame reading is single-owner through `WSConn.readSem` (a one-slot channel, so `Close` can wait on it with a timeout); a `Close` from another goroutine while the handler is in `ReadMessage` lets that call read the peer's close frame (`closeRcvd`) instead of reading alongside it; two `ReadMessage` callers take turns; ping/pong handlers run with the read side handed back. `SetPingHandler`/`SetPongHandler`/`OnClose` take `handlersMu`. Also fixed: an `OnClose` handler calling `Close` deadlocked, and a 64-bit length with the top bit set panicked in `make`. Performance: frames read through a lazily made 4 KB `bufio.Reader` above the liveness reader (one deadline push per socket read), header parsed from a fixed `[14]byte`, word-wise unmask (`maskBytes`, no unsafe). `BenchmarkWSEcho`: 1 KB −21 %, 64 KB −40 %, burst of 64 small messages −48 %, single 16 B round trip not resolved; allocs 4–5 → 2 per message (payload and `*WSMessage`). App-visible: messages already buffered are returned after a read deadline passes; 4 KB read buffer per connection. Tests: `websocket_concurrency_test.go`, `websocket_read_test.go`; benchmark `websocket_bench_test.go`.
- **N-045** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-11, 2026-1111-1143-phase-3-performance (`ff9e98b`)
  `staticFiles` works out the kept route tokens, the absolute root and the base once at registration; a request only decodes, joins and runs the same `Rel` check. Paths only are cached (`os.Root` still opened per request). Behaviour change, documented on `StaticFiles`, SKILL.md and README: a relative root is resolved at registration, so a later `os.Chdir` does not move it; if `os.Getwd` fails then, the route is not registered. Benchmark (`static_bench_test.go`): relative root −27 % synthetic / −15 % keep-alive, absolute −4 % / −2 %; allocs 41–44 → 19–22. Test: `TestStaticFilesRootResolvedAtRegistration`.
- **N-047** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-11, 2026-1111-1143-phase-3-performance (`f5581c2`)
  Decision D6: stream. `proxy.go`: one client per server (cloned `DefaultTransport`, `CheckRedirect` = `ErrUseLastResponse`, `ResponseHeaderTimeout` from new `ServerOptions.ProxyResponseHeaderTimeout` / `WithProxyResponseHeaderTimeout`, default 60 s, negative off; 32 idle conns per host; compression off); `Shutdown` closes its idle conns. Hop-by-hop headers dropped both ways (incl. those named in `Connection`), `Expect` dropped upstream, request headers added rather than set, `X-Forwarded-For` appended, `-Proto`/`-Host` set to this hop's values. The body goes out through `Stream(ctx, resp.Body, resp.ContentLength)`. Header timeout → 504, other upstream failure → 502 (was 500), both via the error handler; client gone unchanged. Reopens declined N-011. Request body still buffered (N-057). Raised N-064, N-065, N-066 on the way. Tests: `proxy_test.go` (wire tests via `Serve`/`Shutdown`), `proxy_client_test.go`.
- **N-046** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-11, 2026-1111-1143-phase-3-performance — merged into N-057. Skipping the body on a route miss would change what global middleware sees for unrouted requests, and gains little while every routed POST takes bodies up to the cap; see N-057.
- **N-061** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-11, 2026-1111-1143-phase-3-performance
  staticcheck 2026.1 (v0.7.0) needs Go 1.25 and builds with the machine's 1.25.4 (`go install honnef.co/go/tools/cmd/staticcheck@v0.7.0`, into the session scratchpad). `staticcheck ./...` is clean at `371ce8c`, so over the 26 commits of the previous session, and after every Phase 3 commit. `~/bin/staticcheck` is still 2025.1.1 built with go1.23.2 and refuses this module; reinstall v0.7.0 there to make it permanent.
- **N-039** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`9b57685`)
  Added `Serve(l)`, `Shutdown(ctx)`, `ErrServerClosed`, `ServerOptions.ShutdownTimeout` (default 10 s) and `WithShutdownTimeout`. `Run` is built on them and calls `signal.Stop`, so a later SIGTERM is no longer swallowed. Shutdown closes listeners, cancels the base context, wakes idle keep-alive conns with a past read deadline, sends WebSockets a 1001 close frame, ends SSE streams, waits for the connection registry to empty, and force-closes on ctx done. This adds the registry N-020 avoided: a mutex per conn open/close and two atomics per request (`shutdown.go`). Accept errors back off 5 ms to 1 s. `RunWithHttpsRedirect` opens both listeners first and shuts both down together. Behaviour to know: `Run` returns an error wrapping `context.DeadlineExceeded` when the shutdown timeout runs out (it used to return nil always), and a SIGTERM with no `Run` listening now kills the process. Tests: `graceful_shutdown_test.go`; `shutdown_test.go` moved to `Shutdown`.
- **N-041** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`863d3b4`)
  `Clean` drops `request.body`, `response.body` or `respBuf` when its capacity exceeds 256 KB (`maxRetainedBuf`). Test: `buffer_retention_test.go`.
- **N-040** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`009122c`..`15d0cd0`)
  Decision D5: on by default. Cross-origin handshakes get 403 (same host or no Origin allowed; `AllowedOrigins` adds, `CheckOrigin` decides alone), manual `UpgradeWebSocket` included. Server pings every 30 s with a 40 s read window (`PingInterval`, `PongTimeout`), 10 s write timeout (`WriteTimeout`), `closed` is atomic, oversized or fragmented control frames close with 1002, `WSOptions.Subprotocols` selects (no blind echo; `ws.Subprotocol()`), handler errors after the upgrade skip the error handler and normal disconnects are not logged, `Close` releases its lock before waiting for the peer. Note for deployments: the origin check compares against `Host()`, so a proxy that rewrites Host needs `AllowedOrigins`. Left for N-043: `Close` from another goroutine during `ReadMessage` puts two readers on the conn. Tests: `websocket_hardening_test.go`, `websocket_reject_test.go`.
- **N-038** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`2e0407d`)
  Decision D4: yes. The request-line version is parsed; HTTP/1.1 closes on `Connection: close`, HTTP/1.0 closes unless `Connection: keep-alive`; the response carries the matching header, and a handler's own `Connection: close` now closes. A missing or unknown version is still served as 1.1. Tests: `connection_close_test.go`.
- **N-037** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`6b6513b`)
  `Expect: 100-continue` on an HTTP/1.1 request with a body is answered `100 Continue` just before the body is read, after the framing and size checks, so a refused request never has its body sent. Any other expectation gets 417. Tests: `expect_continue_test.go`.
- **N-036** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`dcef51d`)
  Decision D3: 400. Refused with 400: Content-Length together with Transfer-Encoding, non-digit or conflicting Content-Length, a header line with no colon or a non-token name, a folded line, a bare-LF end of head, a second Transfer-Encoding line, `chunked` not last. Other codings ending in `chunked` get 501. The chunked reader accepts extensions and trailers within limits and checks each CRLF. `TestBadRequestHeader` rewritten to expect 400. Code in `request_framing.go`; tests `request_framing_test.go`.
- **N-035** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`28f214c`)
  Decision D2: on by default. `MaxHeaderBytes` 1 MB (431 over it), `ReadHeaderTimeout` 30 s (408 mid-head), `IdleTimeout` 120 s (silent close); `ReadTimeout` and `WriteTimeout` off by default; SSE has its own per-flush `SSECfg.WriteTimeout`, default 30 s. Zero selects the default, negative disables. `WriteTimeout` bounds each write, not the response: bodies and sized streams go out in 1 MB slices with the deadline renewed. A failed header/body write now closes the conn. Also fixed: `sendSSE` kept writing to a dead conn after a write error on an event over 4 KB. Open edge: no lingering close after 431/413, so on Linux a client still sending may see a reset before the response. Code in `conn_limits.go`; tests `conn_limits_test.go`; benchmark `keepalive_bench_test.go` (4 allocs/op before and after).
- **N-058** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`c6a1ecd`)
  `Group.StaticFiles`, `StaticFilesAbs` and `Proxy` registered straight on the server, so group middleware never ran for them: files or an upstream under an authenticated group were served to anyone. Done: `Group.wrap` (split out of `addRoute`) is passed to the server's private `staticFiles` / `staticFilesAbs` / `proxy`, which apply it to the handler before registering. Test: `TestGroupMiddlewareCoversStaticAndProxy` (200 on the old code where 401 is expected). SKILL.md notes it.
- **N-034** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`ee2b87c`)
  `writeResponse` sends a 1xx, 204 or 304 as the header block alone: no Content-Length or Transfer-Encoding, no body, pending stream closed unread. An SSE stream on such a status is not started. Synthetic `s.Request` is unchanged. Tests: `bodyless_status_test.go`.
- **N-033** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`db5bce9`)
  Every event goes through `writeSSEEvent`: text per `sseDataText` (string/[]byte as is, error/Stringer by method, basic kinds incl. named types via strconv, else JSON), one `data:` line per payload line, CR/LF stripped from the event name. `rweb.SSEClose` added; bare `"close"` still ends a stream. A payload JSON cannot encode is logged and skipped. The CLAUDE.md "SSE tests need fixing" note was stale (all pass under -race, none skipped) and is removed. Tests: `TestSSEEventFormatting`, `TestSSECloseSentinel`.
- **N-032** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`4b7d319`)
  `OnDisconnect` runs after the hub lock is released, once per client actually removed, on both the unregister and eviction paths; `Close` uses `sync.Once`. Tests: `TestSSEHubOnDisconnectMayCallHub`, `TestSSEHubCloseConcurrent`.
- **N-031** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`2a35db0`)
  `SetHeader`/`AddHeader` replace CR, LF and NUL in values with a space and drop a header whose name is not an RFC 9110 token. The drop is logged unconditionally (the response cannot see the Debug option); revisit with N-053. Other control bytes still pass. Tests: `response_header_test.go`, `header_injection_test.go`.
- **N-030** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`eeb9004`)
  `Group.Group` builds the child slice with `slices.Concat`; `Server.Group` had the same hazard with the caller's variadic slice and now clones it. Tests: `TestSiblingSubGroupsKeepOwnMiddleware`, `TestServerGroupDoesNotAliasCallerSlice`.
- **N-029** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`af2afcf`)
  Decision D1: stop. The auto-continue branch in `Group.addRoute` is gone; group middleware continues only through `Next()`, as `s.Use` does. No example relied on it (their deny paths were the ones leaking). Test: `TestGroupMiddlewareStopsChain`. SKILL.md and README say so.
- **N-028** · raised `2026-1109-1806-once-over-review-phases-1-2` · closed 2026-11-09, 2026-1109-1806-once-over-review-phases-1-2 (`0812f95`)
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
