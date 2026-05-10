package rweb

import (
	"bufio"
	"bytes"
	"fmt"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

// ItfRequest is the Request interface
type ItfRequest interface {
	// Headers returns the request headers.
	Headers() []Header
	// Header returns the header value for the given key.
	// Performs case-sensitive match first, then falls back to lowercase match if not found.
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
	// GetPostValue retrieves a value from an application/x-www-form-urlencoded
	// POST body. It does NOT read multipart form fields — use FormValue for those.
	GetPostValue(string) string
	// FormValue returns the first value for the named field across both
	// multipart/form-data and application/x-www-form-urlencoded request bodies.
	// For multipart requests, this lazily triggers parsing if not already done;
	// parse failures cause an empty string to be returned (use GetFormFile when
	// you need the underlying error).
	FormValue(string) string
	// GetFormFile returns the first file for the provided form key
	GetFormFile(string) (multipart.File, *multipart.FileHeader, error)
	// GetFormFiles returns all FileHeaders for the given form key.
	// Caller is responsible for calling .Open() on each header to read.
	// Returns an error when no multipart form has been parsed or no
	// files exist under the key.
	GetFormFiles(string) ([]*multipart.FileHeader, error)
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

	// multipartForm holds the parsed form once parsing has succeeded.
	// multipartParseErr caches a prior parse failure so callers (GetFormFile,
	// GetFormFiles, FormValue) all surface the same error instead of getting
	// a generic "no multipart form data" message after a real parse error.
	// multipartMaxMem is the per-request memory cap passed to ReadForm; when
	// zero, defaultMultipartMaxMemory is used. It is configured from
	// ServerOptions.MultipartMaxMemory at context construction time.
	multipartForm     *multipart.Form
	multipartParseErr error
	multipartMaxMem   int64

	queryArgs Args

	postArgs       Args
	parsedPostArgs bool
}

// defaultMultipartMaxMemory is the in-memory cap used by ReadForm when the
// server hasn't configured one. Anything beyond this cap spills to temp files
// (which are cleaned up by CleanupMultipartForm). 32 MB matches net/http.
const defaultMultipartMaxMemory int64 = 32 << 20

// Header returns the header value for the given key.
// Performs case-sensitive match first (priority), then falls back to lowercase match if not found.
func (req *request) Header(key string) string {
	for _, header := range req.headers {
		// Give priority to case-sensitive match
		if header.Key == key {
			return header.Value
		}
		// Otherwise, try lowercase match
		if header.Key == strings.ToLower(key) {
			return header.Value
		}
	}
	return ""
}

// Headers returns all the request headers.
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

// ParseMultipartForm parses the request body as a multipart form.
//
// Idempotent: a successful parse caches the form, and a failed parse caches
// the error. Subsequent calls return the cached result without re-reading
// the body. This lets the server eagerly pre-parse while still letting
// handlers re-call defensively (via GetFormFile etc.) and see the same error.
//
// Memory limit comes from req.multipartMaxMem (configured via
// ServerOptions.MultipartMaxMemory); falls back to defaultMultipartMaxMemory
// when zero. Bytes beyond the cap spill to temp files which CleanupMultipartForm
// removes.
func (req *request) ParseMultipartForm() error {
	// Fast paths: already parsed, or already known to be unparseable.
	if req.multipartForm != nil {
		return nil
	}
	if req.multipartParseErr != nil {
		return req.multipartParseErr
	}

	// Validate Content-Type before touching the body.
	contentType := req.ContentType
	if !bytes.HasPrefix(contentType, consts.BytMultipartFormData) {
		req.multipartParseErr = fmt.Errorf("not a multipart form request")
		return req.multipartParseErr
	}

	// Extract boundary parameter
	_, params, err := mime.ParseMediaType(b2s(contentType))
	if err != nil {
		req.multipartParseErr = err
		return err
	}

	boundary, ok := params["boundary"]
	if !ok {
		req.multipartParseErr = fmt.Errorf("no boundary found in multipart form data")
		return req.multipartParseErr
	}

	// Resolve the in-memory cap; 0 means "use default".
	maxMem := req.multipartMaxMem
	if maxMem <= 0 {
		maxMem = defaultMultipartMaxMemory
	}

	// req.body is already buffered, so wrapping with bytes.NewReader is cheap
	// and lets us re-read if needed (it isn't, currently — caching prevents that).
	reader := multipart.NewReader(bytes.NewReader(req.body), boundary)
	form, err := reader.ReadForm(maxMem)
	if err != nil {
		req.multipartParseErr = err
		return err
	}

	req.multipartForm = form
	return nil
}

// GetFormFile returns the first file for the provided form key.
//
// ParseMultipartForm is idempotent — if the server already pre-parsed the
// form during request handling, this is a near-free no-op. If the pre-parse
// failed (or never ran), the cached error is returned here so the handler
// can react to it instead of seeing a generic "no multipart form data".
func (req *request) GetFormFile(key string) (multipart.File, *multipart.FileHeader, error) {
	if err := req.ParseMultipartForm(); err != nil {
		return nil, nil, err
	}

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

// GetFormFiles returns all FileHeaders for the given form key.
// Mirrors GetFormFile but lets callers handle multi-file uploads
// (e.g. <input type="file" multiple>). Caller calls .Open() on each
// header to read; matches the stdlib idiom in mime/multipart.
//
// As with GetFormFile, ParseMultipartForm is invoked defensively so a
// missed/failed pre-parse surfaces here rather than masquerading as
// "no multipart form data".
func (req *request) GetFormFiles(key string) ([]*multipart.FileHeader, error) {
	if err := req.ParseMultipartForm(); err != nil {
		return nil, err
	}

	if req.multipartForm == nil {
		return nil, fmt.Errorf("no multipart form data")
	}

	if req.multipartForm.File == nil {
		return nil, fmt.Errorf("no files in form")
	}

	files := req.multipartForm.File[key]
	if len(files) == 0 {
		return nil, fmt.Errorf("no file found for key: %s", key)
	}

	return files, nil
}

// FormValue returns the first value for the named component of the form data.
//
// Dispatch is by Content-Type rather than by "is multipartForm non-nil",
// because the latter was the source of a cross-request data leak: with
// connection keep-alive the same context is reused, and prior to the fix
// req.multipartForm survived across requests, causing a urlencoded POST on
// the same connection to read string values from the previous multipart
// request's form. Anchoring on the *current* request's Content-Type is the
// correct discriminator.
func (req *request) FormValue(key string) string {
	if bytes.HasPrefix(req.ContentType, consts.BytMultipartFormData) {
		// Lazy-parse defense: ParseMultipartForm is idempotent, so a successful
		// server-side pre-parse short-circuits immediately. Errors are swallowed
		// here (the caller asked for a value, not an error); use GetFormFile
		// when the underlying parse error matters.
		if err := req.ParseMultipartForm(); err != nil || req.multipartForm == nil {
			return ""
		}
		if values := req.multipartForm.Value[key]; len(values) > 0 {
			return values[0]
		}
		return ""
	}
	return req.GetPostValue(key)
}

// CleanupMultipartForm releases any temp files AND nils out the cached form
// and parse error. The latter is the load-bearing part: contexts are pooled
// across requests on a keep-alive connection, and leaving multipartForm /
// multipartParseErr populated would cause the next request on that connection
// to read stale form values via FormValue (Form.Value is a plain map that
// RemoveAll does not clear) or to inherit a stale parse failure.
func (req *request) CleanupMultipartForm() {
	if req.multipartForm != nil {
		_ = req.multipartForm.RemoveAll()
		req.multipartForm = nil
	}
	req.multipartParseErr = nil
}
