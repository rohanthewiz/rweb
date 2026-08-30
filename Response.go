package rweb

import (
	"encoding/json"
	"io"

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
}

// Body returns the response body.
func (res *response) Body() []byte {
	return res.body
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
func (res *response) SetBody(body []byte) {
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
	res.body = append(res.body, body...)
	return len(body), nil
}

// WriteString implements the io.StringWriter interface.
func (res *response) WriteString(body string) (int, error) {
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
