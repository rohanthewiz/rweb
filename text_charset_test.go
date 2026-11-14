package rweb_test

// Tests for N-051: the text write helpers state charset=utf-8, as their docs
// promise, so a client does not have to guess how to decode a body written
// from a Go string. JSON and XML stay bare by design (consts/mime.go).

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

// serveCharsetTest starts s on a loopback port and returns its base URL; the
// server is shut down when the test ends.
func serveCharsetTest(t *testing.T, s *rweb.Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.Nil(t, err)
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return "http://" + ln.Addr().String()
}

// getContentType fetches url and returns every Content-Type value sent (so a
// duplicate would show) and the body.
func getContentType(t *testing.T, url string) ([]string, string) {
	t.Helper()
	resp, err := http.Get(url)
	assert.Nil(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	assert.Nil(t, err)
	return resp.Header.Values("Content-Type"), string(body)
}

// Each text helper sends its media type with charset=utf-8 on the wire, and a
// non-ASCII body arrives byte for byte. Checked with mime.ParseMediaType, the
// way a client reads it, rather than by string equality alone.
func TestTextHelpersSendUTF8Charset(t *testing.T) {
	const body = "café ✓" // two- and three-byte UTF-8 sequences

	s := rweb.NewServer()
	s.Get("/write-html", func(ctx rweb.Context) error { return ctx.WriteHTML(body) })
	s.Get("/write-text", func(ctx rweb.Context) error { return ctx.WriteText(body) })
	s.Get("/write-html-bytes", func(ctx rweb.Context) error { return ctx.WriteHTMLBytes([]byte(body)) })
	s.Get("/write-text-bytes", func(ctx rweb.Context) error { return ctx.WriteTextBytes([]byte(body)) })
	s.Get("/html", func(ctx rweb.Context) error { return rweb.HTML(ctx, body) })
	s.Get("/text", func(ctx rweb.Context) error { return rweb.Text(ctx, body) })
	s.Get("/css", func(ctx rweb.Context) error { return rweb.CSS(ctx, body) })
	s.Get("/js", func(ctx rweb.Context) error { return rweb.JS(ctx, body) })
	s.Get("/csv", func(ctx rweb.Context) error { return rweb.CSV(ctx, body) })
	base := serveCharsetTest(t, s)

	cases := []struct {
		path      string
		mediaType string
		header    string // the exact value, so the constants are pinned too
	}{
		{"/write-html", "text/html", consts.MIMEHTMLUTF8},
		{"/write-text", "text/plain", consts.MIMETextPlainUTF8},
		{"/write-html-bytes", "text/html", consts.MIMEHTMLUTF8},
		{"/write-text-bytes", "text/plain", consts.MIMETextPlainUTF8},
		{"/html", "text/html", consts.MIMEHTMLUTF8},
		{"/text", "text/plain", consts.MIMETextPlainUTF8},
		{"/css", "text/css", consts.MIMECSSUTF8},
		{"/js", "text/javascript", consts.MIMEJavaScriptUTF8},
		{"/csv", "text/csv", consts.MIMECSVUTF8},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			cts, got := getContentType(t, base+c.path)
			assert.Equal(t, 1, len(cts))
			assert.Equal(t, c.header, cts[0])
			mt, params, err := mime.ParseMediaType(cts[0])
			assert.Nil(t, err)
			assert.Equal(t, c.mediaType, mt)
			assert.Equal(t, "utf-8", params["charset"])
			assert.Equal(t, body, got)
		})
	}
}

// JSON and XML carry no charset parameter: RFC 8259 defines none for
// application/json, and on application/xml one would override the
// document's own encoding declaration (RFC 7303 §3.2).
func TestJSONAndXMLStayWithoutCharset(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/write-json", func(ctx rweb.Context) error { return ctx.WriteJSON(map[string]string{"a": "é"}) })
	s.Get("/json", func(ctx rweb.Context) error { return rweb.JSON(ctx, map[string]string{"a": "é"}) })
	s.Get("/xml", func(ctx rweb.Context) error { return rweb.XML(ctx, "<a>é</a>") })
	base := serveCharsetTest(t, s)

	for path, want := range map[string]string{
		"/write-json": consts.MIMEJSON,
		"/json":       consts.MIMEJSON,
		"/xml":        consts.MIMEXML,
	} {
		cts, _ := getContentType(t, base+path)
		assert.Equal(t, 1, len(cts))
		assert.Equal(t, want, cts[0])
	}
}

// A handler that needs another encoding sets Content-Type itself and writes
// with WriteString (or Write), which leave the header alone. WriteHTML after
// such a SetHeader replaces it, leaving one Content-Type line, not two.
func TestTextCharsetHandlerOverride(t *testing.T) {
	const latin1 = "text/html; charset=iso-8859-1"
	s := rweb.NewServer()
	s.Get("/own", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("Content-Type", latin1)
		return ctx.WriteString("caf\xe9") // é in ISO-8859-1
	})
	s.Get("/own-then-write-html", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("content-type", latin1)
		return ctx.WriteHTML("café")
	})
	base := serveCharsetTest(t, s)

	cts, body := getContentType(t, base+"/own")
	assert.Equal(t, 1, len(cts))
	assert.Equal(t, latin1, cts[0])
	assert.Equal(t, "caf\xe9", body)

	cts, _ = getContentType(t, base+"/own-then-write-html")
	assert.Equal(t, 1, len(cts))
	assert.Equal(t, consts.MIMEHTMLUTF8, cts[0])
}

// The default error page is written with WriteHTML, so it carries the charset.
func TestDefaultErrorPageHasCharset(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/boom", func(ctx rweb.Context) error { return errors.New("boom") })
	base := serveCharsetTest(t, s)

	cts, _ := getContentType(t, base+"/boom")
	assert.Equal(t, 1, len(cts))
	assert.Equal(t, consts.MIMEHTMLUTF8, cts[0])
}
