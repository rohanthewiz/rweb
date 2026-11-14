package rweb

import (
	stdctx "context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
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
//	           Location / Content-Location on the target's own host mapped
//	           under the route prefix (proxyRedirect)
//	           response headers: hop-by-hop dropped, Content-Length
//	           re-derived from the stream size
//	           body: rweb.Stream(resp.Body, resp.ContentLength), copied to
//	           the client by writeResponse after the handler returns
//
//	no response headers within ProxyResponseHeaderTimeout ─► 504
//	upstream unreachable, reset, bad response ──────────────► 502
//	client gone / server shutting down ─────────────────────► 502, quietly
//	                                                          (clientGone)
//	a body read stalled past ProxyReadTimeout (off by
//	default) ────────────────────────────────────────────────► body cut off,
//	                                                          client conn
//	                                                          closed

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
// Proto and Host from an untrusted peer are replaced. They describe this
// hop, which this server knows first-hand; a client-sent value is unchecked
// and would let anyone claim https, or a host name the upstream builds
// links or redirects from. From a trusted peer (ServerOptions.TrustedProxies,
// since N-055), a value it sent is passed on as it is: that peer is the
// front of the chain (a TLS-terminating load balancer, say), and knows the
// scheme and host the client really used, where this hop would only report
// its own (http from the balancer). A trusted peer that sent none gets this
// hop's values, as before.
func setForwardedHeaders(h http.Header, ctx Context) {
	if ip := peerIP(ctx); ip != "" {
		if prior := h.Values(consts.HeaderXForwardedFor); len(prior) > 0 {
			ip = strings.Join(prior, ", ") + ", " + ip
		}
		h.Set(consts.HeaderXForwardedFor, ip)
	}

	trusted := false
	if c, ok := ctx.(*context); ok {
		trusted = c.forwardedTrusted()
	}
	if scheme := ctx.Request().Scheme(); scheme != "" && !(trusted && h.Get(consts.HeaderXForwardedProto) != "") {
		h.Set(consts.HeaderXForwardedProto, scheme)
	}
	if host := ctx.Request().Host(); host != "" && !(trusted && h.Get(consts.HeaderXForwardedHost) != "") {
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

// errProxyReadTimeout is what a read of an upstream body returns when it
// waited longer than ServerOptions.ProxyReadTimeout. It shows in the Debug
// line for a stream cut short ("rweb: body stream cut short").
var errProxyReadTimeout = errors.New("rweb: proxy: upstream body read timed out (ProxyReadTimeout)")

// proxyBodyReader is an upstream response body whose every Read must return
// within timeout (ServerOptions.ProxyReadTimeout); otherwise the upstream
// request is cancelled and the read fails with errProxyReadTimeout.
//
// Why: once the response headers are in, nothing else bounds the body. An
// upstream that stalls mid-body (hung, or a slow rweb.Stream producer
// behind it) held the client connection, a goroutine and an upstream
// connection until Shutdown; and since the disconnect watch is stopped
// before writeResponse, a client that left meanwhile was not noticed until
// the next write.
//
// Why each Read, not the gap between reads as nginx's proxy_read_timeout
// counts it: nginx buffers the upstream's body, rweb does not. Between two
// reads here lies the write of the previous piece to the client, so a gap
// timer would also fire on a slow but live client. Timing only the wait
// inside Read charges the upstream alone.
//
// How a read is cut off: http.Response.Body has no deadline to set. The
// upstream request runs under its own cancellable context (a child of
// Request().Context(), see Server.proxy); a timer armed on entry to Read
// and stopped on its return cancels that context, which makes the
// transport fail the pending Read and close the upstream connection.
//
//	Read ─► timer.Reset(timeout) ─► body.Read ─────────► timer.Stop ─► n, err
//	                     │ timer fires first
//	                     ▼
//	        fired = true, cancel(upstream ctx) ─► body.Read fails
//	                                            ─► n, errProxyReadTimeout
//
// writeBodyStream and writeChunkedBody then see a failed read and close the
// client connection, so the client gets a truncated body (never a short one
// that looks complete), and closing the stream (Close below) releases the
// upstream side.
type proxyBodyReader struct {
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
	// fired is set by the timer before it cancels, so a read failing on the
	// cancellation can be told apart from any other upstream error.
	fired  atomic.Bool
	cancel stdctx.CancelFunc
}

// newProxyBodyReader wraps body, whose request runs under the context that
// cancel cancels. The timer is created stopped: it runs only inside Read.
func newProxyBodyReader(body io.ReadCloser, timeout time.Duration, cancel stdctx.CancelFunc) *proxyBodyReader {
	pr := &proxyBodyReader{body: body, timeout: timeout, cancel: cancel}
	pr.timer = time.AfterFunc(time.Hour, func() {
		pr.fired.Store(true)
		cancel()
	})
	pr.timer.Stop()
	return pr
}

func (pr *proxyBodyReader) Read(p []byte) (int, error) {
	pr.timer.Reset(pr.timeout)
	n, err := pr.body.Read(p)
	pr.timer.Stop()
	// A timer that fired during this read (or just after the previous one
	// returned, making this one fail at once) is the cause of the error,
	// whatever the transport calls it (context canceled, use of closed
	// connection). io.EOF is kept: the body was complete.
	if err != nil && err != io.EOF && pr.fired.Load() {
		err = errProxyReadTimeout
	}
	return n, err
}

// Close stops the timer, closes the body (returning the upstream
// connection to the pool when it was read to the end) and releases the
// upstream request's context.
func (pr *proxyBodyReader) Close() error {
	pr.timer.Stop()
	err := pr.body.Close()
	pr.cancel()
	return err
}

// proxyRedirect maps a URL the upstream sends back in Location or
// Content-Location onto the address the client reaches it at through the
// proxy: the reverse of the request mapping in Server.proxy, as nginx's
// "proxy_redirect default" and Apache's ProxyPassReverse do.
//
//	request:  client /<pathPrefix>/rest ──► upstream <target>/<upBase>/rest
//	response: Location <target scheme://host>/<upBase>/rest?q
//	                                  ──► /<pathPrefix>/rest?q   (to the client)
//
// e.g. Proxy("/api", "http://127.0.0.1:9000", 1) strips "api", so upBase is
// "/" and clientBase "/api": an upstream redirect to
// http://127.0.0.1:9000/login reaches the client as /api/login, instead of
// sending a browser to an address that is only reachable from this server.
//
// Only an absolute URL on the target's own scheme and host (default ports
// equal to none) whose path lies under upBase is rewritten; it comes back
// as an absolute path, which the client resolves against the scheme and
// host it used itself (RFC 9110 §10.2.2 allows a relative Location), so
// nothing here has to guess them. Everything else passes through as sent:
//
//   - another host: not ours to map;
//   - the target's host but a path outside upBase: nothing through this
//     proxy reaches it, and a wrong rewrite would be worse than none;
//   - a relative reference ("/new", "next"): an upstream that sends one may
//     not know about the prefix (it wants /api/new) or may already include
//     it (an app configured with a sub-path root URL sends /api/new itself,
//     and a rewrite would make it /api/api/new). The two cannot be told
//     apart from here, so, like nginx by default, it is left alone.
//
// Set-Cookie Path is not rewritten either: an upstream that sets Path=/
// often means the whole site (a login service mounted under a prefix
// setting the site's session), and narrowing that to the prefix would
// break it. nginx (proxy_cookie_path) and Apache (ProxyPassReverseCookiePath)
// leave cookies alone unless told to.
type proxyRedirect struct {
	scheme string // the target's scheme, lower-case
	host   string // the target's host:port, lower-case, default port added
	// upBase and clientBase are path prefixes with no trailing slash; ""
	// stands for the root.
	upBase     string
	clientBase string
}

// newProxyRedirect derives the mapping from a Proxy route's settings: the
// parsed target, the route's prefix (no leading slash) and that prefix with
// its stripped tokens removed, as Server.proxy computes them.
func newProxyRedirect(target *url.URL, pathPrefix, strippedPrefix string) *proxyRedirect {
	scheme := strings.ToLower(target.Scheme)
	return &proxyRedirect{
		scheme:     scheme,
		host:       hostWithDefaultPort(scheme, target.Host),
		upBase:     strings.TrimSuffix(path.Join("/", strippedPrefix, target.Path), "/"),
		clientBase: strings.TrimSuffix(path.Join("/", pathPrefix), "/"),
	}
}

// rewrite returns loc mapped for the client, or loc itself when it is not
// an absolute URL under the target's base (see proxyRedirect).
func (m *proxyRedirect) rewrite(loc string) string {
	u, err := url.Parse(loc)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil ||
		strings.ToLower(u.Scheme) != m.scheme || hostWithDefaultPort(m.scheme, u.Host) != m.host {
		return loc
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	// Under upBase on a segment boundary: "/app" covers "/app" and "/app/x",
	// not "/apple". With upBase "" (the root) every absolute path is under it.
	if p != m.upBase && !strings.HasPrefix(p, m.upBase+"/") {
		return loc
	}
	out := m.clientBase + p[len(m.upBase):]
	if out == "" {
		out = "/"
	}
	if u.RawQuery != "" || u.ForceQuery {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}

// hostWithDefaultPort lower-cases host and gives it the scheme's default
// port when it names none, so "example.com" and "example.com:80" compare
// equal under http.
func hostWithDefaultPort(scheme, host string) string {
	host = strings.ToLower(host)
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	switch scheme {
	case "http", "ws":
		return net.JoinHostPort(strings.Trim(host, "[]"), "80")
	case "https", "wss":
		return net.JoinHostPort(strings.Trim(host, "[]"), "443")
	}
	return host
}
