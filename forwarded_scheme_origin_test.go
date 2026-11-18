package rweb

import (
	"bufio"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/rohanthewiz/rweb/consts"
)

// N-075: behind a trusted proxy, Request().Scheme() takes the proxy's
// X-Forwarded-Proto, and the WebSocket origin check also admits an Origin
// whose host is in the proxy's X-Forwarded-Host. Before, Scheme() reported
// the proxy → server hop (http behind a TLS-terminating nginx, so an app's
// http → https redirect looped), and a proxy that rewrites Host (rweb's own
// Proxy does) had every browser handshake refused with 403 unless its
// origin was listed in AllowedOrigins. Host() itself is unchanged.

// notLoopback trusts only a range the test client is not in, so the
// loopback client is an untrusted peer.
var notLoopback = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

func xfp(v string) Header { return Header{Key: "X-Forwarded-Proto", Value: v} }

// The rightmost entry decides, across lines; only http and https count.
func TestForwardedProtoParsing(t *testing.T) {
	cases := []struct {
		name    string
		headers []Header
		want    string
	}{
		{"absent", nil, ""},
		{"https", []Header{xfp("https")}, "https"},
		{"http", []Header{xfp("http")}, "http"},
		{"any case", []Header{xfp("HTTPS")}, "https"},
		{"lower-case name", []Header{{Key: "x-forwarded-proto", Value: "https"}}, "https"},
		{"OWS", []Header{xfp(" \thttps\t ")}, "https"},
		{"empty", []Header{xfp("")}, ""},
		{"unknown", []Header{xfp("ftp")}, ""},
		{"wss is not a request scheme", []Header{xfp("wss")}, ""},
		// An appending proxy after a client that forged "https".
		{"rightmost of a list", []Header{xfp("https, http")}, "http"},
		{"rightmost of a list, other way", []Header{xfp("http,https")}, "https"},
		{"empty elements skipped", []Header{xfp("http, https, ,")}, "https"},
		{"unknown rightmost is not skipped", []Header{xfp("https, ftp")}, ""},
		{"last line wins", []Header{xfp("http"), {Key: "Accept", Value: "*/*"}, xfp("https")}, "https"},
		{"empty last line falls back", []Header{xfp("https"), xfp(" , ")}, "https"},
		{"other header names ignored", []Header{{Key: "X-Forwarded-Protocol", Value: "https"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := forwardedProto(tc.headers); got != tc.want {
				t.Fatalf("forwardedProto = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestForwardedHostHas(t *testing.T) {
	xfh := func(v string) Header { return Header{Key: "X-Forwarded-Host", Value: v} }
	cases := []struct {
		name    string
		headers []Header
		host    string
		want    bool
	}{
		{"absent", nil, "app.example", false},
		{"equal", []Header{xfh("app.example")}, "app.example", true},
		{"any case", []Header{xfh("APP.Example")}, "app.example", true},
		{"leftmost of a chain", []Header{xfh("app.example, internal:8080")}, "app.example", true},
		{"rightmost of a chain", []Header{xfh("internal:8080,app.example")}, "app.example", true},
		{"second line", []Header{xfh("internal"), xfh(" app.example ")}, "app.example", true},
		{"port must match", []Header{xfh("app.example:8443")}, "app.example", false},
		{"no port must match", []Header{xfh("app.example")}, "app.example:8443", false},
		{"other host", []Header{xfh("other.example")}, "app.example", false},
		{"suffix is not equal", []Header{xfh("evil-app.example")}, "app.example", false},
		{"empty", []Header{xfh(" , ")}, "app.example", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := forwardedHostHas(tc.headers, tc.host); got != tc.want {
				t.Fatalf("forwardedHostHas = %v, want %v", got, tc.want)
			}
		})
	}
}

// Over the wire: the test client is on loopback, a trusted peer by default.
// Each case sends two requests on one keep-alive connection, the second
// without the header, so a scheme taken from one request cannot stick to
// the next.
func TestSchemeFromTrustedForwardedProto(t *testing.T) {
	cases := []struct {
		name  string
		opts  ServerOptions
		tr    transport
		proto string // X-Forwarded-Proto on the first request; "" for none
		want  string // Scheme() for the first request
	}{
		{"trusted peer, https", ServerOptions{}, transports[0], "https", "https"},
		{"trusted peer, upper case", ServerOptions{}, transports[0], "HTTPS", "https"},
		{"trusted peer, appended list", ServerOptions{}, transports[0], "https, http", "http"},
		{"trusted peer, unknown value", ServerOptions{}, transports[0], "ftp", "http"},
		{"trusted peer, none sent", ServerOptions{}, transports[0], "", "http"},
		// A trusted proxy that reached us over TLS for a client on http.
		{"trusted peer over TLS says http", ServerOptions{}, transports[1], "http", "http"},
		{"trusted peer over TLS, none sent", ServerOptions{}, transports[1], "", "https"},
		{"no proxy trusted", ServerOptions{TrustedProxies: NoTrustedProxies()}, transports[0], "https", "http"},
		{"peer not in the list", ServerOptions{TrustedProxies: notLoopback}, transports[0], "https", "http"},
		{"peer not in the list, TLS", ServerOptions{TrustedProxies: notLoopback}, transports[1], "http", "https"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(tc.opts)
			s.Get("/scheme", func(ctx Context) error { return ctx.WriteString(ctx.Request().Scheme()) })
			c := dialTest(t, serveTest(t, s, tc.tr), tc.tr)
			br := bufio.NewReader(c)

			extra := ""
			if tc.proto != "" {
				extra = "X-Forwarded-Proto: " + tc.proto + "\r\n"
			}
			writeAll(t, c, "GET /scheme HTTP/1.1\r\nHost: test\r\n"+extra+"\r\n")
			if _, body := readBody(t, br); body != tc.want {
				t.Fatalf("Scheme() = %q, want %q", body, tc.want)
			}

			transportScheme := "http"
			if tc.tr.tls {
				transportScheme = "https"
			}
			writeAll(t, c, getReq("/scheme"))
			if _, body := readBody(t, br); body != transportScheme {
				t.Fatalf("next request on the conn: Scheme() = %q, want %q", body, transportScheme)
			}
		})
	}
}

// A synthetic s.Request has no peer: its headers count as trusted unless no
// proxy is, as for ClientIP. An absolute-form target's scheme still wins.
func TestSchemeFromForwardedProtoSynthetic(t *testing.T) {
	handler := func(ctx Context) error { return ctx.WriteString(ctx.Request().Scheme()) }
	def := NewServer()
	def.Get("/scheme", handler)
	none := NewServer(ServerOptions{TrustedProxies: NoTrustedProxies()})
	none.Get("/scheme", handler)

	h := []Header{xfp("https")}
	if got := string(def.Request(consts.MethodGet, "/scheme", h, nil).Body()); got != "https" {
		t.Errorf("default list: Scheme() = %q, want https", got)
	}
	if got := string(none.Request(consts.MethodGet, "/scheme", h, nil).Body()); got != "http" {
		t.Errorf("no proxy trusted: Scheme() = %q, want http", got)
	}
	if got := string(def.Request(consts.MethodGet, "http://example.com/scheme", h, nil).Body()); got != "http" {
		t.Errorf("absolute-form target: Scheme() = %q, want its own http", got)
	}
}

// handshakeStatus sends an opening handshake for /ws with Host "test" and
// the extra header lines, and returns the response status.
func handshakeStatus(t *testing.T, s *Server, extra string) int {
	t.Helper()
	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	writeAll(t, c, "GET /ws HTTP/1.1\r\nHost: test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"+extra+"\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The origin check over the wire, Host "test" standing for the target host a
// proxy put there and X-Forwarded-Host for the public one the browser used.
func TestWebSocketOriginFromTrustedForwardedHost(t *testing.T) {
	cases := []struct {
		name    string
		opts    ServerOptions
		manual  bool // a manual ctx.UpgradeWebSocket() instead of s.WebSocket
		origin  string
		fwdHost string // X-Forwarded-Host value(s), one line each, "|" between
		want    int
	}{
		{"trusted peer, public host", ServerOptions{}, false, "https://app.example", "app.example", 101},
		{"trusted peer, manual upgrade", ServerOptions{}, true, "https://app.example", "app.example", 101},
		{"trusted peer, any case", ServerOptions{}, false, "https://APP.example", "app.EXAMPLE", 101},
		{"trusted peer, chain", ServerOptions{}, false, "https://app.example", "app.example, 10.0.0.5:8080", 101},
		{"trusted peer, second line", ServerOptions{}, false, "https://app.example", "lb.internal|app.example", 101},
		{"trusted peer, Host still counts", ServerOptions{}, false, "http://test", "app.example", 101},
		{"trusted peer, other origin", ServerOptions{}, false, "https://evil.example", "app.example", 403},
		{"trusted peer, port differs", ServerOptions{}, false, "https://app.example", "app.example:8443", 403},
		{"trusted peer, no header", ServerOptions{}, false, "https://app.example", "", 403},
		{"no proxy trusted", ServerOptions{TrustedProxies: NoTrustedProxies()}, false, "https://app.example", "app.example", 403},
		{"no proxy trusted, manual upgrade", ServerOptions{TrustedProxies: NoTrustedProxies()}, true, "https://app.example", "app.example", 403},
		{"peer not in the list", ServerOptions{TrustedProxies: notLoopback}, false, "https://app.example", "app.example", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(tc.opts)
			if tc.manual {
				s.Get("/ws", func(ctx Context) error {
					ws, err := ctx.UpgradeWebSocket()
					if err != nil {
						return nil // the 403 is already set
					}
					return ws.Close(wsCloseNormalClosure, "")
				})
			} else {
				s.WebSocket("/ws", func(ws *WSConn) error { return nil })
			}
			extra := "Origin: " + tc.origin + "\r\n"
			if tc.fwdHost != "" {
				for _, line := range strings.Split(tc.fwdHost, "|") {
					extra += "X-Forwarded-Host: " + line + "\r\n"
				}
			}
			if got := handshakeStatus(t, s, extra); got != tc.want {
				t.Fatalf("status %d, want %d", got, tc.want)
			}
		})
	}
}

// CheckOrigin still decides alone: X-Forwarded-Host does not override a no.
func TestWebSocketCheckOriginIgnoresForwardedHost(t *testing.T) {
	s := NewServer()
	s.WebSocketWithOptions("/ws", WSOptions{CheckOrigin: func(Context) bool { return false }},
		func(*WSConn) error { return nil })
	if got := handshakeStatus(t, s, "Origin: https://app.example\r\nX-Forwarded-Host: app.example\r\n"); got != 403 {
		t.Fatalf("status %d, want 403", got)
	}
}

// The case that raised the item: an rweb WebSocket upstream with default
// options behind an rweb Proxy. The upstream sees the target's Host, and
// admits the browser's origin through the X-Forwarded-Host the front sets,
// since the front (on loopback) is a trusted peer. An upstream that trusts
// no proxy still refuses it, as before N-075.
func TestProxyWebSocketOriginThroughForwardedHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ServerOptions
		want int
	}{
		{"upstream trusts the front", ServerOptions{}, http.StatusSwitchingProtocols},
		{"upstream trusts no proxy", ServerOptions{TrustedProxies: NoTrustedProxies()}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := NewServer(tc.opts)
			up.WebSocket("/ws", func(ws *WSConn) error {
				_ = ws.WriteMessage(TextMessage, []byte("hello from upstream"))
				_, _ = ws.ReadMessage()
				return nil
			})
			target := "http://" + serveTest(t, up, transports[0])
			_, addr := proxyFront(t, target)

			c := dialTest(t, addr, transports[0])
			writeAll(t, c, wsHandshake("/up/ws", "Origin: http://front.test\r\n"))
			br := bufio.NewReader(c)
			if tc.want != http.StatusSwitchingProtocols {
				if status, _ := readBody(t, br); status != tc.want {
					t.Fatalf("status %d, want %d", status, tc.want)
				}
				return
			}
			ws := wsClient(t, c, br)
			expectEcho(t, ws, "hello from upstream")
			_ = ws.Close(wsCloseNormalClosure, "")
		})
	}
}
