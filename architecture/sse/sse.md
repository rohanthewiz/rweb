# SSE Architecture in RWeb

## Overview

RWeb provides built-in Server-Sent Events (SSE) support for real-time, server-to-client streaming over HTTP. The implementation spans two layers: a low-level per-connection streaming loop (`sendSSE`) and a high-level multi-client broadcast hub (`SSEHub`). Both integrate tightly with the framework's `Context` interface and connection lifecycle.

## Design Principles

1. **Proactive Disconnect Detection** - A background read on the underlying TCP connection detects client departure in sub-second time, avoiding stale resource accumulation
2. **Non-blocking Broadcast** - Slow or disconnected clients never block event delivery to other clients
3. **Automatic Lifecycle Management** - SSEHub handles per-client channel creation, registration, heartbeat, stale eviction, and cleanup without handler code
4. **Zero External Dependencies** - Built entirely on Go's standard library (`net`, `bufio`, `fmt`, `sync`)

## Architecture Components

### 1. Context Integration (`Context.go`)

The `context` struct carries three SSE-specific fields:

```go
type context struct {
    // ...
    sseEventsChan <-chan any   // Event source channel
    sseEventName  string       // Default event type (e.g. "message")
    sseCleanup    func()       // Called when sendSSE exits
    conn          net.Conn     // Underlying TCP connection
}
```

**`SetSSE(ch <-chan any, eventName string)`** stores the channel and event name, then calls `SetSSEHeaders()` to write the required SSE response headers. The server's request handler checks `ctx.sseEventsChan != nil` after the handler returns — if set, it calls `sendSSE()` instead of writing a normal response body.

**`Clean()`** resets all SSE state between requests when the context is returned to the pool:

```go
ctx.sseCleanup = nil
ctx.sseEventsChan = nil
ctx.sseEventName = ""
```

### 2. SSE Response Headers (`Response.go`)

`SetSSEHeaders()` configures the response for event streaming:

| Header | Value | Purpose |
|--------|-------|---------|
| `Content-Type` | `text/event-stream; charset=utf-8` | SSE MIME type |
| `Cache-Control` | `no-cache` | Prevent caching |
| `Connection` | `keep-alive` | Persist the connection |
| `Content-Encoding` | `text/plain` | No compression |
| `X-Accel-Buffering` | `no` | Disable Nginx buffering |
| `Access-Control-Allow-Origin` | `*` | CORS |

### 3. SSE Event Types (`Server.go`)

```go
// Named event with explicit type — formatted as:
//   event: {Type}\ndata: {Data}\n\n
type SSEvent struct {
    Type string
    Data interface{}
}

// Sentinel type for heartbeat keepalives — formatted as:
//   :keepalive\n\n
type sseKeepalive struct{}
```

The `sendSSE` event loop dispatches on the concrete type received from the channel:

| Type | Wire Format | Notes |
|------|-------------|-------|
| `sseKeepalive` | `:keepalive\n\n` | SSE comment; keeps connection alive through proxies without triggering `onmessage` |
| `SSEvent` | `event: {Type}\ndata: {Data}\n\n` | Typed event with explicit name |
| `string` | `event: {default}\ndata: {string}\n\n` | Uses the event name from `SetSSE()` |
| Other | `event: {default}\ndata: {formatted}\n\n` | Uses `%+v` formatting |

### 4. The `sendSSE` Streaming Loop (`Server.go`)

This is the core of SSE delivery. It runs for the lifetime of a single client connection.

#### Flow

```
handleConnection()
  └─ handleRequest()
       └─ handler sets ctx.SetSSE(ch, name)
       └─ server detects sseEventsChan != nil
            └─ sendSSE(ctx, respWriter)
                 ├─ defer sseCleanup()
                 ├─ spawn connGone goroutine
                 ├─ optional "Connected" event
                 └─ select loop: connGone | eventsChan
```

#### Client Disconnect Detection

```go
connGone := make(chan struct{})
if ctx.conn != nil {
    go func() {
        buf := make([]byte, 1)
        _, _ = ctx.conn.Read(buf)  // blocks until client closes
        close(connGone)
    }()
}
```

**Why this matters:** Without proactive detection, `sendSSE` only discovers a dead client when it attempts a write/flush and gets a broken-pipe error. That can take up to one full heartbeat interval (~25s). During that window, the server-side goroutine and connection resources remain alive for a client that is already gone.

In HTTP/1.1, browsers enforce a ~6-connection-per-origin limit. SSE-backed dashboards typically hold 2 persistent connections (one per hub). If a user refreshes or navigates away, the browser closes its end immediately, but stale server-side connections accumulate. The browser can exhaust its connection slots and the next page load hangs — the HTML GET queues behind connections the browser already abandoned.

The `connGone` goroutine reads from the underlying `net.Conn`. A read on a half-closed or fully-closed TCP connection returns immediately (EOF or error). This gives sub-second disconnect detection (~12 microseconds in testing). The goroutine exits naturally when `conn.Close()` fires in `handleConnection`'s deferred cleanup, so there is no leak.

#### Event Loop

```go
for {
    select {
    case <-connGone:
        _ = rw.Flush()
        return nil

    case event, ok := <-ctx.sseEventsChan:
        if !ok { return }          // channel closed
        if event == "close" { return }  // explicit close signal
        // format and write event based on type
        // flush buffer for immediate delivery
    }
}
```

### 5. SSEHub — Multi-Client Broadcast (`sse_hub.go`)

SSEHub manages fan-out from a single event source to many connected clients.

#### Structure

```go
type SSEHub struct {
    mu      sync.RWMutex
    clients map[chan any]*hubClient
    opts    SSEHubOptions
    done    chan struct{}
}

type hubClient struct {
    dropped int  // consecutive broadcast sends that hit a full buffer
}
```

#### Configuration

```go
type SSEHubOptions struct {
    ChannelSize       int            // Per-client buffer (default: 8)
    MaxDropped        int            // Eviction threshold (default: 3; 0 = disabled)
    HeartbeatInterval time.Duration  // Keepalive period (default: 0 = disabled)
    OnDisconnect      func()         // Callback on client removal
}
```

#### Registration & Cleanup

`hub.Handler(server)` returns an `rweb.Handler` that:

1. Creates a per-client buffered channel (`make(chan any, ChannelSize)`)
2. Registers it with the hub
3. Sets `ctx.sseCleanup` to auto-unregister when `sendSSE` exits
4. Calls `server.SetupSSE()` to start streaming

When the client disconnects (detected by `connGone` or channel close), `sendSSE` returns, the deferred `sseCleanup` fires, and the client is unregistered from the hub.

#### Broadcasting

Three broadcast methods, all backed by `broadcastToClients()`:

| Method | Behavior |
|--------|----------|
| `Broadcast(SSEvent)` | JSON-wraps `Data`, sends as `"message"` event |
| `BroadcastRaw(SSEvent)` | Sends `SSEvent` directly without JSON wrapping |
| `BroadcastAny(type, data)` | Convenience wrapper around `BroadcastRaw` |

**Non-blocking send:** Uses a `select` with `default` case. If a client's channel is full, the send is skipped and `hubClient.dropped` is incremented. When `dropped >= MaxDropped`, the client is collected for eviction.

#### Stale Client Eviction

During each broadcast, clients whose drop count exceeds `MaxDropped` are collected into a `stale` slice. After releasing the read lock, a write lock is acquired and each stale client is unregistered (channel closed, removed from map, `OnDisconnect` called).

Setting `MaxDropped = 0` disables eviction entirely.

#### Heartbeat Keepalives

When `HeartbeatInterval > 0`, `NewSSEHub` spawns a `runHeartbeat()` goroutine that periodically calls `broadcastToClients(sseKeepalive{})`. This:

- Keeps connections alive through proxies and load balancers
- Triggers stale client detection on the regular broadcast path
- Stops cleanly when `hub.Close()` is called (signals via `done` channel)

### 6. Server Configuration (`Server.go`)

```go
type SSECfg struct {
    SendConnectedEvent bool  // Send "Connected" event on new SSE connection
}
```

Set via `WithSSEConfig(cfg)` or `WithSSESendConnectedEvent()` on `ServerOptions`.

### 7. Convenience Wrappers (`Server.go`)

```go
// Low-level: handler manages its own channel
func (s *Server) SetupSSE(ctx Context, ch <-chan any, eventType ...string) error

// Mid-level: server wraps a shared channel as a handler
func (s *Server) SSEHandler(ch <-chan any, eventType ...string) Handler

// High-level: SSEHub manages per-client channels automatically
func (hub *SSEHub) Handler(server *Server, eventName ...string) Handler
```

## Data Flow

```
                    ┌─────────────┐
                    │  App Code   │
                    │ hub.Broadcast│
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │   SSEHub    │
                    │  fan-out    │
                    └──┬───┬───┬─┘
                       │   │   │   per-client channels
              ┌────────┘   │   └────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ sendSSE  │ │ sendSSE  │ │ sendSSE  │
        │ select { │ │ select { │ │ select { │
        │  connGone│ │  connGone│ │  connGone│
        │  events  │ │  events  │ │  events  │
        │ }        │ │ }        │ │ }        │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │             │             │
        TCP conn      TCP conn      TCP conn
             │             │             │
        Browser 1     Browser 2     Browser 3
```

## Test Coverage

### `sse_hub_test.go` (14 tests)

| Test | What it verifies |
|------|-----------------|
| `TestSSEHubRegisterUnregister` | Basic client lifecycle |
| `TestSSEHubUnregisterIdempotent` | Multiple unregister calls are safe |
| `TestSSEHubBroadcast` | JSON-wrapped event delivery |
| `TestSSEHubBroadcastRaw` | Direct SSEvent delivery |
| `TestSSEHubBroadcastSkipsFullChannels` | Non-blocking send on full buffers |
| `TestSSEHubBroadcastAny` | Convenience wrapper |
| `TestSSEHubHandler` | Handler registration and cleanup setup |
| `TestSSEHubStaleClientEviction` | Auto-eviction after MaxDropped |
| `TestSSEHubEvictionDisabled` | MaxDropped=0 disables eviction |
| `TestSSEHubHeartbeatDelivery` | Periodic keepalive delivery |
| `TestSSEHubCloseStopsHeartbeat` | Close() terminates heartbeat goroutine |
| `TestSSEHubCloseIdempotent` | Multiple Close() calls are safe |
| `TestSSEHubBackwardCompatibility` | Default options preserved |
| `TestSSEHubCustomChannelSize` | Custom ChannelSize respected |
| `TestSSEHubOnDisconnectCalledOnUnregister` | Callback fires on removal |

### `sse_test.go` (2 tests)

| Test | What it verifies |
|------|-----------------|
| `TestSSEHandler` | Full integration: connect, receive 3 events, channel close |
| `TestSSEClientDisconnect` | `sendSSE` returns in sub-second time after client TCP close |

## Usage Examples

### Simple: Shared Channel

```go
events := make(chan any, 8)
s.Get("/events", s.SSEHandler(events, "updates"))

// Send from anywhere
events <- "hello"
events <- rweb.SSEvent{Type: "alert", Data: "important"}
close(events) // ends all connections
```

### Production: SSEHub with Heartbeat and Eviction

```go
hub := rweb.NewSSEHub(rweb.SSEHubOptions{
    ChannelSize:       8,
    MaxDropped:        3,
    HeartbeatInterval: 30 * time.Second,
    OnDisconnect: func() {
        log.Println("Client disconnected")
    },
})
defer hub.Close()

s.Get("/events", hub.Handler(s, "updates"))

// Broadcast to all connected clients
hub.Broadcast(rweb.SSEvent{Type: "update", Data: payload})
```

## File Reference

| File | Role |
|------|------|
| `Server.go` | `SSEvent`, `sseKeepalive` types; `SetupSSE()`, `SSEHandler()`, `sendSSE()` |
| `Context.go` | `SetSSE()` on Context interface; SSE fields on `context` struct; `Clean()` |
| `Response.go` | `SetSSEHeaders()` |
| `sse_hub.go` | `SSEHub`, `SSEHubOptions`, `hubClient`; broadcast, heartbeat, eviction |
| `sse_hub_test.go` | 14 SSEHub unit tests |
| `sse_test.go` | 2 SSE integration tests (handler + disconnect detection) |
| `consts/mime.go` | `MIMETextEventStream` constant |
| `examples/sse_hub/main.go` | Complete working example with UI |