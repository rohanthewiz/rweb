# N-064, N-065, N-066: Proxy read timeout, Location mapping, WebSocket tunnel

Session id: `3a7f4f72-7532-415b-a72a-5eb9a2277e67`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `c449917` → end `e2a8cae` (+ this doc)
Run: `/work-next-list`, second batch (the proxy group). The first batch is
in `2026-1114-1718-n062-n059-n060-n072-ws-browser-linger-strict-head`.

## Done

### N-064: `ProxyReadTimeout` (`042379d`)

- New `ServerOptions.ProxyReadTimeout` / `WithProxyReadTimeout` (copied by
  `WithOptions`). When set, each upstream call runs under its own child of
  `Request().Context()`. The body is streamed through `proxyBodyReader`
  (`proxy.go`): a `time.AfterFunc` timer is reset on entry to each `Read`
  and stopped on return. If it fires, it sets `fired` and cancels the child
  context; the transport then fails the read, which is reported as
  `errProxyReadTimeout`. The stream writers close the client conn, so the
  client sees a truncated body. `Close` stops the timer, closes the body and
  releases the context. The handler's deferred `cancelUp` covers the paths
  where no body reader takes the context over.
- **Per-Read, not gap:** nginx's `proxy_read_timeout` times the gap between
  reads, which works because nginx buffers the body. rweb streams, so a gap
  includes writes to the client, and a slow client would trip a gap timer.
  Verified: `TestProxyReadTimeoutIgnoresSlowClient` cut off at 823 KB of
  2 MB with the timer moved to the gap.
- **Decision: off by default**, like `ReadTimeout`. A proxied SSE stream or
  long poll may rightly stay quiet for minutes; nginx's 60 s default is
  known for cutting those off.
- Tests `proxy_read_timeout_test.go`: a stalled body, sized and chunked, is
  cut off at about the timeout with the part already sent delivered and
  the upstream call abandoned (without the wiring it ran 10 s, until the
  upstream gave up); short stalls, default and negative leave the body
  whole; the slow-client test above.

### N-065: absolute `Location` mapped under the prefix (`0cd99bd`); item stays Open with a question

- `proxyRedirect` (`proxy.go`) reverses the request mapping. An absolute
  URL on the target's scheme and host is rewritten when its path lies
  under `upBase`, on a segment boundary. Host case is ignored, and a
  default port counts as equal to none (`hostWithDefaultPort`). `upBase` is
  `path.Join("/", strippedPrefix, target.Path)`, the URL comes back as
  `clientBase` + the remainder, and query and fragment are kept. It applies
  to `Location` and `Content-Location`.
- **Left alone on purpose:** relative references, including the item's own
  `/new` example. An upstream configured with a sub-path root already
  sends `/api/new`, and a rewrite would make it `/api/api/new`; nginx does
  not rewrite these by default either. Cookie `Path` is left alone too (an
  upstream's `Path=/` often means the whole site). The N-065 text now asks
  the user to choose between (a) leaving it, i.e. Non-goals, and (b) an
  opt-in per-route option (e.g. `ProxyWithOptions` with
  `RewriteRelativeLocation`, `RewriteCookiePath`).
- Tests: `TestProxyRedirectRewrite` (22 mappings), and
  `TestProxyRewritesTargetLocation` on the wire. The existing
  `TestProxyPassesRedirectThrough` (relative `/new` unchanged) still holds.

### N-066: WebSocket upgrades through `Proxy` (`1d08d66`)

- A handshake keeps its upgrade: `Connection: Upgrade` and
  `Upgrade: websocket` are put back after `removeHopByHop`. It goes through
  the proxy's own `http.Client`. Go's Transport forces HTTP/1.1 for upgrade
  requests (`requiresHTTP1`/`onlyH1`) and, on a 101, returns
  `readWriteCloserBody` over the upstream conn as `resp.Body`. The readLoop
  then marks the conn caller-owned (`errCallerOwnsConn`), so the request
  context no longer closes it, and the relay ends itself.
- `relayProxiedUpgrade` (`proxy.go`):
  - takes the client conn over: `GetConn`, `readAhead` for frames sent
    with the handshake, and `wsUpgraded = true`, so `writeResponse` skips
    the conn and the loop closes it;
  - writes the upstream's 101 (`appendUpgradeHead`: hop-by-hop headers
    dropped, `Connection`/`Upgrade` set, names sorted, values sanitized);
  - copies both ways; the first copy to end, or the request context
    (Shutdown), closes both sides, as `httputil.ReverseProxy` does;
  - writes to the client under `WriteTimeout` when it is set.
- Other answers to a handshake pass through as normal responses (e.g. the
  upstream's 403 from its origin check). A 101 that answers no handshake is
  a 502; the old code relayed it as a bare 101.
- Shutdown closes a tunnel at once and sends no 1001 from the proxy: a
  frame written by the proxy could land inside one the upstream is
  sending. An upstream that is itself shutting down sends its own 1001,
  which passes through.
- Tests `proxy_websocket_test.go`:
  - tunnel over tcp and tls fronts: accept key, the upstream's
    subprotocol, the upstream's greeting sent right after its 101, echo up
    to 200 KB, and a client close that ends the upstream handler;
  - lead bytes;
  - the upstream's 403 passed through;
  - Shutdown;
  - an unasked 101 gets 502.

  All five fail on the old handler (426 from the upstream).
- By hand: an rweb `Proxy` on :18084 in front of the N-062 harness.
  wscat: echo works with no Origin and with a listed origin, and the
  upstream's origin refusal comes back as 403. Headless Chrome: open and
  echo; the upstream's 1001 at its own shutdown reached Chrome as
  `close code=1001 clean=true`.

## Checks run for each item

`go test ./...`, `go test -race .`, and `go vet ./...` for darwin, linux and
windows. `gofmt -l .` is clean. `staticcheck` here is built with Go 1.23
and cannot load the module.

## Process notes

- While N-064 was committed, the N-065 rewriter was cut out of `proxy.go`
  into the scratchpad and restored afterwards, so that each commit holds
  one item.
- The Location test helper is in package `rweb` (it needs the unexported
  type); the read-timeout tests are in `rweb_test` next to the existing
  proxy tests.

## Next

Closed: N-064, N-066. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: N-065 (absolute mapping done in
`0cd99bd`; the relative `Location` / cookie `Path` question is left for the
user). Full list: `ai_docs/todo/next-list.md`.
