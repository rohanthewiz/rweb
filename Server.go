package rweb

import (
	"bufio"
	"bytes"
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
	radRouter    *rtr.RadRouter[Handler]
	hashRouter   *rtr.HashRouter[Handler]
	errorHandler func(Context, error)
}

// NewServer creates a new HTTP server.
func NewServer() *Server {
	radRtr := &rtr.RadRouter[Handler]{}
	hashRtr := rtr.NewHashRouter[Handler]()

	s := &Server{
		radRouter:  radRtr,
		hashRouter: hashRtr,

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
	if strings.IndexByte(path, ':') < 0 {
		s.hashRouter.Add(consts.MethodGet, path, handler)
	} else {
		s.radRouter.Add(consts.MethodGet, path, handler)
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

// Run starts the server on the given address.
func (s *Server) Run(address string) error {
	listener, err := net.Listen(consts.ProtocolTCP, address)

	if err != nil {
		return err
	}

	defer listener.Close()

	go func() {
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
		message, err := ctx.reader.ReadString('\n')

		if err != nil {
			return
		}

		space := strings.IndexByte(message, ' ')

		if space <= 0 {
			_, _ = io.WriteString(conn, consts.HTTPBadRequest)
			return
		}

		method = message[:space]

		if !isRequestMethod(method) {
			_, _ = io.WriteString(conn, consts.HTTPBadRequest)
			return
		}

		lastSpace := strings.LastIndexByte(message, ' ')

		if lastSpace == space {
			lastSpace = len(message) - len("\r\n")
		}

		url = message[space+1 : lastSpace]

		// Add headers until we meet an empty line
		for {
			message, err = ctx.reader.ReadString('\n')

			if err != nil {
				return
			}

			if message == "\r\n" {
				break
			}

			colon := strings.IndexByte(message, ':')

			if colon <= 0 {
				continue
			}

			key := message[:colon]
			value := message[colon+2 : len(message)-2]

			ctx.request.headers = append(ctx.request.headers, Header{
				Key:   key,
				Value: value,
			})
		}

		// Handle the request
		s.handleRequest(ctx, method, url, conn)

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
