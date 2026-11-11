package rweb

// The proxy's upstream client as configured (proxy.go): its transport
// settings, the option convention for ProxyResponseHeaderTimeout, and one
// client shared by every Proxy route of a server. Behaviour on the wire is
// tested in proxy_test.go.

import (
	"net/http"
	"testing"
	"time"
)

func TestProxyClientConfiguration(t *testing.T) {
	cases := []struct {
		name string
		s    *Server
		want time.Duration
	}{
		{"default", NewServer(), defaultProxyResponseHeaderTimeout},
		{"set", NewServerWithOptions(WithProxyResponseHeaderTimeout(5 * time.Second)), 5 * time.Second},
		{"negative disables", NewServer(ServerOptions{ProxyResponseHeaderTimeout: -1}), 0},
		{"copied by WithOptions", NewServerWithOptions(WithOptions(ServerOptions{ProxyResponseHeaderTimeout: 7 * time.Second})), 7 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.s.proxyClient.Load() != nil {
				t.Fatal("a client was built before any Proxy route")
			}
			if err := c.s.Proxy("/a", "http://127.0.0.1:1", 1); err != nil {
				t.Fatal(err)
			}
			if err := c.s.Proxy("/b", "http://127.0.0.1:2", 1); err != nil {
				t.Fatal(err)
			}
			client := c.s.proxyClient.Load()
			if client == nil {
				t.Fatal("no client after Proxy")
			}
			if client != c.s.proxyHTTPClient() {
				t.Error("Proxy routes do not share one client")
			}
			if client == http.DefaultClient || client.Transport == http.DefaultTransport {
				t.Error("the proxy uses net/http's default client or transport")
			}
			if err := client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
				t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse", err)
			}
			if client.Timeout != 0 {
				t.Errorf("Client.Timeout = %v, want 0 (it would cut off long bodies)", client.Timeout)
			}
			tr := client.Transport.(*http.Transport)
			if tr.ResponseHeaderTimeout != c.want {
				t.Errorf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, c.want)
			}
			if tr.MaxIdleConnsPerHost != proxyMaxIdleConnsPerHost {
				t.Errorf("MaxIdleConnsPerHost = %d, want %d", tr.MaxIdleConnsPerHost, proxyMaxIdleConnsPerHost)
			}
			if !tr.DisableCompression {
				t.Error("transport compression is on: it would decompress responses behind the client's back")
			}
		})
	}
}

func TestRemoveHopByHop(t *testing.T) {
	h := http.Header{}
	for _, k := range hopByHopHeaders {
		h.Set(k, "v")
	}
	// Set after the fixed list, which includes Connection itself.
	h.Set("Connection", "keep-alive, X-A")
	h.Add("Connection", " x-b ,,") // a second line, odd spacing, empty members
	h.Set("X-A", "1")
	h.Set("X-B", "2")
	h.Set("X-C", "end-to-end")
	removeHopByHop(h)
	if len(h) != 1 || h.Get("X-C") != "end-to-end" {
		t.Errorf("after removeHopByHop: %v, want only X-C", h)
	}
}
