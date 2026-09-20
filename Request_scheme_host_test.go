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
