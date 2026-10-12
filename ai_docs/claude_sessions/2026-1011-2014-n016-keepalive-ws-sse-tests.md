# Session: N-016 — WebSocket and SSE on a reused keep-alive connection

Session ID: `051b9cb2-f352-4956-8abb-ea5649a7fb9b`
Date: 2026-10-11

## N-016: test WS upgrade and SSE as a later request on a reused conn

`handleConnection` serves keep-alive requests with one pooled context.
`Clean()` between requests drops `ctx.conn`, and the loop restores it
(`Server.go:1488`). This restore was covered only through `GetConn()`. The
WebSocket and SSE paths both read `ctx.conn` directly:
- `UpgradeWebSocket` writes the 101 to it and wraps it in `NewWSConn`.
- `sendSSE` parks a 1-byte read on it (`connGone`) to spot a client leaving.

Existing tests in `request_context_test.go` upgrade or stream only on a
conn's first request.

Added two tests at the end of `request_context_test.go`, each over tcp and
tls:
- `TestRequestContextWebSocketAfterKeepAlive`: two plain GETs, then an
  upgrade on the same conn. Checks the 101 and two echoes, that the context
  stays live during the session, and that it is cancelled after the peer
  leaves. Reuses `wsServer` plus a `/plain` route.
- `TestRequestContextSSEAfterKeepAlive`: two plain GETs, then an SSE stream.
  The producer sends 3 events, then waits only on the request context and
  never closes the channel. Design point: the request-context watch is
  stopped before the stream (`Server.go:1593`), and with no events going out
  no write fails, so the `connGone` read on `ctx.conn` is the only disconnect
  signal. A producer that kept sending (like `sseServer`) would detect the
  close through a write error and pass even without the restore.

Sensitivity check: with `ctx.conn = conn` commented out, both tests fail.
- WebSocket: the upgrade panics on the nil conn, and the backstop's 500 is
  misread as a frame with a reserved bit set.
- SSE: times out with "did not notice the client leave".

`Server.go` was restored from a scratch copy, and `git diff` shows no change
to it. `go vet ./...` and `go test -race ./...` pass.

## Files
- `request_context_test.go`: new "Takeover on a reused connection" section
  with the two tests.
- `ai_docs/todo/next-list.md`: N-016 moved from Validate to Closed. Validate
  is now empty.

## Next

Closed: N-016. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
