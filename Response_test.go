package rweb_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestWrite(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		_, err := ctx.Response().Write([]byte("Hello"))
		return err
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Hello")
}

func TestWriteString(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		_, err := io.WriteString(ctx.Response(), "Hello")
		return err
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Hello")
}

func TestResponseCompression(t *testing.T) {
	s := rweb.NewServer()
	uncompressed := bytes.Repeat([]byte("This text should be compressed to a size smaller than the original."), 5)

	s.Use(func(ctx rweb.Context) error {
		defer func() {
			body := ctx.Response().Body()
			ctx.Response().SetBody(nil)
			zip := gzip.NewWriter(ctx.Response())
			_, _ = zip.Write(body)
			_ = zip.Close()
		}()

		return ctx.Next()
	})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Bytes(uncompressed)
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.True(t, len(response.Body()) < len(uncompressed))

	reader, err := gzip.NewReader(bytes.NewReader(response.Body()))
	assert.Nil(t, err)

	decompressed, err := io.ReadAll(reader)
	assert.Nil(t, err)
	assert.DeepEqual(t, decompressed, uncompressed)
}

func TestResponseHeader(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("Content-Type", "text/plain")
		contentType := ctx.Response().Header("Content-Type")
		return ctx.String(contentType)
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, response.Header("Content-Type"), "text/plain")
	assert.Equal(t, response.Header("Non existent header"), "")
	assert.Equal(t, string(response.Body()), "text/plain")
}

func TestResponseHeaderOverwrite(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("Content-Type", "text/plain")
		ctx.Response().SetHeader("Content-Type", "text/html")
		return nil
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, response.Header("Content-Type"), "text/html")
	assert.Equal(t, string(response.Body()), "")
}
