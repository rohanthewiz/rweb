package rweb

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// Context is the interface for a request and its response.
type Context interface {
	Bytes([]byte) error
	Error(...any) error
	Next() error
	Redirect(int, string) error
	Request() IntfRequest
	Response() Response
	Status(int) Context
	Server() *Server
	WriteString(string) error
	WriteError(error, int) error
	WriteJSON(interface{}) error
	WriteHTML(string) error
	WriteText(string) error
	SetSSE(chan any)
}

// context contains the request and response data.
type context struct {
	request
	response
	server       *Server
	handlerIndex uint8
	sseEvents    chan any // channel for SSE events
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
}

func (ctx *context) SetSSE(ch chan any) {
	ctx.sseEvents = ch
	ctx.SetSSEHeaders()
}

func (ctx *context) sendSSE(respWriter io.Writer) (err error) {
	rw := bufio.NewWriter(respWriter)
	for {
		select {
		case event, ok := <-ctx.sseEvents:
			if !ok { // Channel closed and drained, clean up and exit
				_ = rw.Flush()
				return
			}

			// Format and send the event
			switch v := event.(type) {
			case string:
				_, err = fmt.Fprintf(rw, "data: %s\n\n", v)
			default:
				_, err = fmt.Fprintf(rw, "data: %+v\n\n", v)
			}

			if err != nil {
				return err
			}

			// Important: Flush the buffer to send data immediately
			if err = rw.Flush(); err != nil {
				return err
			}

			// TODO incorporate context Done()
			/*		case <-ctx.Done():
					// Context canceled, clean up
					rw.Flush()
					return
			*/
		}
		_ = rw.Flush()
		// time.Sleep(1 * time.Second) // slow it down for demo purposes
	}
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
func (ctx *context) Request() IntfRequest {
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
