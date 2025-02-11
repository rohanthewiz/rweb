package rweb_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

const (
	HTTP11OK = "HTTP/1.1 200"
)

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

func TestGet(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	const msg = "You pinged root"
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString(msg)
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), msg)
	}()

	_ = s.Run()
}

func TestNoRoutes(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, "404 Not Found", resp.Status)
	}()

	_ = s.Run()
}

func TestPost(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	s.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().GetPostValue("def"))
	})

	s.ListRoutes()

	go func() {
		defer func() {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()

		<-readyChan // wait for server

		buf := bytes.NewReader([]byte("abc=123&def=456"))

		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort()),
			string(consts.BytFormData), buf)
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "456")
	}()

	_ = s.Run() // run with high-order port
}

func TestMultipleRequests(t *testing.T) {
	readyChan := make(chan struct{}, 1)

	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	const getMsg = "Get root"
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString(getMsg)
	})

	s.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().GetPostValue("def"))
	})

	s.Post("/comment", func(ctx rweb.Context) error {
		return ctx.WriteString(string(ctx.Request().Body()))
	})

	s.ListRoutes()

	go func() {
		defer func() {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()

		<-readyChan // wait for server

		// POST
		buf := bytes.NewReader([]byte("abc=123&def=456"))

		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort()),
			string(consts.BytFormData), buf)
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "456")

		// GET
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), getMsg)

		// POST comment
		jBody := []byte(`{"key": "value", "count": 20}`)
		buf.Reset(jBody)
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/comment", s.GetListenPort()),
			string(consts.BytJSONData), buf)
		assert.Nil(t, err)
		assert.Equal(t, consts.OK200, resp.Status)

		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(jBody), string(body))
	}()

	_ = s.Run()
}

func TestBadRequest(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server
		addr := fmt.Sprintf(":%s", s.GetListenPort())
		conn, err := net.Dial(consts.ProtocolTCP, addr) // addr here is ":port" only
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "BadRequest\r\n\r\n")
		assert.Nil(t, err)

		response, err := io.ReadAll(conn)
		assert.Nil(t, err)
		assert.Equal(t, string(response), consts.HTTPBadRequest)
	}()

	_ = s.Run()
}

func TestBadRequestHeader(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello")
	})
	s.ListRoutes()

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, fmt.Sprintf(":%s", s.GetListenPort()))
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nBadHeader\r\nGood: Header\r\n\r\n")
		assert.Nil(t, err)

		buffer := make([]byte, len(HTTP11OK))
		_, err = conn.Read(buffer)
		assert.Nil(t, err)
		assert.Equal(t, string(buffer), HTTP11OK)
	}()

	_ = s.Run()
}

func TestBadRequestMethod(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan, Address: "localhost:"})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, fmt.Sprintf(":%s", s.GetListenPort()))
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, consts.HTTPBadMethod)
		assert.Nil(t, err)

		response, err := io.ReadAll(conn)
		assert.Nil(t, err)
		assert.Equal(t, string(response), consts.HTTPBadMethod)
	}()

	_ = s.Run()
}

func TestBadRequestProtocol(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello")
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, fmt.Sprintf(":%s", s.GetListenPort()))
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "GET /\r\n\r\n")
		assert.Nil(t, err)

		buffer := make([]byte, len(HTTP11OK))
		_, err = conn.Read(buffer)
		assert.Nil(t, err)
		assert.Equal(t, string(buffer), HTTP11OK)
	}()

	_ = s.Run()
}

func TestEarlyClose(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: readyChan})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server
		conn, err := net.Dial(consts.ProtocolTCP, fmt.Sprintf(":%s", s.GetListenPort()))
		assert.Nil(t, err)

		_, err = io.WriteString(conn, "GET /\r\n")
		assert.Nil(t, err)

		err = conn.Close()
		assert.Nil(t, err)
	}()

	_ = s.Run()
}

func TestUnavailablePort(t *testing.T) {
	const testPort = ":8080"

	listener, err := net.Listen(consts.ProtocolTCP, testPort)
	assert.Nil(t, err)
	defer listener.Close()

	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, Address: testPort})
	_ = s.Run()
}
