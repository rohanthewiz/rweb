package rweb_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestBytes(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Bytes([]byte("Hello"))
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Hello")
}

func TestString(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello")
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Equal(t, string(response.Body()), "Hello")
}

func TestError(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.SetStatus(401).Error("Not logged in")
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 401)
	// We are return some message to the user now
	// assert.Equal(t, string(response.Body()), "")
}

func TestErrorMultiple(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.SetStatus(401).Error("Not logged in", errors.New("Missing auth token"))
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 401)
	// We are return some message to the user now
	// assert.Equal(t, string(response.Body()), "")
}

func TestRedirect(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.Redirect(301, "/target")
	})

	response := s.Request(consts.MethodGet, "/", nil, nil)
	assert.Equal(t, response.Status(), 301)
	assert.Equal(t, response.Header("Location"), "/target")
}

func TestNoContent(t *testing.T) {
	s := rweb.NewServer()

	s.Delete("/widgets/:id", func(ctx rweb.Context) error {
		return ctx.NoContent()
	})

	response := s.Request(consts.MethodDelete, "/widgets/42", nil, nil)
	assert.Equal(t, int(response.Status()), int(consts.StatusNoContent))
	assert.Equal(t, len(response.Body()), 0)
}

// TestClientIP exercises the X-Forwarded-For → X-Real-IP → RemoteAddr fallback.
// The synthetic Request() path doesn't have a real net.Conn, so the
// final fallback returns "" — which is the correct documented behavior.
func TestClientIP(t *testing.T) {
	s := rweb.NewServer()

	var got string
	s.Get("/whoami", func(ctx rweb.Context) error {
		got = ctx.ClientIP()
		return ctx.WriteString(got)
	})

	cases := []struct {
		name    string
		headers []rweb.Header
		want    string
	}{
		{
			name: "XFF wins; first entry, trimmed",
			headers: []rweb.Header{
				{Key: "X-Forwarded-For", Value: "203.0.113.7, 10.0.0.1, 10.0.0.2"},
				{Key: "X-Real-IP", Value: "10.0.0.99"},
			},
			want: "203.0.113.7",
		},
		{
			name:    "X-Real-IP used when XFF absent",
			headers: []rweb.Header{{Key: "X-Real-IP", Value: "198.51.100.5"}},
			want:    "198.51.100.5",
		},
		{
			name:    "empty XFF entry falls through to X-Real-IP",
			headers: []rweb.Header{{Key: "X-Forwarded-For", Value: "  "}, {Key: "X-Real-IP", Value: "198.51.100.6"}},
			want:    "198.51.100.6",
		},
		{
			name:    "no headers and no conn → empty string (synthetic Request path)",
			headers: nil,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = ""
			_ = s.Request(consts.MethodGet, "/whoami", tc.headers, nil)
			assert.Equal(t, got, tc.want)
		})
	}
}

// TestClientIPRemoteAddrFallback exercises the third resolution branch:
// when neither X-Forwarded-For nor X-Real-IP is set, ClientIP must return
// the connection's RemoteAddr with the port stripped. The synthetic
// Request() path doesn't carry a real conn, so this needs a real server.
func TestClientIPRemoteAddrFallback(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	var captured atomic.Value // string
	s.Get("/whoami", func(ctx rweb.Context) error {
		captured.Store(ctx.ClientIP())
		return ctx.WriteString("ok")
	})

	go func() {
		defer close(clientDone)
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-readyChan

		// No XFF/X-Real-IP — exercise the RemoteAddr fallback.
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/whoami", s.GetListenPort()))
		assert.Nil(t, err)
		_ = resp.Body.Close()

		ip, _ := captured.Load().(string)
		// Port should be stripped (net.SplitHostPort succeeds on real conns).
		// We accept either the IPv4 loopback (127.0.0.1) or the IPv6 loopback
		// (::1) depending on how the OS resolves it.
		if ip != "127.0.0.1" && ip != "::1" {
			t.Errorf("expected loopback IP without port, got %q", ip)
		}
		// Defense in depth: ensure no `:port` slipped through.
		if strings.Contains(ip, ":") && !strings.Contains(ip, "::") {
			t.Errorf("ClientIP returned host:port form, expected host only: %q", ip)
		}
	}()

	_ = s.Run()
	<-clientDone
}

func TestBasicAuth(t *testing.T) {
	s := rweb.NewServer()

	type result struct{ user, pass string; ok bool }
	var got result
	s.Get("/auth", func(ctx rweb.Context) error {
		u, p, ok := ctx.BasicAuth()
		got = result{u, p, ok}
		return ctx.WriteString("done")
	})

	mk := func(v string) []rweb.Header {
		return []rweb.Header{{Key: "Authorization", Value: v}}
	}
	enc := func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	}

	cases := []struct {
		name  string
		hdrs  []rweb.Header
		user  string
		pass  string
		ok    bool
	}{
		{"valid creds", mk("Basic " + enc("alice:s3cret")), "alice", "s3cret", true},
		{"empty password is valid per RFC", mk("Basic " + enc("alice:")), "alice", "", true},
		{"empty username is valid per RFC", mk("Basic " + enc(":s3cret")), "", "s3cret", true},
		{"colon in password is preserved", mk("Basic " + enc("u:a:b:c")), "u", "a:b:c", true},
		{"case-insensitive scheme prefix", mk("basic " + enc("u:p")), "u", "p", true},
		{"missing header → not ok", nil, "", "", false},
		{"non-Basic scheme → not ok", mk("Bearer abc.def"), "", "", false},
		{"malformed base64 → not ok", mk("Basic !!!not-base64!!!"), "", "", false},
		{"no colon after decode → not ok", mk("Basic " + enc("nocolon")), "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = result{}
			_ = s.Request(consts.MethodGet, "/auth", tc.hdrs, nil)
			assert.Equal(t, got.user, tc.user)
			assert.Equal(t, got.pass, tc.pass)
			assert.Equal(t, got.ok, tc.ok)
		})
	}
}
