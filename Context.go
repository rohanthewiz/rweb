package rweb

import (
	"errors"
)

// Context is the interface for a request and its response.
// It provides a unified API for handling HTTP requests and responses,
// and is the central abstraction in the rweb framework.
// Every handler function receives a Context which contains all the
// information about the current request and provides methods to
// construct the response.
type Context interface {
	// Bytes writes raw bytes to the response body.
	// This is useful for binary data like images or files.
	Bytes([]byte) error

	// Error combines multiple error messages into a single error.
	// Accepts both error types and strings, converting strings to errors.
	Error(...any) error

	// Next calls the next handler in the middleware chain.
	// This is how middleware passes control to subsequent handlers.
	Next() error

	// Redirect sends an HTTP redirect response to the client.
	// Common status codes: 301 (permanent), 302 (temporary), 303 (see other).
	Redirect(int, string) error

	// Request returns the HTTP request object for accessing
	// request data like headers, parameters, and body.
	Request() ItfRequest

	// Response returns the HTTP response object for setting
	// response headers and other low-level operations.
	Response() Response

	// Status sets the HTTP status code and returns the context
	// for method chaining (e.g., ctx.Status(404).WriteString("Not Found")).
	Status(int) Context

	// Server returns the server instance, useful for accessing
	// server-wide configuration or state.
	Server() *Server

	// WriteString writes a plain string to the response body.
	// No content-type header is set automatically.
	WriteString(string) error

	// WriteError writes an error message with a specific HTTP status code.
	// This is a convenience method for error responses.
	WriteError(error, int) error

	// WriteJSON serializes the given value to JSON and writes it
	// to the response with appropriate content-type header.
	WriteJSON(interface{}) error

	// WriteHTML writes HTML content to the response with
	// the text/html content-type header.
	WriteHTML(string) error

	// WriteText writes plain text to the response with
	// the text/plain content-type header.
	WriteText(string) error

	// SetSSE configures Server-Sent Events for real-time data streaming.
	// Takes a channel for events and an event name for the SSE protocol.
	SetSSE(<-chan any, string) error

	// Custom data storage methods for request-scoped data.
	// Useful for authentication state, user info, or passing data between middleware.

	// Get retrieves a value by key from request-scoped storage.
	// Returns nil if the key doesn't exist.
	Get(key string) any

	// Set stores a key-value pair in request-scoped storage.
	// The storage is lazily initialized on first use.
	Set(key string, value any)

	// Has checks if a key exists in request-scoped storage.
	Has(key string) bool

	// Delete removes a key-value pair from request-scoped storage.
	Delete(key string)
}

// context is the concrete implementation of the Context interface.
// It embeds both request and response structs to inherit their methods
// and adds additional fields for middleware chain management,
// Server-Sent Events, and request-scoped data storage.
type context struct {
	// Embedded request struct provides all request-related functionality
	request
	// Embedded response struct provides all response-related functionality
	response
	// Reference to the server instance for accessing global state
	server *Server
	// Current position in the middleware chain (used by Next())
	handlerIndex uint8
	// Channel for Server-Sent Events data streaming
	sseEventsChan <-chan any
	// Event name used in SSE protocol (e.g., "message", "update")
	sseEventName string
	// Request-scoped key-value storage for passing data between handlers
	data map[string]any
}

// Clean resets the context for reuse in the next request.
// This is called between requests to avoid allocating new context objects.
// It clears all request/response data while preserving the underlying
// slice capacities for performance.
func (ctx *context) Clean() {
	// Reset slices to zero length but keep capacity for reuse
	ctx.request.headers = ctx.request.headers[:0]
	ctx.request.body = ctx.request.body[:0]
	ctx.response.headers = ctx.response.headers[:0]
	ctx.response.body = ctx.response.body[:0]
	ctx.params = ctx.params[:0]

	// Reset request state flags
	ctx.parsedPostArgs = false

	// Reset middleware chain position
	ctx.handlerIndex = 0

	// Reset to default HTTP status
	ctx.status = 200

	// Cleanup any multipart form data (releases file handles)
	ctx.request.CleanupMultipartForm()

	// Clear custom data map but keep it allocated if it exists
	if ctx.data != nil {
		ctx.data = make(map[string]any)
	}
}

// SetSSE configures the context for Server-Sent Events streaming.
// It stores the event channel and name, then sets appropriate HTTP headers
// for SSE (Content-Type: text/event-stream, Cache-Control: no-cache, etc.).
func (ctx *context) SetSSE(ch <-chan any, eventName string) error {
	ctx.sseEventsChan = ch
	ctx.sseEventName = eventName
	// SetSSEHeaders() sets Content-Type, Cache-Control, and Connection headers
	ctx.SetSSEHeaders()
	return nil
}

// Server returns the server instance associated with this context.
// This allows handlers to access server-wide configuration,
// such as debug settings or shared resources.
func (ctx *context) Server() *Server {
	return ctx.server
}

// Bytes adds the raw byte slice to the response body.
// This is the low-level method used by other write methods.
// The bytes are appended to any existing response body content.
func (ctx *context) Bytes(body []byte) error {
	ctx.response.body = append(ctx.response.body, body...)
	return nil
}

// Error provides a convenient way to wrap multiple errors.
// It accepts both error values and strings, converting strings to errors.
// All errors are combined using errors.Join (Go 1.20+).
// Example: ctx.Error(err1, "additional context", err2)
func (ctx *context) Error(messages ...any) error {
	var combined []error

	// Convert each message to an error
	for _, msg := range messages {
		switch err := msg.(type) {
		case error:
			// Already an error, add directly
			combined = append(combined, err)
		case string:
			// Convert string to error
			combined = append(combined, errors.New(err))
		}
	}

	// Combine all errors into a single error value
	return errors.Join(combined...)
}

// Next executes the next handler in the middleware chain.
// Middleware functions call this to pass control to the next handler.
// The handler chain includes both middleware and the final route handler.
// Returns any error from the executed handler.
func (ctx *context) Next() error {
	// Move to next handler in the chain
	ctx.handlerIndex++
	// Execute the handler at the current index
	return ctx.server.handlers[ctx.handlerIndex](ctx)
}

// Redirect redirects the client to a different location
// with the specified status code.
func (ctx *context) Redirect(status int, location string) error {
	ctx.response.SetStatus(status)
	ctx.response.SetHeader("Location", location)
	return nil
}

// Request returns the HTTP request.
func (ctx *context) Request() ItfRequest {
	return &ctx.request
}

// Response returns the HTTP response.
func (ctx *context) Response() Response {
	return &ctx.response
}

// Status sets the HTTP status of the response
// and returns the context for method chaining.
func (ctx *context) Status(status int) Context {
	ctx.response.SetStatus(status)
	return ctx
}

// WriteString adds the given string to the response body.
// Unlike WriteText, this doesn't set any Content-Type header,
// allowing you to set custom headers before writing.
// The string is appended to any existing response body content.
func (ctx *context) WriteString(body string) error {
	ctx.response.body = append(ctx.response.body, body...)
	return nil
}

// WriteError is a convenience method for sending error responses.
// It sets the HTTP status code and writes the error message as the response body.
// Common usage: ctx.WriteError(errors.New("Not Found"), 404)
func (ctx *context) WriteError(err error, code int) error {
	ctx.response.SetStatus(code)
	_, er := ctx.response.WriteString(err.Error())
	return er
}

// WriteJSON serializes the given value to JSON and writes it to the response.
// It automatically sets the Content-Type header to "application/json".
// Returns an error if JSON marshaling fails.
func (ctx *context) WriteJSON(body interface{}) error {
	_, er := ctx.response.WriteJSON(body)
	return er
}

// WriteHTML writes HTML content to the response.
// It automatically sets the Content-Type header to "text/html; charset=utf-8".
// Use this for returning rendered HTML pages.
func (ctx *context) WriteHTML(body string) error {
	_, er := ctx.response.WriteHTML(body)
	return er
}

// WriteText writes plain text to the response.
// It automatically sets the Content-Type header to "text/plain; charset=utf-8".
// Use this for returning simple text responses.
func (ctx *context) WriteText(body string) error {
	_, er := ctx.response.WriteText(body)
	return er
}

// Get retrieves a value from the context's custom data storage.
// Returns nil if the key doesn't exist or if no data has been set.
// Common usage: userId := ctx.Get("userId").(string)
// Always type-assert the result since it returns any.
func (ctx *context) Get(key string) any {
	if ctx.data == nil {
		return nil
	}
	return ctx.data[key]
}

// Set stores a value in the context's custom data storage.
// The storage is lazily initialized on first use to save memory.
// Common usage: ctx.Set("userId", "123") or ctx.Set("isAdmin", true)
// Data persists for the lifetime of the request.
func (ctx *context) Set(key string, value any) {
	// Lazy initialization of data map
	if ctx.data == nil {
		ctx.data = make(map[string]any)
	}
	ctx.data[key] = value
}

// Has checks if a key exists in the context's custom data storage.
// Returns false if the data map hasn't been initialized.
// Useful for checking optional values: if ctx.Has("userId") { ... }
func (ctx *context) Has(key string) bool {
	if ctx.data == nil {
		return false
	}
	_, exists := ctx.data[key]
	return exists
}

// Delete removes a key-value pair from the context's custom data storage.
// Safe to call even if the key doesn't exist or data map is nil.
// Use this to clean up sensitive data before passing context to untrusted code.
func (ctx *context) Delete(key string) {
	if ctx.data != nil {
		delete(ctx.data, key)
	}
}
