# Session: Next-list sweep from N-017 on (N-019, N-020, N-024, N-025, N-026)

Session ID: `e3ddedf8-5b83-4c63-8d9a-e0af56fe80b4`
Date: 2026-10-11

The user asked for every open Next-list item from N-017 on, with a commit
after each and any item that needs their attention skipped. N-017 was
already closed (`d65126e`). Done in ID order: N-019, N-020, N-024, N-025,
N-026. **Skipped: N-018**, a decline-or-implement call (see the end).
N-007, N-015 and N-016 fall below N-017 and were not in scope.

| Commit    | Item  | Change |
|-----------|-------|--------|
| `8f8b806` | N-019 | Proxy upstream call carries the request context |
| `22097be` | N-020 | Shutdown cancels request contexts |
| `5885e7a` | N-024 | Synthetic `Request()` removes multipart spill files |
| `91b9e58` | N-025 | WebSocket routes answer non-handshakes with 426/400 |
| `c9be585` | N-026 | `examples/websocket` compiles |

`go test -race -count=1 ./...`, `go vet` and `gofmt -l` are clean after each
commit. Each new test was also run against the pre-change code and fails
there, except the N-020 tests, which call methods that did not exist before.

## N-019: Proxy cancels the upstream call when the client leaves

- `Server.Proxy` builds the upstream request with
  `http.NewRequestWithContext(ctxReq.Context(), …)`.
- When `Do` or the body read fails **and** the request context is done, the
  handler calls `clientGone(ctx)`. It records 502 and returns nil, so the
  default error handler does not log `[ERR]` or write a 500 page to a dead
  connection (the same choice `httputil.ReverseProxy` makes). The check
  looks at the context rather than `errors.Is(err, context.Canceled)`: the
  context can only end mid-handler because the client left or (after
  N-020) the server is shutting down. A genuine upstream failure with the
  client still connected still goes to the error handler.
- Fixed on the way: `resp.Body` was not closed when `io.ReadAll` failed.
- Tests, `proxy_cancel_test.go` (internal, TCP and TLS):
  - `TestProxyCancelsUpstreamWhenClientLeaves`: the httptest upstream parks
    on `r.Context().Done()`; the client closes; the upstream must see the
    cancel within `waitFor`, the proxy handler must return nil with status
    502, and the error handler must not run. Middleware wrapping
    `ctx.Next()` observes the handler's return value. On the old code it
    times out.
  - `TestProxyUpstreamFailureStillReported`: a refused upstream still
    reaches the error handler.
- `ai_docs/SKILL.md`, Reverse Proxy: a paragraph on the cancellation.

## N-020: Shutdown cancels request contexts

Design: a server-wide base context instead of connection tracking. It needs
no registry, costs nothing per request, and reaches handlers blocked on
anything, not just the conn.

```
NewServer ── baseCtx (WithCancel(Background))
                │  snapshotted once per connection (request.baseCtx),
                │  and by synthetic s.Request
                ▼
      Request().Context() = WithCancel(baseCtx)   (lazy, as before)

Run: renewBaseContext() → … SIGINT/SIGTERM → listener.Close() → cancelBaseContext()
```

- `Server.baseMu`, `baseCtx` and `baseCancel` are set in `NewServer`. The
  helpers `baseContext()` (Background for a Server not built by
  `NewServer`), `renewBaseContext()` and `cancelBaseContext()` live in
  `Server.go`.
- `request.baseCtx` is set in `handleConnection` and in `Server.Request`.
  `Clean` leaves it alone, because the next request on the connection has
  the same parent.
- `Run` renews a cancelled base before listening, so a server can be run
  again. Connections left open from before keep their cancelled snapshot.
- Keep-alive: after writing a response, `handleConnection` returns (closing
  the conn) if its base is cancelled. A connection idle in its read is not
  woken; it serves at most one more request and then closes. `Run` still
  does not wait for in-flight handlers.
- Tests, `shutdown_test.go`, call `cancelBaseContext` directly, the step
  `Run` takes. Signalling the test process was ruled out: the signal would
  reach every server other tests left in `Run`, and one sent before `Run`'s
  `signal.Notify` would kill the process.
  - `TestShutdownCancelsInFlightRequests` (TCP and TLS): the handler is
    cancelled, its response is still delivered, and the next read is EOF.
  - `TestShutdownBaseContextRenewed` and `TestShutdownSyntheticRequest`.
- Docs: `Request.Context()` doc comment, `ai_docs/SKILL.md` Request
  Context, and the `CLAUDE.md` Request context bullet.

## N-024: synthetic `Request()` leaves multipart temp files

`Server.Request` cannot run `Clean`, because that would zero the `Response`
it returns. It now calls `ctx.request.CleanupMultipartForm()` after
`handleRequest`, which touches only the request half. The
`TestMultipartParsedOnlyOnUse` comment that pointed at the gap is replaced
by an assertion that the temp dir is empty (the old code left 2 files).

## N-025: WebSocket route plus non-upgrade request gave 500

- `s.WebSocket` and `s.WebSocketWithOptions` check `IsWebSocketUpgrade()`
  first. On a non-handshake they call `rejectWebSocketRequest`
  (`websocket.go`), which answers and returns nil:
  - no `Upgrade: websocket`, or any non-GET such as HEAD → **426** with
    `Upgrade: websocket` and `Connection: Upgrade` (RFC 9110 §15.5.22, §7.8);
  - `Sec-WebSocket-Version` other than 13 → **426** with
    `Sec-WebSocket-Version: 13` (RFC 6455 §4.4);
  - otherwise (missing key or `Connection: upgrade`) → **400**.
- New `consts.StatusUpgradeRequired` (426), with its status text.
- The `Upgrade` token is now case-insensitive in `IsWebSocketUpgrade` and
  `performHandshake` (RFC 6455 §4.2.1). Otherwise an `Upgrade: WebSocket`
  client would fall through the reject classifier to a 400.
- A handler that calls `ctx.UpgradeWebSocket()` itself still gets
  `ErrWebSocketNotUpgraded`. That is the caller's path to handle, and the
  SKILL's Manual Upgrade example already checks `IsWebSocketUpgrade`.
- Tests, `websocket_reject_test.go`: every case through both route helpers
  via synthetic requests, with the error handler asserted unused, plus a
  wire test showing `Upgrade: WebSocket` gets 101.
- `ai_docs/SKILL.md`, WebSockets → Registration: the reject behaviour.

## N-026: `examples/websocket` did not compile

`rweb.NewServer(rweb.WithAddress(…), rweb.WithVerbose())` became
`rweb.NewServerWithOptions(…)`. All four example modules and
`./examples/...` build. The example was run briefly on :8080: `/` returned
200 and `curl /ws/echo` returned `426 Upgrade Required` with
`Upgrade: websocket`, confirming N-025 end to end. The process was stopped
by its own PID.

## Skipped: N-018 (needs the user)

N-018 asks whether synthetic `s.Request()` should run the wire path's Host
validation. The item itself calls it a candidate for Non-goals, so the
remaining work is a scope decision. Recommendation: decline. Synthetic
headers come from trusted test code, and the 400-on-bad-Host path is
already covered over the wire. Implementing parity would be about 10 lines
if wanted. The item is left unchanged in Open.

## Next

Closed: N-019, N-020, N-024, N-025, N-026. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
