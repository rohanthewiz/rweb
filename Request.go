package rweb

import (
	"bufio"

	"github.com/rohanthewiz/rweb/core/rtr"
)

// Request is an interface for HTTP requests.
type Request interface {
	Header(string) string
	Host() string
	Method() string
	Path() string
	Scheme() string
	Param(string) string
}

// request represents the HTTP request used in the given context.
type request struct {
	reader  *bufio.Reader
	scheme  string
	host    string
	method  string
	path    string
	query   string
	headers []Header
	body    []byte
	params  []rtr.Parameter
}

// Header returns the header value for the given key.
func (req *request) Header(key string) string {
	for _, header := range req.headers {
		if header.Key == key {
			return header.Value
		}
	}

	return ""
}

// Host returns the requested host.
func (req *request) Host() string {
	return req.host
}

// Method returns the request method.
func (req *request) Method() string {
	return req.method
}

// Param retrieves a parameter.
func (req *request) Param(name string) string {
	for i := range len(req.params) {
		p := req.params[i]

		if p.Key == name {
			return p.Value
		}
	}

	return ""
}

// Path returns the requested path.
func (req *request) Path() string {
	return req.path
}

// Scheme returns either `http`, `https` or an empty string.
func (req request) Scheme() string {
	return req.scheme
}

// addParameter adds a new parameter to the request.
func (req *request) addParameter(key string, value string) {
	req.params = append(req.params, rtr.Parameter{
		Key:   key,
		Value: value,
	})
}
