package rweb

import (
	"io"
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
func (res *response) Header(key string) string {
	for _, header := range res.headers {
		if header.Key == key {
			return header.Value
		}
	}

	return ""
}

// SetHeader sets the header value for the given key.
func (res *response) SetHeader(key string, value string) {
	for i, header := range res.headers {
		if header.Key == key {
			res.headers[i].Value = value
			return
		}
	}

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
