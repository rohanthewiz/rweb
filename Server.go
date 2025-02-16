package rweb

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

type ServerOptions struct {
	// Address is the non-TLS  listen address. When UseTLS is true,
	// this is the address for the HTTP server which will redirect to the HTTPS server.
	// TCP addresses can be port only or address only in which case a high port is chosen. See: https://pkg.go.dev/net#Listen
	Address             string
	TLS                 TLSCfg
	Verbose             bool
	Debug               bool
	DebugRequestContext bool
	URLOptions          URLOptions
	// ReadyChan is a channel signalling that the server is about to enter its listen loop -- effectively running.
	// It should be a buffered chan (cap 1 is all that is needed), so there is no chance the server will hang
	ReadyChan chan struct{}
}

type URLOptions struct {
	// KeepTrailingSlashes is used to determine if trailing slashes should be kept in the URL path
	KeepTrailingSlashes bool
}

type TLSCfg struct {
	TLSAddr  string // [Port] to listen on for TLS
	CertFile string // Path to certificate file
	KeyFile  string // Path to private key file
	UseTLS   bool   // Whether to use TLS
}

// Server is the HTTP Server.
type Server struct {
	handlers     []Handler
	contextPool  sync.Pool
	radixRouter  *rtr.RadixRouter[Handler]
	hashRouter   *rtr.HashRouter[Handler]
	errorHandler func(Context, error)
	options      ServerOptions
	listenAddr   string // the actual listen address used by net.Listen
}

// NewServer creates a new HTTP server.
func NewServer(options ...ServerOptions) *Server {
	radRtr := &rtr.RadixRouter[Handler]{}
	hashRtr := rtr.NewHashRouter[Handler]()

	opts := ServerOptions{}
	if len(options) == 1 {
		// Not sure why doing this  (opts := options[0]) instead of individually setting hangs
		// likely something to do with copy of the ready channel

		opts.Verbose = options[0].Verbose // Verbose
		opts.Debug = options[0].Debug
		opts.TLS = options[0].TLS
		opts.Address = options[0].Address

		// Ready Channel
		if options[0].ReadyChan != nil && cap(options[0].ReadyChan) < 1 && opts.Verbose {
			fmt.Println("Ready channel capacity should be at least 1, or we may hang")
		}
		opts.ReadyChan = options[0].ReadyChan // Assign even if it is nil as we will do nil check on use
	}

	s := &Server{
		radixRouter: radRtr,
		hashRouter:  hashRtr,
		options:     opts,
		errorHandler: func(ctx Context, err error) {
			errCode := GenRandString(8, true)
			log.Printf("[ERR: %s] %q - error: %s\n", errCode, ctx.Request().Path(), err)

			if ctx.Response().Status() == 0 || ctx.Response().Status() == consts.StatusOK {
				ctx.Status(consts.StatusInternalServerError)
			}
			_ = ctx.WriteHTML(fmt.Sprintf("<h3>%d Internal Server Error</h3>\n<p>Error code: %s</p>",
				ctx.Response().Status(), errCode))
		},
	}

	s.handlers = []Handler{
		func(c Context) error { // default handler
			ctx := c.(*context)
			var hdlr Handler

			if s.options.Debug {
				fmt.Printf("Request - method: %q, path: %q\n", ctx.request.method, ctx.request.path)
			}

			// Try exact match first
			hdlr = s.hashRouter.Lookup(ctx.request.method, ctx.request.path)
			if hdlr == nil {
				if s.options.Debug {
					fmt.Println("Route not found in hash router (it could be a dynamic route)  -- trying radix router")
				}
				hdlr = radRtr.LookupNoAlloc(ctx.request.method, ctx.request.path, ctx.request.addParameter)
			}

			if hdlr == nil {
				if s.options.Verbose {
					fmt.Println("Route not found in radix router either -- returning 404")
				}
				ctx.SetStatus(consts.StatusNotFound)
				return nil
			}

			return hdlr(c)
		},
	}

	s.contextPool.New = func() any { return s.newContext() }
	return s
}

func (s *Server) AddMethod(method string, path string, handler Handler) {
	if strings.IndexByte(path, consts.RuneColon) < 0 && strings.IndexByte(path, consts.RuneAsterisk) < 0 {
		s.hashRouter.Add(method, path, handler)
	} else {
		s.radixRouter.Add(method, path, handler)
	}
}

// Get registers your function to be called when the given GET path has been requested.
func (s *Server) Get(path string, handler Handler) {
	s.AddMethod(consts.MethodGet, path, handler)
}

// Post registers your function to be called when the given POST path has been requested.
func (s *Server) Post(path string, handler Handler) {
	s.AddMethod(consts.MethodPost, path, handler)
}

// Put registers your function to be called when the given PUT path has been requested.
func (s *Server) Put(path string, handler Handler) {
	s.AddMethod(consts.MethodPut, path, handler)
}

func (s *Server) Patch(path string, handler Handler) {
	s.AddMethod(consts.MethodPatch, path, handler)
}

func (s *Server) Delete(path string, handler Handler) {
	s.AddMethod(consts.MethodDelete, path, handler)
}

func (s *Server) Head(path string, handler Handler) {
	s.AddMethod(consts.MethodHead, path, handler)
}

func (s *Server) Options(path string, handler Handler) {
	s.AddMethod(consts.MethodOptions, path, handler)
}

func (s *Server) Connect(path string, handler Handler) {
	s.AddMethod(consts.MethodConnect, path, handler)
}

func (s *Server) Trace(path string, handler Handler) {
	s.AddMethod(consts.MethodTrace, path, handler)
}

func (s *Server) SSEHandler(eventChan chan any) Handler {
	return func(ctx Context) error {
		ctx.SetSSE(eventChan)
		return nil
	}
}

// Proxy sets up a reverse proxy for the provided path prefix to the specified target URL (targetURL can include a path)
// The pathPrefix can help us to distinguish between different proxy targets, from which we can strip any unneeded tokens (from the left)  in the handler
// If there is any prefix left after stripping, it is added to the leftmost of the target URL.
// If there is a path specified in the target URL, it is appended after the stripped prefix.
func (s *Server) Proxy(pathPrefix string, targetURL string, prefixTokensToRemove int) (err error) {
	tURL, err := url.Parse(targetURL)
	if err != nil {
		return err
	}

	qry := tURL.RawQuery
	urlWithoutPath := tURL.Scheme + "://" + tURL.Host

	// Normalize path prefix by removing any leading slashes
	if strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = pathPrefix[1:]
	}

	// Strip off the left (most significant tokens as those can act as a switch between targets) -- keep the right side tokens here
	strippedPrefix := pathPrefix
	if prefixTokensToRemove > 0 {
		tokens := strings.Split(pathPrefix, "/")
		if len(tokens) >= prefixTokensToRemove {
			strippedPrefix = strings.Join(tokens[prefixTokensToRemove:], "/")
		}
	}
	// fmt.Println("**-> strippedPrefix", strippedPrefix)

	hdlr := func(ctx Context) (err error) {
		ctxReq := ctx.Request()

		// Get the request path minus the prefix, then add back the prefix and the targetPath, minus any dropped tokens
		pathWoPrefix := ctxReq.Path()
		if idx := strings.Index(ctxReq.Path(), pathPrefix); idx >= 0 {
			pathWoPrefix = pathWoPrefix[idx+len(pathPrefix):]
		}

		proxyURL := urlWithoutPath + filepath.Join("/", strippedPrefix, tURL.Path, pathWoPrefix)

		if qry != "" {
			proxyURL = proxyURL + "?" + qry
		}

		if s.options.Verbose {
			fmt.Printf("Proxying %q to %q\n", ctxReq.Path(), proxyURL)
		}

		var req *http.Request

		if ctxReq.Body() != nil {
			buf := bytes.NewBuffer(ctxReq.Body())
			req, err = http.NewRequest(ctx.Request().Method(), proxyURL, buf)
		} else {
			req, err = http.NewRequest(ctx.Request().Method(), proxyURL, nil)
		}
		if err != nil {
			return err
		}

		// Take the original headers too
		for _, hdr := range ctxReq.Headers() {
			req.Header.Set(hdr.Key, hdr.Value)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()

		err = ctx.Bytes(body)
		if err != nil {
			return err
		}

		ctx.Response().SetStatus(resp.StatusCode)

		for hdr, vals := range resp.Header {
			if strings.EqualFold(consts.HeaderContentLength, hdr) { // we auto set content-length - don't set it twice
				continue
			}
			ctx.Response().SetHeader(hdr, strings.Join(vals, ","))
		}
		return nil
	}

	s.setMethodProxyHandler(filepath.Join("/", pathPrefix, "*path"), hdlr)
	// The wildcard route does not handle the root of the prefix, so have to handle that separately
	s.setMethodProxyHandler(filepath.Join("/", pathPrefix), hdlr)
	return nil
}

func (s *Server) setMethodProxyHandler(proxyPath string, hdlr func(ctx Context) (err error)) {
	if s.options.Verbose {
		fmt.Println("Setting up proxy handlers to route:", proxyPath)
	}

	s.Get(proxyPath, hdlr)
	s.Post(proxyPath, hdlr)
	s.Put(proxyPath, hdlr)
	s.Patch(proxyPath, hdlr)
	s.Delete(proxyPath, hdlr)
	s.Head(proxyPath, hdlr)
	s.Options(proxyPath, hdlr)
	s.Connect(proxyPath, hdlr)
	s.Trace(proxyPath, hdlr)
}

// StaticFiles maps a route to serve static files from a specified directory after optionally stripping route tokens.
// If tokens are stripped, the leftmost tokens are removed from the request path before building the file path.
// Examples:
//  1. s.StaticFiles("static/images/", "/assets/images", 2)
//  2. s.StaticFiles("/css/", "assets/css", 1)
//  3. s.StaticFiles("/.well-known/", "/", 0)
func (s *Server) StaticFiles(reqDir string, targetDir string, nbrOfTokensToStrip int) {
	if len(reqDir) < 2 {
		fmt.Println("StaticFiles request dir is too short -- not handling")
		return
	}

	// Build wildcard route
	route := filepath.Join("/", reqDir, "*path")
	if s.options.Debug {
		fmt.Println("**-> static route:", route)
	}

	// Remove any leading "/" so we can properly split below
	if reqDir[0] == '/' {
		reqDir = reqDir[1:]
	}

	// We use the wildcard parameter in the route here
	s.Get(route, func(ctx Context) error {
		var rhTokens []string
		// Strip off the left -- keep the right side tokens here
		// It is okay if we strip all
		if s.options.Debug {
			fmt.Printf("**-> reqPath: %q\n", reqDir)
		}

		tokens := strings.Split(reqDir, "/")
		if s.options.Debug {
			fmt.Printf("**-> tokens: %q", tokens)
		}

		// Remove unwanted tokens from the request path
		if len(tokens) >= nbrOfTokensToStrip {
			rhTokens = tokens[nbrOfTokensToStrip:]
		}
		if s.options.Debug {
			fmt.Printf("**-> rhTokens: %q\n", rhTokens)
		}

		// Build the actual filepath now
		wildcardPath := ctx.Request().Param("path")
		fileSpec := filepath.Join("/", targetDir,
			strings.Join(rhTokens, "/"), wildcardPath)
		if s.options.Debug {
			fmt.Println("**-> fileFullPath", fileSpec)
		}

		body, err := os.ReadFile("." + fileSpec)
		if err != nil {
			return err
		}

		return File(ctx, filepath.Base(fileSpec), body)
	})
}

// Request performs a synthetic request and returns the response.
// This function keeps the response in memory so it's slightly slower than a real request.
// However it is very useful inside tests where you don't want to spin up a real web server.
func (s *Server) Request(method string, url string, headers []Header, body io.Reader) Response {
	ctx := s.newContext()
	ctx.request.headers = headers
	s.handleRequest(ctx, method, url, io.Discard)
	return ctx.Response()
}

func (s *Server) RunWithHttpsRedirect() error {
	// Start HTTPS server
	go func() {
		err := s.Run()
		if err != nil {
			fmt.Println("Error starting HTTPS server: ", err)
		}
	}()

	// Start HTTP redirect server
	return http.ListenAndServe(s.options.Address, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpsURL := "https://" + r.Host + r.RequestURI
		http.Redirect(w, r, httpsURL, http.StatusMovedPermanently)
	}))
}

// Run starts the server on the given address.
func (s *Server) Run() (err error) {
	var listener net.Listener

	if s.options.TLS.UseTLS {
		cert, err := tls.LoadX509KeyPair(s.options.TLS.CertFile, s.options.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %v", err)
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12, // Require TLS 1.2 or higher
		}

		// Create TLS listener
		listener, err = tls.Listen(consts.ProtocolTCP, s.options.TLS.TLSAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to create TLS listener: %v", err)
		}

	} else { // Create regular TCP listener
		listener, err = net.Listen(consts.ProtocolTCP, s.options.Address)
		if err != nil {
			return err
		}
	}
	defer listener.Close()

	s.listenAddr = listener.Addr().String()

	// Go accept and handle connections
	go func() {
		if s.options.Verbose {
			protocol := consts.HTTP
			if s.options.TLS.UseTLS {
				protocol = consts.HTTPS
			}
			fmt.Printf("Serving at %s://%s\n", protocol, listener.Addr()) // address
		}

		if s.options.ReadyChan != nil { // don't forget nil check!
			s.options.ReadyChan <- struct{}{} // Let the caller know we are running
		}

		for { // maybe TODO optional graceful shutdown based on SIGTERM
			conn, err := listener.Accept() // accept next client connection
			if err != nil {
				if s.options.Debug { // TODO: check deeper into "use of closed network connection"
					fmt.Println("Error accepting connection:", err)
				}
				continue
			}
			fmt.Printf("** Connection established: %s <-- %s\n", conn.LocalAddr(), conn.RemoteAddr())

			// Handle each connection separately
			go func(conn *net.Conn) {
				s.handleConnection(*conn)
			}(&conn)
		}
	}()

	// Handle SIGTERM (like CTRL-C)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	listener.Close()

	return nil
}

// ListRoutes prints all server routes by method in tabular format.
func (s *Server) ListRoutes() {
	fmt.Println("\n---- Routes (routes with params are not listed) ----")
	routesList := s.hashRouter.ListRoutes()
	// Can we list routes for radix router? Maybe we will just track the number of Adds

	fmt.Println("Method\t\tPath\t\t\tHandler")
	fmt.Println("------\t\t----\t\t\t----------")

	for _, route := range routesList {
		fmt.Printf("%-8s\t%-20s\t%-30s\n", route.Method, route.Path, route.HandlerRef)
	}
	fmt.Println()
	// s.radixRouter.PrintRoutes()
}

// Use adds handlers to your handlers chain.
func (s *Server) Use(handlers ...Handler) {
	last := s.handlers[len(s.handlers)-1]
	// Re-slice to exclude last and add append the incoming handlers
	s.handlers = append(s.handlers[:len(s.handlers)-1], handlers...)
	s.handlers = append(s.handlers, last) // add back the last
}

// handleConnection handles an accepted connection.
func (s *Server) handleConnection(conn net.Conn) {
	var method, url string
	var ctx = s.contextPool.Get().(*context) // get a new context from the pool

	ctx.reader.Reset(conn) // prepare to read from the accepted connection

	defer conn.Close()

	defer func() {
		// Clean up the context and return it to the pool
		ctx.Clean()
		s.contextPool.Put(ctx)
	}()

	for {
		// Read a line from the connection
		message, err := ctx.reader.ReadString(consts.RuneNewLine)
		if err != nil {
			if s.options.Debug && err.Error() != consts.EOF {
				fmt.Println("Error reading connection:", err)
			}
			return
		}

		space := strings.IndexByte(message, consts.RuneSingleSpace)

		if space <= 0 {
			_, _ = io.WriteString(conn, consts.HTTPBadRequest)
			return
		}

		method = message[:space]

		if !isValidRequestMethod(method) {
			_, _ = io.WriteString(conn, consts.HTTPBadMethod)
			return
		}

		if s.options.Verbose {
			fmt.Println(strings.Repeat("-", 50))
		}

		lastSpace := strings.LastIndexByte(message, consts.RuneSingleSpace)

		if lastSpace == space {
			lastSpace = len(message) - len(consts.CRLF)
		}

		url = message[space+1 : lastSpace]

		var contentLen int64
		var isChunked bool

		// Read headers until we meet an empty line
		for {
			message, err = ctx.reader.ReadString(consts.RuneNewLine) // read a line
			if err != nil {
				return
			}

			if message == consts.CRLF { // "empty" line // end of headers
				break
			}

			colon := strings.IndexByte(message, consts.RuneColon)

			if colon <= 0 {
				continue // header should include a colon
			}

			key := message[:colon]
			value := message[colon+2 : len(message)-2]

			ctx.request.headers = append(ctx.request.headers, Header{
				Key:   key,
				Value: value,
			})

			// Check for Content-Length and Transfer-Encoding headers
			if strings.EqualFold(key, consts.HeaderContentLength) {
				contentLen, err = strconv.ParseInt(value, 10, 64)
				if err != nil {
					_, _ = io.WriteString(conn, consts.HTTPBadRequest)
					return
				}
			} else if strings.EqualFold(key, consts.HeaderContentType) {
				ctx.request.ContentType = s2b(value)
			} else if strings.EqualFold(key, consts.HeaderTransferEncoding) &&
				strings.Contains(strings.ToLower(value), "chunked") {
				isChunked = true
			}
		}

		// Read the request body if present
		if contentLen > 0 {
			// Fixed-length body
			body := make([]byte, contentLen)
			_, err = io.ReadFull(ctx.reader, body)
			if err != nil {
				if s.options.Verbose {
					fmt.Println("Error reading request body:", err)
				}
				return
			}

			if method != consts.MethodHead && method != consts.MethodTrace {
				ctx.request.body = append(ctx.request.body, body...)
			}

		} else if isChunked {
			// Chunked encoding
			for {
				// Read chunk size
				chunkSize, err := ctx.reader.ReadString(consts.RuneNewLine)
				if err != nil {
					return
				}

				// Parse chunk size (hex)
				size, err := strconv.ParseInt(strings.TrimSpace(chunkSize), 16, 64)
				if err != nil {
					_, _ = io.WriteString(conn, consts.HTTPBadRequest)
					return
				}

				// Zero size chunk means end of body
				if size == 0 {
					// Read final CRLF
					_, err = ctx.reader.ReadString(consts.RuneNewLine)
					if err != nil {
						return
					}
					break
				}

				// Read chunk data
				chunk := make([]byte, size)
				_, err = io.ReadFull(ctx.reader, chunk)
				if err != nil {
					return
				}
				ctx.request.body = append(ctx.request.body, chunk...)

				// Read chunk LF
				_, err = ctx.reader.ReadString(consts.RuneNewLine)
				if err != nil {
					return
				}
			}
		}

		if s.options.Debug && len(ctx.request.body) > 0 {
			fmt.Printf("** ctx.request.body: %q\n", string(ctx.request.body))
		}

		// Handle the request
		s.handleRequest(ctx, method, url, conn)
		if s.options.DebugRequestContext {
			fmt.Printf("** ctx -> %#v\n\n", ctx)
		}

		// Clean up the context by zeroing some slices, etc
		ctx.Clean()
	}
}

// handleRequest handles the given request.
func (s *Server) handleRequest(ctx *context, method string, url string, respWriter io.Writer) {
	ctx.method = method
	ctx.scheme, ctx.host, ctx.path, ctx.query = parseURL(url, s.options.URLOptions)
	if s.options.Debug {
		fmt.Printf(" %s - ContentType: %q, Request Body Length: %d, Scheme: %q, Host: %q, Path: %q, Query: %q\n",
			method, string(ctx.ContentType), len(ctx.request.body), ctx.scheme, ctx.host, ctx.path, ctx.query)
	}

	// Parse Post Args or Multipart Form
	if len(ctx.request.body) > 0 {
		if bytes.HasPrefix(ctx.ContentType, consts.BytMultipartFormData) {
			if err := ctx.request.ParseMultipartForm(); err != nil {
				fmt.Printf("Error parsing multipart form: %v\n", err)
			} else {
				if s.options.Verbose {
					fmt.Println("Parsed Multipart Form")
				}
			}
		} else if bytes.EqualFold(ctx.ContentType, consts.BytFormData) {
			ctx.request.parsePostArgs()
			if s.options.Debug {
				fmt.Println("** Post Args -->", ctx.request.postArgs.String())
			}
		}
	}

	// Call the first handler in the chain
	// (which will call any subsequent handlers)
	err := s.handlers[0](ctx)
	if err != nil {
		s.errorHandler(ctx, err)
	}

	s.writeResponse(ctx, respWriter)
}

func (s *Server) writeResponse(ctx *context, respWriter io.Writer) {
	tmp := bytes.Buffer{}

	// HTTP1.1 header and status
	tmp.WriteString(consts.HTTP1)
	tmp.WriteString(consts.StrSingleSpace)
	tmp.WriteString(strconv.Itoa(int(ctx.status)))
	if st, ok := consts.StatusTextFromCode[int(ctx.status)]; ok {
		tmp.WriteByte(consts.RuneSingleSpace)
		tmp.WriteString(st)
	}
	tmp.WriteString(consts.CRLF)

	if ctx.sseEvents == nil { // For SSE -- don't set content-length
		// Content-Length
		tmp.WriteString(consts.HeaderContentLength)
		tmp.WriteString(consts.ColonSpace)
		tmp.WriteString(strconv.Itoa(len(ctx.response.body)))
		tmp.WriteString(consts.CRLF)
	}

	// Other Headers
	for _, header := range ctx.response.headers {
		tmp.WriteString(header.Key)
		tmp.WriteString(consts.ColonSpace)
		tmp.WriteString(header.Value)
		tmp.WriteString(consts.CRLF)
	}
	tmp.WriteString(consts.CRLF)

	// Write what we have so far to the response writer
	_, err := respWriter.Write(tmp.Bytes())
	if err != nil {
		fmt.Println("Error writing response: ", err)
	}

	// Body
	if ctx.sseEvents == nil {
		_, _ = respWriter.Write(ctx.response.body)
	} else {
		err = ctx.sendSSE(respWriter)
		if err != nil {
			fmt.Println("Error sending SSE events: ", err)
		}
	}
}

// newContext allocates a new context with the default state.
func (s *Server) newContext() *context {
	return &context{
		server: s,
		request: request{
			reader:  bufio.NewReader(nil),
			body:    make([]byte, 0),
			headers: make([]Header, 0, 8),
			params:  make([]rtr.Parameter, 0, 8),
		},
		response: response{
			body:    make([]byte, 0, 1024),
			headers: make([]Header, 0, 8),
			status:  200,
		},
	}
}

func (s *Server) GetListenAddr() string {
	return s.listenAddr
}

func (s *Server) GetListenPort() (port string) {
	addr := s.listenAddr

	lastColonIndex := strings.LastIndex(addr, ":")
	if lastColonIndex != -1 {
		return addr[lastColonIndex+1:]
	}
	return
}
