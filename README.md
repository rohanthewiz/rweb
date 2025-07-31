## Intro
RWeb is a light, high performance web server for Go.

It is a fork of Akyoto's [web](http://git.akyoto.dev/go/web) with some additional features and changes.

> Imitation is the sincerest form of flattery.

Thanks and credit to Akyoto, especially for the radix tree!

## Caution
- This is still in beta - use with caution.

## Features

- High performance
- Low latency
- Server Sent Events
- Flexible static files handling
- Scales incredibly well with the number of routes
- Route grouping with middleware support

## Installation

```shell
go get -u github.com/rohanthewiz/rweb
```

## Usage

(See examples in examples/hello/main.go)

```go
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/rohanthewiz/rweb"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: "localhost:8080",
		Verbose: true, Debug: true,
		TLS: rweb.TLSCfg{
			UseTLS:   false,
			KeyFile:  "certs/localhost.key",
			CertFile: "certs/localhost.crt",
		},
	})

	// Middleware
	s.Use(func(ctx rweb.Context) error {
		start := time.Now()

		defer func() {
			fmt.Println(ctx.Request().Method(), ctx.Request().Path(), time.Since(start))
		}()

		return ctx.Next()
	})

	s.Use(func(ctx rweb.Context) error {
		fmt.Println("In Middleware 2")
		return ctx.Next()
	})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Welcome\n")
	})

	// Similar URLs, one with a parameter, other without - works great!
	s.Get("/greet/:name", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello " + ctx.Request().Param("name"))
	})
	s.Get("/greet/city", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi big city!")
	})

	// Long URL is not a problem
	s.Get("/long/long/long/url/:thing", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello " + ctx.Request().Param("thing"))
	})
	s.Get("/long/long/long/url/otherthing", func(ctx rweb.Context) error {
		return ctx.WriteString("Hey other thing!")
	})

	s.Get("/home", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome home</h1>")
	})

	s.Get("/some-json", func(ctx rweb.Context) error {
		data := map[string]string{
			"message": "Hello, World!",
			"status":  "success",
		}
		return ctx.WriteJSON(data)
	})

	s.Get("/css", func(ctx rweb.Context) error {
		return rweb.CSS(ctx, "body{}")
	})

	s.Post("/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.WriteString("Posted - form_id: " + ctx.Request().Param("form_id"))
	})

	// We could do this for one specific file, but better to use s.StaticFiles to map a whole directory
	s.Get("/static/my.css", func(ctx rweb.Context) error {
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err
		}
		return rweb.File(ctx, "the.css", body)
	})

	// e.g. http://localhost:8080/static/images/laptop.png
	s.StaticFiles("static/images/", "/assets/images", 2)

	// e.g. http://localhost:8080/css/my.css
	s.StaticFiles("/css/", "assets/css", 1)

	// e.g. http://localhost:8080/.well-known/some-file.txt
	s.StaticFiles("/.well-known/", "/", 0)

	// File upload
	s.Post("/upload", func(c rweb.Context) error {
		req := c.Request()

		// Get form fields
		name := req.FormValue("vehicle")
		fmt.Println("vehicle:", name)

		// Get uploaded file
		file, _, err := req.GetFormFile("file")
		if err != nil {
			return err
		}
		defer file.Close()

		// Save the file
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		err = os.WriteFile("uploaded_file.txt", data, 0666)
		if err != nil {
			return err
		}
		return nil
	})

	// Server Sent Events
	eventsChan := make(chan any, 8)
	eventsChan <- "event 1"
	eventsChan <- "event 2"
	eventsChan <- "event 3"
	eventsChan <- "event 4"
	eventsChan <- "event 5"

	s.Get("/events", s.SSEHandler(eventsChan))

	// PROXY
	// Here we are proxying all routes with a prefix of `/admin` to the targetURL (optionally) prefixed with incoming
	// e.g. curl -X POST http://localhost:8080/admin/post-form-data/330 -d '{"hi": "there"}' -H 'Content-Type: application/json'
	// e.g. curl http://localhost:8080/via-proxy/usa/status
	// 		- This will proxy to http://localhost:8081/usa/proxy-incoming/status
	err := s.Proxy("/via-proxy/usa", "http://localhost:8081/proxy-incoming", 1)
	if err != nil {
		log.Fatal(err)
	}

	/*	// Enable this to proxy from root
		// You should disable the root route above if doing this
		err = s.Proxy("/", "http://localhost:8081/")
		if err != nil {
			log.Fatal(err)
		}
	*/

	log.Fatal(s.Run())
}
```

## Route Groups

Route groups allow you to organize routes with common prefixes and apply middleware to specific sets of routes:

```go
// Basic group
api := s.Group("/api")
api.Get("/users", getUsersHandler)
api.Post("/users", createUserHandler)

// Group with middleware
admin := s.Group("/admin", authMiddleware, loggerMiddleware)
admin.Get("/dashboard", dashboardHandler)
admin.Delete("/users/:id", deleteUserHandler)

// Nested groups
v1 := api.Group("/v1")
v1.Get("/status", statusHandler)  // Available at /api/v1/status

// Add middleware after group creation
v2 := api.Group("/v2")
v2.Use(rateLimiterMiddleware)
v2.Get("/users", v2UsersHandler)
```

Groups support all HTTP methods (`Get`, `Post`, `Put`, `Patch`, `Delete`, `Head`, `Options`, `Connect`, `Trace`) as well as `StaticFiles` and `Proxy`.

## Cookies

RWeb provides built-in cookie support with secure defaults and a simple API:

### Basic Usage

```go
// Set a simple session cookie (expires when browser closes)
s.Get("/set-cookie", func(ctx rweb.Context) error {
    err := ctx.SetCookie("session_id", "abc123")
    return ctx.WriteString("Cookie set!")
})

// Read a cookie value
s.Get("/get-cookie", func(ctx rweb.Context) error {
    value, err := ctx.GetCookie("session_id")
    if err != nil {
        return ctx.WriteString("No cookie found")
    }
    return ctx.WriteString("Session ID: " + value)
})

// Check if a cookie exists
s.Get("/check-cookie", func(ctx rweb.Context) error {
    if ctx.HasCookie("session_id") {
        return ctx.WriteString("You have a session")
    }
    return ctx.WriteString("No session found")
})

// Delete a cookie
s.Post("/logout", func(ctx rweb.Context) error {
    ctx.DeleteCookie("session_id")
    return ctx.WriteString("Logged out")
})

// Flash messages pattern (read once and delete)
s.Post("/save", func(ctx rweb.Context) error {
    // Set flash message
    ctx.SetCookie("flash", "Settings saved successfully!")
    return ctx.Redirect(302, "/settings")
})

s.Get("/settings", func(ctx rweb.Context) error {
    // Get and immediately clear flash message
    flash, _ := ctx.GetCookieAndClear("flash")
    return ctx.WriteHTML("<h1>Settings</h1><p>" + flash + "</p>")
})
```

### Advanced Options

```go
// Set cookie with custom options
s.Post("/remember-me", func(ctx rweb.Context) error {
    cookie := &rweb.Cookie{
        Name:     "remember_token",
        Value:    "unique_token_here",
        Path:     "/",
        Domain:   "example.com",
        Expires:  time.Now().Add(30 * 24 * time.Hour), // 30 days
        MaxAge:   30 * 24 * 60 * 60,                    // 30 days in seconds
        Secure:   true,                                  // HTTPS only
        HttpOnly: true,                                  // No JavaScript access
        SameSite: rweb.SameSiteStrictMode,              // CSRF protection
    }
    return ctx.SetCookieWithOptions(cookie)
})

// Configure server-wide cookie defaults
s := rweb.NewServer(rweb.ServerOptions{
    Address: ":8080",
    Cookie: rweb.CookieConfig{
        HttpOnly: true,                  // Default for all cookies
        SameSite: rweb.SameSiteLaxMode, // Default CSRF protection
        Path:     "/",                   // Default path
        Secure:   true,                  // Force HTTPS (auto-detected with TLS)
    },
})
```

### Cookie Security

By default, RWeb cookies are secure:
- `HttpOnly: true` - Prevents JavaScript access (XSS protection)
- `SameSite: Lax` - CSRF protection
- `Secure: true` - Automatically enabled when using TLS
- `Path: "/"` - Available site-wide by default

### Complete Example

For a comprehensive example including session management, login/logout, flash messages, and remember me functionality, see [examples/cookies/main.go](examples/cookies/main.go).



## Benchmarks

Benchmarks have not been updated.
TODO: need to re-run these...

![wrk Benchmark](https://i.imgur.com/6cDeZVA.png)

## License

Please see the [license documentation](https://akyoto.dev/license).

## Copyright

© 2025 Rohan Allison
