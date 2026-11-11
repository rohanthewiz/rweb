package rweb

import (
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// Reverse proxy support for Server.Proxy: the HTTP client the proxy sends
// upstream requests with, and the header rules applied on the way through.
//
// One request through the proxy:
//
//	client ──► rweb ─────────────────────────────────────────► upstream
//	           request headers: hop-by-hop dropped (Connection and what it
//	           names, Keep-Alive, TE, Upgrade, ...), Expect dropped,
//	           X-Forwarded-For/-Proto/-Host set; Host = the target's
//	           body: the buffered request body
//
//	client ◄── rweb ◄───────────────────────────────────────── upstream
//	           status as is (3xx included: redirects are not followed)
//	           response headers: hop-by-hop dropped, Content-Length
//	           re-derived from the stream size
//	           body: rweb.Stream(resp.Body, resp.ContentLength), copied to
//	           the client by writeResponse after the handler returns
//
//	no response headers within ProxyResponseHeaderTimeout ─► 504
//	upstream unreachable, reset, bad response ──────────────► 502
//	client gone / server shutting down ─────────────────────► 502, quietly
//	                                                          (clientGone)

// defaultProxyResponseHeaderTimeout bounds the wait for an upstream's
// response headers when ServerOptions.ProxyResponseHeaderTimeout is zero.
//
// 60 s is nginx's proxy_read_timeout default, the bound most deployments
// already live with: long enough for an upstream doing real work before it
// answers (a report, a slow query), short enough that an upstream that has
// hung does not hold a client, a goroutine and an upstream connection until
// the client gives up. It covers only the wait for the headers, counted
// from the end of the request write; a body that streams for longer is not
// cut off.
const defaultProxyResponseHeaderTimeout = 60 * time.Second

// proxyMaxIdleConnsPerHost is how many idle keep-alive connections the
// proxy keeps per upstream host. http.DefaultTransport keeps 2
// (http.DefaultMaxIdleConnsPerHost), which suits a client calling many
// hosts now and then, not a proxy sending all its traffic to one or two:
// with more than two requests in flight, every extra connection is closed
// when its response ends and a new one dialled for the next request,
// leaving a trail of TIME_WAIT sockets on both sides. 32 is Caddy's
// reverse_proxy default; the transport's overall MaxIdleConns (100, from
// DefaultTransport) still caps the total across hosts.
const proxyMaxIdleConnsPerHost = 32

// newProxyClient builds the client Server.Proxy sends upstream requests
// with. headerTimeout is the resolved ProxyResponseHeaderTimeout (0 = none).
//
// The transport is a clone of http.DefaultTransport, so dialling, TLS
// handshake and idle timeouts and the HTTP_PROXY environment settings are
// net/http's defaults, but the connection pool is the server's own: it can
// be sized for a proxy and closed by Shutdown without touching other users
// of DefaultTransport in the process.
func newProxyClient(headerTimeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = headerTimeout
	tr.MaxIdleConnsPerHost = proxyMaxIdleConnsPerHost
	// The transport would otherwise ask for gzip whenever the client did not
	// send Accept-Encoding, and then decompress the response itself: the
	// client would get bytes the upstream did not send, without their
	// Content-Length, decompressed on this server's CPU. A proxy passes the
	// representation through, so the client's own Accept-Encoding (or its
	// absence) is what the upstream sees and answers.
	tr.DisableCompression = true
	return &http.Client{
		Transport: tr,
		// A redirect is the upstream's answer to the client, not an
		// instruction to the proxy: the browser must see the Location (a
		// login page, a canonical URL) and decide for itself, and following
		// it here would also resend the request's headers to wherever the
		// upstream points. ErrUseLastResponse returns the 3xx as is.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		// No Client.Timeout: it would bound the whole exchange, body
		// included, and cut off a long download. The header wait is bounded
		// by the transport above; the rest ends with the request context
		// (client gone, shutdown) or a failed write to the client.
	}
}

// proxyHTTPClient returns the server's proxy client, building it on first
// use: the first Proxy registration. A server that proxies nothing never
// builds one.
func (s *Server) proxyHTTPClient() *http.Client {
	s.proxyClientOnce.Do(func() {
		s.proxyClient.Store(newProxyClient(resolveLimit(s.options.ProxyResponseHeaderTimeout, defaultProxyResponseHeaderTimeout)))
	})
	return s.proxyClient.Load()
}

// closeProxyIdleConns closes the proxy client's idle upstream connections,
// if it has a client. Shutdown calls it last, when every request is done,
// so the connections those requests used are idle by then. The client
// stays usable: a server served again after Shutdown dials afresh.
func (s *Server) closeProxyIdleConns() {
	if c := s.proxyClient.Load(); c != nil {
		c.CloseIdleConnections()
	}
}

// hopByHopHeaders are the headers that describe one connection rather than
// the message (RFC 9110 §7.6.1), in canonical form. They are dropped from
// the request sent upstream and from the response sent back, as are the
// headers a Connection header names (removeHopByHop).
//
//   - Proxy-Connection and Keep-Alive are not in the RFC's list but are
//     connection-scoped by every implementation that sends them.
//   - TE: trailers cannot be relayed (rweb does not send response
//     trailers), so the client's TE is not passed on either.
//   - Proxy-Authenticate / Proxy-Authorization are between a client and the
//     proxy it authenticates to; rweb's proxy takes no credentials of its
//     own, and an upstream has no business with a client's proxy
//     credentials, nor the client with an upstream proxy's challenge.
//   - Upgrade: a protocol switch (a WebSocket, h2c) is a property of the
//     connection, and this proxy relays messages, not connections.
var hopByHopHeaders = []string{
	consts.HeaderConnection,
	"Keep-Alive",
	consts.HeaderProxyConnection,
	consts.HeaderProxyAuthenticate,
	consts.HeaderProxyAuthorization,
	"Te", // canonical form of TE
	consts.HeaderTrailer,
	consts.HeaderTransferEncoding,
	consts.HeaderUpgrade,
}

// removeHopByHop deletes h's hop-by-hop headers: first every header named
// in its Connection header(s), which a sender uses to mark its own
// extension headers as connection-scoped, then the fixed list. The order
// matters, since the fixed list removes Connection itself.
func removeHopByHop(h http.Header) {
	for _, v := range h[consts.HeaderConnection] {
		for _, name := range strings.Split(v, ",") {
			if name = textproto.TrimString(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

// setForwardedHeaders tells the upstream who asked, in h (the upstream
// request's headers, already stripped of hop-by-hop headers):
//
//   - X-Forwarded-For: the immediate peer's IP, appended to whatever earlier
//     proxies sent, so the chain reads "client, proxy1, ..., peer". Earlier
//     values are passed on rather than trusted or dropped: which entries to
//     believe is for the upstream to decide, by counting from the right
//     past the proxies it knows. A synthetic request (s.Request) has no
//     peer and adds nothing.
//   - X-Forwarded-Proto: the scheme the client used to reach this server.
//   - X-Forwarded-Host: the host the client asked this server for.
//
// Proto and Host replace any value the client sent. They describe this
// hop, which this server knows first-hand; a client-sent value is unchecked
// and would let anyone claim https, or a host name the upstream builds
// links or redirects from. The cost is behind another proxy that already
// set them (a TLS-terminating load balancer), whose values are replaced
// with this hop's; honouring them needs a list of trusted proxies.
func setForwardedHeaders(h http.Header, ctx Context) {
	if ip := peerIP(ctx); ip != "" {
		if prior := h.Values(consts.HeaderXForwardedFor); len(prior) > 0 {
			ip = strings.Join(prior, ", ") + ", " + ip
		}
		h.Set(consts.HeaderXForwardedFor, ip)
	}
	if scheme := ctx.Request().Scheme(); scheme != "" {
		h.Set(consts.HeaderXForwardedProto, scheme)
	}
	if host := ctx.Request().Host(); host != "" {
		h.Set(consts.HeaderXForwardedHost, host)
	}
}

// peerIP is the IP address of the connection's remote end, without the
// port, or "" when there is no connection (a synthetic request). Unlike
// ClientIP it ignores X-Forwarded-For and X-Real-IP: the proxy appends the
// peer it actually talks to, whatever the request claims.
func peerIP(ctx Context) string {
	c, ok := ctx.(*context)
	if !ok || c.conn == nil {
		return ""
	}
	addr := c.conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
