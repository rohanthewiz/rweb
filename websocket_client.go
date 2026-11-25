package rweb

import (
	"bufio"
	"bytes"
	"compress/flate"
	stdctx "context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WebSocket client: DialWebSocket opens a connection to a WebSocket server
// and returns the same *WSConn a server handler gets, in the client role.
//
// Design: the client is only the opening handshake. Everything after it —
// frame reading and writing, fragmentation, control frames, the closing
// handshake, keepalive, deadlines, permessage-deflate — is WSConn's, which
// has been symmetric from the start (NewWSConn(conn, false) masks what it
// writes and refuses masked frames from the server). So an app reads and
// writes on a dialed connection exactly as a server handler does, and the
// two sides share one tested implementation instead of keeping two.
//
// One dial, step by step:
//
//	DialWebSocket(ctx, "wss://host/path", opts)
//	  │ parse the URL, build the request, check every option and header
//	  │ (nothing is dialed for a request that cannot be sent)
//	  ▼
//	dial TCP ──► TLS handshake (wss) ──► write the upgrade request
//	  │                                        ▲
//	  │  ctx, narrowed by HandshakeTimeout,    │ all three bounded the same way
//	  │  bounds every step: a deadline on the conn, moved into the past
//	  │  if ctx is cancelled first
//	  ▼
//	read the response head (bufio over a reader capped at 1 MB)
//	  │ not 101 ──► (nil, resp with ≤ 64 KB of body, ErrWebSocketBadHandshake)
//	  │ 101: Upgrade, Connection, Sec-WebSocket-Accept, -Protocol and
//	  │      -Extensions checked (RFC 6455 §4.1, RFC 7692 §5)
//	  ▼
//	WSConn in the client role
//	  r = [bytes the bufio read past the 101 ─►] conn
//	  write timeout, pings and liveness read deadline as on the server
//	  ▼
//	(ws, resp, nil)
//
// The handshake's deadlines are cleared before WSConn takes over; the
// context bounds the dial only, as with net.Dialer.DialContext. The app ends
// the connection with ws.Close.

// ErrWebSocketBadHandshake is wrapped by the error DialWebSocket returns when
// the server answered, but not with an acceptable 101: another status (the
// *http.Response is returned alongside, with up to 64 KB of its body), or a
// 101 whose headers do not complete the handshake (wrong accept key, a
// subprotocol or extension the client did not offer, …).
var ErrWebSocketBadHandshake = errors.New("websocket handshake failed")

const (
	// defaultHandshakeTimeout bounds dial + TLS + upgrade when the caller's
	// context sets no earlier deadline: the server's ReadHeaderTimeout
	// default, so a dial waits no longer for a head than the server would.
	defaultHandshakeTimeout = 30 * time.Second
	// maxHandshakeResponse caps what may be read while waiting for the
	// response head (and a refusal's body): a server that streams header
	// lines forever cannot grow the client's memory without bound. The
	// server's own MaxHeaderBytes default.
	maxHandshakeResponse = 1 << 20
	// maxHandshakeErrorBody is how much of a non-101 response's body is kept
	// for the caller: enough for any error page or JSON error.
	maxHandshakeErrorBody = 64 << 10
)

// WSDialOptions configures DialWebSocket. The zero value is ready to use.
type WSDialOptions struct {
	// Header holds extra request headers for the handshake: Authorization,
	// Cookie, Origin, User-Agent and the like. Each value goes on its own
	// line. Host may be given, to name a virtual host other than the URL's
	// (the TLS server name stays the URL's host). The handshake's own
	// headers — Upgrade, Connection, Sec-WebSocket-Key, -Version,
	// -Protocol, -Extensions — and the body framing headers Content-Length
	// and Transfer-Encoding are set by DialWebSocket, or never sent, and
	// are refused here; use Subprotocols and Compression instead. A name
	// that is not a token, or a value with a control byte other than tab,
	// is refused too (it would break or smuggle request lines).
	//
	// Credentials in the URL (wss://user:pass@host/) are sent as Basic
	// authorization unless Header has its own Authorization.
	Header http.Header

	// Subprotocols are offered in order of preference
	// (Sec-WebSocket-Protocol). The server's choice is ws.Subprotocol(); ""
	// when it chose none. A server that names one not offered fails the dial.
	Subprotocols []string

	// Compression offers permessage-deflate (RFC 7692). The connection is
	// compressed only if the server accepts; ws works the same either way.
	Compression bool
	// CompressionLevel is the compress/flate level for outgoing messages;
	// 0 selects 2, as on the server (see defaultCompressionLevel).
	CompressionLevel int

	// TLSConfig is used for wss:// URLs. nil means Go's defaults. Its
	// ServerName defaults to the URL's host; "h2" is dropped from its
	// NextProtos, since the handshake is HTTP/1.1.
	TLSConfig *tls.Config

	// NetDialContext opens the TCP connection, e.g. through a SOCKS or
	// CONNECT tunnel; nil uses a plain net.Dialer. For wss://, TLS is run
	// over the connection it returns.
	NetDialContext func(ctx stdctx.Context, network, addr string) (net.Conn, error)

	// HandshakeTimeout bounds the dial, the TLS handshake and the upgrade
	// together, on top of ctx. 0 selects 30 s; negative leaves only ctx.
	HandshakeTimeout time.Duration

	// WriteTimeout, PingInterval and PongTimeout keep the connection healthy
	// exactly as on a server route (WSOptions), with the same defaults: each
	// write must finish within 10 s; the client pings every 30 s, and a
	// server that sends nothing at all — no pong, no data — for 40 s makes
	// ReadMessage fail instead of blocking forever. 0 selects the default,
	// negative disables (for PongTimeout: pings without the read deadline).
	// Any server must answer pings (RFC 6455 §5.5.2), so the defaults hold
	// for every compliant server, not only rweb.
	WriteTimeout time.Duration
	PingInterval time.Duration
	PongTimeout  time.Duration
}

// DialWebSocket opens a WebSocket connection to rawURL (ws://, wss://, or
// http:// and https:// for the same) and returns it with the server's 101
// response.
//
// ctx bounds the dial and handshake only (as with net.Dialer.DialContext);
// once DialWebSocket returns, the connection lives until ws.Close, a close
// frame from the server, or an I/O error. Read with ws.ReadMessage, in one
// goroutine at a time, as a server handler does — pings from the server are
// answered and a close frame is replied to inside it — and write from any
// goroutine. Call ws.Close when done, also after a read error: it stops the
// ping goroutine and closes the socket.
//
// Errors: a server that answers with anything but a valid 101 gives an error
// wrapping ErrWebSocketBadHandshake, and for a status other than 101 the
// response too (its Body holds up to 64 KB of the page), so the caller can
// tell a 401 from a 404. A ctx that ends first gives an error wrapping
// ctx.Err().
//
//	ws, _, err := rweb.DialWebSocket(ctx, "wss://example.com/ws", rweb.WSDialOptions{
//		Header: http.Header{"Authorization": {"Bearer " + token}},
//	})
//	if err != nil {
//		return err
//	}
//	defer ws.Close(1000, "")
//	_ = ws.WriteMessage(rweb.TextMessage, []byte("hello"))
//	msg, err := ws.ReadMessage()
func DialWebSocket(ctx stdctx.Context, rawURL string, opts ...WSDialOptions) (*WSConn, *http.Response, error) {
	var o WSDialOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	target, err := parseWebSocketURL(rawURL)
	if err != nil {
		return nil, nil, err
	}
	// Errors name the URL without its password (url.Redacted).
	fail := func(err error) error {
		return fmt.Errorf("websocket dial %s: %w", target.u.Redacted(), err)
	}
	if o.CompressionLevel < flate.HuffmanOnly || o.CompressionLevel > flate.BestCompression {
		return nil, nil, fail(fmt.Errorf("compression level %d out of range", o.CompressionLevel))
	}
	key, err := newWebSocketKey()
	if err != nil {
		return nil, nil, fail(err)
	}
	req, err := buildHandshakeRequest(target, key, o)
	if err != nil {
		return nil, nil, fail(err)
	}

	if timeout := resolveWSDuration(o.HandshakeTimeout, defaultHandshakeTimeout); timeout > 0 {
		var cancel stdctx.CancelFunc
		ctx, cancel = stdctx.WithTimeout(ctx, timeout)
		defer cancel()
	}

	conn, err := dialWebSocketConn(ctx, target, o)
	if err != nil {
		return nil, nil, fail(handshakeCtxErr(ctx, err))
	}

	// The upgrade itself is plain conn I/O, which knows nothing of ctx: its
	// deadline goes on the conn, and a cancel moves the deadline into the
	// past, which ends a blocked read or write at once. AfterFunc's stop
	// reports whether that happened; if it did, the handshake's outcome is
	// discarded even when it raced to success, since the caller has already
	// given up on it.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := stdctx.AfterFunc(ctx, func() { _ = conn.SetDeadline(aLongTimeAgo) })
	resp, lead, deflate, err := clientHandshake(conn, target, key, req, o)
	if !stop() {
		_ = conn.Close()
		return nil, resp, fail(ctx.Err())
	}
	if err != nil {
		_ = conn.Close()
		return nil, resp, fail(handshakeCtxErr(ctx, err))
	}
	// From here on the conn's deadlines belong to WSConn (write timeout,
	// liveness reader, the app's SetReadDeadline): none may be left over.
	_ = conn.SetDeadline(time.Time{})

	ws := NewWSConn(conn, false)
	// Bytes the server sent straight after its 101 — a greeting written as
	// the handler starts — may already sit in the handshake's read buffer.
	// They are the first frame's bytes, so WSConn reads them before the
	// conn, as the server does for bytes behind an upgrade request.
	if len(lead) > 0 {
		ws.r = io.MultiReader(bytes.NewReader(lead), conn)
	}
	ws.deflate = deflate
	ws.subprotocol = resp.Header.Get("Sec-WebSocket-Protocol")
	// The keepalive starts last, once ws.r is final, since it wraps it.
	ws.applyOptions(WSOptions{
		WriteTimeout: o.WriteTimeout,
		PingInterval: o.PingInterval,
		PongTimeout:  o.PongTimeout,
	})
	ws.startKeepalive()
	return ws, resp, nil
}

// handshakeCtxErr reports a failed dial or handshake step as the context's
// error when the context is what ended it, so callers can test for
// context.DeadlineExceeded or context.Canceled.
//
// ctx.Err() alone is not enough: the conn's deadline is ctx's deadline, and
// the socket's timer can fire a moment before the context's own, so the
// step fails with an i/o timeout while ctx.Err() is still nil. During the
// handshake every deadline on the conn comes from ctx (its deadline, or the
// past one a cancel sets), so a timeout there is the context's deadline.
func handshakeCtxErr(ctx stdctx.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return stdctx.DeadlineExceeded
	}
	return err
}

// wsTarget is a WebSocket URL resolved for dialing.
type wsTarget struct {
	u          *url.URL
	secure     bool   // wss:// or https://
	addr       string // host:port to dial (IPv6 bracketed)
	host       string // Host header value: the URL's host[:port]
	requestURI string // request-target: path and query
}

// parseWebSocketURL checks rawURL and works out where to dial and what to
// ask for. http and https are accepted as ws and wss: the opening handshake
// is an HTTP request, and many callers hold the URL in that form.
func parseWebSocketURL(rawURL string) (wsTarget, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return wsTarget{}, fmt.Errorf("websocket dial: %w", err)
	}
	t := wsTarget{u: u}
	port := "80"
	switch strings.ToLower(u.Scheme) {
	case "ws", "http":
	case "wss", "https":
		t.secure, port = true, "443"
	default:
		return t, fmt.Errorf("websocket dial %s: scheme %q is not ws or wss", u.Redacted(), u.Scheme)
	}
	if u.Hostname() == "" {
		return t, fmt.Errorf("websocket dial %s: no host in URL", u.Redacted())
	}
	if p := u.Port(); p != "" {
		port = p
	}
	// JoinHostPort, not Hostname()+":"+port: an IPv6 literal needs its
	// brackets back ("[::1]:8080").
	t.addr = net.JoinHostPort(u.Hostname(), port)
	t.host = u.Host
	t.requestURI = u.RequestURI()
	// url.Parse refuses control bytes but keeps a literal space in a query,
	// which would split the request line into more than three fields.
	for i := 0; i < len(t.requestURI); i++ {
		if c := t.requestURI[i]; c <= ' ' || c == 0x7f {
			return t, fmt.Errorf("websocket dial %s: space or control byte in path or query", u.Redacted())
		}
	}
	return t, nil
}

// newWebSocketKey returns a fresh Sec-WebSocket-Key: 16 random bytes,
// base64 (RFC 6455 §4.1). Its only job is to prove the response was made
// for this request, so it need not be secret, only unpredictable.
func newWebSocketKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

// handshakeOwnedHeaders are the request headers DialWebSocket writes itself
// (or, for the body framing pair, must never write on a bodiless GET); a
// caller's copy would duplicate or contradict them.
var handshakeOwnedHeaders = []string{
	"Upgrade", "Connection",
	"Sec-WebSocket-Key", "Sec-WebSocket-Version",
	"Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions",
	"Content-Length", "Transfer-Encoding",
}

// buildHandshakeRequest writes the opening handshake (RFC 6455 §4.1) in one
// buffer, so it goes out in one write:
//
//	GET /path?query HTTP/1.1
//	Host: host[:port]
//	Upgrade: websocket
//	Connection: Upgrade
//	Sec-WebSocket-Key: <key>
//	Sec-WebSocket-Version: 13
//	[Sec-WebSocket-Protocol: a, b]         Subprotocols
//	[Sec-WebSocket-Extensions: permessage-deflate]   Compression
//	[Authorization: Basic …]               credentials in the URL
//	[caller's headers, sorted by name]
//
// Every caller-supplied name and value is checked first: they come from the
// app, but may carry data from elsewhere (a token, a cookie), and a CR or LF
// in one would let that data write request lines of its own. Sorted so the
// request does not depend on map order.
func buildHandshakeRequest(t wsTarget, key string, o WSDialOptions) ([]byte, error) {
	host := t.host
	hasAuth := false
	names := make([]string, 0, len(o.Header))
	for name, values := range o.Header {
		if !isValidHeaderKey(name) {
			return nil, fmt.Errorf("header name %q is not a token", name)
		}
		if slices.ContainsFunc(handshakeOwnedHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			return nil, fmt.Errorf("header %q is set by the handshake; use WSDialOptions.Subprotocols or Compression", name)
		}
		for _, v := range values {
			if !isValidHeaderValue(v) {
				return nil, fmt.Errorf("header %q has a control character in its value", name)
			}
		}
		switch {
		case strings.EqualFold(name, "Host"):
			// The Host line is written first and once; a caller's value
			// replaces the URL's rather than adding a second line.
			if len(values) != 1 || values[0] == "" {
				return nil, errors.New("header \"Host\" needs exactly one non-empty value")
			}
			host = values[0]
			continue
		case strings.EqualFold(name, "Authorization"):
			hasAuth = true
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, p := range o.Subprotocols {
		if !isValidHeaderKey(p) {
			return nil, fmt.Errorf("subprotocol %q is not a token", p)
		}
	}

	b := make([]byte, 0, 256)
	b = append(b, "GET "...)
	b = append(b, t.requestURI...)
	b = append(b, " HTTP/1.1\r\nHost: "...)
	b = append(b, host...)
	b = append(b, "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: "...)
	b = append(b, key...)
	b = append(b, "\r\nSec-WebSocket-Version: 13\r\n"...)
	if len(o.Subprotocols) > 0 {
		b = append(b, "Sec-WebSocket-Protocol: "...)
		b = append(b, strings.Join(o.Subprotocols, ", ")...)
		b = append(b, "\r\n"...)
	}
	if o.Compression {
		// The bare offer: no client_max_window_bits, since compress/flate
		// cannot compress with a window under 32 KB, so the server may not
		// ask this client for a smaller one (RFC 7692 §7.1.2.2).
		b = append(b, "Sec-WebSocket-Extensions: permessage-deflate\r\n"...)
	}
	if t.u.User != nil && !hasAuth {
		pass, _ := t.u.User.Password()
		b = append(b, "Authorization: Basic "...)
		b = append(b, base64.StdEncoding.EncodeToString([]byte(t.u.User.Username()+":"+pass))...)
		b = append(b, "\r\n"...)
	}
	for _, name := range names {
		for _, v := range o.Header[name] {
			b = append(b, name...)
			b = append(b, ": "...)
			b = append(b, v...)
			b = append(b, "\r\n"...)
		}
	}
	return append(b, "\r\n"...), nil
}

// dialWebSocketConn opens the TCP connection and, for wss://, runs TLS over
// it. Both are ctx-aware on their own.
func dialWebSocketConn(ctx stdctx.Context, t wsTarget, o WSDialOptions) (net.Conn, error) {
	dial := o.NetDialContext
	if dial == nil {
		var d net.Dialer
		dial = d.DialContext
	}
	conn, err := dial(ctx, "tcp", t.addr)
	if err != nil {
		return nil, err
	}
	if !t.secure {
		return conn, nil
	}

	cfg := &tls.Config{}
	if o.TLSConfig != nil {
		cfg = o.TLSConfig.Clone() // never modify the caller's config
	}
	if cfg.ServerName == "" {
		cfg.ServerName = t.u.Hostname()
	}
	// A config shared with an http.Transport often offers h2 first; a
	// server that picks it would then read the HTTP/1.1 upgrade as a broken
	// HTTP/2 preface.
	cfg.NextProtos = slices.DeleteFunc(slices.Clone(cfg.NextProtos), func(p string) bool { return p == "h2" })
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tc, nil
}

// clientHandshake sends the upgrade request and reads and checks the
// response. On success lead holds any bytes read past the 101's head: the
// start of the WebSocket stream.
//
// resp is returned whenever a response head was read, so the caller can see
// a refusal's status and headers; err then says why it is not a handshake.
func clientHandshake(conn net.Conn, t wsTarget, key string, req []byte, o WSDialOptions) (resp *http.Response, lead []byte, deflate *wsDeflate, err error) {
	if _, err := conn.Write(req); err != nil {
		return nil, nil, nil, err
	}

	// The response is read through a bufio.Reader, as http.ReadResponse
	// needs, over a reader capped at maxHandshakeResponse. The cap is for
	// the handshake only: WSConn later reads the conn directly, so it does
	// not carry over (the bytes bufio read ahead are handed on as lead).
	lr := &io.LimitedReader{R: conn, N: maxHandshakeResponse}
	br := bufio.NewReader(lr)
	// The URL on the request ReadResponse records in resp.Request carries
	// no credentials.
	ru := *t.u
	ru.User = nil
	resp, err = http.ReadResponse(br, &http.Request{Method: http.MethodGet, URL: &ru, Host: t.host})
	if err != nil {
		if lr.N == 0 {
			err = fmt.Errorf("%w: response head over %d bytes", ErrWebSocketBadHandshake, maxHandshakeResponse)
		}
		return nil, nil, nil, err
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		// Keep a bounded copy of the body (an error page, a JSON error) for
		// the caller; the conn is closed after this, so resp.Body cannot
		// stay a live reader on it.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHandshakeErrorBody))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil, nil, fmt.Errorf("%w: server answered %s", ErrWebSocketBadHandshake, resp.Status)
	}

	// A 1xx response has no body (http.ReadResponse gives it http.NoBody
	// and reads nothing past the head), so br stands at the first byte of
	// the WebSocket stream.
	if err := checkUpgradeResponse(resp, key, o.Subprotocols); err != nil {
		return resp, nil, nil, err
	}
	if deflate, err = clientDeflate(resp.Header, o); err != nil {
		return resp, nil, nil, err
	}
	if n := br.Buffered(); n > 0 {
		peeked, _ := br.Peek(n)
		lead = bytes.Clone(peeked)
	}
	return resp, lead, deflate, nil
}

// checkUpgradeResponse checks a 101 as RFC 6455 §4.1 requires of a client:
// it must say it switches to websocket (Upgrade and Connection tokens, case
// insensitive), prove it read this request's key (Sec-WebSocket-Accept),
// and name at most one subprotocol, one that was offered. Extensions are
// checked by clientDeflate.
func checkUpgradeResponse(resp *http.Response, key string, offered []string) error {
	if !headerListHas(resp.Header, "Upgrade", "websocket") {
		return fmt.Errorf("%w: 101 without \"Upgrade: websocket\"", ErrWebSocketBadHandshake)
	}
	if !headerListHas(resp.Header, "Connection", "upgrade") {
		return fmt.Errorf("%w: 101 without \"Connection: Upgrade\"", ErrWebSocketBadHandshake)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != wsAcceptKey(key) {
		return fmt.Errorf("%w: Sec-WebSocket-Accept does not match the key sent", ErrWebSocketBadHandshake)
	}
	switch protocols := resp.Header.Values("Sec-WebSocket-Protocol"); {
	case len(protocols) > 1:
		return fmt.Errorf("%w: server named more than one subprotocol", ErrWebSocketBadHandshake)
	case len(protocols) == 1 && !slices.Contains(offered, protocols[0]):
		return fmt.Errorf("%w: server chose subprotocol %q, which was not offered", ErrWebSocketBadHandshake, protocols[0])
	}
	return nil
}

// headerListHas reports whether any line of the list-valued header name
// lists token (see hasToken).
func headerListHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		if hasToken(v, token) {
			return true
		}
	}
	return false
}

// clientDeflate reads the server's answer to the permessage-deflate offer
// (RFC 7692 §5, §7.1) and returns the connection's compression state, or nil
// when the server declined. The client must fail the connection on anything
// it did not offer or cannot honour, so every parameter is accounted for:
//
//	server_no_context_takeover   the server resets its window per message:
//	                             inflate each message on its own (readNoContext)
//	client_no_context_takeover   the server asks the client to reset (rweb's
//	                             server always does): writeNoContext
//	server_max_window_bits=8…15  the server's window may be smaller than 32 KB;
//	                             inflating needs no change (the kept 32 KB of
//	                             history covers any smaller window)
//	client_max_window_bits       never offered, so not allowed in the answer:
//	                             compress/flate cannot use a smaller window
//	anything else, a repeat, or a second extension: the dial fails
//
// Without either no_context_takeover the window carries over between
// messages in that direction, which is what makes a stream of similar
// messages (JSON with the same keys) cheap; wsDeflate keeps the writer and
// the reader's dictionary for it.
func clientDeflate(h http.Header, o WSDialOptions) (*wsDeflate, error) {
	answer := strings.TrimSpace(strings.Join(h.Values("Sec-WebSocket-Extensions"), ","))
	if answer == "" {
		return nil, nil
	}
	if !o.Compression {
		return nil, fmt.Errorf("%w: server chose extensions that were not offered: %q", ErrWebSocketBadHandshake, answer)
	}
	bad := func(why string) error {
		return fmt.Errorf("%w: Sec-WebSocket-Extensions %q: %s", ErrWebSocketBadHandshake, answer, why)
	}

	var d *wsDeflate
	for ext := range strings.SplitSeq(answer, ",") {
		if strings.TrimSpace(ext) == "" {
			continue
		}
		if d != nil {
			return nil, bad("more than one extension, only permessage-deflate was offered")
		}
		params := strings.Split(ext, ";")
		if strings.TrimSpace(params[0]) != "permessage-deflate" {
			return nil, bad("an extension that was not offered")
		}
		level := o.CompressionLevel
		if level == 0 {
			level = defaultCompressionLevel
		}
		d = &wsDeflate{level: level}
		seen := map[string]bool{}
		for _, p := range params[1:] {
			name, value, hasValue := strings.Cut(strings.TrimSpace(p), "=")
			name = strings.TrimSpace(name)
			if name == "" {
				continue // a stray ";"
			}
			if seen[name] {
				return nil, bad("parameter " + name + " repeated")
			}
			seen[name] = true
			switch name {
			case "server_no_context_takeover":
				if hasValue {
					return nil, bad(name + " takes no value")
				}
				d.readNoContext = true
			case "client_no_context_takeover":
				if hasValue {
					return nil, bad(name + " takes no value")
				}
				d.writeNoContext = true
			case "server_max_window_bits":
				bits, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"`))
				if err != nil || bits < 8 || bits > 15 {
					return nil, bad(name + " must be 8 to 15")
				}
			case "client_max_window_bits":
				return nil, bad(name + " was not offered (this client always uses a 32 KB window)")
			default:
				return nil, bad("unknown parameter " + name)
			}
		}
	}
	return d, nil
}
