package rweb

import (
	"bufio"
	"bytes"
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
}

// request represents the HTTP request used in the given context.
type request struct {
	reader *bufio.Reader
	// bodyStream io.Reader

	scheme string
	host   string
	method string
	path   string
	query  string

	ContentType []byte // shortcut to content type
	headers     []Header
	// Header      RequestHeader
	body   []byte
	params []rtr.Parameter

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

/* TODO - Let's do Multiform later
// ErrNoMultipartForm means that the request's Content-Type
// isn't 'multipart/form-data'.
var ErrNoMultipartForm = errors.New("request Content-Type has bad boundary or is not multipart/form-data")

// MultipartForm returns request's multipart form.
//
// Returns ErrNoMultipartForm if request's Content-Type
// isn't 'multipart/form-data'.
//
// RemoveMultipartFormFiles must be called after returned multipart form
// is processed.
func (req *request) MultipartForm() (*multipart.Form, error) {
	if req.multipartForm != nil {
		return req.multipartForm, nil
	}

	req.multipartFormBoundary = string(req.Header.MultipartFormBoundary())
	if req.multipartFormBoundary == "" {
		return nil, ErrNoMultipartForm
	}

	var err error
	ce := req.Header.peek(consts.StrContentEncoding)

	if req.bodyStream != nil {
		bodyStream := req.bodyStream
		if bytes.Equal(ce, consts.StrGzip) {
			// Do not care about memory usage here.
			if bodyStream, err = gzip.NewReader(bodyStream); err != nil {
				return nil, fmt.Errorf("cannot gunzip request body: %w", err)
			}
		} else if len(ce) > 0 {
			return nil, fmt.Errorf("unsupported Content-Encoding: %q", ce)
		}

		mr := multipart.NewReader(bodyStream, req.multipartFormBoundary)
		req.multipartForm, err = mr.ReadForm(8 * 1024)
		if err != nil {
			return nil, fmt.Errorf("cannot read multipart/form-data body: %w", err)
		}
	} else {
		body := req.bodyBytes()
		if bytes.Equal(ce, consts.StrGzip) {
			// Do not care about memory usage here.
			if body, err = AppendGunzipBytes(nil, body); err != nil {
				return nil, fmt.Errorf("cannot gunzip request body: %w", err)
			}
		} else if len(ce) > 0 {
			return nil, fmt.Errorf("unsupported Content-Encoding: %q", ce)
		}

		req.multipartForm, err = readMultipartForm(bytes.NewReader(body), req.multipartFormBoundary, len(body), len(body))
		if err != nil {
			return nil, err
		}
	}

	return req.multipartForm, nil
}

func readMultipartForm(r io.Reader, boundary string, size, maxInMemoryFileSize int) (*multipart.Form, error) {
	// Do not care about memory allocations here, since they are tiny
	// compared to multipart data (aka multi-MB files) usually sent
	// in multipart/form-data requests.

	if size <= 0 {
		return nil, fmt.Errorf("form size must be greater than 0. Given %d", size)
	}
	lr := io.LimitReader(r, int64(size))
	mr := multipart.NewReader(lr, boundary)
	f, err := mr.ReadForm(int64(maxInMemoryFileSize))
	if err != nil {
		return nil, fmt.Errorf("cannot read multipart/form-data body: %w", err)
	}
	return f, nil
}

// FormValue returns form value associated with the given key.
//
// The value is searched in the following places:
//
//   - Query string.
//   - POST or PUT body.
//
// There are more fine-grained methods for obtaining form values:
//
//   - QueryArgs for obtaining values from query string.
//   - PostArgs for obtaining values from POST or PUT body.
//   - MultipartForm for obtaining values from multipart form.
//   - FormFile for obtaining uploaded files.
//
// The returned value is valid until your request handler returns.
func (ctx *request) FormValue(key string) []byte {
	if ctx.formValueFunc != nil {
		return ctx.formValueFunc(ctx, key)
	}
	return defaultFormValue(ctx, key)
}

type FormValueFunc func(*RequestCtx, string) []byte

var (
	defaultFormValue = func(ctx *request, key string) []byte {
		v := ctx.QueryArgs().Peek(key)
		if len(v) > 0 {
			return v
		}
		v = ctx.PostArgs().Peek(key)
		if len(v) > 0 {
			return v
		}
		mf, err := ctx.MultipartForm()
		if err == nil && mf.Value != nil {
			vv := mf.Value[key]
			if len(vv) > 0 {
				return []byte(vv[0])
			}
		}
		return nil
	}

	// NetHttpFormValueFunc gives consistent behavior with net/http.
	// POST and PUT body parameters take precedence over URL query string values.
	//
	//nolint:stylecheck // backwards compatibility
	NetHttpFormValueFunc = func(ctx *RequestCtx, key string) []byte {
		v := ctx.PostArgs().Peek(key)
		if len(v) > 0 {
			return v
		}
		mf, err := ctx.MultipartForm()
		if err == nil && mf.Value != nil {
			vv := mf.Value[key]
			if len(vv) > 0 {
				return []byte(vv[0])
			}
		}
		v = ctx.QueryArgs().Peek(key)
		if len(v) > 0 {
			return v
		}
		return nil
	}
)

func (req *request) bodyBytes() []byte {
	if req.body != nil {
		return req.body
	}
	if req.bodyStream != nil {
		bodyBuf := req.bodyBuffer()
		bodyBuf.Reset()
		_, err := copyZeroAlloc(bodyBuf, req.bodyStream)
		req.closeBodyStream() //nolint:errcheck
		if err != nil {
			bodyBuf.SetString(err.Error())
		}
	}
	if req.body == nil {
		return nil
	}
	return req.body.B
}

func (req *Request) bodyBuffer() *bytebufferpool.ByteBuffer {
	if req.body == nil {
		req.body = requestBodyPool.Get()
	}
	req.bodyRaw = nil
	return req.body
}

var (
	responseBodyPool bytebufferpool.Pool
	requestBodyPool  bytebufferpool.Pool
)

// AppendGzipBytes appends gzipped src to dst and returns the resulting dst.
func AppendGzipBytes(dst, src []byte) []byte {
	return AppendGzipBytesLevel(dst, src, CompressDefaultCompression)
}

// WriteGunzip writes ungzipped p to w and returns the number of uncompressed
// bytes written to w.
func WriteGunzip(w io.Writer, p []byte) (int, error) {
	r := &byteSliceReader{b: p}
	zr, err := acquireGzipReader(r)
	if err != nil {
		return 0, err
	}
	n, err := copyZeroAlloc(w, zr)
	releaseGzipReader(zr)
	nn := int(n)
	if int64(nn) != n {
		return 0, fmt.Errorf("too much data gunzipped: %d", n)
	}
	return nn, err
}

// AppendGunzipBytes appends gunzipped src to dst and returns the resulting dst.
func AppendGunzipBytes(dst, src []byte) ([]byte, error) {
	w := &byteSliceWriter{b: dst}
	_, err := WriteGunzip(w, src)
	return w.b, err
}

// AppendDeflateBytesLevel appends deflated src to dst using the given
// compression level and returns the resulting dst.
//
// Supported compression levels are:
//
//   - CompressNoCompression
//   - CompressBestSpeed
//   - CompressBestCompression
//   - CompressDefaultCompression
//   - CompressHuffmanOnly
func AppendDeflateBytesLevel(dst, src []byte, level int) []byte {
	w := &byteSliceWriter{b: dst}
	WriteDeflateLevel(w, src, level) //nolint:errcheck
	return w.b
}
*/
