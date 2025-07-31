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

// writeResponse writes the given body and sets the content type header
func (res *response) writeResponse(body string, contentType string) (int, error) {
	res.SetHeader(consts.HeaderContentType, contentType)
	return res.WriteString(body)
}

func (res *response) SetSSEHeaders() {
	res.SetHeader(consts.HeaderContentType, consts.MIMETextEventStream+"; charset=utf-8")
	res.SetHeader(consts.HeaderCacheControl, consts.HeaderNoCache)
	res.SetHeader(consts.HeaderConnection, consts.HeaderKeepAlive)
	res.SetHeader(consts.HeaderAccessControlAllowOrigin, "*")
}
