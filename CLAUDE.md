# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

RWeb is a high-performance, lightweight HTTP web server framework for Go, featuring a custom radix tree router implementation. It's a fork of Akyoto's web framework with additional features and improvements.

## Development Commands

### Testing
```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run router benchmarks
go test -bench=. ./core/rtr/

# Run specific benchmark
go test -bench=BenchmarkGitHub ./core/rtr/
```

### Building and Running
```bash
# Build the project
go build

# Run examples
cd examples/hello && go run main.go

# Test endpoints
curl http://localhost:8080/
curl http://localhost:8080/greet/John
curl http://localhost:8080/some-json
```

## Architecture

### Core Components
- **Server.go**: Main server implementation with TLS support
- **Context.go**: Request/Response context interface - central to all handlers
- **Request.go/Response.go**: HTTP request and response handling
- **core/rtr/**: Radix tree router for high-performance route matching
- **middleware.go**: Middleware chain implementation
- **conn_reader.go**: The reader under each connection's bufio.Reader; runs the disconnect watch behind `ctx.Request().Context()` (a 1-byte background read, as in net/http). Anything that takes over a conn's reads (WebSocket upgrade, `GetConn`, `sendSSE`) must stop the watch first

- **conn_limits.go**: Connection limits (`MaxHeaderBytes`, `ReadHeaderTimeout`, `IdleTimeout`, `ReadTimeout`, `WriteTimeout`, `SSECfg.WriteTimeout`; zero = default, negative = off), the bounded `lineReader` for request heads, and the diagram of which deadline is armed in each phase of a request. Read deadlines are cleared before the handler and write deadlines after each response; keep it that way, or the disconnect watch, WebSocket and `GetConn` meet a stale deadline
- **shutdown.go**: Server lifecycle — `Serve(listener)`, `Shutdown(ctx)`, `ErrServerClosed`, accept backoff (5 ms doubling to 1 s), and the connection registry (`Server.conns`, `trackedConn` with an atomic idle/active/closing state). Diagrams of the shutdown sequence and connection states are at the top. `lifeMu` is held only for map work, never while closing conns or waiting. The request loop (`serveConn` in Server.go) arms its idle read deadline *before* publishing the idle state, then checks `inShutdown`; keep that order or Shutdown's wake-up deadline can be overwritten. `Run` = listen + `signal.Notify` + `serve` + `signal.Stop` + `Shutdown(ShutdownTimeout)`; tests should use `Serve`/`Shutdown` (a SIGTERM with no `Run` listening kills the test process)
- **proxy.go**: `Proxy`'s upstream client and header rules (hop-by-hop list, `X-Forwarded-*`), with a diagram of one request through the proxy; the handler itself is `proxy` in Server.go
- **request_framing.go**: Strict request framing (anti request-smuggling): `Content-Length` parsing, `Transfer-Encoding` rules, and `readChunkedBody` (extensions, trailers, CRLF after data, overflow and framing-overhead bounds). Ambiguous messages get `400`/`501` and a close; do not loosen these without considering a proxy that reads the bytes differently

### Key Patterns

1. **Handler Signature**: All handlers follow `func(ctx rweb.Context) error`
2. **Middleware Pattern**: Use `ctx.Next()` to pass control to next middleware
3. **Context Interface**: Central abstraction for request/response operations
4. **Route Parameters**: Access via `ctx.Request().Param("name")`
5. **Context Data Storage**: Store request-scoped data via `ctx.Set()`, `ctx.Get()`, `ctx.Has()`, `ctx.Delete()`

### Important Features
- **Server-Sent Events (SSE)**: Built-in support via `SSEHandler()`. Every event is written by `writeSSEEvent` (Server.go): payload text per `sseDataText` (string/[]byte as is, error/Stringer via method, basic kinds via strconv, else JSON), one `data:` line per payload line, CR/LF stripped from the event name. `rweb.SSEClose` (or the legacy bare string `"close"`) ends a stream
- **Request context**: `ctx.Request().Context()` is cancelled when the client disconnects mid-handler, when the server shuts down (`Shutdown`, which `Run` calls on SIGINT/SIGTERM, cancels `Server.baseCtx`, which it derives from; keep-alive conns then close after their current response), and always when the request ends (after the stream, for SSE)
- **Graceful shutdown**: `Shutdown(ctx)` closes listeners, cancels the base context, wakes and closes idle keep-alive conns at once (past read deadline), sends WebSockets a 1001 close frame (`WSConn.goingAway`), ends SSE streams (`sendSSE` selects on the base context), waits for active requests, and at ctx's end closes what remains and returns `ctx.Err()`; on either return it closes the Proxy client's idle upstream conns. `Run` waits `ServerOptions.ShutdownTimeout` (default 10 s; negative = no wait) and calls `signal.Stop` before returning. `RunWithHttpsRedirect` runs its redirector as an `http.Server` with timeouts, shut down with the main server; an error on either side stops both
- **Static File Serving**: `StaticFiles(urlPrefix, localPath, stripPrefixSegments)` — streamed from disk via a pending body stream (`response.bodyStream`); touching the body (`Body`, `Write`, `SetBody`) loads it into memory first
- **Streamed bodies**: `rweb.Stream(ctx, r, size)` sends any `io.Reader` after the handler returns (size → Content-Length; negative → chunked); `rweb.SendFile`/`SendFileAs` stream a file from disk with file headers and 304 support. A handler error drops a pending stream; a failed/short stream closes the conn so the client sees a truncated body
- **Bodyless statuses**: `writeResponse` sends a 1xx, 204 or 304 (`statusHasNoBody`) as the header block alone — no Content-Length/Transfer-Encoding, no body, pending stream closed unread, SSE not started. The WebSocket 101 is written by `UpgradeWebSocket` and never gets here
- **WebSockets**: `s.WebSocket`, `s.WebSocketWithOptions(path, WSOptions, h)` or a manual `ctx.UpgradeWebSocket()`; all go through `context.upgradeWebSocket`, which applies `WSOptions` with their defaults (`WSConn.applyOptions`) and starts the keepalive (`startKeepalive`: ping goroutine + a `livenessReader` that pushes the read deadline out on every received byte). A WebSocket's life is its handler's: `handleRequest` calls `WSConn.finish` when the handler returns (closes `Done()`, stops the ping goroutine), then the HTTP loop closes the conn. Frame reading is single-owner (`WSConn.readSem`): `ReadMessage` holds it per call (handing it back while an app ping/pong handler runs), and `Close` waits for it to read the peer's close frame instead of reading alongside a blocked `ReadMessage` (diagram on `Close`); never read frames without it. Frames are read through a per-connection 4 KB `bufio.Reader` (`WSConn.br`) that `readFrame` creates on first use around `ws.r` (lead bytes from `readAhead` → `livenessReader` → conn), so anything that wraps `ws.r` must do so before the first read; the header lands in the fixed `WSConn.hdr` and payloads are unmasked eight bytes at a time (`maskBytes`). `BenchmarkWSEcho` (websocket_bench_test.go) measures the read/echo path
- **Connection limits**: on by default — 1 MB request head (`431`), 30 s to read a head (`408`; also bounds the TLS handshake), 120 s keep-alive idle. Body-read and write timeouts are off by default; `WriteTimeout` bounds each stalled write of ≤ 1 MB (sized streams are sliced so sendfile is kept), and SSE has its own 30 s per-flush timeout
- **Expect: 100-continue**: `serveConn` (the request loop; `handleConnection` registers a conn and calls it) writes `100 Continue` right before reading a body (HTTP/1.1 only), after the framing and body-cap checks; other expectations get `417`
- **Keep-alive**: the request-line version is parsed; `request.http10` and `request.connClose` are set per request in `serveConn`. 1.1 persists unless `Connection: close`; 1.0 closes unless `Connection: keep-alive`. `writeResponse` adds the matching `Connection` header, honours a handler's own `Connection: close`, and sends an unknown-length body to a 1.0 client unframed (then closes); the loop ends after any response with `connClose`
- **Reverse Proxy**: `Proxy(urlPrefix, targetURL, stripPrefixSegments)`. The handler (`proxy` in Server.go) sends upstream through the server's own client (`proxyHTTPClient`, proxy.go: cloned `DefaultTransport`, `CheckRedirect` = `ErrUseLastResponse` so 3xx passes through, `ResponseHeaderTimeout` = `ProxyResponseHeaderTimeout` (default 60 s; zero = default, negative = off), 32 idle conns per host, compression off; `Shutdown` closes its idle conns). Hop-by-hop headers are dropped both ways (`removeHopByHop`), `X-Forwarded-For` is appended to and `-Proto`/`-Host` set (`setForwardedHeaders`), and the response body goes out via `rweb.Stream(ctx, resp.Body, resp.ContentLength)`, so the upstream call must stay tied to `Request().Context()`, which lives until the response is written. Header timeout → 504, other upstream failure → 502 (both via the error handler), client gone → `clientGone` (502, no error handler). The request body is still buffered
- **File Uploads**: Handled via `GetFormFile()` on Request
- **Multiple Response Types**: JSON, HTML, Text, CSS, File responses
- **Context Data Storage**: Request-scoped data storage for auth, sessions, etc.

### Context Data Storage

The Context interface provides request-scoped data storage through a `map[string]any` field. This is useful for:
- Authentication state (`isLoggedIn`, `userId`, `isAdmin`)
- Session data
- Request-specific metadata
- Passing data between middleware and handlers

**Methods:**
- `ctx.Set(key string, value any)` - Store a value
- `ctx.Get(key string) any` - Retrieve a value (returns nil if not found)
- `ctx.Has(key string) bool` - Check if a key exists
- `ctx.Delete(key string)` - Remove a value

**Example:**
```go
// Auth middleware
s.Use(func(ctx rweb.Context) error {
    if validToken {
        ctx.Set("isLoggedIn", true)
        ctx.Set("userId", "123")
    }
    return ctx.Next()
})

// Handler
s.Get("/profile", func(ctx rweb.Context) error {
    if !ctx.Has("isLoggedIn") || !ctx.Get("isLoggedIn").(bool) {
        return ctx.SetStatus(401).WriteString("Unauthorized")
    }
    userId := ctx.Get("userId").(string)
    // ... handle request
})
```

**Notes:**
- Data is automatically cleared between requests via `Clean()` method
- Thread-safe within a request (each context is used by one goroutine)
- Map is lazily initialized on first `Set()` call

### Directory Structure
```
/consts/            - HTTP constants (headers, status codes, MIME types)
/core/rtr/          - Radix tree router implementation
/middleware/stylus/ - Stylus CSS middleware (compiles .styl via go-styl; optional subpackage)
/examples/          - Example applications
/*_test.go          - Test files for each component
```

## Important Notes

- Go 1.24+ required (`os.Root`, used by `StaticContainSymlinks`)
- Minimal dependencies (only test assertion library)
- Server addresses should use ":8080" format (not "localhost:8080") for Docker compatibility
- The radix tree router provides O(log n) route matching performance