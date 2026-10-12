package rweb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"slices"

	"github.com/rohanthewiz/rweb/consts"
)

// Response is the interface for an HTTP response.
type Response interface {
	io.Writer
	io.StringWriter
	Body() []byte
	Header(string) string
	SetHeader(key string, value string)
	// AddHeader appends a header without replacing existing values for the
	// same key — required for headers that legally repeat, like Set-Cookie.
	AddHeader(key string, value string)
	SetBody([]byte)
	SetStatus(int)
	Status() int
}

// response represents the HTTP response used in the given context.
type response struct {
	body    []byte
	headers []Header
	status  uint16

	// bodyStream, when set, is the tail of the body not yet read: the
	// response body is body followed by bodyStream, which is either exactly
	// bodyStreamSize bytes or, when bodyStreamSize is negative, everything
	// up to EOF. writeResponse copies it straight to the connection instead
	// of the handler reading it into memory first, so a large file or a
	// generated download (a database dump, an export) costs a copy buffer,
	// not its own size.
	//
	//	known size (>= 0)  Content-Length: len(body)+size, then body, then
	//	                   the stream. An *os.File goes out by sendfile(2)
	//	                   on plain TCP.
	//	unknown size (< 0) Transfer-Encoding: chunked, body and stream sent
	//	                   as chunks, ended by the zero-length chunk.
	//
	// The stream is pending, not hidden: anything that reads or changes the
	// body through this type (Body, Write, SetBody, ctx.Bytes, ...) first
	// pulls it into body with materializeBody, so a middleware that
	// rewrites the response after Next() sees the same bytes it always did
	// and only that request pays for the read. Whoever clears the field
	// closes the stream (when it is an io.Closer); Clean is the backstop.
	bodyStream     io.Reader
	bodyStreamSize int64
}

// Body returns the response body.
func (res *response) Body() []byte {
	res.materializeBody()
	return res.body
}

// setBodyStream makes r the tail of the body, to be streamed when the
// response is written: exactly size bytes of it, or all of it up to EOF
// when size is negative. The response owns r from here on and closes it
// if it is an io.Closer. A stream already pending is closed and replaced,
// as SetBody would replace an in-memory body.
func (res *response) setBodyStream(r io.Reader, size int64) {
	res.closeBodyStream()
	if size < 0 {
		size = -1 // one spelling of "unknown", so checks can compare to it
	}
	res.bodyStream = r
	res.bodyStreamSize = size
}

// bodyChunked reports whether the response goes out with chunked transfer
// encoding: only when a stream of unknown length is still pending.
func (res *response) bodyChunked() bool {
	return res.bodyStream != nil && res.bodyStreamSize < 0
}

// materializeBody reads a pending body stream into body, after whatever
// body already holds, and releases the stream. It is a nil check when
// nothing is pending, so every body accessor can call it unconditionally.
//
// If a sized stream comes up short (a file that shrank after it was
// opened) body keeps what was read, so Content-Length still matches the
// bytes sent. A read error is logged rather than returned because Body has
// no error result.
func (res *response) materializeBody() {
	if res.bodyStream == nil {
		return
	}
	r, size := res.bodyStream, res.bodyStreamSize
	res.bodyStream, res.bodyStreamSize = nil, 0
	defer closeStream(r)

	var err error
	if size < 0 {
		// Unknown length: grow as needed. bytes.Buffer starts with body's
		// bytes as its unread content and ReadFrom appends after them.
		buf := bytes.NewBuffer(res.body)
		_, err = buf.ReadFrom(r)
		res.body = buf.Bytes()
	} else {
		// Known length: one allocation, and ReadFull into an exact-size
		// window never reads past size even if the source has more.
		n := len(res.body)
		res.body = slices.Grow(res.body, int(size))[:n+int(size)]
		var read int
		read, err = io.ReadFull(r, res.body[n:])
		res.body = res.body[:n+read]
	}
	if err != nil {
		log.Printf("rweb: reading response body stream %s: %v", streamName(r), err)
	}
}

// closeBodyStream drops a pending body stream without reading it.
func (res *response) closeBodyStream() {
	if res.bodyStream != nil {
		closeStream(res.bodyStream)
		res.bodyStream, res.bodyStreamSize = nil, 0
	}
}

// closeStream closes r if it can be closed. Closing is how the producer
// behind a stream learns it is no longer wanted: the writer side of an
// io.Pipe gets io.ErrClosedPipe, an *os.File releases its descriptor.
func closeStream(r io.Reader) {
	if c, ok := r.(io.Closer); ok {
		_ = c.Close()
	}
}

// streamName names a stream in log lines: the file name for an *os.File
// (or anything else with a Name method), otherwise its type.
func streamName(r io.Reader) string {
	if n, ok := r.(interface{ Name() string }); ok {
		return n.Name()
	}
	return fmt.Sprintf("%T", r)
}

// Header returns the header value for the given key.
func (res *response) Header(key string) (value string) {
	for _, header := range res.headers {
		if header.Key == key {
			return header.Value
		}
	}
	return
}

// SetHeader sets a header
func (res *response) SetHeader(key string, value string) {
	for i, header := range res.headers {
		if header.Key == key {
			res.headers[i].Value = value
			return
		}
	}
	res.headers = append(res.headers, Header{Key: key, Value: value})
}

// AddHeader adds a header (allows multiple values for the same key, like Set-Cookie)
func (res *response) AddHeader(key string, value string) {
	res.headers = append(res.headers, Header{Key: key, Value: value})
}

// SetBody replaces the response body with the new contents.
// A pending body stream is part of the body being replaced, so it is dropped.
func (res *response) SetBody(body []byte) {
	res.closeBodyStream()
	res.body = body
}

// SetStatus sets the HTTP status code.
func (res *response) SetStatus(status int) {
	res.status = uint16(status)
}

// Status returns the HTTP status code.
func (res *response) Status() int {
	return int(res.status)
}

// Write implements the io.Writer interface.
func (res *response) Write(body []byte) (int, error) {
	res.materializeBody() // appended bytes go after a pending stream, not before it
	res.body = append(res.body, body...)
	return len(body), nil
}

// WriteString implements the io.StringWriter interface.
func (res *response) WriteString(body string) (int, error) {
	res.materializeBody()
	res.body = append(res.body, body...)
	return len(body), nil
}

// ------ Convenience functions ------

// WriteJSON writes the given JSON to the response body
// also setting the content type to application/json.
func (res *response) WriteJSON(obj any) (int, error) {
	byts, err := json.Marshal(obj)
	if err != nil {
		return 0, err
	}
	res.SetHeader(consts.HeaderContentType, consts.MIMEJSON)
	return res.Write(byts)
}

// WriteHTML writes the given HTML to the response body
// also setting the content type to text/html.
func (res *response) WriteHTML(body string) (int, error) {
	return res.writeResponse(body, consts.MIMEHTML)
}

// WriteText writes the given text to the response body
// also setting the content type to text/plain.
func (res *response) WriteText(body string) (int, error) {
	return res.writeResponse(body, consts.MIMETextPlain)
}

// WriteHTMLBytes writes the given HTML bytes to the response body
// also setting the content type to text/html.
func (res *response) WriteHTMLBytes(body []byte) (int, error) {
	return res.writeResponseBytes(body, consts.MIMEHTML)
}

// WriteTextBytes writes the given text bytes to the response body
// also setting the content type to text/plain.
func (res *response) WriteTextBytes(body []byte) (int, error) {
	return res.writeResponseBytes(body, consts.MIMETextPlain)
}

// writeResponse writes the given body and sets the content type header
func (res *response) writeResponse(body string, contentType string) (int, error) {
	res.SetHeader(consts.HeaderContentType, contentType)
	return res.WriteString(body)
}

// writeResponseBytes writes the given body bytes and sets the content type header
func (res *response) writeResponseBytes(body []byte, contentType string) (int, error) {
	res.SetHeader(consts.HeaderContentType, contentType)
	return res.Write(body)
}

// SetSSEHeaders writes the header set an event stream needs: the event-stream
// media type, no caching, a connection the proxy chain must keep open, and
// X-Accel-Buffering to stop nginx (and friends) from buffering the stream into
// silence.
//
// Deliberately absent: Content-Encoding. That header names a *content coding*
// (gzip, br, zstd) applied to the body, not a media type, and rweb never
// compresses a response, so the correct signal for "this body is not encoded"
// is to omit the header entirely — RFC 9110 §8.4 defines no token for it
// ("identity" exists only as an Accept-Encoding value).
//
// This once sent `Content-Encoding: text/plain`, which is a media type in a
// content-coding slot. Go's http client ignores the header (it only ever
// auto-decodes gzip), so Go clients saw nothing wrong — but browsers and curl
// discard a body whose coding they cannot recognise, so an SSE stream read by
// a real browser connected and then delivered nothing. Hence the header
// assertion in TestSSEHeadersCarryNoContentEncoding: no Go client behaviour
// can catch a regression here, only the header itself.
func (res *response) SetSSEHeaders() {
	res.SetHeader(consts.HeaderContentType, consts.MIMETextEventStream+"; charset=utf-8")
	res.SetHeader(consts.HeaderCacheControl, consts.HeaderNoCache)
	res.SetHeader(consts.HeaderConnection, consts.HeaderKeepAlive)
	res.SetHeader(consts.HeaderXAccelBuffering, "no")
	res.SetHeader(consts.HeaderAccessControlAllowOrigin, "*")
}
