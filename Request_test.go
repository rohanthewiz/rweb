package rweb_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestRequest(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/request", func(ctx rweb.Context) error {
		req := ctx.Request()
		method := req.Method()
		scheme := req.Scheme()
		host := req.Host()
		path := req.Path()
		return ctx.WriteString(fmt.Sprintf("%s %s %s %s", method, scheme, host, path))
	})

	response := s.Request(consts.MethodGet, "http://example.com/request?x=1", []rweb.Header{{"Accept", "*/*"}}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "GET http example.com /request")
}

func TestRequestHeader(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		accept := ctx.Request().Header("Accept")
		empty := ctx.Request().Header("")
		return ctx.WriteString(accept + empty)
	})

	response := s.Request(consts.MethodGet, "/", []rweb.Header{{"Accept", "*/*"}}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "*/*")
}

func TestRequestHeaderLowercaseFallback(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		// Test case-sensitive match (priority)
		contentType := ctx.Request().Header("Content-Type")
		// Test lowercase fallback when exact match not found
		accept := ctx.Request().Header("Accept")
		return ctx.WriteString(contentType + "|" + accept)
	})

	// Headers stored in lowercase - should still match when queried with mixed case
	response := s.Request(consts.MethodGet, "/", []rweb.Header{
		{"content-type", "application/json"},
		{"accept", "text/html"},
	}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "application/json|text/html")

	// Test exact case-sensitive match takes priority
	s2 := rweb.NewServer()
	s2.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().Header("X-Custom"))
	})

	response2 := s2.Request(consts.MethodGet, "/", []rweb.Header{{"X-Custom", "exact"}}, nil)
	assert.Equal(t, response2.Status(), 200)
	assert.Equal(t, string(response2.Body()), "exact")
}

func TestRequestParam(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/blog/:article", func(ctx rweb.Context) error {
		article := ctx.Request().Param("article")
		empty := ctx.Request().Param("")
		return ctx.WriteString(article + empty)
	})

	response := s.Request(consts.MethodGet, "/blog/my-article", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "my-article")
}

// TestGetFormFiles exercises the new multi-file accessor end-to-end. The
// synthetic s.Request() path doesn't pipe the body through to the multipart
// parser, so we use a real HTTP server here. Three files are uploaded under
// the same form key to confirm we see all of them (not just the first, which
// is what GetFormFile returns) and that filenames + contents survive intact.
func TestGetFormFiles(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	type result struct {
		count    int
		names    []string
		contents []string
		err      string
	}
	var got atomic.Pointer[result]

	s.Post("/upload", func(ctx rweb.Context) error {
		files, err := ctx.Request().GetFormFiles("attachments")
		r := result{}
		if err != nil {
			r.err = err.Error()
		}
		for _, fh := range files {
			r.count++
			r.names = append(r.names, fh.Filename)
			f, oerr := fh.Open()
			if oerr != nil {
				r.err = oerr.Error()
				continue
			}
			body, _ := io.ReadAll(f)
			_ = f.Close()
			r.contents = append(r.contents, string(body))
		}
		got.Store(&r)
		return ctx.NoContent()
	})

	go func() {
		defer close(clientDone)
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		// Build a real multipart body: two attachments under the same key,
		// plus a normal text field, plus an unrelated file under a different
		// key (which GetFormFiles("attachments") must NOT return).
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("vehicle", "car")

		w1, err := mw.CreateFormFile("attachments", "first.txt")
		assert.Nil(t, err)
		_, _ = w1.Write([]byte("alpha"))

		w2, err := mw.CreateFormFile("attachments", "second.txt")
		assert.Nil(t, err)
		_, _ = w2.Write([]byte("beta"))

		w3, err := mw.CreateFormFile("attachments", "third.txt")
		assert.Nil(t, err)
		_, _ = w3.Write([]byte("gamma"))

		// Sanity decoy under a different key
		wOther, err := mw.CreateFormFile("avatar", "ignored.png")
		assert.Nil(t, err)
		_, _ = wOther.Write([]byte("decoy"))

		assert.Nil(t, mw.Close())

		url := fmt.Sprintf("http://127.0.0.1:%s/upload", s.GetListenPort())
		resp, err := http.Post(url, mw.FormDataContentType(), &buf)
		assert.Nil(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, int(resp.StatusCode), int(consts.StatusNoContent))

		r := got.Load()
		if r == nil {
			t.Error("handler did not capture any result")
			return
		}
		if r.err != "" {
			t.Errorf("handler reported error: %s", r.err)
			return
		}
		assert.Equal(t, r.count, 3)
		assert.Equal(t, strings.Join(r.names, ","), "first.txt,second.txt,third.txt")
		assert.Equal(t, strings.Join(r.contents, ","), "alpha,beta,gamma")
	}()

	_ = s.Run()
	<-clientDone
}

// TestGetFormFilesNotPresent confirms the error paths: the missing-key case
// returns an error rather than silently returning nil/empty.
func TestGetFormFilesNotPresent(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	var sawErr atomic.Bool
	s.Post("/upload", func(ctx rweb.Context) error {
		files, err := ctx.Request().GetFormFiles("nope")
		if err != nil && len(files) == 0 {
			sawErr.Store(true)
		}
		return ctx.NoContent()
	})

	go func() {
		defer close(clientDone)
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		w, err := mw.CreateFormFile("attachments", "ok.txt")
		assert.Nil(t, err)
		_, _ = w.Write([]byte("hello"))
		assert.Nil(t, mw.Close())

		url := fmt.Sprintf("http://127.0.0.1:%s/upload", s.GetListenPort())
		resp, err := http.Post(url, mw.FormDataContentType(), &buf)
		assert.Nil(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, sawErr.Load(), true)
	}()

	_ = s.Run()
	<-clientDone
}

func TestUserAgent(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		userAgent := ctx.UserAgent()
		return ctx.WriteString(userAgent)
	})

	// Test with standard User-Agent header
	response := s.Request(consts.MethodGet, "/", []rweb.Header{{"User-Agent", "Mozilla/5.0"}}, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Mozilla/5.0")

	// Test with lowercase user-agent header (case-insensitive matching)
	response2 := s.Request(consts.MethodGet, "/", []rweb.Header{{"user-agent", "Chrome/100.0"}}, nil)
	assert.Equal(t, response2.Status(), 200)
	assert.Equal(t, string(response2.Body()), "Chrome/100.0")

	// Test with User-Agent header absent (should return empty string)
	response3 := s.Request(consts.MethodGet, "/", []rweb.Header{}, nil)
	assert.Equal(t, response3.Status(), 200)
	assert.Equal(t, string(response3.Body()), "")
}
