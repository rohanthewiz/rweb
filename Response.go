package rweb

import (
	"encoding/json"
	"io"
	"log"
	"os"
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

	// bodyFile, when set, is the tail of the body still on disk: the
	// response body is body followed by the first bodyFileSize bytes of
	// bodyFile. writeResponse copies it straight to the connection
	// (sendfile where the OS and conn type allow), so a static file is
	// never held in memory just to be sent.
	//
	// The file is pending, not hidden: anything that reads or changes the
	// body through this type (Body, Write, SetBody, ctx.Bytes, ...) first
	// pulls it into body with materializeBody, so a middleware that
	// rewrites the response after Next() sees the same bytes it always did
	// and only that request pays for the read. Whoever clears the field
	// closes the file; Clean is the backstop.
	bodyFile     *os.File
	bodyFileSize int64
}

// Body returns the response body.
func (res *response) Body() []byte {
	res.materializeBody()
	return res.body
}

// setBodyFile makes the first size bytes of f the tail of the body, to be
// streamed when the response is written. The response owns f from here on.
// A file already pending is closed and replaced, as SetBody would replace
// an in-memory body.
func (res *response) setBodyFile(f *os.File, size int64) {
	res.closeBodyFile()
	res.bodyFile = f
	res.bodyFileSize = size
}

// materializeBody reads a pending body file into body, after whatever body
// already holds, and releases the file. It is a nil check when nothing is
// pending, so every body accessor can call it unconditionally.
//
// If the file comes up short (it shrank after it was opened) body keeps
// what was read, so Content-Length still matches the bytes sent. A read
// error is logged rather than returned because Body has no error result.
func (res *response) materializeBody() {
	if res.bodyFile == nil {
		return
	}
	f, size := res.bodyFile, res.bodyFileSize
	res.bodyFile, res.bodyFileSize = nil, 0
	defer f.Close()

	n := len(res.body)
	res.body = slices.Grow(res.body, int(size))[:n+int(size)]
	read, err := io.ReadFull(f, res.body[n:])
	res.body = res.body[:n+read]
	if err != nil {
		log.Printf("rweb: reading response body file %s: %v", f.Name(), err)
	}
}

// closeBodyFile drops a pending body file without reading it.
func (res *response) closeBodyFile() {
	if res.bodyFile != nil {
		_ = res.bodyFile.Close()
		res.bodyFile, res.bodyFileSize = nil, 0
	}
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
// A pending body file is part of the body being replaced, so it is dropped.
func (res *response) SetBody(body []byte) {
	res.closeBodyFile()
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
	res.materializeBody() // appended bytes go after a pending file, not before it
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
