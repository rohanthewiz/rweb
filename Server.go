package rweb

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

type ServerOptions struct {
	// TODO
	//  - add port here  -- also come up with code for choosing an unused high level port
	//  - add TLS
	Verbose bool
	Debug   bool
	// ReadyChan is a channel signalling that the server is about to enter its listen loop -- effectively running.
	// It should be a buffered chan (cap 1 is all that is needed), so there is no chance the server will hang
	ReadyChan chan struct{}
}

// Server is the HTTP Server.
type Server struct {
	handlers     []Handler
	contextPool  sync.Pool
	radixRouter  *rtr.RadixRouter[Handler]
	hashRouter   *rtr.HashRouter[Handler]
	errorHandler func(Context, error)
	options      ServerOptions
}

// NewServer creates a new HTTP server.
func NewServer(options ...ServerOptions) *Server {
	radRtr := &rtr.RadixRouter[Handler]{}
	hashRtr := rtr.NewHashRouter[Handler]()

	opts := ServerOptions{}
	if len(options) == 1 {
		opts.Verbose = options[0].Verbose // Verbose

		// Running Channel
		if options[0].ReadyChan != nil && cap(options[0].ReadyChan) < 1 && opts.Verbose {
			fmt.Println("Running channel capacity should be at least 1, or we may hang")
		}
		// Assign even if it is nil as we will do nil check on use
		opts.ReadyChan = options[0].ReadyChan
	}

	s := &Server{
		radixRouter: radRtr,
		hashRouter:  hashRtr,
		options:     opts,
		errorHandler: func(ctx Context, err error) {
			log.Println(ctx.Request().Path(), err)
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
					fmt.Println("Route not found in hash router")
				}
				hdlr = radRtr.LookupNoAlloc(ctx.request.method, ctx.request.path, ctx.request.addParameter)
			}

			if hdlr == nil {
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
	if strings.IndexByte(path, consts.RuneColon) < 0 {
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

// Request performs a synthetic request and returns the response.
// This function keeps the response in memory so it's slightly slower than a real request.
// However it is very useful inside tests where you don't want to spin up a real web server.
func (s *Server) Request(method string, url string, headers []Header, body io.Reader) Response {
	ctx := s.newContext()
	ctx.request.headers = headers
	s.handleRequest(ctx, method, url, io.Discard)
	return ctx.Response()
}

// Run starts the server on the given address.
func (s *Server) Run(address string) error {
	listener, err := net.Listen(consts.ProtocolTCP, address)
	if err != nil {
		return err
	}

	defer listener.Close()

	go func() {
		if s.options.Verbose {
			fmt.Printf("Server is running at %s\n", address)
		}

		if s.options.ReadyChan != nil { // don't forget nil check!
			s.options.ReadyChan <- struct{}{} // Let the caller know we are running
		}

		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}

			go s.handleConnection(conn)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	return nil
}

// ListRoutes prints all server routes by method in tabular format.
func (s *Server) ListRoutes() {
	fmt.Println("\n---- Routes (note that routes with params are not listed) ----")
	routesList := s.hashRouter.ListRoutes()
	// TODO can we list routes for radix router? Maybe we will just track the number of Adds

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
	var (
		ctx    = s.contextPool.Get().(*context)
		method string
		url    string
	)

	ctx.reader.Reset(conn)

	defer conn.Close()

	defer func() {
		// Clean up the context
		ctx.request.headers = ctx.request.headers[:0]
		ctx.request.body = ctx.request.body[:0]
		ctx.response.headers = ctx.response.headers[:0]
		ctx.response.body = ctx.response.body[:0]
		ctx.params = ctx.params[:0]
		ctx.handlerCount = 0
		ctx.status = 200

		// Cleanup any multipart form data
		ctx.request.CleanupMultipartForm()
		s.contextPool.Put(ctx)
	}()

	for {
		// Read the HTTP request line
		message, err := ctx.reader.ReadString(consts.RuneNewLine)
		if err != nil {
			return
		}

		space := strings.IndexByte(message, consts.RuneSingleSpace)

		if space <= 0 {
			_, _ = io.WriteString(conn, consts.HTTPBadRequest)
			return
		}

		method = message[:space]

		if !isRequestMethod(method) {
			_, _ = io.WriteString(conn, consts.HTTPBadMethod)
			return
		}

		lastSpace := strings.LastIndexByte(message, consts.RuneSingleSpace)

		if lastSpace == space {
			lastSpace = len(message) - len(consts.CRLF)
		}

		url = message[space+1 : lastSpace]

		var contentLen int64
		var isChunked bool

		// Add headers until we meet an empty line
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
			if strings.EqualFold(key, "Content-Length") {
				contentLen, err = strconv.ParseInt(value, 10, 64)
				if err != nil {
					_, _ = io.WriteString(conn, consts.HTTPBadRequest)
					return
				}
			} else if strings.EqualFold(key, consts.HeaderContentType) {
				ctx.request.ContentType = s2b(value)
			} else if strings.EqualFold(key, "Transfer-Encoding") && strings.Contains(strings.ToLower(value), "chunked") {
				isChunked = true
			}
		}

		// Read the request body if present
		if contentLen > 0 {
			// Fixed-length body
			body := make([]byte, contentLen)
			_, err = io.ReadFull(ctx.reader, body)
			if err != nil {
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

				// Read chunk CRLF
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
		if s.options.Debug {
			fmt.Printf("** ctx -> %#v\n\n", ctx)
		}

		// Clean up the context
		ctx.request.headers = ctx.request.headers[:0]
		ctx.request.body = ctx.request.body[:0]
		ctx.response.headers = ctx.response.headers[:0]
		ctx.response.body = ctx.response.body[:0]
		ctx.params = ctx.params[:0]
		ctx.handlerCount = 0
		ctx.status = 200
	}
}

// handleRequest handles the given request.
func (s *Server) handleRequest(ctx *context, method string, url string, writer io.Writer) {
	fmt.Println("**-> Server Handling Request...")
	ctx.method = method
	ctx.scheme, ctx.host, ctx.path, ctx.query = parseURL(url)
	// if s.options.Debug {
	fmt.Println("**-> ctx.ContentType:", string(ctx.ContentType))
	// }

	// Parse Post Args or Multipart Form
	if len(ctx.request.body) > 0 {
		if bytes.HasPrefix(ctx.ContentType, consts.StrMultipartFormData) {
			if err := ctx.request.ParseMultipartForm(); err != nil {
				fmt.Printf("Error parsing multipart form: %v\n", err)
			} else {
				fmt.Println("**-> Parsed Multipart Form")
			}
		} else if bytes.EqualFold(ctx.ContentType, consts.StrFormData) {
			// fmt.Println("**-> Parsing Post Args")
			ctx.request.parsePostArgs()
			if s.options.Verbose {
				fmt.Println("** Post Args -->", ctx.request.postArgs.String())
			}
		}
	}

	// Call the Request handler
	err := s.handlers[0](ctx)
	if err != nil {
		s.errorHandler(ctx, err)
	}

	tmp := bytes.Buffer{}
	tmp.WriteString("HTTP/1.1 ")
	tmp.WriteString(strconv.Itoa(int(ctx.status)))

	if st, ok := consts.StatusTextFromCode[int(ctx.status)]; ok {
		tmp.WriteByte(consts.RuneSingleSpace)
		tmp.WriteString(st)
	}

	tmp.WriteString("\r\nContent-Length: ")
	tmp.WriteString(strconv.Itoa(len(ctx.response.body)))
	tmp.WriteString("\r\n")

	for _, header := range ctx.response.headers {
		tmp.WriteString(header.Key)
		tmp.WriteString(": ")
		tmp.WriteString(header.Value)
		tmp.WriteString("\r\n")
	}

	tmp.WriteString("\r\n")
	tmp.Write(ctx.response.body)
	writer.Write(tmp.Bytes())
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
