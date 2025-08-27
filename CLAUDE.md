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

### Key Patterns

1. **Handler Signature**: All handlers follow `func(ctx rweb.Context) error`
2. **Middleware Pattern**: Use `ctx.Next()` to pass control to next middleware
3. **Context Interface**: Central abstraction for request/response operations
4. **Route Parameters**: Access via `ctx.Request().Param("name")`
5. **Context Data Storage**: Store request-scoped data via `ctx.Set()`, `ctx.Get()`, `ctx.Has()`, `ctx.Delete()`

### Important Features
- **Server-Sent Events (SSE)**: Built-in support via `SSEHandler()`
- **Static File Serving**: `StaticFiles(urlPrefix, localPath, stripPrefixSegments)`
- **Reverse Proxy**: `Proxy(urlPrefix, targetURL, stripPrefixSegments)`
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
/consts/        - HTTP constants (headers, status codes, MIME types)
/core/rtr/      - Radix tree router implementation
/examples/      - Example applications
/*_test.go      - Test files for each component
```

## Important Notes

- Go 1.22+ required
- Minimal dependencies (only test assertion library)
- Server addresses should use ":8080" format (not "localhost:8080") for Docker compatibility
- The radix tree router provides O(log n) route matching performance
- TODO: SSE tests need fixing