package rweb_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// multipartUpload builds a body with one 64 KB file part and one value part,
// returning the body and its Content-Type (with boundary).
func multipartUpload(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "big.bin")
	assert.Nil(t, err)
	_, _ = fw.Write(bytes.Repeat([]byte("x"), 64<<10))
	assert.Nil(t, w.WriteField("name", "lazy"))
	assert.Nil(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

// tempEntries counts what ReadForm has spilled into the temp dir.
func tempEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	assert.Nil(t, err)
	return len(entries)
}

// A multipart body is parsed only when the handler asks for the form. With a
// 1 KB memory cap a 64 KB file part spills to a temp file on parse, so the
// temp dir shows whether a parse ran. Before the parse was made lazy, the
// server parsed every multipart body ahead of routing, so the unrouted and
// form-ignoring cases below spilled too.
func TestMultipartParsedOnlyOnUse(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // os.TempDir, and so ReadForm's spill files, use it

	s := rweb.NewServerWithOptions(rweb.WithMultipartMaxMemory(1 << 10))

	// Runs after routing and the handler, but before the request's cleanup
	// removes temp files, so it sees whatever the request spilled.
	spilled := -1
	s.Use(func(ctx rweb.Context) error {
		err := ctx.Next()
		spilled = tempEntries(t, tmp)
		return err
	})
	s.Post("/ignores-form", func(ctx rweb.Context) error {
		return ctx.WriteString("ignored")
	})
	s.Post("/reads-form", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().FormValue("name"))
	})
	s.Post("/reads-file", func(ctx rweb.Context) error {
		_, hdr, err := ctx.Request().GetFormFile("file")
		if err != nil {
			return err
		}
		return ctx.WriteString(hdr.Filename)
	})

	body, ct := multipartUpload(t)
	post := func(path string) rweb.Response {
		return s.Request("POST", path, []rweb.Header{{Key: "Content-Type", Value: ct}}, bytes.NewReader(body))
	}

	// No parse: the path is unrouted, or the handler never reads the form.
	for _, path := range []string{"/nowhere", "/ignores-form"} {
		post(path)
		assert.Equal(t, 0, spilled)
	}

	// Parse on use: the form is there, and the large part spilled as before.
	resp := post("/reads-form")
	assert.Equal(t, "lazy", string(resp.Body()))
	if spilled == 0 {
		t.Fatal("reading the form did not spill the 64 KB part; the test cannot see a parse")
	}

	resp = post("/reads-file")
	assert.Equal(t, 200, resp.Status())
	assert.Equal(t, "big.bin", string(resp.Body()))
	// Cleanup of the spill files is checked over the wire in
	// TestMultipartLazyParseCleanedUp: synthetic s.Request() never runs the
	// per-request Clean, since it hands back the context's own Response.
}

// A form parsed lazily by the handler still has its spill files removed by
// the per-request cleanup on a real connection.
func TestMultipartLazyParseCleanedUp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	readyChan := make(chan struct{}, 1)
	s := rweb.NewServerWithOptions(
		rweb.WithMultipartMaxMemory(1<<10),
		rweb.WithReadyChan(readyChan),
		rweb.WithAddress("localhost:"),
	)
	// Atomic: written by the server's connection goroutine, read by the
	// client goroutine, and only the socket orders the two.
	var spilled atomic.Int64
	s.Post("/reads-file", func(ctx rweb.Context) error {
		_, hdr, err := ctx.Request().GetFormFile("file")
		if err != nil {
			return err
		}
		spilled.Store(int64(tempEntries(t, tmp)))
		return ctx.WriteString(hdr.Filename)
	})

	go func() {
		defer func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
		<-readyChan

		body, ct := multipartUpload(t)
		resp, err := http.Post("http://127.0.0.1:"+s.GetListenPort()+"/reads-file", ct, bytes.NewReader(body))
		assert.Nil(t, err)
		got, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, "big.bin", string(got))
		if spilled.Load() == 0 {
			t.Error("the 64 KB part did not spill; the test cannot see cleanup")
		}

		// Clean runs just after the response is written, so poll briefly.
		deadline := time.Now().Add(2 * time.Second)
		for tempEntries(t, tmp) != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := tempEntries(t, tmp); n != 0 {
			t.Errorf("%d temp file(s) left after the request", n)
		}
	}()

	_ = s.Run()
}
