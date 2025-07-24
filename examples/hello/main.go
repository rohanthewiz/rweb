// Package main demonstrates the usage of rweb, a lightweight HTTP web server framework for Go.
// This example showcases various features including routing, middleware, static files, file uploads,
// Server-Sent Events (SSE), and reverse proxy capabilities.
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
	// Create a new rweb server instance with configuration options
	s := rweb.NewServer(rweb.ServerOptions{
		// Listen on port 8080 on all network interfaces
		// Use ":8080" format (not "localhost:8080") for Docker compatibility
		Address: ":8080",

		// Enable verbose logging to see detailed request/response information
		Verbose: true,

		// Debug mode is disabled (would show additional debugging information)
		Debug: false,

		// TLS configuration (currently disabled, but shows how to configure HTTPS)
		TLS: rweb.TLSCfg{
			UseTLS:   false,                 // Set to true to enable HTTPS
			KeyFile:  "certs/localhost.key", // Path to TLS private key
			CertFile: "certs/localhost.crt", // Path to TLS certificate
		},
	})

	// Middleware 1: Request logging middleware
	// This middleware logs each request's method, path, response status, and duration
	// Middleware in rweb is executed in the order it's registered
	s.Use(func(ctx rweb.Context) error {
		// Record the start time of the request
		start := time.Now()

		// defer ensures this runs after the request is handled
		// This allows us to capture the final response status and calculate duration
		defer func() {
			// Log format: GET "/path" -> 200 [150ms]
			fmt.Printf("%s %q -> %d [%s]\n",
				ctx.Request().Method(),  // HTTP method (GET, POST, etc.)
				ctx.Request().Path(),    // Request path
				ctx.Response().Status(), // Response status code
				time.Since(start))       // Request duration
		}()

		// Call ctx.Next() to pass control to the next middleware or handler
		// This is crucial - without it, the request chain stops here
		return ctx.Next()
	})

	// Middleware 2: Simple demonstration middleware
	// Shows that multiple middleware can be chained together
	s.Use(func(ctx rweb.Context) error {
		fmt.Println("In Middleware 2")
		// Always call ctx.Next() unless you want to stop the request chain
		return ctx.Next()
	})

	// Route: Root endpoint
	// Handles GET requests to "/"
	// Test with: curl http://localhost:8080/
	s.Get("/", func(ctx rweb.Context) error {
		// WriteString sends a plain text response
		return ctx.WriteString("Welcome\n")
	})

	// Route parameters demonstration
	// The radix tree router correctly distinguishes between parameterized and static routes
	// Test with: curl http://localhost:8080/greet/John
	s.Get("/greet/:name", func(ctx rweb.Context) error {
		// Access route parameters using ctx.Request().Param("paramName")
		// The :name parameter captures any value in that URL segment
		return ctx.WriteString("Hello " + ctx.Request().PathParam("name"))
	})

	// Static route takes precedence over parameterized route when exact match
	// Test with: curl http://localhost:8080/greet/city
	s.Get("/greet/city", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi big city!")
	})

	// Deep nested routes are handled efficiently by the radix tree router
	// Test with: curl http://localhost:8080/long/long/long/url/something
	s.Get("/long/long/long/url/:thing", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello " + ctx.Request().PathParam("thing"))
	})

	// Again, static routes have priority over parameterized ones
	// Test with: curl http://localhost:8080/long/long/long/url/otherthing
	s.Get("/long/long/long/url/otherthing", func(ctx rweb.Context) error {
		return ctx.WriteString("Hey other thing!")
	})

	// HTML response example
	// WriteHTML automatically sets Content-Type: text/html
	// Test with: curl http://localhost:8080/home
	s.Get("/home", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome home</h1>")
	})

	// JSON response example
	// WriteJSON automatically marshals Go data structures to JSON
	// and sets Content-Type: application/json
	// Test with: curl http://localhost:8080/some-json
	s.Get("/some-json", func(ctx rweb.Context) error {
		// Any Go data structure that can be marshaled to JSON
		data := map[string]string{
			"message": "Hello, World!",
			"status":  "success",
		}
		return ctx.WriteJSON(data)
	})

	// CSS response example
	// rweb.CSS is a helper that sets Content-Type: text/css
	// Test with: curl http://localhost:8080/css
	s.Get("/css", func(ctx rweb.Context) error {
		return rweb.CSS(ctx, "body{}")
	})

	// POST request with route parameter
	// Demonstrates that route parameters work with all HTTP methods
	// Test with: curl -X POST http://localhost:8080/post-form-data/123
	s.Post("/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.WriteString("Posted - form_id: " + ctx.Request().PathParam("form_id"))
	})

	// Manual file serving example (not recommended for multiple files)
	// This shows how to serve a single file manually
	// For serving multiple static files, use s.StaticFiles() instead (shown below)
	s.Get("/static/my.css", func(ctx rweb.Context) error {
		// Read the file from disk
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err
		}
		// rweb.File sends the file with appropriate headers
		// The second parameter is the filename sent to the client
		return rweb.File(ctx, "the.css", body)
	})

	// Static file serving - maps URL prefixes to local directories
	// StaticFiles(urlPrefix, localPath, stripPrefixSegments)
	// stripPrefixSegments: number of URL path segments to strip before mapping to local path

	// Example 1: Serve images
	// URL: http://localhost:8080/static/images/laptop.png
	// Maps to: /assets/images/laptop.png (strips 2 segments: "static" and "images")
	s.StaticFiles("static/images/", "/assets/images", 2)

	// Example 2: Serve CSS files
	// URL: http://localhost:8080/css/my.css
	// Maps to: assets/css/my.css (strips 1 segment: "css")
	s.StaticFiles("/css/", "assets/css", 1)

	// Example 3: Serve .well-known files (for SSL certificates, etc.)
	// URL: http://localhost:8080/.well-known/some-file.txt
	// Maps to: /some-file.txt (strips 0 segments, keeps full path)
	s.StaticFiles("/.well-known/", "/", 0)

	// File upload handler
	// Handles multipart/form-data POST requests
	// Test with: curl -X POST -F "vehicle=car" -F "file=@somefile.txt" http://localhost:8080/upload
	s.Post("/upload", func(c rweb.Context) error {
		req := c.Request()

		// Extract regular form fields from the multipart form
		name := req.FormValue("vehicle")
		fmt.Println("vehicle:", name)

		// Get the uploaded file
		// GetFormFile returns: file handle, file header (with metadata), error
		// We're ignoring the file header (second return value) here
		file, _, err := req.GetFormFile("file")
		if err != nil {
			return err
		}
		// Always close the file when done
		defer file.Close()

		// Read the entire file content into memory
		// For large files, consider streaming to disk instead
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}

		// Save the uploaded file to disk
		// 0666 permissions: read/write for owner, group, and others
		err = os.WriteFile("uploaded_file.txt", data, 0666)
		if err != nil {
			return err
		}

		// Return nil indicates successful handling
		return nil
	})

	// Server-Sent Events (SSE) example
	// SSE allows the server to push real-time updates to the client
	// Unlike WebSockets, SSE is unidirectional (server to client only)
	// Test with: curl http://localhost:8080/events
	// Or in browser: new EventSource('http://localhost:8080/events')

	// Create a buffered channel to hold some events
	eventsChan := make(chan any, 4)

	// Use SetupSSE (recommended) as you can receive multiple event types
	s.Get("/events", func(c rweb.Context) error {
		return s.SetupSSE(c, eventsChan)
	})

	// Populate some events for demonstration
	eventsChan <- "event 1"
	eventsChan <- "event 2"
	eventsChan <- "event 3"
	eventsChan <- "event 4"

	eventsChan2 := make(chan any, 4)

	// SSEHandler is a convenience method for creating a handler that streams events from the channel
	s.Get("/events2", s.SSEHandler(eventsChan2))

	// Populate some events for demonstration
	eventsChan2 <- "event2 1"
	eventsChan2 <- "event2 2"
	eventsChan2 <- "event2 3"
	eventsChan2 <- "event2 4"

	// Reverse proxy configuration
	// Forwards requests matching a URL prefix to another server
	// Useful for microservices, load balancing, or API gateways

	// Proxy(urlPrefix, targetURL, stripPrefixSegments)
	// - urlPrefix: incoming URL pattern to match
	// - targetURL: where to forward the requests
	// - stripPrefixSegments: number of path segments to remove from incoming URL

	// Example: Incoming /via-proxy/usa/status
	// With stripPrefixSegments=1, strips "/via-proxy"
	// Forwards to: http://localhost:8081/proxy-incoming/usa/status
	// Test with: curl http://localhost:8080/via-proxy/usa/status
	err := s.Proxy("/via-proxy/usa", "http://localhost:8081/proxy-incoming", 1)
	if err != nil {
		log.Fatal(err)
	}

	// Example: Proxy all requests from root
	// This would forward ALL requests to another server
	// WARNING: If enabled, disable the root GET handler above to avoid conflicts
	/*
		err = s.Proxy("/", "http://localhost:8081/")
		if err != nil {
			log.Fatal(err)
		}
	*/

	// Start the HTTP server
	// This blocks until the server is shut down
	// log.Fatal ensures any startup errors are logged before exiting
	log.Fatal(s.Run())
}
