package rweb_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
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

		// Use full localhost URL for compatibility
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

	// Testing query string parameters
	const qryParamForGet = "soda"
	s.Get("/products", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().QueryParam(qryParamForGet))
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

		// GET 1
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/", s.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), getMsg)

		// GET 2
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/products?%s=grape", s.GetListenPort(), qryParamForGet))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "grape")

		// POST
		buf := bytes.NewReader([]byte("abc=123&def=456"))

		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s", s.GetListenPort()),
			string(consts.BytFormData), buf)
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "456")

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

// TestStaticFilesPathTraversal asserts that StaticFiles refuses requests whose
// wildcard path contains `..` segments — directly or percent-encoded — and
// that legitimate requests still resolve. Regression guard: prior to the
// containment fix, `filepath.Join` would normalize `..` and let a request
// like /static/../sentinel.txt escape the configured root.
func TestStaticFilesPathTraversal(t *testing.T) {
	// StaticFiles serves relative to the process CWD (it reads via "." + path),
	// so we hop CWD into a temp dir for the duration of the test and restore
	// it on exit. Using a sibling layout: <tmp>/safe/ is the served root,
	// <tmp>/secret.txt is the off-root sentinel that must remain unreachable.
	origWD, err := os.Getwd()
	assert.Nil(t, err)
	tmp := t.TempDir()
	assert.Nil(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	const allowedBody = "ok-allowed"
	const secretBody = "off-root-secret"
	assert.Nil(t, os.MkdirAll(filepath.Join(tmp, "safe"), 0o755))
	assert.Nil(t, os.WriteFile(filepath.Join(tmp, "safe", "allowed.txt"),
		[]byte(allowedBody), 0o644))
	assert.Nil(t, os.WriteFile(filepath.Join(tmp, "secret.txt"),
		[]byte(secretBody), 0o644))

	s := rweb.NewServer()
	// Strip the "/static" prefix and serve from "./safe" relative to CWD.
	s.StaticFiles("/static/", "safe", 1)

	// Happy path: legit request resolves and returns content.
	resp := s.Request(consts.MethodGet, "/static/allowed.txt", nil, nil)
	assert.Equal(t, int(200), int(resp.Status()))
	assert.Equal(t, allowedBody, string(resp.Body()))

	// Each of these traversal attempts must NOT return secret content.
	// We accept any non-200 status (404 is what the guard returns) and
	// require the body to never contain the off-root sentinel.
	traversals := []string{
		"/static/../secret.txt",
		"/static/%2E%2E/secret.txt",
		"/static/%2e%2e/secret.txt",
		"/static/foo/../../secret.txt",
		"/static/%2e%2e%2fsecret.txt",
		// NUL byte truncation: some C-string-based filesystems would treat
		// "allowed.txt\x00.evil" as "allowed.txt". We reject NUL outright.
		"/static/allowed.txt%00.evil",
		// Double-slash: attempts to make filepath.Join collapse into an
		// absolute lookup. Containment check must still catch it.
		"/static//etc/passwd",
		// Absolute-path injection (rare but possible if a router decoded the
		// wildcard into a path that begins with `/`).
		"/static/%2fetc/passwd",
	}
	for _, url := range traversals {
		r := s.Request(consts.MethodGet, url, nil, nil)
		if int(r.Status()) == 200 {
			t.Errorf("traversal %q unexpectedly succeeded with 200", url)
		}
		if bytes.Contains(r.Body(), []byte(secretBody)) {
			t.Errorf("traversal %q leaked off-root content", url)
		}
	}
}

func TestUnavailablePort(t *testing.T) {
	const testPort = ":8080"

	listener, err := net.Listen(consts.ProtocolTCP, testPort)
	assert.Nil(t, err)
	defer listener.Close()

	s := rweb.NewServer(rweb.ServerOptions{Verbose: true, Address: testPort})
	_ = s.Run()
}
