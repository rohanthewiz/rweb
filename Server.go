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

// Server is the HTTP Server.
type Server struct {
	handlers     []Handler
	contextPool  sync.Pool
	radixRouter  *rtr.RadixRouter[Handler]
	hashRouter   *rtr.HashRouter[Handler]
	errorHandler func(Context, error)
}

// NewServer creates a new HTTP server.
func NewServer() *Server {
	radRtr := &rtr.RadixRouter[Handler]{}
	hashRtr := rtr.NewHashRouter[Handler]()

	s := &Server{
		radixRouter: radRtr,
		hashRouter:  hashRtr,

		handlers: []Handler{
			func(c Context) error { // default handler
				ctx := c.(*context)
				var hdlr Handler

				// Try exact match first
				hdlr = hashRtr.Lookup(ctx.request.method, ctx.request.path)
				if hdlr == nil {
					hdlr = radRtr.LookupNoAlloc(ctx.request.method, ctx.request.path, ctx.request.addParameter)
				}

				if hdlr == nil {
					ctx.SetStatus(consts.StatusNotFound)
					return nil
				}

				return hdlr(c)
			},
		},
		errorHandler: func(ctx Context, err error) {
			log.Println(ctx.Request().Path(), err)
		},
	}

	s.contextPool.New = func() any { return s.newContext() }
	return s
}

// Get registers your function to be called when the given GET path has been requested.
func (s *Server) Get(path string, handler Handler) {
	if strings.IndexByte(path, consts.RuneColon) < 0 {
		s.hashRouter.Add(consts.MethodGet, path, handler)
	} else {
		s.radixRouter.Add(consts.MethodGet, path, handler)
	}
}

// Post registers your function to be called when the given POST path has been requested.
func (s *Server) Post(path string, handler Handler) {
	if strings.IndexByte(path, consts.RuneColon) < 0 {
		s.hashRouter.Add(consts.MethodPost, path, handler)
	} else {
		s.radixRouter.Add(consts.MethodPost, path, handler)
	}
}

// Put registers your function to be called when the given PUT path has been requested.
func (s *Server) Put(path string, handler Handler) {
	if strings.IndexByte(path, consts.RuneColon) < 0 {
		s.hashRouter.Add(consts.MethodPut, path, handler)
	} else {
		s.radixRouter.Add(consts.MethodPut, path, handler)
	}
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

type RunOpts struct {
	Verbose bool
	// StatusChan is a channel signalling that the server is about to enter its listen loop
	// It should be a buffered chan (cap 1 is all that is needed), so the server will not hang
	StatusChan chan struct{}
}

// Run starts the server on the given address.
func (s *Server) Run(address string, runOpts ...RunOpts) error {
	opts := RunOpts{}

	if len(runOpts) == 1 {
		opts.Verbose = runOpts[0].Verbose // Verbose

		// Running Channel
		if runOpts[0].StatusChan != nil && cap(runOpts[0].StatusChan) < 1 && opts.Verbose {
			fmt.Println("Running channel capacity should be at least 1, or we may hang")
		}
		// Assign even if it is nil as we will do nil check on use
		opts.StatusChan = runOpts[0].StatusChan
	}

	listener, err := net.Listen(consts.ProtocolTCP, address)
	if err != nil {
		return err
	}

	defer listener.Close()

	go func() {
		if opts.StatusChan != nil { // don't forget nil check!
			opts.StatusChan <- struct{}{} // Let the caller know we are running
		}

		if opts.Verbose {
			fmt.Printf("Server is running at %s\n", address)
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

/*// Router returns the router used by the server.
func (s *Server) Router(path string) *rtr.RadRouter[Handler] {
	return s.radRouter
}
*/

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
	defer s.contextPool.Put(ctx)

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
			ctx.request.body = append(ctx.request.body, body...)

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

		fmt.Printf("**-> ctx.request.body: %q\n", string(ctx.request.body))

		// Handle the request
		s.handleRequest(ctx, method, url, conn)
		// fmt.Println("**-> Request Handled.")
		fmt.Printf("**-> ctx %#v\n", ctx)
		fmt.Println(strings.Repeat("-", 20))

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
	ctx.method = method
	ctx.scheme, ctx.host, ctx.path, ctx.query = parseURL(url)

	// Parse Post Args
	if len(ctx.request.body) > 0 {
		// if bytes.EqualFold(ctx.ContentType, consts.StrFormData) {
		fmt.Println("**-> Parsing Post Args")
		ctx.request.parsePostArgs()
		// }
	}

	fmt.Println("** Post Args -->", ctx.request.postArgs.String())

	// Call the Request handler
	err := s.handlers[0](ctx)
	if err != nil {
		s.errorHandler(ctx, err)
	}

	tmp := bytes.Buffer{}
	tmp.WriteString("HTTP/1.1 ")
	tmp.WriteString(strconv.Itoa(int(ctx.status)))
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
