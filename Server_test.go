package rweb_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
	"time"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

const (
	testPort = ":8080"
	HTTP11OK = "HTTP/1.1 200"
)

var dialDelay = 100 * time.Millisecond

func TestPanic(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/panic", func(ctx rweb.Context) error {
		panic("Something unbelievable happened")
	})

	defer func() {
		r := recover()

		if r == nil {
			t.Error("Didn't panic")
		}
	}()

	s.Request(consts.MethodGet, "/panic", nil, nil)
}

func TestRun(t *testing.T) {
	s := rweb.NewServer()
	runChan := make(chan struct{}, 1)

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-runChan // wait for server

		_, err := http.Get(fmt.Sprintf("http://127.0.0.1%s/", testPort))
		assert.Nil(t, err)
	}()

	s.Run(testPort, rweb.RunOpts{Verbose: true, StatusChan: runChan})
}

func TestBadRequest(t *testing.T) {
	s := rweb.NewServer()
	runChan := make(chan struct{}, 1)

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-runChan // wait for server

		conn, err := net.Dial(consts.ProtocolTCP, testPort)
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "BadRequest\r\n\r\n")
		assert.Nil(t, err)

		response, err := io.ReadAll(conn)
		assert.Nil(t, err)
		assert.Equal(t, string(response), consts.HTTPBadRequest)
	}()

	s.Run(testPort, rweb.RunOpts{Verbose: true, StatusChan: runChan})
}

func TestBadRequestHeader(t *testing.T) {
	s := rweb.NewServer()
	runChan := make(chan struct{}, 1)

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.String("Hello")
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-runChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, testPort)
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nBadHeader\r\nGood: Header\r\n\r\n")
		assert.Nil(t, err)

		buffer := make([]byte, len(HTTP11OK))
		_, err = conn.Read(buffer)
		assert.Nil(t, err)
		assert.Equal(t, string(buffer), HTTP11OK)
	}()

	s.Run(testPort, rweb.RunOpts{Verbose: true, StatusChan: runChan})
}

func TestBadRequestMethod(t *testing.T) {
	s := rweb.NewServer()
	runChan := make(chan struct{}, 1)

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-runChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, testPort)
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, consts.HTTPBadMethod)
		assert.Nil(t, err)

		response, err := io.ReadAll(conn)
		assert.Nil(t, err)
		assert.Equal(t, string(response), consts.HTTPBadMethod)
	}()

	s.Run(testPort, rweb.RunOpts{Verbose: true, StatusChan: runChan})
}

func TestBadRequestProtocol(t *testing.T) {
	s := rweb.NewServer()
	runChan := make(chan struct{}, 1)

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.String("Hello")
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-runChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, testPort)
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "GET /\r\n\r\n")
		assert.Nil(t, err)

		buffer := make([]byte, len(HTTP11OK))
		_, err = conn.Read(buffer)
		assert.Nil(t, err)
		assert.Equal(t, string(buffer), HTTP11OK)
	}()

	s.Run(testPort, rweb.RunOpts{Verbose: true, StatusChan: runChan})
}

func TestEarlyClose(t *testing.T) {
	s := rweb.NewServer()
	runChan := make(chan struct{}, 1)

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-runChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, testPort)
		assert.Nil(t, err)

		_, err = io.WriteString(conn, "GET /\r\n")
		assert.Nil(t, err)

		err = conn.Close()
		assert.Nil(t, err)
	}()

	s.Run(testPort, rweb.RunOpts{Verbose: true, StatusChan: runChan})
}

func TestUnavailablePort(t *testing.T) {
	listener, err := net.Listen(consts.ProtocolTCP, testPort)
	assert.Nil(t, err)
	defer listener.Close()

	s := rweb.NewServer()
	s.Run(testPort)
}
