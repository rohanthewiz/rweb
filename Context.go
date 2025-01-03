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
	Request() Request
	Response() Response
	Status(int) Context
	String(string) error
}

// context contains the request and response data.
type context struct {
	request
	response
	server       *server
	handlerCount uint8
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
	ctx.handlerCount++
	return ctx.server.handlers[ctx.handlerCount](ctx)
}

// Redirect redirects the client to a different location
// with the specified status code.
func (ctx *context) Redirect(status int, location string) error {
	ctx.response.SetStatus(status)
	ctx.response.SetHeader("Location", location)
	return nil
}

// Request returns the HTTP request.
func (ctx *context) Request() Request {
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
func (ctx *context) String(body string) error {
	ctx.response.body = append(ctx.response.body, body...)
	return nil
}
