package rweb

// Requests to a WebSocket route that are not a valid opening handshake get a
// 4xx from rejectWebSocketRequest instead of a 500 through the error
// handler. Internal test so it can watch s.errorHandler directly.

import (
	"bufio"
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
