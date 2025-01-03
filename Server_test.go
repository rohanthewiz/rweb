package rweb_test

import (
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
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

	s.Request("GET", "/panic", nil, nil)
}

func TestRun(t *testing.T) {
	s := rweb.NewServer()

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		_, err := http.Get("http://127.0.0.1:8080/")
		assert.Nil(t, err)
	}()

	s.Run(":8080")
}

func TestBadRequest(t *testing.T) {
	s := rweb.NewServer()

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		conn, err := net.Dial("tcp", ":8080")
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "BadRequest\r\n\r\n")
		assert.Nil(t, err)

		response, err := io.ReadAll(conn)
		assert.Nil(t, err)
		assert.Equal(t, string(response), "HTTP/1.1 400 Bad Request\r\n\r\n")
	}()

	s.Run(":8080")
}

func TestBadRequestHeader(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.String("Hello")
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		conn, err := net.Dial("tcp", ":8080")
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nBadHeader\r\nGood: Header\r\n\r\n")
		assert.Nil(t, err)

		buffer := make([]byte, len("HTTP/1.1 200"))
		_, err = conn.Read(buffer)
		assert.Nil(t, err)
		assert.Equal(t, string(buffer), "HTTP/1.1 200")
	}()

	s.Run(":8080")
}

func TestBadRequestMethod(t *testing.T) {
	s := rweb.NewServer()

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		conn, err := net.Dial("tcp", ":8080")
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "BAD-METHOD / HTTP/1.1\r\n\r\n")
		assert.Nil(t, err)

		response, err := io.ReadAll(conn)
		assert.Nil(t, err)
		assert.Equal(t, string(response), "HTTP/1.1 400 Bad Request\r\n\r\n")
	}()

	s.Run(":8080")
}

func TestBadRequestProtocol(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.String("Hello")
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		conn, err := net.Dial("tcp", ":8080")
		assert.Nil(t, err)
		defer conn.Close()

		_, err = io.WriteString(conn, "GET /\r\n\r\n")
		assert.Nil(t, err)

		buffer := make([]byte, len("HTTP/1.1 200"))
		_, err = conn.Read(buffer)
		assert.Nil(t, err)
		assert.Equal(t, string(buffer), "HTTP/1.1 200")
	}()

	s.Run(":8080")
}

func TestEarlyClose(t *testing.T) {
	s := rweb.NewServer()

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		conn, err := net.Dial("tcp", ":8080")
		assert.Nil(t, err)

		_, err = io.WriteString(conn, "GET /\r\n")
		assert.Nil(t, err)

		err = conn.Close()
		assert.Nil(t, err)
	}()

	s.Run(":8080")
}

func TestUnavailablePort(t *testing.T) {
	listener, err := net.Listen("tcp", ":8080")
	assert.Nil(t, err)
	defer listener.Close()

	s := rweb.NewServer()
	s.Run(":8080")
}
