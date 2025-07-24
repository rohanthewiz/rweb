package rweb

import (
	"bufio"
	"bytes"
	"fmt"
	"mime"
	"mime/multipart"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

// ItfRequest is the Request interface
type ItfRequest interface {
	// Headers returns the request headers.
	Headers() []Header
	// Header returns the header value for the given key.
	Header(string) string
	Host() string
	// Method returns the HTTP method of the request
	Method() string
	// Path  returns the request path
	Path() string
	// Query returns the whole query string.
	Query() string
	// QueryParam returns the value of a particular query string param.
	QueryParam(string) string
	Scheme() string
	// Param retrieves a Path parameter's value.
	Param(string) string
	// PathParam retrieves a Path parameter's value.
	PathParam(string) string
	// GetPostValue retrieves the value of POST param - cannot be used for non-multipart forms
	// use FormValue for multipart form values.
	GetPostValue(string) string
	// FormValue retrieves multipart form parameter values
	FormValue(string) string
	// GetFormFile returns the first file for the provided form key
	GetFormFile(string) (multipart.File, *multipart.FileHeader, error)
	Body() []byte
}

// request represents the HTTP request used in the given context.
type request struct {
	reader *bufio.Reader
	scheme string
	host   string
	method string
	path   string
	query  string

	// Header
	ContentType []byte // shortcut to content type
	headers     []Header
	body        []byte
	params      []rtr.Parameter

	multipartForm         *multipart.Form
	multipartFormBoundary string

	queryArgs       Args
	parsedQueryArgs bool

	postArgs       Args
	parsedPostArgs bool
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

// Headers returns the request headers.
func (req *request) Headers() []Header {
	return req.headers
}

// Host returns the requested host.
func (req *request) Host() string {
	return req.host
}

// Method returns the request method.
func (req *request) Method() string {
	return req.method
}

// Param retrieves a Path parameter's value.
func (req *request) Param(name string) (value string) {
	for i := range len(req.params) {
		if req.params[i].Key == name {
			return req.params[i].Value
		}
	}
	return
}

// PathParam retrieves a Path parameter's value.
func (req *request) PathParam(name string) (value string) {
	for i := range len(req.params) {
		if req.params[i].Key == name {
			return req.params[i].Value
		}
	}
	return
}

// Path returns the request path.
func (req *request) Path() string {
	return req.path
}

// Query returns the query string.
func (req *request) Query() string {
	return req.query
}

// QueryParam returns the value of a particular query param.
func (req *request) QueryParam(param string) (value string) {
	var args Args
	args.Parse(req.query)
	return b2s(args.Peek(param))
}

// Scheme returns either `http`, `https` or an empty string.
func (req *request) Scheme() string {
	return req.scheme
}

// addParameter adds a new parameter to the request.
func (req *request) addParameter(key string, value string) {
	req.params = append(req.params, rtr.Parameter{
		Key:   key,
		Value: value,
	})
}

func (req *request) Body() []byte {
	return req.body
}

// GetPostValue retrieves the value of a non-multipart form POST parameter.
func (req *request) GetPostValue(key string) string {
	return b2s(req.PostArgs().Peek(key))
}

// PostArgs returns POST arguments.
func (req *request) PostArgs() *Args {
	req.parsePostArgs()
	return &req.postArgs
}

func (req *request) parsePostArgs() {
	if req.parsedPostArgs {
		return
	}

	if !bytes.EqualFold(req.ContentType, consts.BytFormData) {
		return
	}

	req.postArgs.ParseBytes(req.body)
	req.parsedPostArgs = true
}

func (req *request) ParseMultipartForm() error {
	if req.multipartForm != nil {
		return nil
	}

	// Get the Content-Type header
	contentType := req.ContentType
	if !bytes.HasPrefix(contentType, consts.BytMultipartFormData) {
		return fmt.Errorf("not a multipart form request")
	}

	// Extract boundary
	_, params, err := mime.ParseMediaType(b2s(contentType))
	if err != nil {
		return err
	}

	fmt.Println("**-> params", params)

	boundary, ok := params["boundary"]
	if !ok {
		return fmt.Errorf("no boundary found in multipart form data")
	}

	// Create a new multipart reader
	reader := multipart.NewReader(bytes.NewReader(req.body), boundary)
	form, err := reader.ReadForm(32 << 20) // 32MB max memory
	if err != nil {
		return err
	}

	req.multipartForm = form
	return nil
}

// GetFormFile returns the first file for the provided form key
func (req *request) GetFormFile(key string) (multipart.File, *multipart.FileHeader, error) {
	// if err := req.ParseMultipartForm(); err != nil {
	// 	return nil, nil, err
	// }

	if req.multipartForm == nil {
		return nil, nil, fmt.Errorf("no multipart form data")
	}

	if req.multipartForm.File == nil {
		return nil, nil, fmt.Errorf("no files in form")
	}

	files := req.multipartForm.File[key]
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no file found for key: %s", key)
	}

	file, err := files[0].Open()
	if err != nil {
		return nil, nil, err
	}

	return file, files[0], nil
}

// FormValue returns the first value for the named component of the form data
func (req *request) FormValue(key string) string {
	if req.multipartForm != nil {
		if values := req.multipartForm.Value[key]; len(values) > 0 {
			return values[0]
		}
		fmt.Printf("** req.multipartForm.Value ->  %v\n", req.multipartForm.Value)
	}
	return req.GetPostValue(key)
}

// CleanupMultipartForm removes any temporary files
func (req *request) CleanupMultipartForm() {
	if req.multipartForm != nil {
		_ = req.multipartForm.RemoveAll()
	}
}
