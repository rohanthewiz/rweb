package rweb_test

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// A redirect target taken from the query string arrives percent-decoded, so
// "%0d%0a" in it is a real CR LF. Written into the Location header as is, it
// ended the header line and let the client add one of its own: here a
// Set-Cookie that fixes the victim's session id. The value must stay on its
// one line, and a header with an invalid name must not be sent at all.
func TestHeaderInjectionOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/redir", func(ctx rweb.Context) error {
		return ctx.Redirect(302, ctx.Request().QueryParam("to"))
	})
	s.Get("/badkey", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("X-Bad Key", "v")
		ctx.Response().AddHeader("X-Bad:Key", "v")
		ctx.Response().SetHeader("X-Good", "ok")
		return ctx.WriteString("body")
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		rdr := bufio.NewReader(conn)

		_, err = io.WriteString(conn, "GET /redir?to=/x%0d%0aSet-Cookie:%20sid=attacker HTTP/1.1\r\nHost: example.com\r\n\r\n")
		assert.Nil(t, err)
		resp, err := http.ReadResponse(rdr, nil)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, resp.StatusCode, 302)
		assert.Equal(t, resp.Header.Get("Set-Cookie"), "")
		assert.Equal(t, resp.Header.Get("Location"), "/x  Set-Cookie: sid=attacker")

		// Same connection: the response above must have been framed
		// correctly for this one to parse. Go's reader rejects a header
		// line whose name holds a space, so an invalid key that got
		// through shows up as an error here.
		_, err = io.WriteString(conn, "GET /badkey HTTP/1.1\r\nHost: example.com\r\n\r\n")
		assert.Nil(t, err)
		resp, err = http.ReadResponse(rdr, nil)
		assert.Nil(t, err)
		if err != nil {
			return
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "body")
		assert.Equal(t, resp.Header.Get("X-Good"), "ok")
		for k := range resp.Header {
			if k == "X-Bad Key" || k == "X-Bad" {
				t.Errorf("invalid header name sent: %q", k)
			}
		}
	}()

	_ = s.Run()
}
