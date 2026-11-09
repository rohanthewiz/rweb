package rweb_test

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

// Scheme() for a synthetic request: an absolute-form target wins, and an
// origin-form one — which has no connection to inspect — reports http.
func TestRequestSchemeSynthetic(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/scheme", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().Scheme())
	})

	r := s.Request(consts.MethodGet, "/scheme", nil, nil)
	assert.Equal(t, string(r.Body()), "http")

	r = s.Request(consts.MethodGet, "https://example.com/scheme", nil, nil)
	assert.Equal(t, string(r.Body()), "https")
}

// Over a plain TCP connection Scheme() is http. The same connection is then
// reused for a second request, which must still see its net.Conn: Clean()
// between keep-alive requests used to leave ctx.conn nil from the second
// request on.
func TestRequestSchemePlainAndKeepAliveConn(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/info", func(ctx rweb.Context) error {
		return ctx.WriteString(fmt.Sprintf("scheme=%s conn=%t", ctx.Request().Scheme(), ctx.GetConn() != nil))
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		rdr := bufio.NewReader(conn)

		for i := 0; i < 2; i++ { // second pass exercises the reused connection
			_, err = io.WriteString(conn, "GET /info HTTP/1.1\r\nHost: example.com\r\n\r\n")
			assert.Nil(t, err)
			resp, err := http.ReadResponse(rdr, nil)
			assert.Nil(t, err)
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			assert.Equal(t, string(body), "scheme=http conn=true")
		}
	}()

	_ = s.Run()
}

// Over a TLS listener an origin-form request reports https.
func TestRequestSchemeTLS(t *testing.T) {
	cert := genSelfSignedCert(t, "scheme-test")
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		TLS: rweb.TLSCfg{
			UseTLS:  true,
			TLSAddr: "localhost:",
			Config:  &tls.Config{Certificates: []tls.Certificate{cert}},
		},
	})
	s.Get("/scheme", func(ctx rweb.Context) error {
		return ctx.WriteString(ctx.Request().Scheme())
	})

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%s/scheme", s.GetListenPort()))
		assert.Nil(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "https")
	}()

	_ = s.Run()
}

// Host header validation happens while reading headers off the wire, so it
// is tested over real connections with hand-written requests.
func TestHostHeaderValidation(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("host=" + ctx.Request().Host())
	})

	cases := []struct {
		name, headers string
		wantStatus    string
	}{
		{"single valid host", "Host: example.com:8080\r\n", "200"},
		{"ipv6 literal", "Host: [::1]:8080\r\n", "200"},
		{"no host at all is tolerated", "", "200"},
		{"two Host headers", "Host: good.com\r\nHost: evil.com\r\n", "400"},
		{"two Host headers, differing case", "Host: good.com\r\nhOsT: evil.com\r\n", "400"},
		{"two identical Host headers", "Host: good.com\r\nHost: good.com\r\n", "400"},
		{"path in host", "Host: example.com/evil\r\n", "400"},
		{"userinfo in host", "Host: user@example.com\r\n", "400"},
		{"space in host", "Host: exa mple.com\r\n", "400"},
	}

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		for _, c := range cases {
			conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
			assert.Nil(t, err)
			_, err = io.WriteString(conn, "GET / HTTP/1.1\r\n"+c.headers+"\r\n")
			assert.Nil(t, err)

			statusLine, err := bufio.NewReader(conn).ReadString('\n')
			assert.Nil(t, err)
			if !strings.Contains(statusLine, c.wantStatus) {
				t.Errorf("%s: status line %q, want %s", c.name, strings.TrimSpace(statusLine), c.wantStatus)
			}
			_ = conn.Close()
		}
	}()

	_ = s.Run()
}

// targetRouteServer registers "/", "/redirect" and "/admin", each answering
// "<route> <scheme> <host> <path> <query>" so a test can see which route the
// target reached and what the parse put in each field. A Use middleware
// records the path for targets no route matches (OPTIONS *).
func targetRouteServer(opts ...rweb.ServerOptions) (*rweb.Server, *string) {
	s := rweb.NewServer(opts...)
	seenPath := new(string)
	s.Use(func(ctx rweb.Context) error {
		*seenPath = ctx.Request().Path()
		return ctx.Next()
	})
	for _, route := range []string{"/", "/redirect", "/admin"} {
		s.Get(route, func(ctx rweb.Context) error {
			r := ctx.Request()
			return ctx.WriteString(fmt.Sprintf("%s %s %s %s %s", route, r.Scheme(), r.Host(), r.Path(), r.Query()))
		})
	}
	return s, seenPath
}

// A "://" in the query string is data, not a scheme: it must not move the
// request to another route or set Host(). parseURL once searched the whole
// target for "://", so "/redirect?url=http://evil.com/admin" was served by
// /admin with Host() "evil.com", while a proxy in front applying path rules
// saw /redirect.
func TestQuerySchemeDoesNotRouteSynthetic(t *testing.T) {
	s, _ := targetRouteServer()

	cases := []struct{ target, want string }{
		// origin-form: no scheme or host detection at all
		{"/redirect?url=http://evil.com/admin", "/redirect http localhost /redirect url=http://evil.com/admin"},
		{"/redirect?next=https://a/b", "/redirect http localhost /redirect next=https://a/b"},
		{"/?u=x://y/admin", "/ http localhost / u=x://y/admin"},
		// absolute-form: scheme and host come only from before the query
		{"http://example.com/redirect?url=http://evil.com/admin", "/redirect http example.com /redirect url=http://evil.com/admin"},
		{"https://example.com/admin", "/admin https example.com /admin "},
		// absolute-form with an empty path: the authority ends at "?", not at
		// the first "/" the query happens to contain
		{"http://example.com?url=http://evil.com/admin", "/ http example.com / url=http://evil.com/admin"},
		{"http://example.com", "/ http example.com / "},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			r := s.Request(consts.MethodGet, c.target, nil, nil)
			assert.Equal(t, r.Status(), 200)
			assert.Equal(t, string(r.Body()), c.want)
		})
	}
}

// The asterisk-form target of "OPTIONS *" (RFC 9112 §3.2.4) is kept as the
// path; it is not mistaken for a host or rewritten to "/".
func TestOptionsAsteriskTargetSynthetic(t *testing.T) {
	s, seenPath := targetRouteServer()
	_ = s.Request(consts.MethodOptions, "*", nil, nil)
	assert.Equal(t, *seenPath, "*")
}

// The same targets over a real connection, where the Host header supplies
// the host for origin-form requests and is ignored for absolute-form ones.
func TestQuerySchemeDoesNotRouteOverWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s, seenPath := targetRouteServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})

	cases := []struct{ target, want string }{
		{"/redirect?url=http://evil.com/admin", "/redirect http good.com /redirect url=http://evil.com/admin"},
		{"/redirect?next=https://a/b", "/redirect http good.com /redirect next=https://a/b"},
		{"http://example.com/redirect?url=http://evil.com/admin", "/redirect http example.com /redirect url=http://evil.com/admin"},
		{"http://example.com?url=http://evil.com/admin", "/ http example.com / url=http://evil.com/admin"},
	}

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer conn.Close()
		rdr := bufio.NewReader(conn)

		for _, c := range cases {
			_, err = io.WriteString(conn, "GET "+c.target+" HTTP/1.1\r\nHost: good.com\r\n\r\n")
			assert.Nil(t, err)
			resp, err := http.ReadResponse(rdr, nil)
			assert.Nil(t, err)
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(body) != c.want {
				t.Errorf("%s: body %q, want %q", c.target, body, c.want)
			}
		}

		// OPTIONS *: no route matches, but the request is parsed with "*"
		// as its path and answered on the same connection.
		_, err = io.WriteString(conn, "OPTIONS * HTTP/1.1\r\nHost: good.com\r\n\r\n")
		assert.Nil(t, err)
		resp, err := http.ReadResponse(rdr, nil)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, *seenPath, "*")
	}()

	_ = s.Run()
}
