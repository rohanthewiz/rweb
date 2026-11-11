# Plan — once-over review fixes (2026-11-09)

Source: the reliability / performance / convenience review of 2026-11-09
(branch `roh/once-over-fab5.2`, code at `9781e96`). Each item below is also a
line in `next-list.md` under the same ID; this file holds the detail.

How findings were established is marked per item:

- **ran** — reproduced with a throwaway test against the code at `9781e96`
- **read** — found by reading the code; not reproduced

## Ground rules for every item

1. Write the failing test first, in the root package's `_test.go` files
   (wire tests use the raw-socket style of `Server_test.go`).
2. `go test -race ./...` and `staticcheck ./...` clean before commit.
3. One item per commit, message ending `(N-0xx)`.
4. Comment the design choice at the change, as elsewhere in the repo.
5. Where behaviour visible to apps changes, update `ai_docs/SKILL.md`,
   `README.md` and (for architecture) `CLAUDE.md` in the same commit.
6. Performance items land only with a before/after benchmark in the commit
   message. Phase 3 starts by adding the benchmark file.

## Decisions needed before the affected item starts

These change behaviour that an existing app could depend on. Each has a
recommendation; the item is blocked only on the answer, not the rest of its
phase.

| # | Item | Question | Recommendation |
|---|------|----------|----------------|
| D1 | N-029 | Group middleware that returns nil without `Next()`: stop the chain (as `s.Use` does), or keep auto-continuing? | Stop. Auto-continue lets a denied request reach the handler. |
| D2 | N-035 | Defaults for the new limits: on or off? | On: 1 MB headers, 30 s header read, 120 s idle. Body-read and write timeouts off by default. |
| D3 | N-036 | A header line with no colon is tolerated today and `TestBadRequestHeader` asserts it. Answer 400 instead? | 400. The test is rewritten to expect it. |
| D4 | N-038 | Honour `Connection: close` and HTTP/1.0 (reopens declined N-013)? | Yes, it is small once the version is parsed for N-037. |
| D5 | N-040 | WebSocket: same-origin check and server pings on by default? | Both on by default, each with an opt-out in `WSOptions`. |
| D6 | N-047 | Stream proxy responses (reopens declined N-011)? | Yes. `rweb.Stream` (N-027) makes it a few lines. |
| D7 | N-049 | Percent-decode path params by default? | Yes. Apps that decode themselves would double-decode, so call it out in release notes. |
| D8 | N-054 | SSE `Access-Control-Allow-Origin: *`: keep as default or make opt-in? | Opt-in via `SSECfg.AllowOrigin`. |
| D9 | N-055 | `ClientIP`: trust `X-Forwarded-For` only from configured proxies? | Yes, with an explicit "trust all" setting for the current behaviour. |

## Order of work

```
Phase 1  correctness bugs      N-028 … N-034   small, independent, no API change (except D1)
Phase 2  connection hardening  N-035 … N-041   N-035 first: N-036/037/038 edit the same read loop
Phase 3  performance           N-042 … N-047   N-042 adds the benchmark the rest are judged by
Phase 4  convenience           N-048 … N-056   independent; N-052 and N-056 are the large ones
Roadmap                        N-056, N-057
```

Phase 1 can ship as its own tag. N-039 (lifecycle) is the one Phase 2 item
other work leans on: tests for later items get simpler once a server can be
stopped without signalling the test process.

---

## Phase 1 — correctness bugs

### N-028 · `://` in a query string misroutes the request · ran · value high

`GET /redirect?url=http://evil.com/admin` is served by the `/admin` handler
with `Host()` = `evil.com`. `parseURL` (`http.go:24`) looks for `://` anywhere
in the request target. A proxy in front that applies path rules sees
`/redirect`; rweb routes `/admin`.

Fix, in `parseURL`:

- A target starting with `/` is origin-form (RFC 9112 §3.2.1): skip scheme and
  host detection entirely. This is nearly every request, and it also saves the
  `strings.Index` scan.
- Otherwise look for `://` only in the part before the first `?`.
- `*` (the `OPTIONS *` form) is left as the path.

Tests: extend `Request_scheme_host_test.go` with the query case (synthetic and
on the wire), `?next=https://a/b`, an absolute-form target whose query also
contains `://`, and `OPTIONS *`.

### N-029 · group middleware cannot stop the chain by returning nil · ran · value high · needs D1

`Group.addRoute` (`Group.go:155-160`) calls the next handler when a middleware
returns nil without calling `Next()`. A group auth middleware that sets 401
and returns produced `401 "denied;SECRET"`. `s.Use` middleware stops in the
same situation, and SKILL.md documents "call `ctx.Next()` to continue".

Fix: delete the auto-continue branch and the `nextCalled` tracking. The chain
continues only through `Next()`.

Fallout to check: `group_test.go` (all its middleware already call `Next()`),
the 13 `Next()` call sites in `examples/` and `middleware/`, and the Groups
section of SKILL.md. Add `TestGroupMiddlewareStopsChain`.

While there: the wrapper allocates one closure and one `contextWrapper` per
middleware per request. With auto-continue gone the chain can be built once at
registration as a slice, and run by a single per-request wrapper holding an
index. Do this only if it stays simple; it is not the point of the item.

### N-030 · sibling sub-groups overwrite each other's middleware · ran · value high

`g.Group()` does `append(g.handlers, handlers...)` (`Group.go:29`). When the
parent's slice has spare capacity (after two `Use` calls: len 3, cap 4) both
children write into the same backing array, and the first child ends up with
the second child's middleware.

Fix: build the child's slice with its own backing array
(`slices.Concat(g.handlers, handlers)`). `Group.Use` on the child then cannot
reach the parent's array either.

Test: the reproduction (parent with two `Use`, two children, each asserts its
own middleware ran).

### N-031 · response headers accept CR and LF · ran · value high

`ctx.Redirect(302, userValue)` let a decoded query parameter add a
`Set-Cookie` line to the response (`Response.go:159`, `170`).

Fix: sanitise in `SetHeader` and `AddHeader`, the only public ways in, so
`Header()` reads back what will be sent:

- value: replace `\r`, `\n` and NUL with a space (what net/http does);
- key: drop the header if it contains anything outside the RFC 9110 token
  alphabet, and log at debug level.

The scan is `strings.IndexAny` on values that are almost always clean, so the
common path does not allocate.

Tests: redirect injection on the wire; a header key containing `:` or a space.

### N-032 · `SSEHubOptions.OnDisconnect` runs under the hub's write lock · ran · value medium

`unregisterLocked` (`sse_hub.go:164`) calls the callback while `h.mu` is held
for writing. A callback that calls `ClientCount`, `Broadcast` or `Register`
deadlocks.

Fix: `unregisterLocked` returns whether it removed the client; `Unregister`
and the eviction loop in `broadcastToClients` call `OnDisconnect` after
releasing the lock (once per removed client).

Also in this item: `Close()` checks-then-closes `h.done` and can panic on a
concurrent double close; use `sync.Once`.

Test: `OnDisconnect` calling `hub.ClientCount()` returns within a timeout, for
both the unregister and the eviction path.

### N-033 · SSE events are malformed for non-string and multi-line data · ran · value medium

`sendSSE` (`Server.go:1955-1961`) formats `SSEvent.Data` with `%s`, so `42`
goes out as `%!s(int=42)`, and writes the payload as one `data:` line, so
everything after the first newline is dropped by the browser.

Fix: one helper, `writeSSEEvent(w, name, data)`:

- payload to text: `string` and `[]byte` as they are, `error` and
  `fmt.Stringer` through their method, numbers and bools via `strconv`,
  anything else as JSON (behaviour change for structs and maps sent bare on
  the channel, which were printed with `%+v`);
- split the payload on `\r\n`, `\r` or `\n` and write one `data: ` line per
  piece;
- strip newlines from the event name.

The `"close"` string sentinel (`Server.go:1940`) stays for compatibility, and
an exported typed sentinel (`rweb.SSEClose`) is added and documented as the
way to end a stream, so an event whose text is "close" can be sent by using
`SSEvent`.

Tests: rewrite the SSE assertions in `sse_test.go` for an int, a map, a
multi-line string and a `[]byte`. CLAUDE.md's "SSE tests need fixing" note is
checked and removed if this clears it.

### N-034 · 204 and 304 responses carry `Content-Length: 0` · ran · value low

RFC 9110 §8.6 forbids Content-Length on 1xx and 204; on a 304 it must be the
length of the representation or absent, and `0` is neither.

Fix in `writeResponse`: for status < 200, 204 and 304, write no
Content-Length and no body (and drop a pending stream).

Tests: extend `head_and_unknown_method_test.go` or `Response_test.go` on the
wire.

---

## Phase 2 — connection hardening

### N-035 · no header size limit and no read, write or idle timeouts · ran · value high · needs D2

A single 64 MB header line was buffered and answered normally
(`Server.go:1231`, `1276`: `ReadString` grows without bound). No deadline is
set anywhere on the HTTP path, so an idle or slow client holds a goroutine and
a pooled context indefinitely. This reopens declined N-012.

New `ServerOptions` (each with a `With…` option; negative disables, zero
selects the default):

| Option | Default | Applies to |
|--------|---------|------------|
| `MaxHeaderBytes` | 1 MB | request line plus all header lines of one request |
| `ReadHeaderTimeout` | 30 s | from the first byte of a request to the end of its headers |
| `IdleTimeout` | 120 s | waiting for the next request on a keep-alive connection |
| `ReadTimeout` | off | reading the body |
| `WriteTimeout` | off | any single stalled write (see below) |

Read side, in `handleConnection`:

```
loop:
  SetReadDeadline(now + IdleTimeout)        wait for a request
  read request line
  SetReadDeadline(now + ReadHeaderTimeout)  headers
  read headers, charging each line to the MaxHeaderBytes budget
  SetReadDeadline(now + ReadTimeout | none) body
  read body
  SetReadDeadline(none)                     handler, disconnect watch, WebSocket
  handleRequest
```

- Lines are read by a bounded reader built on `bufio.Reader.ReadSlice`: it
  accumulates across `ErrBufferFull` into a per-context buffer and stops when
  the budget is spent. Over budget answers `431 Request Header Fields Too
  Large` (new `consts.HTTPHeaderTooLarge`) and closes.
- The first request on a connection uses `ReadHeaderTimeout` for the request
  line too, which also bounds the TLS handshake (it runs inside the first
  read).
- A timeout while idle closes silently; a timeout mid-headers answers 408.
- Clearing the deadline before the handler keeps `connReader`'s watch
  (`conn_reader.go`), which sets and clears its own deadline, exactly as it is.
- Chunk-size and trailer lines use the same bounded reader with a small cap.

Write side: `WriteTimeout` means "no single write may stall longer than this",
not "the whole response must finish in this time", so large downloads still
work:

- headers and in-memory body: one deadline before the write;
- `writeBodyStream`: copy in slices of a few MB with `io.CopyN`, refreshing
  the deadline before each (each slice is still `*os.File` → `*net.TCPConn`,
  so sendfile is kept);
- `writeChunkedBody`: refresh before each chunk;
- `sendSSE`: refresh before each flush. This is what frees the goroutine of a
  client that stays connected but stops reading. Give SSE its own default
  (30 s) even when `WriteTimeout` is off.

Tests: a 2 MB header gets 431; a connection that sends nothing is closed after
a short `IdleTimeout`; half a header then silence gets 408; a keep-alive
connection that sends a second request within the idle window is served; SSE
to a client that never reads returns within the timeout.

### N-036 · request framing accepts ambiguous messages · ran · value medium · needs D3

Observed: with both `Content-Length` and `Transfer-Encoding: chunked`,
Content-Length wins; `Content-Length: -5` is accepted; with two different
Content-Length lines the last wins (`Server.go:1305-1341`). Behind a proxy
that reads the same bytes differently this is request smuggling.

Fix, all in the header loop and all answered 400 and closed:

- both Content-Length and Transfer-Encoding present;
- Content-Length that is not all digits, or repeated with a different value;
- a header name with whitespace before the colon or any non-token character;
- a line starting with space or tab (obsolete line folding);
- a line with no colon (D3);
- Transfer-Encoding other than exactly `chunked` → 501.

Chunked reader: accept chunk extensions (ignore after `;`), read and discard
trailer lines until the empty line (within the bounded reader's cap), require
CRLF after each chunk's data, and reject a size that would overflow when added
to the body length.

Tests: one wire case per rule; `TestBadRequestHeader` and
`TestHeaderEdgeCases` in `Server_test.go` are updated.

### N-037 · `Expect: 100-continue` gets no interim response · ran · value medium

curl sends it for large uploads and waits about a second before giving up and
sending the body anyway.

Fix: after the headers, when the request has a body within the cap and
`Expect: 100-continue` (case-insensitive) on HTTP/1.1, write
`HTTP/1.1 100 Continue\r\n\r\n` before reading the body. Over the cap, the
existing 413 goes out without the body ever being sent, which is the purpose
of the mechanism. Any other expectation → 417. Needs the request-line version
(shared with N-038).

Test: wire test asserting the interim response arrives before the body is
sent, and the 413 path.

### N-038 · `Connection: close` and HTTP/1.0 are ignored · read · value low · needs D4

The request-line version is never parsed (`Server.go:1262-1268`). An HTTP/1.0
client, or any client sending `Connection: close`, waits on a connection the
server keeps open, and an HTTP/1.0 client cannot read a chunked stream.

Fix: parse the version; keep-alive is true for 1.1 unless `Connection: close`,
and for 1.0 only with `Connection: keep-alive`. When false, add
`Connection: close` to the response and leave the loop after writing it. For
an HTTP/1.0 client and a stream of unknown size, send the body unframed and
close (the only delimiter 1.0 has). This reopens declined N-013.

### N-039 · no graceful shutdown; `Run` owns the process's signals · ran (signals) / read · value high

Observed: a process running `Run` in a goroutine survived SIGTERM, because
`signal.Notify` (`Server.go:1141`) is never undone. That is the
`RunWithHttpsRedirect` case: the HTTPS listener stops and the process stays
up serving redirects. Also: nothing waits for in-flight requests, there is no
way to stop a server from code, the accept loop retries errors with no delay
(`Server.go:1130`), and `sendSSE` ignores shutdown.

API:

```go
func (s *Server) Serve(l net.Listener) error        // accept loop on a caller's listener
func (s *Server) Shutdown(ctx stdctx.Context) error // stop accepting, drain, then force
// ServerOptions.ShutdownTimeout: how long Run waits after a signal (default 10 s)
```

`Run` becomes: build the listener, `Serve` it in a goroutine, wait for a
signal or a `Serve` error, `signal.Stop`, `Shutdown` with the timeout.
`ReadyChan`, `GetListenAddr` and the tests that SIGTERM the test process keep
working.

Shutdown sequence:

```
close listeners
cancel baseCtx                       handlers and SSE producers are told (as today)
for each tracked conn:
    idle   → set a past read deadline, its loop wakes and closes
    active → left to finish its response
wait for the conn WaitGroup or ctx.Done()
on ctx.Done(): close every remaining conn (WebSockets included)
```

This needs a connection registry, which N-020 deliberately avoided. The cost
is a mutex on connection open and close and an atomic idle/active flag per
request; the base context stays as the mechanism that reaches handlers.

Also in this item:

- accept loop: on a non-closed error, sleep 5 ms doubling to 1 s, as net/http
  does, instead of spinning;
- `sendSSE`: add `case <-ctx.request.baseCtx.Done()` so streams end at
  shutdown;
- `RunWithHttpsRedirect`: the redirect listener becomes an `http.Server` with
  timeouts that is shut down together with the main server, and an error from
  either side stops both.

Tests: `Shutdown` waits for a slow handler; an idle keep-alive connection is
closed promptly; a handler that outlives the timeout has its connection
closed; an SSE stream ends; `Serve` on a caller's listener; `Run` twice.
`shutdown_test.go` moves from `cancelBaseContext` to `Shutdown`.

### N-040 · WebSocket hardening · read · value high · needs D5

1. **Origin check.** Browsers send cookies with a cross-site WebSocket
   handshake and apply no CORS, so any page can open a socket as the user
   (`websocket.go:195`). Add `WSOptions.CheckOrigin func(Context) bool` and
   `WSOptions.AllowedOrigins []string`. Default: a request with an `Origin`
   header must have an origin host equal to `Host()`, else 403. No `Origin`
   (non-browser clients) is allowed.
2. **Dead peers.** `defaultPingInterval`, `defaultPongTimeout` and
   `defaultWriteTimeout` (`websocket.go:61-63`) are unused; a peer that
   vanishes leaves `ReadMessage` blocked forever. Add
   `WSOptions.PingInterval` (default 30 s, negative disables): a goroutine
   pings on the interval and exits on `Done()`, and every received frame
   extends the read deadline to interval + pong timeout.
3. **Write timeout.** `flushFrames` applies a deadline only if it is in the
   future, so an expired one silently stops being applied. Add
   `WSOptions.WriteTimeout` (default 10 s) applied per flush, and make
   `SetWriteDeadline` behave as on `net.Conn`.
4. **`closed` race.** Written under `closeMutex`, read under `writeMutex`.
   Make it `atomic.Bool`.
5. **Control frames.** Reject a control frame over 125 bytes or without FIN
   (RFC 6455 §5.5) with close code 1002.
6. **Subprotocol.** The first offered subprotocol is echoed back unvalidated.
   Add `WSOptions.Subprotocols`; select the first offered one that is listed,
   and send no header when nothing matches.
7. **Log noise.** A handler returning the read error after a normal close
   reaches the error handler and logs `[ERR]`. In `handleRequest`, when
   `ctx.wsUpgraded`, skip the error handler (its page cannot be sent anyway)
   and log only errors that are not a close or EOF.

Tests: `websocket_reject_test.go` for origin cases; a peer that stops
answering pings is dropped; `go test -race` with concurrent `Close` and
`WriteMessage`; oversized ping.

### N-041 · pooled buffers keep their largest size · read · value medium

`Clean` (`Context.go:229-232`) truncates but keeps capacity. One 100 MB upload
pins 100 MB for the life of that keep-alive connection, and in the pool
afterwards; the same holds for a large response body and `respBuf`.

Fix: in `Clean`, release any of `request.body`, `response.body` and `respBuf`
whose capacity exceeds a retention cap (256 KB), as `WSConn.wbuf` already does
with `maxRetainedWriteBuf`.

Test: after a large request and `Clean`, the capacities are back under the
cap (internal test).

---

## Phase 3 — performance

Baseline at `9781e96`, keep-alive hello on loopback, one connection:
**14.7 µs/op, 144 B/op, 8 allocs/op**. The hot path is already lean, so
these are targeted.

### N-042 · headers and body leave in two writes · ran · value medium

`writeResponse` writes the header block, then the body (`Server.go:1675`,
`1702`): two syscalls and, on TLS, two records. A prototype that appended
bodies up to 16 KB to the header buffer measured 14.3 µs/op against 14.7
(3–4 %) on plain TCP; TLS should gain more and was not measured.

Steps:

1. Add `bench_test.go` with `BenchmarkHelloKeepAlive` (plain and TLS) and a
   JSON-response variant. This is the benchmark for the whole phase.
2. In `writeResponse`, when there is no stream, the request is not HEAD and
   the body is at most 64 KB, append it to `respBuf` and write once. Larger
   bodies keep two writes (the copy would cost more than the syscall).
3. Depends on N-041 so `respBuf` cannot grow and stay grown.

> **As done (2026-11-11):** the cap is 8 KB, not 64 KB. Coalescing lost
> 6–25 % on plain TCP from 16 KB bodies up. The benchmarks are named
> `BenchmarkKeepAlive*`. See N-042 under Closed in `next-list.md`.

### N-043 · WebSocket frame reads are unbuffered and allocate per frame · read · value medium

`readFrame` (`websocket.go:383-441`) does three or four reads on the socket
per frame and allocates the header, extended length and mask separately; the
unmask loop works a byte at a time.

Steps: add `BenchmarkWSEcho` first. Then wrap `ws.r` in a `bufio.Reader`
(the upgrade's lead bytes already arrive through `ws.r`), parse the header
from a fixed array on `WSConn`, and unmask eight bytes at a time. Keep only
what the benchmark shows to matter.

### N-044 · one allocation per request line and header line · read · value low

Seven of the eight allocations per request are the `ReadString` results.
With N-035's bounded reader the header block can be accumulated in a
per-context byte buffer and converted to one string, with header keys and
values as substrings of it. The strings still own their memory, so a handler
that keeps a header value after the request stays safe. Expected: 8 → about 3
allocs/op. Do after N-035; drop the item if the benchmark shows under 5 %.

> **As done (2026-11-11):** built and dropped. Allocations fell 12 → 2 per
> browser-like request, but there was no time gain on one connection or on
> 64 connections in parallel. See N-044 under Non-goals in `next-list.md`.

### N-045 · static handler recomputes per request · read · value low

Each request splits `reqDir` and, for relative roots, calls `filepath.Abs`
twice, which calls `os.Getwd` (`Server.go:888`, `940`). Compute the token
tail and the absolute root once in `staticFiles`. One behaviour difference: a
relative root is resolved when the route is registered, not per request, so a
later `os.Chdir` no longer moves it. Document that on `StaticFiles`.

### N-046 · request bodies are fully buffered before routing · read · value medium (step 2 is N-057, roadmap)

Every body is read into memory, up to the 100 MB default cap, before the
route is looked up, including for paths that will 404
(`Server.go:1352-1427`).

Two steps, the first cheap:

1. **Route before reading.** Look the route up after the headers. On a miss
   answer 404/405 without reading the body and close the connection (or
   discard a small body to keep it alive).
2. **Lazy body (design sketch, larger).** `Request().BodyReader() io.Reader`
   reads from the connection inside the handler; `Body()` reads it all on
   first call, so existing handlers behave as before. After the handler, any
   unread remainder is discarded up to a bound, else the connection closes.
   Consequences to design for: urlencoded parsing becomes lazy like multipart;
   the disconnect watch must not start until the body is consumed; a
   per-route cap becomes possible (`rweb.MaxBody(n)` as middleware); `Proxy`
   can stream uploads.

Step 1 is N-046, in Open. Step 2 is N-057, on the Roadmap until someone
needs it.

> **Decided (2026-11-11): step 1 dropped and folded into N-057.** Global
> middleware (`s.Use`) runs before routing and can answer an unrouted
> request itself. Skipping the body on a miss would therefore hand that
> middleware an empty `Body()`. Every routed POST also accepts bodies up to
> the cap already. Only a lazy body (step 2) removes the cost without
> changing what middleware sees.

### N-047 · `Proxy` buffers the response and uses `http.DefaultClient` · read · value medium · needs D6

`Server.go:738-771`: no timeout, redirects are followed by the proxy instead
of being passed to the client, hop-by-hop headers are forwarded both ways,
and the whole upstream body is held in memory.

Fix: a client owned by the server with `CheckRedirect` returning
`http.ErrUseLastResponse` and a configurable `ResponseHeaderTimeout`; strip
hop-by-hop headers (RFC 9110 §7.6.1) in both directions; add
`X-Forwarded-For`, `-Proto` and `-Host`; hand `resp.Body` to
`rweb.Stream(ctx, resp.Body, resp.ContentLength)`, which closes it and keeps
the request context live until it is sent. Reopens declined N-011.

Tests: extend `proxy_test.go` (redirect passed through, large body streamed,
upstream stalls → timeout) and keep `proxy_cancel_test.go` green.

---

## Phase 4 — convenience

### N-048 · no hooks for errors and not-found · read · value medium

`errorHandler` (`Server.go:281`) has no setter, and 404/405 are an empty body
set inside the default handler. Add `SetErrorHandler(func(Context, error))`,
`SetNotFoundHandler(Handler)` and `SetMethodNotAllowedHandler(Handler)`. The
defaults stay as they are. Also list radix routes in `ListRoutes` by recording
them at `Add`.

### N-049 · path params are not percent-decoded · ran · value medium · needs D7

`/greet/John%20Doe` gives `John%20Doe`; query params are decoded. Decode in
`request.addParameter` when the value contains `%` (keep the raw value if it
does not decode). The static handler then stops decoding its `*path` itself
(`Server.go:909`) so the value is not decoded twice; its traversal checks run
on the decoded value as now. `TestStaticFilesPathTraversal` must stay green,
with an added double-encoding case (`%252e%252e`).

### N-050 · response header names are matched case-sensitively · read · value low

`SetHeader("content-type", …)` followed by `WriteJSON` sends two
Content-Type lines (`Response.go:151`, `161`). Use `strings.EqualFold` in
`Header` and `SetHeader`.

### N-051 · `WriteHTML` and `WriteText` send no charset · ran · value low

Their docs promise `charset=utf-8`; `consts.MIMEHTML` and `MIMETextPlain` are
bare, so a page without a meta charset is guessed at. Send
`text/html; charset=utf-8` and `text/plain; charset=utf-8` from
`WriteHTML`/`WriteText` (and the `…Bytes` forms) and from `rweb.HTML`, `Text`,
`CSS`, `JS`. The constants keep their values (`middleware/critical` matches
with `Contains`); `Response_test.go:112,125` and `send_test.go:54,57` are
updated.

### N-052 · static files and `SendFile` lack Range, ETag and options · read · value medium

- **Range**: single-range `206`/`416` with `If-Range`, by seeking the open
  file and streaming the span. Needed for video seeking and resumed
  downloads.
- **ETag**: weak tag from size and mod time, honoured via `If-None-Match`
  ahead of `If-Modified-Since`.
- **Options**: `StaticFilesWithOptions(reqDir, targetDir, strip,
  StaticOptions{CacheControl, Index, SPAFallback})`; the existing methods are
  unchanged.
- **Types**: before falling back to a forced `application/octet-stream`
  download, ask `mime.TypeByExtension`.
- **Filename**: build Content-Disposition with `mime.FormatMediaType`, which
  encodes non-ASCII names properly; `url.QueryEscape` turns spaces into `+`.

Tests: extend `static_stream_test.go` and `send_test.go`.

### N-053 · logging goes straight to stdout · read · value medium

About thirty `fmt.Print*` calls, several unconditional (for example
`Server.go:1928` on every SSE channel close, `Server.go:639`, `1677`). Add
`ServerOptions.Logger` as a small interface (`Printf`-shaped, so
`rohanthewiz/logger` or `log.Logger` plugs in), default to the `log` package,
and route everything through `s.logf` / `s.debugf`. Messages that are not
errors move to debug level.

### N-054 · SSE options · read · value low · needs D8

`SSECfg.AllowOrigin` replaces the unconditional
`Access-Control-Allow-Origin: *` (`Response.go:277`). `SSEvent` gains `ID`
and `Retry` fields, written as `id:` and `retry:` lines by N-033's helper.
Document on `SSEHandler` that one shared channel delivers each event to one
client, and that `SSEHub` is the fan-out.

### N-055 · `ClientIP` trusts forwarding headers from anyone · read · value low · needs D9

Add `ServerOptions.TrustedProxies []netip.Prefix`. `X-Forwarded-For` and
`X-Real-IP` are read only when the peer address is in the list; a documented
"trust all" value keeps today's behaviour.

### N-056 · helpers and middleware · read · value low · roadmap

Package functions, in the style of `rweb.Stream`, so the `Context` interface
does not grow: `rweb.BindJSON(ctx, &v)` (with a size check and a clear error
for a wrong Content-Type). Middleware subpackages beside `stylus` and
`critical`: `cors`, `reqlog`, `gzip` (compressing in-memory bodies; streams
pass through unless wrapped). Each is its own small item when picked up.

---

## What this plan does not cover

HTTP/2, and anything about the router's matching rules. Neither came up as a
defect.
