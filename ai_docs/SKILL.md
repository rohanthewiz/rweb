---
name: rweb-light-go-webserver
description: Build HTTP web servers with the light and low-dependency RWeb Go framework. Covers routing, middleware, cookies, groups, SSE, SSE Hub (multi-client broadcast), WebSockets, static files, proxying, and file uploads.
---

# RWeb Framework Skill

RWeb is a high-performance, lightweight HTTP web server framework for Go featuring a custom radix tree router and practically zero third party dependencies.

## Getting Started

### Basic Server Setup

```go
package main

import (
    "log"
    "github.com/rohanthewiz/rweb"
)

func main() {
    s := rweb.NewServer(rweb.ServerOptions{
        Address: ":8080",  // Use ":port" format for Docker compatibility
        Verbose: true,     // Enable request logging
        Debug:   false,    // Debug mode
    })

    s.Get("/", func(ctx rweb.Context) error {
        return ctx.WriteString("Hello, World!")
    })

    log.Fatal(s.Run())
}
```

### TLS/HTTPS Configuration

```go
s := rweb.NewServer(rweb.ServerOptions{
    Address: ":443",
    TLS: rweb.TLSCfg{
        UseTLS:   true,
        KeyFile:  "certs/localhost.key",
        CertFile: "certs/localhost.crt",
    },
})
```

## Routing

### HTTP Methods

```go
s.Get("/path", handler)
s.Post("/path", handler)
s.Put("/path", handler)
s.Delete("/path", handler)
```

### Route Parameters

```go
// Access via ctx.Request().PathParam("name")
s.Get("/users/:id", func(ctx rweb.Context) error {
    id := ctx.Request().PathParam("id")
    return ctx.WriteString("User ID: " + id)
})

// Nested parameters
s.Get("/orgs/:org/repos/:repo", func(ctx rweb.Context) error {
    org := ctx.Request().PathParam("org")
    repo := ctx.Request().PathParam("repo")
    return ctx.WriteJSON(map[string]string{"org": org, "repo": repo})
})
```

### Fixed vs Parameterized Routes

Fixed routes take precedence over parameterized ones when there's an exact match:

```go
s.Get("/greet/:name", handler)    // Matches /greet/john, /greet/mary
s.Get("/greet/city", handler)     // Exact match takes priority for /greet/city
```

## Response Types

```go
// Plain text
ctx.WriteString("Hello")

// HTML
ctx.WriteHTML("<h1>Welcome</h1>")

// JSON (auto-marshals Go structs/maps)
ctx.WriteJSON(map[string]string{"status": "ok"})

// CSS helper
rweb.CSS(ctx, "body { color: red; }")

// File download
rweb.File(ctx, "filename.txt", fileBytes)

// Set status code
ctx.SetStatus(404).WriteString("Not found")

// Redirect
ctx.Redirect(302, "/new-location")
```

## Middleware

Middleware executes in registration order. Call `ctx.Next()` to continue the chain.

```go
// Global middleware
s.Use(func(ctx rweb.Context) error {
    start := time.Now()
    defer func() {
        fmt.Printf("%s %s -> %d [%s]\n",
            ctx.Request().Method(),
            ctx.Request().Path(),
            ctx.Response().Status(),
            time.Since(start))
    }()
    return ctx.Next()
})

// Built-in request info logger -- highly recommended
s.Use(rweb.RequestInfo)
```

## Context Data Storage

Store request-scoped data accessible to all middleware and handlers:

```go
// Set data (typically in middleware)
ctx.Set("userId", "123")
ctx.Set("isAdmin", true)

// Get data
userId := ctx.Get("userId").(string)

// Check existence
if ctx.Has("isLoggedIn") {
    // user is logged in
}

// Delete data
ctx.Delete("userId")
```

### Authentication Pattern

```go
s.Use(func(ctx rweb.Context) error {
    authHeader := ctx.Request().Header("Authorization")
    if authHeader == "Bearer valid-token" {
        ctx.Set("isLoggedIn", true)
        ctx.Set("userId", "123")
    }
    return ctx.Next()
})

s.Get("/profile", func(ctx rweb.Context) error {
    if !ctx.Has("isLoggedIn") || !ctx.Get("isLoggedIn").(bool) {
        return ctx.SetStatus(401).WriteString("Unauthorized")
    }
    return ctx.WriteJSON(map[string]string{"userId": ctx.Get("userId").(string)})
})
```

## Route Groups

Organize routes with common prefixes and middleware:

```go
// API versioning
api := s.Group("/api")
v1 := api.Group("/v1")
v1.Get("/status", statusHandler)  // GET /api/v1/status

// Protected routes with middleware
users := v1.Group("/users", authMiddleware)
users.Get("/", listUsers)         // GET /api/v1/users
users.Get("/:id", getUser)        // GET /api/v1/users/:id
users.Post("/", createUser)       // POST /api/v1/users

// Multiple middleware (executed in order)
admin := s.Group("/admin", authMiddleware, adminMiddleware)
admin.Get("/dashboard", dashboardHandler)
```

## Cookies

### Server-wide Cookie Config

```go
s := rweb.NewServer(rweb.ServerOptions{
    Address: ":8080",
    Cookie: rweb.CookieConfig{
        HttpOnly: true,
        SameSite: rweb.SameSiteLaxMode,
        Path:     "/",
    },
})
```

### Cookie Operations

```go
// Set simple cookie
ctx.SetCookie("name", "value")

// Set cookie with options
cookie := &rweb.Cookie{
    Name:    "session_id",
    Value:   sessionID,
    Expires: time.Now().Add(30 * 24 * time.Hour),
    MaxAge:  30 * 24 * 60 * 60,
}
ctx.SetCookieWithOptions(cookie)

// Get cookie
value, err := ctx.GetCookie("name")

// Check if cookie exists
if ctx.HasCookie("session_id") {
    // cookie exists
}

// Delete cookie
ctx.DeleteCookie("name")

// Get and clear (useful for flash messages)
value, err := ctx.GetCookieAndClear("flash")
```

## Static Files

```go
// StaticFiles(urlPrefix, localPath, stripPrefixSegments)

// /static/images/photo.png -> ./assets/images/photo.png
s.StaticFiles("/static/images/", "./assets/images", 2)

// /css/style.css -> ./assets/css/style.css
s.StaticFiles("/css/", "./assets/css", 1)

// /.well-known/acme -> ./.well-known/acme
s.StaticFiles("/.well-known/", "./", 0)
```

## File Uploads

```go
s.Post("/upload", func(ctx rweb.Context) error {
    req := ctx.Request()

    // Get form field
    name := req.FormValue("name")

    // Get uploaded file
    file, header, err := req.GetFormFile("file")
    if err != nil {
        return err
    }
    defer file.Close()

    // Read file content
    data, err := io.ReadAll(file)
    if err != nil {
        return err
    }

    // Save to disk
    return os.WriteFile("uploads/"+header.Filename, data, 0666)
})
```

## Server-Sent Events (SSE)

### Single-Channel SSE

For simple cases where one channel feeds one endpoint:

```go
// Create event channel
eventsChan := make(chan any, 100)

// Option 1: Using SetupSSE
s.Get("/events", func(ctx rweb.Context) error {
    return s.SetupSSE(ctx, eventsChan)
})

// Option 2: Using SSEHandler helper
s.Get("/events2", s.SSEHandler(eventsChan))

// Send events from anywhere
eventsChan <- "event data"
eventsChan <- map[string]string{"type": "update", "data": "value"}
```

### SSE Hub (Multi-Client Broadcast)

SSEHub provides a fan-out pattern for broadcasting events to all connected clients.
Each client gets its own buffered channel, and the hub manages registration and
cleanup automatically. The hub is standalone — not tied to a specific server or route.

```go
// Create the hub — can be shared across routes
hub := rweb.NewSSEHub()

// Register the SSE endpoint. hub.Handler() manages per-client lifecycle:
// creates a buffered channel, registers it, and auto-unregisters on disconnect.
s.Get("/logs/stream", hub.Handler(s))

// Broadcast from anywhere — e.g., a log ingestion goroutine.
// Broadcast() JSON-wraps {type, data} and sends as a "message" SSE event,
// so JS clients use a single onmessage handler + JSON.parse().
hub.Broadcast(rweb.SSEvent{
    Type: "log",
    Data: "2026-02-21 10:05:32 [INFO] User logged in",
})

// BroadcastAny is a convenience wrapper
hub.BroadcastAny("error", "disk usage at 92%")

// BroadcastRaw sends the SSEvent as-is without JSON wrapping —
// use when JS clients listen with addEventListener on specific event names
hub.BroadcastRaw(rweb.SSEvent{Type: "heartbeat", Data: "ok"})

// Check connected client count (useful for status endpoints)
count := hub.ClientCount()
```

#### Log Stream Example

A real-time log viewer that tails application logs to all connected browsers:

```go
func main() {
    s := rweb.NewServer(
        rweb.WithAddress(":8080"),
        rweb.WithVerbose(),
    )

    logHub := rweb.NewSSEHub()

    // SSE endpoint — clients connect here to receive log events
    s.Get("/logs/stream", logHub.Handler(s))

    // Status endpoint
    s.Get("/logs/viewers", func(ctx rweb.Context) error {
        return ctx.WriteJSON(map[string]any{
            "viewers": logHub.ClientCount(),
        })
    })

    // Simulate log ingestion — in production this would tail a file or consume a queue
    go func() {
        entries := []string{
            "[INFO] Server started on :8080",
            "[INFO] Connected to database",
            "[WARN] Slow query detected (1.2s)",
            "[ERROR] Failed to send email: timeout",
            "[INFO] User alice logged in",
        }

        i := 0
        for range time.NewTicker(2 * time.Second).C {
            logHub.Broadcast(rweb.SSEvent{
                Type: "log",
                Data: fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), entries[i%len(entries)]),
            })
            i++
        }
    }()

    log.Fatal(s.Run())
}
```

#### JS Client for SSE Hub

Since `Broadcast()` sends JSON-wrapped data under the standard "message" event type,
JS clients only need `onmessage` — no `addEventListener` required:

```js
const evtSource = new EventSource('/logs/stream');

evtSource.onmessage = function(e) {
    // payload is JSON: {"type": "log", "data": "10:05:32 [INFO] User logged in"}
    const payload = JSON.parse(e.data);
    console.log('[' + payload.type + ']', payload.data);
};

evtSource.onerror = function() {
    console.log('Disconnected — EventSource will auto-reconnect');
};
```

## WebSockets

```go
s.WebSocket("/ws/echo", func(ws *rweb.WSConn) error {
    defer ws.Close(1000, "Closing")

    // Send message
    ws.WriteMessage(rweb.TextMessage, []byte("Welcome"))

    // Read loop
    for {
        msg, err := ws.ReadMessage()
        if err != nil {
            break
        }

        switch msg.Type {
        case rweb.TextMessage:
            ws.WriteMessage(rweb.TextMessage, msg.Data)
        case rweb.BinaryMessage:
            ws.WriteMessage(rweb.BinaryMessage, msg.Data)
        case rweb.CloseMessage:
            return nil
        }
    }
    return nil
})

// Ping/Pong for keepalive
ws.SetPongHandler(func(data []byte) error {
    return nil
})
ws.WritePing([]byte("ping"))
```

## Reverse Proxy

```go
// Proxy(urlPrefix, targetURL, stripPrefixSegments)

// /api/backend/* -> http://backend:8081/*
err := s.Proxy("/api/backend", "http://backend:8081", 2)
if err != nil {
    log.Fatal(err)
}
```

## Request Information

```go
req := ctx.Request()

req.Method()              // GET, POST, etc.
req.Path()                // /users/123
req.PathParam("id")       // Route parameter
req.Header("Authorization") // Request header
req.Body()                // Raw request body bytes
req.FormValue("field")    // Form field value
req.GetPostValue("field") // POST form value
```

## Handler Signature

All handlers follow this signature:

```go
func(ctx rweb.Context) error
```

Return `nil` for success, or an error which rweb will handle appropriately.

## Testing Endpoints

```bash
# Basic GET
curl http://localhost:8080/

# With headers
curl -H "Authorization: Bearer token" http://localhost:8080/api/users

# POST with form data
curl -X POST -d "name=John" http://localhost:8080/users

# File upload
curl -X POST -F "file=@document.pdf" http://localhost:8080/upload

# JSON body
curl -X POST -H "Content-Type: application/json" -d '{"name":"John"}' http://localhost:8080/users
```
