package rweb

import (
	"errors"
)

// Context is the interface for a request and its response.
type Context interface {
	Bytes([]byte) error
	Error(...any) error
	Next() error
	Redirect(int, string) error
	Request() ItfRequest
	Response() Response
	Status(int) Context
	Server() *Server
	WriteString(string) error
	WriteError(error, int) error
	WriteJSON(interface{}) error
	WriteHTML(string) error
	WriteText(string) error
	SetSSE(<-chan any, string) error
	// Custom data storage
	Get(key string) any
	Set(key string, value any)
	Has(key string) bool
	Delete(key string)
}

// context contains the request and response data.
type context struct {
	request
	response
	server        *Server
	handlerIndex  uint8
	sseEventsChan <-chan any // channel for SSE events
	sseEventName  string
	data          map[string]any // custom data storage
}

func (ctx *context) Clean() {
	ctx.request.headers = ctx.request.headers[:0]
	ctx.request.body = ctx.request.body[:0]
	ctx.response.headers = ctx.response.headers[:0]
	ctx.response.body = ctx.response.body[:0]
	ctx.params = ctx.params[:0]
	ctx.parsedPostArgs = false
	ctx.handlerIndex = 0
	ctx.status = 200
	// Cleanup any multipart form data
	ctx.request.CleanupMultipartForm()
	// Clear custom data
	if ctx.data != nil {
		ctx.data = make(map[string]any)
	}
}

func (ctx *context) SetSSE(ch <-chan any, eventName string) error {
	ctx.sseEventsChan = ch
	ctx.sseEventName = eventName
	ctx.SetSSEHeaders()
	return nil
}

func (ctx *context) Server() *Server {
	return ctx.server
}

// Bytes adds the raw byte slice to the response body.
func (ctx *context) Bytes(body []byte) error {
	ctx.response.body = append(ctx.response.body, body...)
	return nil
}

// Error provides a convenient way to wrap multiple errors.
func (ctx *context) Error(messages ...any) error {
	var combined []error

	for _, msg := range messages {
		switch err := msg.(type) {
		case error:
			combined = append(combined, err)
		case string:
			combined = append(combined, errors.New(err))
		}
	}

	return errors.Join(combined...)
}

// Next executes the next handler in the middleware chain.
func (ctx *context) Next() error {
	ctx.handlerIndex++
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

// String adds the given string to the response body.
func (ctx *context) WriteString(body string) error {
	ctx.response.body = append(ctx.response.body, body...)
	return nil
}

func (ctx *context) WriteError(err error, code int) error {
	ctx.response.SetStatus(code)
	_, er := ctx.response.WriteString(err.Error())
	return er
}

func (ctx *context) WriteJSON(body interface{}) error {
	_, er := ctx.response.WriteJSON(body)
	return er
}

func (ctx *context) WriteHTML(body string) error {
	_, er := ctx.response.WriteHTML(body)
	return er
}

func (ctx *context) WriteText(body string) error {
	_, er := ctx.response.WriteText(body)
	return er
}

// Get retrieves a value from the context's custom data storage.
func (ctx *context) Get(key string) any {
	if ctx.data == nil {
		return nil
	}
	return ctx.data[key]
}

// Set stores a value in the context's custom data storage.
func (ctx *context) Set(key string, value any) {
	if ctx.data == nil {
		ctx.data = make(map[string]any)
	}
	ctx.data[key] = value
}

// Has checks if a key exists in the context's custom data storage.
func (ctx *context) Has(key string) bool {
	if ctx.data == nil {
		return false
	}
	_, exists := ctx.data[key]
	return exists
}

// Delete removes a key from the context's custom data storage.
func (ctx *context) Delete(key string) {
	if ctx.data != nil {
		delete(ctx.data, key)
	}
}
