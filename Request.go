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

// IntfRequest is an interface for HTTP requests.
type IntfRequest interface {
	Header(string) string
	Host() string
	Method() string
	Path() string
	Scheme() string
	Param(string) string
	GetPostValue(string) string
	FormValue(string) string
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

func (req *request) Body() []byte {
	return req.body
}

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
	req.parsedPostArgs = true

	if !bytes.EqualFold(req.ContentType, consts.StrFormData) {
		return
	}
	req.postArgs.ParseBytes(req.body)
}

func (req *request) ParseMultipartForm() error {
	if req.multipartForm != nil {
		return nil
	}

	// Get the Content-Type header
	contentType := req.ContentType
	if !bytes.HasPrefix(contentType, consts.StrMultipartFormData) {
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
		fmt.Printf("**-> req.multipartForm.Value %v\n", req.multipartForm.Value)
	}
	return req.GetPostValue(key)
}

// CleanupMultipartForm removes any temporary files
func (req *request) CleanupMultipartForm() {
	if req.multipartForm != nil {
		req.multipartForm.RemoveAll()
	}
}
