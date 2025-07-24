// Package main demonstrates a proxy server example using the rweb framework.
// This example shows how to set up middleware, handle various HTTP routes,
// and serve static files. It's designed to work as a backend server that could
// be proxied to by another server.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rohanthewiz/rweb"
)

func main() {
	// Initialize a new rweb server with custom configuration.
	// This server is configured to listen on port 8081 to avoid conflicts
	// with other examples that might be running on default ports.
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8081", // Port-only format is Docker-friendly (avoids localhost binding issues)
		Verbose: true,    // Enable verbose logging for debugging
		Debug:   false,   // Disable debug mode in this example
		// TLS configuration is commented out but shows how to enable HTTPS
		// TLS: rweb.TLSCfg{
		// 	UseTLS:   false,
		// 	KeyFile:  "certs/localhost.key",
		// 	CertFile: "certs/localhost.crt",
		// },
	})

	// Request timing middleware
	// This middleware tracks the duration of each request and logs it after completion.
	// The defer ensures the timing is captured even if an error occurs downstream.
	s.Use(func(ctx rweb.Context) error {
		start := time.Now()

		// This deferred function runs after the request is processed,
		// capturing the final response status and request duration
		defer func() {
			fmt.Printf("%s %q -> %d [%s]\n", ctx.Request().Method(), ctx.Request().Path(), ctx.Response().Status(), time.Since(start))
		}()

		// Pass control to the next middleware or handler in the chain
		return ctx.Next()
	})

	// Request logging middleware
	// This logs incoming requests before they're processed.
	// Running before the timing middleware, it provides immediate visibility of incoming traffic.
	s.Use(func(ctx rweb.Context) error {
		fmt.Printf("%s - %s\n", ctx.Request().Method(), ctx.Request().Path())
		return ctx.Next()
	})

	// Root endpoint - provides a simple welcome message
	// This could serve as a health check or landing page
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Welcome to the Proxy Server Example\n")
	})

	// Example HTML endpoint under /usa path prefix
	// This demonstrates how you might organize routes by region or feature area
	s.Get("/usa/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome The Proxy Incoming home!</h1>")
	})

	// JSON API endpoint demonstrating structured data responses
	// This is useful for building RESTful APIs that return JSON data
	s.Get("/usa/proxy-incoming/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"message": "Everything's good",
			"status":  "success",
		}
		return ctx.WriteJSON(data)
	})

	// POST endpoint with URL parameters
	// The :form_id syntax creates a route parameter that can be accessed via ctx.Request().PathParam()
	// This endpoint also demonstrates accessing the raw request body
	s.Post("/eur/proxy-incoming/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.WriteString("Posted to Proxy Incoming - form_id: " + ctx.Request().PathParam("form_id") +
			"\n" + string(ctx.Request().Body()))
	})

	// Static file serving example
	// This shows how to serve individual files, though s.StaticFiles() is preferred
	// for serving entire directories of static assets
	s.Get("/static/my.css", func(ctx rweb.Context) error {
		// Read the CSS file from the local filesystem
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err // rweb will handle the error and return appropriate HTTP status
		}
		// Serve the file with a custom filename in the response
		// The rweb.File helper sets appropriate Content-Type and Content-Disposition headers
		return rweb.File(ctx, "the.css", body)
	})

	// Start the server and log any fatal errors
	// The server will block here and handle incoming requests
	log.Fatal(s.Run())
}
