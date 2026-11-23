package rweb

import (
	"bufio"
	stdctx "context" // aliased: this package's own Context interface and context type own the name
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
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
	// Body returns the whole request body. It is read off the connection on
	// the first call (bodies are lazy: nothing is read before a handler
	// asks), and kept for later calls. If the body cannot be read, the
	// handler is ended with ErrAbortHandler and the server answers the
	// request; see request_body.go.
	Body() []byte
	// BodyReader returns the request body as a stream, read from the
	// connection as the handler reads it, so an upload need not be held in
	// memory. See (*request).BodyReader for the rules.
	BodyReader() io.Reader
	// Context returns the request's context.Context, the counterpart of
	// net/http's Request.Context(). It is cancelled when the client closes
	// the connection while the handler is still running, and in every case
	// once the request is finished. Pass it to anything slow the handler
	// starts — outbound HTTP calls, queries, worker pools — so that work stops
	// when nobody is left to receive its result. See request.Context for the
	// cases (pipelining, WebSocket, GetConn) where only the second applies.
	Context() stdctx.Context
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

	queryArgs       Args
	parsedQueryArgs bool

	postArgs       Args
	parsedPostArgs bool

	// cr is the io.Reader under reader. It is per-connection, not
	// per-request: a byte it read ahead belongs to the next request on the
	// same connection, so Clean leaves it alone (serveConn resets it
	// when the pooled context moves to a new connection).
	cr connReader

	// reqCtx is the request's context.Context, created on the first
	// Context() call so a handler that never asks pays nothing. reqCancel
	// ends it; finishContext calls it when the request is done.
	reqCtx    stdctx.Context
	reqCancel stdctx.CancelFunc

	// baseCtx is reqCtx's parent: the server's base context, which Shutdown
	// cancels (Run calls it on a signal). Set per connection by serveConn (and by
	// Server.Request); Clean leaves it, since the next request on the
	// connection has the same parent. Nil means context.Background.
	baseCtx stdctx.Context

	// noWatch is set once something other than the HTTP loop owns the
	// connection's reads for the rest of this request (a WebSocket upgrade,
	// a GetConn caller). The disconnect watch is then stopped and never
	// restarted, since it would steal their bytes.
	noWatch bool

	// http10 is set for an "HTTP/1.0" request line, and connClose when the
	// connection ends after this response: the request said
	// "Connection: close", or is HTTP/1.0 without "keep-alive", or (set by
	// writeResponse) the handler's response says close, or the body can
	// only be delimited by closing. serveConn assigns both for every
	// request before the handler runs, so they need no reset in Clean; a
	// synthetic Server.Request leaves them false, which writes nothing.
	http10    bool
	connClose bool

	// lazy is the reading state of the body still on the connection (see
	// request_body.go): serveConn records the framing there and reads
	// nothing; Body, BodyReader and the form accessors read on demand.
	// body above holds what has been buffered so far.
	lazy lazyBody

	// bodyGen numbers the requests this pooled struct has served, as far as
	// body readers are concerned: handleRequest bumps it when the handlers
	// return, which turns every BodyReader handed out for that request
	// stale (bodyHandle). Never reset.
	bodyGen uint32

	// lr reads the lines of the request head, and of a chunked body's
	// framing, off reader. Per connection (serveConn sets it up), kept
	// here rather than in serveConn so that a body read from inside a
	// handler can use it; its scratch buffer is reused across the
	// connection's requests.
	lr lineReader

	// lim is the server's resolved connection limits (ReadTimeout for the
	// body, MaxHeaderBytes for a chunked body's trailers). Set once by
	// newContext; nil in a request built outside NewServer.
	lim *connLimits

	// debugLog is the server's logger when ServerOptions.Debug is on, nil
	// otherwise, for the lines that are now written when the body is first
	// buffered or its form parsed rather than before the handler.
	debugLog Logger
}

// Context returns the request's context.Context, created on first use.
//
// It is cancelled:
//   - when the client closes the connection while the handler runs — the
//     disconnect watch (connReader) sees the close as it happens, without
//     waiting for a write to fail;
//   - in every case, once the request is finished: after the response is
//     written, or for Server-Sent Events after the stream ends (so an SSE
//     producer goroutine can select on Done to learn its client has gone),
//     or for a WebSocket after the WebSocket handler returns;
//   - when the server shuts down (Run receives SIGINT or SIGTERM), for
//     every request still in flight. Run does not wait for those handlers.
//
// Disconnects are not watched for, so only the "request finished" rule
// applies, when:
//   - the request has a body that has not been read to its end. The watch
//     reads the connection, where the rest of the body still is, so it
//     starts only once the body has been consumed (Body, a BodyReader read
//     to EOF, a parsed form). A handler that never reads the body of a
//     request that has one gets no disconnect notice;
//   - the client has already sent its next request (HTTP pipelining): it has
//     committed to reading the responses, and a close it makes after that —
//     shutting down its write side after its last request — must not cancel
//     requests it is still waiting on;
//   - the connection has been handed elsewhere: after UpgradeWebSocket (the
//     WebSocket's own reads report the close) or after GetConn;
//   - the request is synthetic (Server.Request), which has no connection.
//
// Otherwise a read error or EOF counts as gone. That includes a client that
// half-closes (shuts down its write side) after a request it is still
// waiting on: on the wire it is indistinguishable from one that left, and
// net/http treats it the same way. A client that vanishes without closing
// (a sleeping laptop, a dropped network) is not noticed, as with any server:
// there is nothing to read until TCP keepalive gives up.
//
// The context is tied to this request only. Like the rweb Context itself it
// must be obtained inside the handler, but unlike the rweb Context it is safe
// to keep and use from other goroutines after the handler returns.
func (req *request) Context() stdctx.Context {
	if req.reqCtx == nil {
		parent := req.baseCtx
		if parent == nil {
			parent = stdctx.Background()
		}
		req.reqCtx, req.reqCancel = stdctx.WithCancel(parent)
		req.startWatchIfIdle()
	}
	return req.reqCtx
}

// startWatchIfIdle starts the disconnect watch if the request's context is
// in use and the connection has nothing more of this request, or of a next
// one, to deliver:
//
//   - the body has been read to its end. The watch reads one byte ahead,
//     and with body bytes still to come that byte would be the body's. So
//     Context() on a request whose body is unread starts nothing, and the
//     read that reaches the body's end calls this again (afterBodyRead);
//   - Buffered() == 0. Bytes already buffered are the next request, queued
//     behind this one: the pipelining case in Context's doc;
//   - the connection's reads have not been handed elsewhere (noWatch).
//
// A synthetic request's reader is bufio.NewReader(nil), and its cr has no
// conn, so startWatch no-ops.
func (req *request) startWatchIfIdle() {
	if req.reqCtx == nil || req.noWatch || !req.bodyComplete() {
		return
	}
	if req.reader.Buffered() == 0 {
		req.cr.startWatch(req.reqCancel)
	}
}

// releaseConn hands the connection's reads to someone else for the rest of
// the request (see noWatch): any running watch is stopped and none restarts.
func (req *request) releaseConn() {
	req.noWatch = true
	req.cr.stopWatch()
}

// readAhead returns, and consumes, the bytes already read off the connection
// but not yet parsed — whatever reader has buffered, then a byte the watch
// took — for a protocol taking the connection over (WebSocket). The bytes are
// copied: reader's buffer belongs to the pooled context and is reused.
func (req *request) readAhead() []byte {
	n := req.reader.Buffered()
	var lead []byte
	if n > 0 {
		b, _ := req.reader.Peek(n) // cannot fail: n bytes are buffered
		lead = append(lead, b...)
		_, _ = req.reader.Discard(n)
	}
	if c, ok := req.cr.takeByte(); ok {
		lead = append(lead, c) // after the buffer: the watch read it later
	}
	return lead
}

// finishContext ends the request's lifecycle: it stops a still-running watch
// (it must not outlive the request — the pooled context it points into is
// about to be reused) and cancels the request context. Idempotent.
func (req *request) finishContext() {
	// Proxy's upstream request must have stopped reading the body before
	// the struct is reused. handleRequest ends the lease after the
	// response; this covers the paths that never get there (a panic).
	req.endBodyLease()
	req.cr.stopWatch()
	if req.reqCancel != nil {
		req.reqCancel()
	}
	req.reqCtx, req.reqCancel = nil, nil
	req.noWatch = false
}

// defaultMultipartMaxMemory is the in-memory cap used by ReadForm when the
// server hasn't configured one. Anything beyond this cap spills to temp files
// (which are cleaned up by CleanupMultipartForm). 32 MB matches net/http.
const defaultMultipartMaxMemory int64 = 32 << 20

// Header returns the header value for the given key.
// Matching is case-insensitive per RFC 7230 (header field names are
// case-insensitive), so Header("Content-Type") finds "content-type",
// "Content-type", etc. EqualFold does this without allocating.
func (req *request) Header(key string) string {
	for _, header := range req.headers {
		if strings.EqualFold(header.Key, key) {
			return header.Value
		}
	}
	return ""
}

// Headers returns all the request headers.
func (req *request) Headers() []Header {
	return req.headers
}

// Host returns the host the client asked for: the one in an absolute-form
// request target if there was one, otherwise the Host header, as sent
// (including any port). It is "localhost" only when the request named no
// host at all.
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
// The query string is parsed once per request and cached, so repeated
// QueryParam calls in a handler don't re-parse (or re-allocate).
func (req *request) QueryParam(param string) (value string) {
	req.parseQueryArgs()
	return b2s(req.queryArgs.Peek(param))
}

func (req *request) parseQueryArgs() {
	if req.parsedQueryArgs {
		return
	}
	req.queryArgs.Parse(req.query)
	req.parsedQueryArgs = true
}

// Scheme returns `http` or `https`. An absolute-form request target supplies
// it directly. Otherwise, when the connection's peer is a trusted proxy
// (ServerOptions.TrustedProxies; by default loopback and private ranges) and
// sent X-Forwarded-Proto `http` or `https`, it is that (its rightmost entry),
// so an app behind a TLS-terminating proxy sees `https`. Otherwise it
// reflects the transport: `https` when the connection is TLS, `http` when it
// is not. See handleRequest, and client_ip.go for the trust rules.
func (req *request) Scheme() string {
	return req.scheme
}

// addParameter adds a new parameter to the request, percent-decoded.
//
// The router matches on the raw path (Request().Path() stays undecoded), so
// a value arrives here as it was on the wire: "/greet/John%20Doe" gives
// "John%20Doe". It is decoded here, once, so Param returns "John Doe", as
// QueryParam already returns decoded values. This is the only place a param
// is decoded: StaticFiles, which used to decode its *path itself, now takes
// the value as given, so "%2520" reaches it as "%20" and never as " ".
//
// url.PathUnescape, not QueryUnescape: "+" means a space only in a query, and
// stays "+" in a path. Decoding happens per segment value, after matching, so
// "%2F" in a :param gives a "/" in the value without changing which route
// matched; likewise "%2e%2e" gives "..". Handlers that build file paths from
// a param must check the decoded value for both, as StaticFiles does.
//
// A value that does not decode (a stray "%", "%zz") is kept raw rather than
// failing the request: it matched a route, and the handler can still see
// exactly what was sent. The IndexByte check keeps the common case, a value
// with no escapes, free of the decode call and its allocation.
func (req *request) addParameter(key string, value string) {
	if strings.IndexByte(value, '%') >= 0 {
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
	}
	req.params = append(req.params, rtr.Parameter{
		Key:   key,
		Value: value,
	})
}

// Body and BodyReader are in request_body.go, with the lazy reading they
// sit on.

// GetPostValue retrieves the value of a non-multipart form POST parameter.
func (req *request) GetPostValue(key string) string {
	return b2s(req.PostArgs().Peek(key))
}

// PostArgs returns POST arguments.
func (req *request) PostArgs() *Args {
	req.parsePostArgs()
	return &req.postArgs
}

// parsePostArgs parses a urlencoded body into postArgs, on first use.
//
// It used to run in handleRequest for every urlencoded request, which was
// free while the body was already in memory. With a lazy body an eager
// parse would read the body of every such request before routing, wanted or
// not, so the parse now waits for the first GetPostValue, PostArgs or
// FormValue, as the multipart parse already did. Body() does the reading
// (and ends the handler with ErrAbortHandler if the body cannot be read:
// these accessors have no error to return), and keeps the bytes, so Body()
// after a form lookup still returns them.
func (req *request) parsePostArgs() {
	if req.parsedPostArgs {
		return
	}

	if !hasContentTypePrefix(req.ContentType, consts.BytFormData) {
		return
	}

	req.postArgs.ParseBytes(req.Body())
	req.parsedPostArgs = true
	if req.debugLog != nil {
		req.debugLog.Debug("rweb: form args", "path", req.path, "args", req.postArgs.String())
	}
}

// ParseMultipartForm parses the request body as a multipart form.
//
// Idempotent: a successful parse caches the form, and a failed parse caches
// the error. Subsequent calls return the cached result without re-reading
// the body. Nothing parses the form before the handler runs: GetFormFile,
// GetFormFiles and FormValue each call this on first use, and later calls,
// from those accessors or the handler, see the same cached form or error.
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
	if !hasContentTypePrefix(contentType, consts.BytMultipartFormData) {
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

	// The form is parsed from the body as a stream (BodyReader): what
	// Body() has already buffered, if a handler or middleware called it
	// first, and otherwise straight off the connection. In the second case
	// an upload is never held in memory whole: ReadForm keeps up to maxMem
	// of file data and spills the rest to temp files as it reads. The body
	// is consumed by this, so a Body() call after the parse returns only
	// what Body() had buffered before it (nothing, usually). A body that
	// cannot be read surfaces as ReadForm's error, cached like any other.
	reader := multipart.NewReader(req.BodyReader(), boundary)
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
// ParseMultipartForm is idempotent: the first accessor to run parses the
// form, and later calls are a near-free no-op. If the parse failed, the
// cached error is returned here so the handler can react to it instead of
// seeing a generic "no multipart form data".
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
// As with GetFormFile, it parses the form on first use, and a failed parse
// surfaces here rather than masquerading as "no multipart form data".
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
	if hasContentTypePrefix(req.ContentType, consts.BytMultipartFormData) {
		// Parse on first use; ParseMultipartForm is idempotent, so later calls
		// short-circuit on the cached form or error. Errors are swallowed
		// here (the caller asked for a value, not an error); use GetFormFile
		// when the underlying parse error matters.
		if err := req.ParseMultipartForm(); err != nil || req.multipartForm == nil {
			// A form that does not parse reads as "no such value", as it
			// always has. A body that did not arrive is another matter:
			// "" would let the handler act on a request it only half
			// received, so it ends the handler, as Body() does.
			if req.bodyFailed() {
				panic(ErrAbortHandler)
			}
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
