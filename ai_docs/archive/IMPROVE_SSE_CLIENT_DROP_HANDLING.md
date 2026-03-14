
## SSE: Detect client disconnect proactively

### Problem

`sendSSE()` (Server.go ~line 1032) blocks exclusively on the Go channel (`ctx.sseEventsChan`).
It has no way to detect that the underlying TCP connection has been closed by the client
until it attempts a write/flush and gets a broken pipe error. This creates a window
(up to the heartbeat interval — typically 25s) where the server-side goroutine and
connection resources remain alive for a client that is already gone.

In HTTP/1.1, browsers enforce a ~6-connection-per-origin limit. Each SSE-backed dashboard
tab holds 2 persistent connections (one per hub). When a user refreshes or navigates away,
the browser closes its end immediately, but the server doesn't know yet. If stale
server-side connections accumulate (multiple refreshes, multiple tabs), the browser can
exhaust its connection slots and the next page load hangs — the GET for the HTML page
queues behind connections the browser already abandoned.

### Symptoms (edp_dataflow dashboard)

```
SSE Channel closed and drained, let's clean up and exit...
Error flushing SSE output from channel ...: write: broken pipe
Error sending SSE events: write: broken pipe
```

Dashboard sometimes fails to return the HTML page at all.

### Plan

#### 1. Server-side: add connection-close detection to `sendSSE` (Server.go)

Spawn a small goroutine that reads from the underlying `net.Conn`. Any read on a
half-closed or fully-closed TCP connection returns immediately (EOF or error), which
signals that the client is gone. Use a `done` channel to integrate this with the
existing `select` loop.

```go
func (s *Server) sendSSE(ctx *context, respWriter io.Writer) (err error) {
    defer func() {
        if ctx.sseCleanup != nil {
            ctx.sseCleanup()
        }
    }()

    // --- NEW: detect client disconnect ---
    connGone := make(chan struct{})
    if ctx.conn != nil {
        go func() {
            buf := make([]byte, 1)
            // Read blocks until the client closes or the conn is closed.
            // We don't expect any incoming data on an SSE connection.
            _, _ = ctx.conn.Read(buf)
            close(connGone)
        }()
    }
    // --- end new ---

    // ... existing setup (connect event, bufio writer, etc.) ...

    for {
        select {
        case <-connGone:
            // Client disconnected — clean exit
            _ = rw.Flush()
            return nil

        case event, ok := <-ctx.sseEventsChan:
            // ... existing event handling (unchanged) ...
        }
    }
}
```

**Key details:**
- `ctx.conn` is already stored by `handleConnection` (Server.go ~line 716)
- The read goroutine exits naturally when `conn.Close()` fires in the deferred
  cleanup of `handleConnection`, so no leak
- The `connGone` select case gives sub-second disconnect detection, eliminating
  the dependency on the heartbeat interval for cleanup
- No changes needed to SSEHub, Context, or any handler code

#### 2. Client-side: explicit EventSource cleanup on page unload (edp_dataflow dashbd.js)

Add a `beforeunload` handler at the bottom of `dashbd.js` to close both EventSource
connections before the browser tears down the page. This makes the browser send a
TCP FIN immediately rather than relying on GC timing.

```js
window.addEventListener('beforeunload', function() {
    if (evtSource) { evtSource.close(); evtSource = null; }
    if (progressSource) { progressSource.close(); progressSource = null; }
});
```

#### 3. Tests

- Add a test in `Server_test.go` that opens an SSE connection, closes the client
  side, and asserts that `sendSSE` returns within ~1 second (not 25s).
- Verify existing SSE tests still pass (see "Fix SSE tests" item above).

### Implementation order

1. Server-side fix in `sendSSE` (rweb) — this is the real fix
2. Client-side cleanup in `dashbd.js` (edp_dataflow) — defense in depth
3. Tests
