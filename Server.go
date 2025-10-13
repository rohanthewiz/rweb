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

	"github.com/rohanthewiz/element"
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
	// Cookie holds server-wide default settings for cookies
	Cookie CookieConfig
	SSECfg SSECfg
}

type SSECfg struct {
	SendConnectedEvent bool // Whether to send "Connected" event to clients
}

type URLOptions struct {
	// KeepTrailingSlashes is used to determine if trailing slashes should be kept in the URL path
	KeepTrailingSlashes bool
}

// SSEvent when received from a source channel will set the Type into the event return to the client,
// otherwise the Type will be set from whatever is in the context, set by SetupSSE() or SSEHandler()
type SSEvent struct {
	Type string // or event name
	Data interface{}
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
		opts.Cookie = options[0].Cookie

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
				ctx.SetStatus(consts.StatusInternalServerError)
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
				if s.options.Debug {
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

// SetupSSE sets the source channel for SSE events and allows you to either
// send the event to the source channel as an rweb.SSEvent from which the event type will be pulled,
// or explicitly state the (single) eventType here -- not flexible, but here if you need it.
// So if you are sending from the source rweb.SSEvent(s), you don't need to set eventTypeOption here
func (s *Server) SetupSSE(ctx Context, eventChan <-chan any, eventTypeOption ...string) error {
	evtType := ""
	if len(eventTypeOption) > 0 {
		evtType = eventTypeOption[0]
	}
	return ctx.SetSSE(eventChan, evtType)
}

// SSEHandler is a convenience method that creates a handler function for Server-Sent Events of a certain type.
// Note: SSEvent data from the source will override the event type set here.
// The eventsChan parameter is the channel from which events will be sourced.
// The optional eventName parameter specifies the event type (defaults to "message").
// Usage: s.Get("/events", s.SSEHandler(eventsChan, "update"))
func (s *Server) SSEHandler(eventsChan <-chan any, eventType ...string) Handler {
	// Default to "message" event type if not specified
	name := "message" // default event name
	if len(eventType) > 0 && eventType[0] != "" {
		name = eventType[0]
	}

	// Return a handler that sets up SSE for the given context
	return func(ctx Context) error {
		return s.SetupSSE(ctx, eventsChan, name)
	}
}

// WebSocketHandler is a type that handles WebSocket connections
// The handler receives a WebSocket connection for bidirectional communication
type WebSocketHandler func(*WSConn) error

// WebSocket registers a WebSocket handler for the given path
// The handler function receives a WebSocket connection after successful upgrade
// Usage: s.WebSocket("/ws", func(ws *WSConn) error { ... })
func (s *Server) WebSocket(path string, handler WebSocketHandler) {
	s.Get(path, func(ctx Context) error {
		// Upgrade the connection to WebSocket
		ws, err := ctx.UpgradeWebSocket()
		if err != nil {
			fmt.Printf("Failed to upgrade connection to WebSocket: %v\n", err)
			// If upgrade fails, return error (will send appropriate HTTP error response)
			return err
		}

		// Call the WebSocket handler with the upgraded connection
		// The handler is responsible for managing the WebSocket communication
		return handler(ws)
	})
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

	urlWithoutPath := tURL.Scheme + "://" + tURL.Host
	// We will not map to the level of the query string // qry := tURL.RawQuery

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

	hdlr := func(ctx Context) (err error) {
		ctxReq := ctx.Request()

		// Get the request path minus the prefix, then add back the prefix and the targetPath, minus any dropped tokens
		pathWoPrefix := ctxReq.Path()
		if idx := strings.Index(ctxReq.Path(), pathPrefix); idx >= 0 {
			pathWoPrefix = pathWoPrefix[idx+len(pathPrefix):]
		}

		proxyURL := urlWithoutPath + filepath.Join("/", strippedPrefix, tURL.Path, pathWoPrefix)

		if qry := ctxReq.Query(); qry != "" {
			proxyURL = proxyURL + "?" + qry
		}

		if s.options.Verbose {
			fmt.Printf("PROXY %q -> %q\n", ctxReq.Path(), proxyURL)
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

		if s.options.ReadyChan != nil { // remember to nil check!
			select {
			// Let the caller know we are running
			case s.options.ReadyChan <- struct{}{}: // attempt to send to channel
			default: // Don't block if out can't receive for some reason
			}
		}

		for { // maybe TODO optional graceful shutdown based on SIGTERM
			conn, err := listener.Accept() // accept next client connection
			if err != nil {
				if s.options.Debug { // TODO: check deeper into "use of closed network connection"
					fmt.Println("Error accepting connection:", err)
				}
				continue
			}
			// fmt.Printf("** Connection established: %s <-- %s\n", conn.LocalAddr(), conn.RemoteAddr())

			// Each connection separately bc a copy is passed in
			go s.handleConnection(conn)
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

// Group creates a new route group with the given prefix and optional middleware.
// Groups allow organizing routes under a common URL prefix and applying middleware
// that only affects routes within the group.
// Example: api := s.Group("/api", authMiddleware) creates a group where all routes
// will be prefixed with "/api" and use the authMiddleware.
// Groups can be nested: v1 := api.Group("/v1") creates "/api/v1" prefix.
func (s *Server) Group(prefix string, handlers ...Handler) *Group {
	return &Group{
		prefix:   prefix,
		server:   s,
		handlers: handlers,
	}
}

// handleConnection handles an accepted connection.
func (s *Server) handleConnection(conn net.Conn) {
	var method, url string
	var ctx = s.contextPool.Get().(*context) // get a new context from the pool

	ctx.reader.Reset(conn) // prepare to read from the accepted connection
	ctx.conn = conn        // store connection for WebSocket upgrades

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
			fmt.Println(strings.Repeat("-", 64))
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

		// If the connection was upgraded to WebSocket, exit the HTTP loop
		if ctx.wsUpgraded {
			// The WebSocket handler is responsible for managing the connection now
			return
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
	// Handlers populate the context, before the response is written
	err := s.handlers[0](ctx)
	if err != nil {
		s.errorHandler(ctx, err)
	}

	s.writeResponse(ctx, respWriter)
}

// writeWebSocketUpgradeResponse writes the WebSocket upgrade response immediately
func (s *Server) writeWebSocketUpgradeResponse(ctx *context, respWriter io.Writer) {
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

	// Write headers
	for _, header := range ctx.response.headers {
		tmp.WriteString(header.Key)
		tmp.WriteString(consts.ColonSpace)
		tmp.WriteString(header.Value)
		tmp.WriteString(consts.CRLF)
	}
	tmp.WriteString(consts.CRLF)

	// Write the upgrade response immediately
	_, _ = respWriter.Write(tmp.Bytes())
}

func (s *Server) writeResponse(ctx *context, respWriter io.Writer) {
	// Skip normal response writing if connection was upgraded to WebSocket
	// The upgrade response has already been written in UpgradeWebSocket()
	if ctx.wsUpgraded {
		return
	}

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

	if ctx.sseEventsChan == nil { // For SSE -- don't set content-length
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

	// Write headers to the response writer
	_, err := respWriter.Write(tmp.Bytes())
	if err != nil {
		fmt.Println("Error writing headers: ", err)
	}

	// Body
	if ctx.sseEventsChan == nil {
		_, _ = respWriter.Write(ctx.response.body)
	} else {
		// fmt.Println("RWEB: SSE events channel is set -- sending events")
		err = s.sendSSE(ctx, respWriter)
		if err != nil {
			fmt.Println("Error sending SSE events: ", err)
		}
	}
}

func (s *Server) sendSSE(ctx *context, respWriter io.Writer) (err error) {
	// Send a connect event -- not required per SSE standard, but may be helpful
	if s.options.SSECfg.SendConnectedEvent {
		_, err = fmt.Fprint(respWriter, "event: message\ndata: Connected\n\n")
		if err != nil {
			fmt.Println("Error writing connect message: ", err)
			// carry on anyway
		}
	}

	rw := bufio.NewWriter(respWriter)

	if ctx.sseEventName == "" {
		ctx.sseEventName = "message" // Default
	}

	if s.options.Verbose {
		fmt.Printf("RWEB Serving SSE %q events from channel: %v...\tStatus code: %d\n",
			ctx.sseEventName, ctx.sseEventsChan, ctx.status)
	}

	// Event Loop - until the input channel is closed or we exit
	for {
		select {
		case event, ok := <-ctx.sseEventsChan:
			if !ok {
				fmt.Println("SSE Channel closed and drained, let's clean up and exit...")
				_ = rw.Flush()
				return
			}

			// fmt.Printf("RWEB Received from SSE source: %v\n", event)

			if strEvt, ok := event.(string); ok {
				if strEvt == "" {
					// fmt.Println("RWEB Received empty string event, skipping...")
					continue
				}
				if strEvt == "close" {
					fmt.Printf("RWEB Received close event, shutting down SSE %q events from channel: %v...\n",
						ctx.sseEventName, ctx.sseEventsChan)
					rw.Flush()
					return
				}
			}

			// Format and send the event
			switch v := event.(type) {
			case SSEvent: // get the eventName from the data (rweb.SSEvent) received
				_, err = fmt.Fprintf(rw, "event: %s\ndata: %s\n\n", v.Type, v.Data)
			case string:
				_, err = fmt.Fprintf(rw, "event: %s\ndata: %s\n\n", ctx.sseEventName, v)
			default:
				_, err = fmt.Fprintf(rw, "event: %s\ndata: %+v\n\n", ctx.sseEventName, v)
			}

			if err != nil {
				fmt.Printf("Error writing SSE event from channel %v: %v\n", ctx.sseEventsChan, err)
				rw.Reset(respWriter) // Reset the buffer for the next event
				continue
			}

			err = rw.Flush() // Flush the buffer to send data immediately
			if err != nil {
				fmt.Printf("Error flushing SSE output from channel %v: %v\n", ctx.sseEventsChan, err)
				rw.Reset(respWriter) // Reset the buffer for the next event
				return err
			}

			if s.options.Verbose {
				fmt.Printf("RWEB Sent (from channel: %v) event: %s\n", ctx.sseEventsChan, event)
			}
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
		data: make(map[string]any),
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

// ElementDebugRoutes adds debug routes for the element package under the /debug prefix.
// This is a convenience method that sets up routes to enable/disable debug mode and view
// collected HTML generation issues. Debug mode helps identify unclosed tags, unpaired attributes,
// and other HTML generation problems by adding data-ele-id attributes to elements.
//
// Routes added:
//   - GET /debug/set - Enable debug mode
//   - GET /debug/show - Display collected issues in formatted table
//   - GET /debug/clear - Disable debug mode and clear all issues
//   - GET /debug/clear-issues - Clear issues but keep debug mode active
//
// Example usage:
//
//	s := rweb.NewServer()
//	s.ElementDebugRoutes()
func (s *Server) ElementDebugRoutes() {
	// Group debug routes under /debug prefix for cleaner URL organization
	debugGrp := s.Group("/debug")

	// Enable debug mode - this adds data-ele-id attributes to elements
	// and tracks any HTML generation issues. After enabling, refresh any page
	// you want to check, then visit /debug/show to see collected issues
	debugGrp.Get("/set", func(c Context) error {
		element.DebugSet()
		return c.WriteHTML("<h3>Debug mode is set.</h3> <a href='/'>Home</a>&nbsp;&nbsp;|&nbsp;&nbsp;<a href='/debug/show'>Show Issues</a>")
	})

	// Display collected issues in a formatted table with HTML and Markdown views
	// This shows any HTML generation problems detected while debug mode was active
	debugGrp.Get("/show", func(c Context) error {
		err := c.WriteHTML(element.DebugShow())
		return err
	})

	// Disable debug mode completely (stops tracking and clears all issues)
	debugGrp.Get("/clear", func(c Context) error {
		element.DebugClear()
		return c.WriteHTML("<h3>Issues are cleared and Debug mode is off.</h3> <a href='/'>Home</a>")
	})

	// Clear collected issues but keep debug mode active for continued tracking
	debugGrp.Get("/clear-issues", func(c Context) error {
		element.DebugClearIssues()
		return c.WriteHTML("<h3>Issues cleared (debug mode still active).</h3> <a href='/'>Home</a>&nbsp;&nbsp;|&nbsp;&nbsp;<a href='/debug/show'>Show Issues</a>")
	})
}
