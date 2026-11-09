package rweb

// Requests to a WebSocket route that are not a valid opening handshake get a
// 4xx from rejectWebSocketRequest instead of a 500 through the error
// handler. Internal test so it can watch s.errorHandler directly.

import (
	"bufio"
	"errors"
	"net/http"
	"testing"

	"github.com/rohanthewiz/rweb/consts"
)

func TestWebSocketRouteRejectsNonHandshake(t *testing.T) {
	upgrade := func(version, key string) []Header {
		h := []Header{
			{Key: "Upgrade", Value: "websocket"},
			{Key: "Connection", Value: "Upgrade"},
			{Key: "Sec-WebSocket-Version", Value: version},
		}
		if key != "" {
			h = append(h, Header{Key: "Sec-WebSocket-Key", Value: key})
		}
		return h
	}
	const key = "dGhlIHNhbXBsZSBub25jZQ=="

	cases := []struct {
		name       string
		method     string
		headers    []Header
		wantStatus int
		wantHeader map[string]string
	}{
		{"plain GET", "GET", nil, consts.StatusUpgradeRequired,
			map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"}},
		{"HEAD", "HEAD", nil, consts.StatusUpgradeRequired,
			map[string]string{"Upgrade": "websocket"}},
		{"HEAD with handshake headers", "HEAD", upgrade("13", key), consts.StatusUpgradeRequired,
			map[string]string{"Upgrade": "websocket"}},
		{"unsupported version", "GET", upgrade("8", key), consts.StatusUpgradeRequired,
			map[string]string{"Sec-WebSocket-Version": "13"}},
		{"missing key", "GET", upgrade("13", ""), consts.StatusBadRequest, nil},
	}

	routes := map[string]func(s *Server){
		"WebSocket": func(s *Server) {
			s.WebSocket("/ws", func(*WSConn) error { return nil })
		},
		"WebSocketWithOptions": func(s *Server) {
			s.WebSocketWithOptions("/ws", WSOptions{Compression: true}, func(*WSConn) error { return nil })
		},
	}

	for routeName, register := range routes {
		for _, tc := range cases {
			t.Run(routeName+"/"+tc.name, func(t *testing.T) {
				s := NewServer()
				var handled error
				s.errorHandler = func(_ Context, err error) { handled = err }
				register(s)

				resp := s.Request(tc.method, "/ws", tc.headers, nil)
				if handled != nil {
					t.Fatalf("error handler called: %v", handled)
				}
				if resp.Status() != tc.wantStatus {
					t.Fatalf("status = %d, want %d", resp.Status(), tc.wantStatus)
				}
				for k, v := range tc.wantHeader {
					if got := resp.Header(k); got != v {
						t.Errorf("%s = %q, want %q", k, got, v)
					}
				}
			})
		}
	}
}

// --- Origin check ------------------------------------------------------------
//
// Browsers attach cookies to a cross-site WebSocket handshake and apply no
// CORS to it, so without an Origin check any page could open a socket as the
// logged-in user. A handshake whose Origin names a different host than the
// request's is refused with 403 before upgrading; no Origin (a non-browser
// client) is allowed.

// handshakeHeaders is a complete opening handshake for Host "test", plus
// an Origin header when origin is not empty.
func handshakeHeaders(origin string) []Header {
	h := []Header{
		{Key: "Host", Value: "test"},
		{Key: "Upgrade", Value: "websocket"},
		{Key: "Connection", Value: "Upgrade"},
		{Key: "Sec-WebSocket-Key", Value: "dGhlIHNhbXBsZSBub25jZQ=="},
		{Key: "Sec-WebSocket-Version", Value: "13"},
	}
	if origin != "" {
		h = append(h, Header{Key: "Origin", Value: origin})
	}
	return h
}

// Refused handshakes, through each way of upgrading. They are answered
// before the upgrade, so the synthetic s.Request can carry them.
func TestWebSocketOriginRefused(t *testing.T) {
	cases := []struct {
		name   string
		opts   WSOptions
		origin string
	}{
		{"other host", WSOptions{}, "https://evil.example"},
		{"same host, other port", WSOptions{}, "http://test:8080"},
		{"opaque origin", WSOptions{}, "null"},
		{"not in AllowedOrigins", WSOptions{AllowedOrigins: []string{"https://app.example"}}, "https://evil.example"},
		{"CheckOrigin says no", WSOptions{CheckOrigin: func(Context) bool { return false }}, ""},
		{"CheckOrigin wins over AllowedOrigins",
			WSOptions{AllowedOrigins: []string{"*"}, CheckOrigin: func(Context) bool { return false }}, "https://evil.example"},
	}

	for _, tc := range cases {
		routes := map[string]func(s *Server, handled *bool, upgradeErr *error){
			"WebSocketWithOptions": func(s *Server, handled *bool, _ *error) {
				s.WebSocketWithOptions("/ws", tc.opts, func(*WSConn) error { *handled = true; return nil })
			},
		}
		// s.WebSocket and a manual UpgradeWebSocket have no options: only the
		// default check applies to them.
		if tc.opts.CheckOrigin == nil && tc.opts.AllowedOrigins == nil {
			routes["WebSocket"] = func(s *Server, handled *bool, _ *error) {
				s.WebSocket("/ws", func(*WSConn) error { *handled = true; return nil })
			}
			routes["manual UpgradeWebSocket"] = func(s *Server, handled *bool, upgradeErr *error) {
				s.Get("/ws", func(ctx Context) error {
					ws, err := ctx.UpgradeWebSocket()
					*upgradeErr = err
					if errors.Is(err, ErrWebSocketOriginNotAllowed) {
						return nil // the 403 is already set
					}
					*handled = ws != nil
					return err
				})
			}
		}

		for routeName, register := range routes {
			t.Run(tc.name+"/"+routeName, func(t *testing.T) {
				s := NewServer()
				var handlerErr, upgradeErr error
				var handled bool
				s.errorHandler = func(_ Context, err error) { handlerErr = err }
				register(s, &handled, &upgradeErr)

				resp := s.Request("GET", "/ws", handshakeHeaders(tc.origin), nil)
				if resp.Status() != consts.StatusForbidden {
					t.Fatalf("status = %d, want 403", resp.Status())
				}
				if handled {
					t.Fatal("handler ran for a refused origin")
				}
				if handlerErr != nil {
					t.Fatalf("error handler called: %v", handlerErr)
				}
				if routeName == "manual UpgradeWebSocket" && !errors.Is(upgradeErr, ErrWebSocketOriginNotAllowed) {
					t.Fatalf("UpgradeWebSocket error = %v, want ErrWebSocketOriginNotAllowed", upgradeErr)
				}
			})
		}
	}
}

// Accepted handshakes upgrade (101); these need a real connection.
func TestWebSocketOriginAccepted(t *testing.T) {
	cases := []struct {
		name   string
		opts   *WSOptions // nil: s.WebSocket
		origin string
	}{
		{"no Origin (non-browser client)", nil, ""},
		{"same host", nil, "http://test"},
		{"same host, any case, other scheme", nil, "HTTPS://TEST"},
		{"listed in AllowedOrigins", &WSOptions{AllowedOrigins: []string{"https://app.example"}}, "https://APP.example"},
		{"listed as a bare host", &WSOptions{AllowedOrigins: []string{"app.example:8443"}}, "https://app.example:8443"},
		{"AllowedOrigins keeps same host", &WSOptions{AllowedOrigins: []string{"https://app.example"}}, "http://test"},
		{"AllowedOrigins wildcard", &WSOptions{AllowedOrigins: []string{"*"}}, "https://evil.example"},
		{"CheckOrigin says yes", &WSOptions{CheckOrigin: func(ctx Context) bool {
			return ctx.Request().Header("Origin") == "https://partner.example"
		}}, "https://partner.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			h := func(ws *WSConn) error { return nil }
			if tc.opts == nil {
				s.WebSocket("/ws", h)
			} else {
				s.WebSocketWithOptions("/ws", *tc.opts, h)
			}
			extra := ""
			if tc.origin != "" {
				extra = "Origin: " + tc.origin + "\r\n"
			}
			upgradeOn(t, s, extra) // fails the test unless 101
		})
	}
}

// The Upgrade token is case-insensitive (RFC 6455 §4.2.1): a client sending
// "Upgrade: WebSocket" is upgraded, not rejected.
func TestWebSocketUpgradeTokenCaseInsensitive(t *testing.T) {
	s := NewServer()
	s.WebSocket("/ws", func(ws *WSConn) error { return ws.Close(wsCloseNormalClosure, "") })

	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	writeAll(t, c, "GET /ws HTTP/1.1\r\nHost: test\r\nUpgrade: WebSocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
}
