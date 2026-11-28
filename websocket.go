package rweb

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rohanthewiz/rweb/consts"
)

// WebSocket opcodes as defined in RFC 6455
const (
	wsContinuation = 0x0
	wsText         = 0x1
	wsBinary       = 0x2
	wsClose        = 0x8
	wsPing         = 0x9
	wsPong         = 0xA
)

// WebSocket close codes
const (
	wsCloseNormalClosure           = 1000
	wsCloseGoingAway               = 1001
	wsCloseProtocolError           = 1002
	wsCloseUnsupportedData         = 1003
	wsCloseNoStatusReceived        = 1005
	wsCloseAbnormalClosure         = 1006
	wsCloseInvalidFramePayloadData = 1007
	wsClosePolicyViolation         = 1008
	wsCloseMessageTooBig           = 1009
	wsCloseMandatoryExtension      = 1010
	wsCloseInternalServerErr       = 1011
	wsCloseTLSHandshake            = 1015
)

// WebSocket errors
var (
	ErrWebSocketNotUpgraded     = errors.New("connection not upgraded to websocket")
	ErrWebSocketAlreadyClosed   = errors.New("websocket connection already closed")
	ErrWebSocketInvalidOpcode   = errors.New("invalid websocket opcode")
	ErrWebSocketPayloadTooLarge = errors.New("websocket payload too large")
	// ErrWebSocketBadMask: a frame masked wrongly for its direction (RFC
	// 6455 §5.1): a client frame without a mask on a server connection, or
	// a masked server frame on a client one (DialWebSocket).
	ErrWebSocketBadMask = errors.New("websocket frame masked wrongly for its direction (client frames must be masked, server frames must not)")
	ErrWebSocketBadRSV  = errors.New("websocket frame has a reserved bit set that no extension negotiated")
	// ErrWebSocketBadControlFrame: a ping, pong or close frame longer than
	// 125 bytes or without FIN (RFC 6455 §5.5). The connection is failed
	// with close code 1002 when ReadMessage returns it. WritePing (and
	// WriteMessage/WriteMessages with PingMessage or PongMessage) return it,
	// wrapped, for a payload over 125 bytes, before anything is written; the
	// connection stays open.
	ErrWebSocketBadControlFrame = errors.New("websocket control frame longer than 125 bytes or fragmented")
	// ErrWebSocketBadFragment: a data frame out of place in the message
	// sequence (RFC 6455 §5.4): a continuation frame with no fragmented
	// message in progress, or a text or binary frame starting a new message
	// before the one in progress has had its final fragment. The connection
	// is failed with close code 1002 when ReadMessage returns it.
	ErrWebSocketBadFragment = errors.New("websocket data frame out of sequence")
	// ErrWebSocketInvalidUTF8: a text message whose payload, reassembled and
	// inflated, is not valid UTF-8 (RFC 6455 §5.6, §8.1). The connection is
	// failed with close code 1007 when ReadMessage returns it.
	ErrWebSocketInvalidUTF8 = errors.New("websocket text message is not valid UTF-8")
	// ErrWebSocketBadClose: a close frame from the peer that RFC 6455 does
	// not allow (§5.5.1, §7.4): a 1-byte body (a body starts with a 2-byte
	// status code), a status code that may not be sent (see validCloseCode:
	// 1005, 1006, 1015, unassigned or out of range), or a reason that is not
	// valid UTF-8. The connection is failed with close code 1002 (1007 for
	// the reason) when ReadMessage returns it, and no OnClose handler runs.
	ErrWebSocketBadClose = errors.New("websocket close frame malformed")
	// ErrWebSocketBadMessageType: WriteMessage or WriteMessages was given a
	// type it does not send. They send TextMessage, BinaryMessage,
	// PingMessage and PongMessage; a close goes through Close(code, reason),
	// which checks the code and reason, marks the connection closed so no
	// data frame follows (RFC 6455 §5.5.1) and waits for the peer's answer.
	// Any other value is no message type at all (0 is a continuation frame,
	// 3–7 and 11–15 are reserved opcodes) and would make a peer fail the
	// connection with 1002. Returned, wrapped, before anything is written;
	// the connection stays open.
	ErrWebSocketBadMessageType = errors.New("websocket message type not writable (WriteMessage sends text, binary, ping and pong; close with Close)")
	// ErrWebSocketOriginNotAllowed is returned by UpgradeWebSocket for a
	// handshake from an origin the route does not accept. The response is
	// already set to 403 when it is returned; a handler that returns nil
	// sends it as is.
	ErrWebSocketOriginNotAllowed = errors.New("websocket handshake from a disallowed origin")
)

// WebSocket GUID as per RFC 6455
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsAcceptKey is the Sec-WebSocket-Accept value for a Sec-WebSocket-Key:
// base64(SHA-1(key + GUID)) (RFC 6455 §4.2.2). The server sends it
// (performHandshake); a client checks it (DialWebSocket), which proves the
// 101 was written by a WebSocket server for this very request rather than
// replayed or made up by something in between.
func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// Default WebSocket configuration values
const (
	defaultMaxMessageSize = 1024 * 1024 * 10 // 10MB
	defaultPingInterval   = 30 * time.Second
	defaultPongTimeout    = 10 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	closeHandshakeTimeout = 2 * time.Second // max wait for peer's close frame response
)

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type MessageType
	Data []byte
}

// MessageType represents the type of WebSocket message
type MessageType int

const (
	// TextMessage denotes a text data message
	TextMessage MessageType = wsText
	// BinaryMessage denotes a binary data message
	BinaryMessage MessageType = wsBinary
	// CloseMessage denotes a close control message
	CloseMessage MessageType = wsClose
	// PingMessage denotes a ping control message
	PingMessage MessageType = wsPing
	// PongMessage denotes a pong control message
	PongMessage MessageType = wsPong
)

// WSConn represents a WebSocket connection
type WSConn struct {
	conn net.Conn
	// r is where frames are read from: conn itself, or — when the client's
	// first bytes arrived with its upgrade request and HTTP had already read
	// them — those bytes followed by conn (see upgradeWebSocket). Deadlines
	// and writes still go to conn directly.
	r io.Reader
	// br buffers r; readFrame reads only through it. The full read chain:
	//
	//	readFrame ─► br (bufio, 4 KB) ─► livenessReader ─► [lead bytes ─►] conn
	//	                                 (pings on: one     (MultiReader, only
	//	                                 deadline push per  when HTTP had read
	//	                                 socket read)       past the upgrade)
	//
	// Without it, each frame took three or four reads on the socket (header,
	// extended length, mask, payload), each one a syscall and, with pings
	// on, a SetReadDeadline. Through br a small frame — or a burst of them
	// queued in the socket buffer — comes in with one.
	//
	// Made by readFrame on first use rather than here, so that it wraps r
	// as upgradeWebSocket left it (lead bytes and liveness reader are added
	// after NewWSConn), and so a connection that only writes never
	// allocates its buffer. Only the holder of readSem touches it.
	br *bufio.Reader
	// hdr receives each frame header (see readFrame). A field rather than a
	// local array: a slice of a local passed to io.ReadFull escapes, which
	// cost two or three small allocations per frame. Holder of readSem only.
	hdr      [maxFrameHeader]byte
	isServer bool
	// closed is set once a close frame has been sent (Close, or the reply in
	// handleClose); every write checks it first. Atomic because it is written
	// under closeMutex and read under writeMutex — two different locks — and
	// it is stored while writeMutex is still held for the close frame, so no
	// data frame can be written after the close frame.
	closed     atomic.Bool
	closeMutex sync.Mutex
	writeMutex sync.Mutex
	// maxMessageSize is the largest message ReadMessage accepts. Atomic
	// because SetMaxMessageSize may be called from any goroutine (a hub
	// tightening the limit, say) while the connection's handler is blocked
	// in ReadMessage, which checks every frame against it; the same reason
	// the callback setters take handlersMu (N-067). A load per frame costs
	// no more than the plain read it replaces.
	maxMessageSize atomic.Int64

	// The app's callbacks. An app may set them from any goroutine — a hub
	// installing a pong handler while the connection's own handler is
	// blocked in ReadMessage — so they are guarded by handlersMu. The read
	// path takes it only when a ping, pong or close frame arrives, to copy
	// the handler out, and calls the copy with the lock released: a callback
	// may itself call a setter. Data frames never touch it.
	handlersMu    sync.Mutex
	closeHandlers []func(code int, text string)
	pingHandler   func([]byte) error
	pongHandler   func([]byte) error

	// readSem makes frame reading single-owner. Whoever reads frames holds
	// its one slot: ReadMessage for the length of the call, or Close while
	// it waits for the peer's close frame. A channel rather than a
	// sync.Mutex because Close must wait for it with a timeout (select).
	// See Close for the two cases it decides between.
	readSem chan struct{}
	// closeRcvd is set once a close frame from the peer has been read, by
	// whichever reader read it, before that reader gives up readSem: a Close
	// that then takes readSem knows the closing handshake is complete.
	closeRcvd atomic.Bool

	// Deadlines an app set explicitly (SetReadDeadline, SetWriteDeadline);
	// the zero time means none. Guarded by deadlineMu rather than by the
	// read or write path's own lock, because, as on a net.Conn, a deadline
	// may be set from any goroutine while a read or write is blocked, and
	// must reach that blocked call.
	deadlineMu    sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time

	// writeTimeout is the per-flush write deadline (WSOptions.WriteTimeout,
	// resolved): each flush gets now+writeTimeout unless an explicit
	// writeDeadline is set. 0 means none, as for a bare NewWSConn.
	writeTimeout time.Duration
	// subprotocol is the one selected at the handshake ("" for none).
	subprotocol string

	// pingInterval is how often the keepalive goroutine pings (0: no pings).
	// readWindow is how long the peer may stay silent before a read fails:
	// pingInterval + pongTimeout, or 0 for no liveness deadline. Both are set
	// once by applyOptions before the handler runs, and only read afterwards.
	pingInterval time.Duration
	readWindow   time.Duration
	// writeDeadlineSet records that a flush left a deadline on the conn, so
	// a later flush with neither a timeout nor a deadline clears it once
	// instead of on every write. Guarded by writeMutex.
	writeDeadlineSet bool
	// writeErr is sticky: once a write failed after putting part of a frame
	// on the wire, the byte stream no longer lines up with the peer's frame
	// parser, and every later write returns this error rather than sending
	// frames the peer would misread. Guarded by writeMutex.
	writeErr error

	// done is closed when the connection shuts down, enabling goroutines
	// (e.g., ping tickers) to detect closure and exit cleanly.
	done     chan struct{}
	doneOnce sync.Once

	// For managing fragmented messages. fragmenting is the state: true from
	// a text or binary frame with FIN=0 until the continuation with FIN=1.
	// It is a flag of its own rather than "fragmentedMessage != nil" because
	// a first fragment may be empty, and appending nothing to a nil slice
	// leaves it nil: the message's continuation was then refused as having no
	// message to continue. Holder of readSem only, as for every read field.
	fragmenting          bool
	fragmentedMessage    []byte
	fragmentedType       MessageType
	fragmentedCompressed bool // the first fragment had RSV1 (permessage-deflate)

	// deflate is the negotiated permessage-deflate state, nil when the
	// connection is uncompressed (websocket_deflate.go).
	deflate *wsDeflate

	// wbuf assembles each outgoing frame — header and payload — so it goes to
	// the socket in one write rather than two or three. Guarded by writeMutex.
	wbuf []byte
}

// maxRetainedWriteBuf caps the frame buffer kept between writes: one large
// message should not pin its size in memory for the connection's lifetime.
const maxRetainedWriteBuf = 1 << 20

// wsReadBufferSize is the size of br, the buffer frames are read through:
// bufio's default, the same as net/http's per-connection reader. It holds a
// small message whole, header and payload, so one socket read brings in a
// frame or several. A payload at least this large mostly bypasses it:
// bufio.Reader.Read with an empty buffer and a destination no smaller than
// the buffer reads straight into the destination, so a 64 KB message is not
// copied twice.
const wsReadBufferSize = 4096

// maxFrameHeader is the longest frame header (RFC 6455 §5.2): 2 bytes, 8 of
// extended payload length, 4 of mask key.
const maxFrameHeader = 2 + 8 + 4

// NewWSConn creates a new WebSocket connection from an existing net.Conn
// The isServer parameter indicates if this is a server-side connection
func NewWSConn(conn net.Conn, isServer bool) *WSConn {
	ws := &WSConn{
		conn:          conn,
		r:             conn,
		isServer:      isServer,
		closeHandlers: make([]func(int, string), 0),
		done:          make(chan struct{}),
		readSem:       make(chan struct{}, 1),
	}
	ws.maxMessageSize.Store(defaultMaxMessageSize)

	// Set default ping handler that responds with pong
	ws.pingHandler = func(data []byte) error {
		return ws.writePong(data)
	}

	// Set default pong handler (no-op)
	ws.pongHandler = func([]byte) error {
		return nil
	}

	return ws
}

// applyOptions configures a connection the server just upgraded from its
// route's WSOptions, resolving each zero value to its default. A bare
// NewWSConn gets none of this — no write timeout — so wrapping a conn by
// hand behaves as it always has; the defaults are a property of the
// server's WebSocket routes.
func (ws *WSConn) applyOptions(opts WSOptions) {
	ws.writeTimeout = resolveWSDuration(opts.WriteTimeout, defaultWriteTimeout)

	ws.pingInterval = resolveWSDuration(opts.PingInterval, defaultPingInterval)
	if ws.pingInterval > 0 {
		if pong := resolveWSDuration(opts.PongTimeout, defaultPongTimeout); pong > 0 {
			ws.readWindow = ws.pingInterval + pong
		}
	}
}

// startKeepalive arms the liveness read deadline and starts the ping
// goroutine, as applyOptions configured them. How the two fit together:
//
//	server:  ping ───────── ping ───────── ping ───────── ping
//	                │              │              │
//	peer:           pong           pong           ✗ (gone: no pong, no data)
//	                │              │
//	read deadline:  └► now+I+P     └► now+I+P ──────────────────► expires
//	                                                              ReadMessage
//	                                                              fails (timeout)
//
//	I = pingInterval, P = pongTimeout, I+P = readWindow
//
// Every socket read that brings bytes in — a pong, a data frame, anything —
// moves the read deadline to now+I+P (livenessReader). A live peer answers
// each ping within P, so the deadline is always pushed out before it is
// reached; a vanished peer sends nothing, and the read blocked in
// ReadMessage fails at most I+P after the last thing it heard. Extending on
// bytes rather than on whole frames also keeps a large message arriving
// slowly from timing out half way through. The liveness reader sits under
// the frame buffer (br), so the deadline moves once per socket read, not
// once per header or payload piece: frames already buffered need no
// deadline, since nothing blocks reading them.
//
// The ping goroutine exits on Done(), which closes when Close or a peer
// close frame ends the connection, or when the handler returns (finish,
// from handleRequest), so it cannot outlive the connection; it also exits
// on its first failed write, since a connection that cannot take a ping is
// already broken and the read deadline will end the handler.
func (ws *WSConn) startKeepalive() {
	if ws.readWindow > 0 {
		// Before the first read: br, made then, wraps this (see WSConn.br).
		ws.r = livenessReader{ws: ws, r: ws.r}
		ws.extendReadDeadline()
	}
	if ws.pingInterval > 0 {
		go ws.pingLoop(ws.pingInterval)
	}
}

// pingLoop sends an empty ping every interval until the connection ends.
func (ws *WSConn) pingLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ws.done:
			return
		case <-ticker.C:
			if err := ws.WritePing(nil); err != nil {
				return
			}
		}
	}
}

// livenessReader extends the connection's read deadline after every read
// that returned data (see startKeepalive).
type livenessReader struct {
	ws *WSConn
	r  io.Reader
}

func (l livenessReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	if n > 0 {
		l.ws.extendReadDeadline()
	}
	return n, err
}

// extendReadDeadline sets the read deadline to now+readWindow, or to the
// app's own deadline (SetReadDeadline) if that is earlier: an app deadline
// can shorten a read, but never switches off dead-peer detection.
//
// Not once the connection is closed: Close then owns the read deadline for
// its bounded wait on the peer's close frame, and that wait reads through
// this same reader. Close stores closed before taking deadlineMu to set its
// deadline, so under deadlineMu either Close's deadline comes last or this
// sees closed and leaves it alone.
func (ws *WSConn) extendReadDeadline() {
	ws.deadlineMu.Lock()
	defer ws.deadlineMu.Unlock()
	if ws.closed.Load() {
		return
	}
	_ = ws.conn.SetReadDeadline(ws.effectiveReadDeadline(ws.readDeadline))
}

// effectiveReadDeadline is the earlier of the app's deadline (zero: none)
// and now+readWindow. Callers hold deadlineMu and have readWindow > 0.
func (ws *WSConn) effectiveReadDeadline(app time.Time) time.Time {
	d := time.Now().Add(ws.readWindow)
	if !app.IsZero() && app.Before(d) {
		return app
	}
	return d
}

// isWebSocketSessionEnd reports whether err, returned by a WebSocket
// handler, only says that the session ended the ordinary way, so it is not
// worth a log line (handleRequest):
//
//	io.EOF, io.ErrUnexpectedEOF   peer closed the TCP connection (mid-frame, for
//	                              the latter)
//	net.ErrClosed, io.ErrClosedPipe
//	                              the conn was closed here, typically by
//	                              handleClose after the peer's close frame;
//	                              the read after it fails this way
//	ErrWebSocketAlreadyClosed     a write after the close handshake
//	ECONNRESET, EPIPE             peer went away without a close frame (tab
//	                              killed, process exited): the same event as
//	                              EOF seen from the other side of a write
//
// Everything else — a protocol error, a read timeout from the ping window
// (a vanished peer), the app's own errors — is still logged.
func isWebSocketSessionEnd(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, ErrWebSocketAlreadyClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}

// finish ends the connection's life as far as WSConn is concerned once its
// handler has returned (handleRequest calls it; the HTTP loop then closes
// the conn). Done() closes, which stops the ping goroutine, and writes from
// other goroutines (a hub broadcasting) fail fast with
// ErrWebSocketAlreadyClosed instead of on a closed socket.
func (ws *WSConn) finish() {
	ws.closed.Store(true)
	ws.signalDone()
}

// resolveWSDuration maps a WSOptions duration to its effective value:
// 0 selects def, a negative value disables (0), anything else is kept.
func resolveWSDuration(d, def time.Duration) time.Duration {
	switch {
	case d == 0:
		return def
	case d < 0:
		return 0
	}
	return d
}

// rejectWebSocketRequest answers a request to a WebSocket route
// (s.WebSocket, s.WebSocketWithOptions) that is not a valid opening
// handshake. Such a request is the client's mistake, not a server failure,
// so it is answered here with a 4xx and returns nil: handing
// ErrWebSocketNotUpgraded to the error handler meant a 500 and an [ERR] log
// line for every browser tab or health check that GETs the URL.
//
//	no "Upgrade: websocket" (a plain GET, or a HEAD) -> 426, Upgrade: websocket
//	                                                    (RFC 9110 §15.5.22)
//	an upgrade with Sec-WebSocket-Version other than 13 -> 426, Sec-WebSocket-Version: 13
//	                                                    (RFC 6455 §4.4)
//	an upgrade missing Connection: upgrade or the key  -> 400 (RFC 6455 §4.2.2)
//
// A HEAD never upgrades (see IsWebSocketUpgrade) and gets the 426 its GET
// counterpart would, whatever headers it carries.
func rejectWebSocketRequest(ctx Context) error {
	req := ctx.Request()
	isHandshakeAttempt := req.Method() == consts.MethodGet &&
		strings.EqualFold(req.Header("Upgrade"), "websocket")

	switch {
	case !isHandshakeAttempt:
		// A sender of Upgrade must also list it in Connection (RFC 9110
		// §7.8), so intermediaries do not forward it.
		ctx.Response().SetHeader("Upgrade", "websocket")
		ctx.Response().SetHeader("Connection", "Upgrade")
		return ctx.SetStatus(consts.StatusUpgradeRequired).WriteString("Upgrade Required: this endpoint serves WebSocket connections only")
	case req.Header("Sec-WebSocket-Version") != "13":
		ctx.Response().SetHeader("Sec-WebSocket-Version", "13")
		return ctx.SetStatus(consts.StatusUpgradeRequired).WriteString("Upgrade Required: unsupported WebSocket version; this server speaks 13")
	default:
		return ctx.SetStatus(consts.StatusBadRequest).WriteString("Bad Request: incomplete WebSocket handshake")
	}
}

// originAllowed reports whether a handshake may be upgraded, by its Origin
// header (RFC 6454 serialised origin, "scheme://host[:port]").
//
// Why: a browser sends the site's cookies with a WebSocket handshake to any
// host, whichever page opened it, and applies no CORS to WebSockets. Without
// this check any page on the web could open a socket to this server as the
// logged-in user (cross-site WebSocket hijacking). The Origin header is set
// by the browser and cannot be forged by page script, which makes it the
// one reliable signal here.
//
//	opts.CheckOrigin set         -> its answer, nothing else consulted
//	no Origin header             -> allowed: not a browser (curl, a Go or
//	                                mobile client), and no ambient cookies
//	                                for a page to abuse
//	origin host[:port] == Host() -> allowed: the server's own pages
//	origin host[:port] is in a   -> allowed: the server's own pages, reached
//	  trusted peer's               through a proxy that rewrites Host
//	  X-Forwarded-Host             (N-075)
//	listed in AllowedOrigins     -> allowed (full origin, bare host, or "*")
//	anything else ("null" too)   -> refused
//
// The host comparison ignores scheme so a page on http://example.com may
// reach wss://example.com (and the reverse, behind a TLS-terminating proxy
// where this server sees plain HTTP), and ignores case since host names are
// case-insensitive. Ports must match exactly: another port is another
// origin, possibly another app.
//
// X-Forwarded-Host: a proxy that rewrites Host (rweb's own Proxy sends the
// target's) hides the host the browser asked for, so the browser's pages
// failed this check unless their origin was listed by hand. The proxy reports
// that host in X-Forwarded-Host, and from a trusted peer
// (ServerOptions.TrustedProxies, as for ClientIP) it now counts like Host().
// Believing it cannot open the check to an attack: a page cannot add headers
// to a WebSocket handshake, so a forged value never comes with a victim's
// browser behind it (client_ip.go, the N-075 notes). The header is scanned
// before the trust check, so a handshake without it costs nothing more.
func originAllowed(ctx Context, opts WSOptions) bool {
	if opts.CheckOrigin != nil {
		return opts.CheckOrigin(ctx)
	}
	origin := ctx.Request().Header(consts.HeaderOrigin)
	if origin == "" {
		return true
	}

	// An opaque origin ("null": sandboxed iframes, file: pages) parses to no
	// host, matches no Host, and so is refused unless listed verbatim.
	originHost := originHostOf(origin)
	if originHost != "" && strings.EqualFold(originHost, ctx.Request().Host()) {
		return true
	}
	if originHost != "" {
		if c, ok := ctx.(*context); ok && forwardedHostHas(c.request.headers, originHost) && c.forwardedTrusted() {
			return true
		}
	}

	listed, _ := originListed(origin, originHost, opts.AllowedOrigins)
	return listed
}

// originHostOf returns the host[:port] of an Origin header value, or "" for
// an opaque origin ("null") or one that does not parse.
func originHostOf(origin string) string {
	if origin == "" {
		return ""
	}
	if u, err := url.Parse(origin); err == nil {
		return u.Host
	}
	return ""
}

// originListed reports whether an allow-list admits origin (originHost being
// originHostOf(origin)), and whether it did so through "*". It holds the
// matching rules WSOptions.AllowedOrigins and SSECfg.AllowedOrigins share:
//
//	"*"                    -> every origin, an empty one included (wildcard)
//	contains "://"         -> that origin exactly, case-insensitively
//	"null"                 -> the opaque origin, verbatim
//	anything else          -> a bare host[:port], matched against originHost,
//	                          so under any scheme
//
// Entries are trimmed of spaces and one trailing slash, since an origin
// copied from a URL bar often carries one and an Origin header never does.
// The scan stops at the first entry that admits the origin, so with both an
// exact entry and "*" listed, the earlier one decides whether wildcard is
// reported; either way the origin is admitted.
func originListed(origin, originHost string, allowed []string) (listed, wildcard bool) {
	for _, a := range allowed {
		a = strings.TrimSuffix(strings.TrimSpace(a), "/")
		switch {
		case a == "*":
			return true, true
		case strings.Contains(a, "://") || a == "null":
			if origin != "" && strings.EqualFold(a, origin) {
				return true, false
			}
		case originHost != "" && strings.EqualFold(a, originHost):
			return true, false
		}
	}
	return false, false
}

// performHandshake performs the WebSocket handshake on the server side
// This validates the client's request and sends the appropriate response.
// The returned deflate state is non-nil when permessage-deflate was
// negotiated (opts.Compression and a client offer this side can honour);
// subprotocol is the one selected from opts.Subprotocols, "" for none.
func performHandshake(ctx *context, opts WSOptions) (deflate *wsDeflate, subprotocol string, err error) {
	// Check for required headers. The Upgrade token is case-insensitive
	// (RFC 6455 §4.2.1).
	if !strings.EqualFold(ctx.request.Header("Upgrade"), "websocket") {
		return nil, "", errors.New("missing or invalid Upgrade header")
	}

	if !strings.Contains(strings.ToLower(ctx.request.Header("Connection")), "upgrade") {
		return nil, "", errors.New("missing or invalid Connection header")
	}

	key := ctx.request.Header("Sec-WebSocket-Key")
	if key == "" {
		return nil, "", errors.New("missing Sec-WebSocket-Key header")
	}

	version := ctx.request.Header("Sec-WebSocket-Version")
	if version != "13" {
		return nil, "", errors.New("unsupported WebSocket version")
	}

	// Set response headers for successful upgrade
	ctx.response.SetStatus(101) // Switching Protocols
	ctx.response.SetHeader("Upgrade", "websocket")
	ctx.response.SetHeader("Connection", "Upgrade")
	ctx.response.SetHeader("Sec-WebSocket-Accept", wsAcceptKey(key))

	// Subprotocol: only one the route lists may be named in the 101. The
	// first offered protocol used to be echoed back unchecked, which told the
	// client the handler speaks a protocol it may never have heard of. A
	// client whose offer is declined gets no header, and decides for itself
	// whether to carry on without a subprotocol (RFC 6455 §4.1 only fails a
	// response that names one the client did not offer).
	if subprotocol = selectSubprotocol(ctx.request.headers, opts.Subprotocols); subprotocol != "" {
		ctx.response.SetHeader("Sec-WebSocket-Protocol", subprotocol)
	}

	if opts.Compression {
		if offer, ok := parseDeflateOffer(ctx.request.Header("Sec-WebSocket-Extensions")); ok {
			ctx.response.SetHeader("Sec-WebSocket-Extensions", offer.response())
			deflate = newServerDeflate(offer, opts.CompressionLevel)
		}
	}
	return deflate, subprotocol, nil
}

// selectSubprotocol returns the first subprotocol the client offers that
// supported lists, or "" when there is none.
//
// The offer is every Sec-WebSocket-Protocol header line, each a
// comma-separated list, in the client's order of preference (RFC 6455
// §4.1, §11.3.4); the first match in that order wins, so the client's
// preference decides between two protocols both sides speak. Tokens are
// compared exactly: subprotocol names are case-sensitive.
func selectSubprotocol(headers []Header, supported []string) string {
	if len(supported) == 0 {
		return ""
	}
	for _, h := range headers {
		if !strings.EqualFold(h.Key, "Sec-WebSocket-Protocol") {
			continue
		}
		for _, offered := range strings.Split(h.Value, ",") {
			offered = strings.TrimSpace(offered)
			if offered != "" && slices.Contains(supported, offered) {
				return offered
			}
		}
	}
	return ""
}

// ReadMessage reads a complete message from the WebSocket connection
// It handles fragmentation and returns the complete message
//
// It holds the read side (readSem) for the whole call, so it never reads
// alongside a Close waiting for the peer's close frame, nor alongside a
// second ReadMessage: calls from two goroutines take turns rather than
// splitting frames between them. After a Close on another goroutine it
// fails (the connection is closed by then) instead of reading.
//
// Once the peer's close frame has been read (returned as a CloseMessage, or
// read by Close), every later call returns ErrWebSocketAlreadyClosed at once:
// RFC 6455 §5.5.1 forbids an endpoint any data frame after its close frame,
// so there is nothing left to wait for. Without this check, a handler that
// read on past the CloseMessage after the server's own close (Shutdown's
// 1001, which leaves the conn open for the handler's loop to close) blocked
// until the 2 s closeHandshakeTimeout deadline, and Shutdown waited with it
// (N-076). ErrWebSocketAlreadyClosed counts as an ordinary session end
// (isWebSocketSessionEnd), so a handler returning it logs nothing.
func (ws *WSConn) ReadMessage() (*WSMessage, error) {
	ws.readSem <- struct{}{}
	defer func() { <-ws.readSem }()

	// closeRcvd is stored before the reader that saw the close frame gives
	// readSem up, so holding readSem here sees it.
	if ws.closeRcvd.Load() {
		return nil, ErrWebSocketAlreadyClosed
	}

	for {
		frameType, fin, rsv1, data, err := ws.readFrame()
		if err != nil {
			// A frame header that breaks the rules fails the connection with
			// its code; an I/O error (EOF, timeout, reset) has no peer left
			// to tell, so it is returned as is (frameFailCode says which).
			if code, ok := frameFailCode(err); ok {
				ws.failConnection(code)
			}
			return nil, err
		}
		// RSV1 is permessage-deflate's "this message is compressed" (RFC 7692
		// §6), and only means that on the FIRST frame of a data message. Set
		// anywhere else, or without the extension, the peer is speaking a
		// protocol this connection never agreed to.
		if rsv1 && (ws.deflate == nil || (frameType != wsText && frameType != wsBinary)) {
			ws.failConnection(wsCloseProtocolError)
			return nil, ErrWebSocketBadRSV
		}

		// The data frames of a connection follow one sequence (RFC 6455
		// §5.4): each message is a text or binary frame, then, if that frame
		// had FIN=0, continuation frames up to one with FIN=1. Control frames
		// may come between fragments; another message's frames may not.
		//
		//	           text/binary FIN=1 ──► message returned
		//	  idle ───┤
		//	   ▲       text/binary FIN=0 ──► fragmenting ──┬─ continuation FIN=0 ─► (stays)
		//	   │                                           └─ continuation FIN=1 ─► message returned
		//	   └────────────────────────────────────────────────────────────────────────┘
		//
		//	  idle + continuation             ─► fail, 1002 (nothing to continue)
		//	  fragmenting + text/binary       ─► fail, 1002 (messages interleaved)
		//
		// Before these checks a text or binary frame mid-message silently
		// replaced the partial message (FIN=0), or was returned with the stale
		// fragments left for the next continuation to append to (FIN=1): a
		// misbehaving peer got garbled messages instead of a protocol error.
		switch frameType {
		case wsText, wsBinary:
			if ws.fragmenting {
				ws.failConnection(wsCloseProtocolError)
				kind := "text"
				if frameType == wsBinary {
					kind = "binary"
				}
				return nil, fmt.Errorf("%w: a %s frame while a fragmented message is in progress", ErrWebSocketBadFragment, kind)
			}
			if fin {
				// Unfragmented message — the common fast path
				if rsv1 {
					if data, err = ws.inflate(data); err != nil {
						return nil, err
					}
				}
				return ws.dataMessage(MessageType(frameType), data)
			}
			// Start of a fragmented message (FIN=0 on first frame per RFC 6455 §5.4)
			ws.fragmenting = true
			ws.fragmentedType = MessageType(frameType)
			ws.fragmentedCompressed = rsv1
			ws.fragmentedMessage = append(ws.fragmentedMessage[:0], data...)

		case wsContinuation:
			// Continuation without a preceding text/binary start frame is a protocol error
			if !ws.fragmenting {
				ws.failConnection(wsCloseProtocolError)
				return nil, fmt.Errorf("%w: unexpected continuation frame (no fragmented message in progress)", ErrWebSocketBadFragment)
			}
			ws.fragmentedMessage = append(ws.fragmentedMessage, data...)
			// The per-frame size check sees one fragment at a time; the
			// message as a whole is held to the same limit.
			if int64(len(ws.fragmentedMessage)) > ws.maxMessageSize.Load() {
				ws.fragmenting = false
				ws.fragmentedMessage = nil
				ws.failConnection(wsCloseMessageTooBig)
				return nil, ErrWebSocketPayloadTooLarge
			}
			if fin {
				// Final fragment — assemble and return the complete message
				data := ws.fragmentedMessage
				ws.fragmenting = false
				ws.fragmentedMessage = nil
				if ws.fragmentedCompressed {
					if data, err = ws.inflate(data); err != nil {
						return nil, err
					}
				}
				return ws.dataMessage(ws.fragmentedType, data)
			}
			// More fragments expected — keep reading

		case wsClose:
			// Recorded before readSem is given up (on return), so a Close
			// waiting for it sees the handshake completed (see Close). Also
			// for a malformed close frame: the peer has sent its close frame
			// either way, so there is nothing left to wait for from it, and a
			// later ReadMessage returns ErrWebSocketAlreadyClosed.
			ws.closeRcvd.Store(true)
			code, text, failCode, err := parseCloseBody(data)
			if err != nil {
				// A close frame that breaks the rules fails the connection
				// like any other protocol error (RFC 6455 §7.1.7): no echo, and
				// no OnClose handlers, so a handler only ever sees a code that
				// may be sent (or 1005 for none) and a UTF-8 reason. Before
				// this, a bad code was echoed back as is and a non-UTF-8 reason
				// reached the handlers (N-082).
				ws.failConnection(failCode)
				return nil, err
			}
			ws.handleClose(code, text)
			return &WSMessage{Type: CloseMessage, Data: data}, nil

		case wsPing:
			// Handle ping frame
			ws.handlersMu.Lock()
			h := ws.pingHandler
			ws.handlersMu.Unlock()
			if h != nil {
				if err := ws.runControlHandler(h, data); err != nil {
					return nil, err
				}
			}
			// Continue reading for the next message

		case wsPong:
			// Handle pong frame
			ws.handlersMu.Lock()
			h := ws.pongHandler
			ws.handlersMu.Unlock()
			if h != nil {
				if err := ws.runControlHandler(h, data); err != nil {
					return nil, err
				}
			}
			// Continue reading for the next message

		default:
			// Opcodes 0x3-0x7 and 0xB-0xF are reserved (RFC 6455 §5.2); no
			// extension here defines them, so the frame cannot be parsed.
			ws.failConnection(wsCloseProtocolError)
			return nil, fmt.Errorf("%w: %d", ErrWebSocketInvalidOpcode, frameType)
		}
	}
}

// frameFailCode maps an error from readFrame to the close code that fails
// the connection for it (RFC 6455 §7.1.7, codes from §7.4.1), and reports
// false for an error that is not the peer breaking the protocol:
//
//	ErrWebSocketBadControlFrame   1002  control frame fragmented or > 125 bytes
//	ErrWebSocketBadRSV            1002  RSV2/RSV3 set (no extension defines them)
//	ErrWebSocketBadMask           1002  masking wrong for the peer's role (§5.1)
//	ErrWebSocketPayloadTooLarge   1009  frame over the message limit, or a 64-bit
//	                                    length with its top bit set
//	anything else                 —     I/O: EOF, reset, deadline, closed conn
//
// A 64-bit length with its top bit set is strictly a framing error (§5.2),
// but readFrame reports it as too large (the length cannot be held), and
// 1009 tells the peer as much; either code ends the connection.
//
// I/O errors get no close frame: the peer has gone, or the read deadline
// (keepalive's dead-peer timeout) says it is not there, and a write to it
// would only wait out closeHandshakeTimeout.
func frameFailCode(err error) (int, bool) {
	switch {
	case errors.Is(err, ErrWebSocketBadControlFrame),
		errors.Is(err, ErrWebSocketBadRSV),
		errors.Is(err, ErrWebSocketBadMask):
		return wsCloseProtocolError, true
	case errors.Is(err, ErrWebSocketPayloadTooLarge):
		return wsCloseMessageTooBig, true
	}
	return 0, false
}

// inflate decompresses a complete compressed data message (unfragmented, or
// reassembled) for ReadMessage, and fails the connection when it cannot:
// 1009 when the message inflates past the message limit (a deflate bomb is
// the usual case: the frame size check only saw the compressed size), 1007
// for anything else, data that does not inflate (§7.4.1: "data within a
// message that was not consistent with the type of the message"; RFC 7692
// names no code for it, and a compressed message that does not inflate is
// data not consistent with its RSV1 bit).
func (ws *WSConn) inflate(data []byte) ([]byte, error) {
	out, err := ws.deflate.decompress(data, ws.maxMessageSize.Load())
	if err != nil {
		if errors.Is(err, ErrWebSocketPayloadTooLarge) {
			ws.failConnection(wsCloseMessageTooBig)
		} else {
			ws.failConnection(wsCloseInvalidFramePayloadData)
		}
		return nil, err
	}
	return out, nil
}

// dataMessage is the last step of ReadMessage for a complete data message
// (unfragmented, or reassembled, and inflated if it was compressed): a text
// message must be valid UTF-8 (RFC 6455 §5.6), and one that is not fails the
// connection with 1007, the code for data inconsistent with its message type
// (§8.1, §7.4.1). Binary messages are returned as they are.
//
// The check runs once on the whole message, not frame by frame. A sender may
// split a message at any byte, so a fragment can end mid-character and only
// the reassembled text can be judged. A compressed message is judged after
// inflating: UTF-8 is a property of the text, not of its deflate stream.
// Waiting for the whole message lets a peer make this side buffer invalid
// text up to the message limit before it is refused, which any valid message
// of that size can do too.
//
// The cost is one utf8.Valid pass over each text payload, which stops at
// the first bad byte and runs eight bytes at a time over ASCII. Measured on
// BenchmarkWSEcho (medians of 8 runs interleaved with the code before the
// check): text16 +0.2 %, text1K +0.8 %, text16x64 +0.7 %, each inside the
// runs' own spread, and no allocation.
func (ws *WSConn) dataMessage(typ MessageType, data []byte) (*WSMessage, error) {
	if typ == TextMessage && !utf8.Valid(data) {
		ws.failConnection(wsCloseInvalidFramePayloadData)
		return nil, ErrWebSocketInvalidUTF8
	}
	return &WSMessage{Type: typ, Data: data}, nil
}

// parseCloseBody reads the body of a close frame from the peer (RFC 6455
// §5.5.1): empty, or a 2-byte status code followed by a UTF-8 reason.
//
//	body     meaning                  result
//	0 bytes  no status code           code 1005 (wsCloseNoStatusReceived), reason ""
//	1 byte   half a status code       fail with 1002
//	≥ 2      code, then reason        code not validCloseCode → fail with 1002
//	                                  reason not UTF-8        → fail with 1007
//
// 1005 for an empty body is how an API reports "no status code" (§7.1.5; a
// browser's CloseEvent says the same); it is never sent on the wire, which
// closeFramePayload sees to. failCode is the code to fail the connection with
// when err is not nil; err wraps ErrWebSocketBadClose with what was wrong.
//
// The reason is checked with utf8.Valid on its own (§5.5.1: "the data MUST
// be UTF-8"), and 1007 is the code for data not consistent with its type
// (§7.4.1), as for a text message (dataMessage).
func parseCloseBody(data []byte) (code int, reason string, failCode int, err error) {
	switch {
	case len(data) == 0:
		return wsCloseNoStatusReceived, "", 0, nil
	case len(data) == 1:
		return 0, "", wsCloseProtocolError,
			fmt.Errorf("%w: a 1-byte body (a body starts with a 2-byte status code)", ErrWebSocketBadClose)
	}
	code = int(binary.BigEndian.Uint16(data[:2]))
	if !validCloseCode(code) {
		return 0, "", wsCloseProtocolError,
			fmt.Errorf("%w: status code %d may not be sent", ErrWebSocketBadClose, code)
	}
	if !utf8.Valid(data[2:]) {
		return 0, "", wsCloseInvalidFramePayloadData,
			fmt.Errorf("%w: the reason is not valid UTF-8", ErrWebSocketBadClose)
	}
	return code, string(data[2:]), 0, nil
}

// validCloseCode reports whether code may appear in a close frame on the
// wire (RFC 6455 §7.4.1, §7.4.2, and the IANA "WebSocket Close Code Number"
// registry):
//
//	code         meaning                                          sendable
//	0–999        not used                                          no
//	1000–1003    normal, going away, protocol error, unsupported   yes
//	1004         reserved                                          no
//	1005, 1006   no status, abnormal closure: an API reports them   no
//	1007–1011    defined by RFC 6455                               yes
//	1012–1014    registered since: service restart, try again       yes
//	             later, bad gateway
//	1015         TLS handshake failure: an API reports it           no
//	1016–2999    reserved for future standards, unassigned         no
//	3000–3999    registered by libraries and frameworks            yes
//	4000–4999    private use, by agreement of the two ends         yes
//	5000–65535   out of range                                      no
//
// 1012–1014 are accepted although RFC 6455 does not define them: they are
// registered, for a server that is restarting (1012), overloaded (1013) or
// a gateway whose upstream failed (1014), so refusing them would fail
// exactly the close a client should act on by reconnecting later.
// Unassigned codes in 1016–2999 are refused, as the Autobahn test suite
// expects: there is no meaning to hand an app.
func validCloseCode(code int) bool {
	switch {
	case code >= 1000 && code <= 1003:
		return true
	case code >= 1007 && code <= 1014:
		return true
	case code >= 3000 && code <= 4999:
		return true
	}
	return false
}

// maxCloseReason is the longest close reason that fits a close frame: a
// control frame's payload is at most 125 bytes (RFC 6455 §5.5), 2 of which
// are the status code.
const maxCloseReason = 125 - 2

// closeFramePayload is the body of a close frame this side sends: the code,
// then the reason. It never builds a body a peer would refuse, since Close's
// job is to close and a refused close frame loses the code and reason both
// (the peer fails the connection with 1002 instead):
//
//	input                               body sent
//	code 1005 (no status code)          empty (1005 may not be sent, §7.4.1)
//	code not validCloseCode             empty, reason dropped
//	  (0, 1004, 1006, 1015, unassigned
//	   1016–2999, ≥ 5000)
//	reason not UTF-8                    invalid bytes → U+FFFD (§5.5.1: UTF-8)
//	reason over 123 bytes               cut at 123, at a character boundary
//
// An unsendable code goes out as no code at all, as 1005 already did, rather
// than being swapped for 1000 or 1011: those would tell the peer "normal" or
// "server error", a meaning the app did not give. A reason cannot go without
// a code (§5.5.1), so it is dropped with it. The reason is informational
// only (§7.1.6), so cutting it is a smaller loss than the frame refused.
//
// handleClose uses it to answer an empty close frame with an empty one
// instead of with 1005 on the wire (N-082), and Close uses it so an OnClose
// handler that answers with the code it was given (1005 for an empty close
// frame) gets the same. Before N-084, Close sent any code and reason as
// given: ws.Close(1011, err.Error()) with a long error string put a control
// frame over 125 bytes on the wire.
func closeFramePayload(code int, reason string) []byte {
	if !validCloseCode(code) {
		return nil
	}
	reason = closeReason(reason)
	data := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(data[:2], uint16(code))
	copy(data[2:], reason)
	return data
}

// closeReason makes reason sendable in a close frame: valid UTF-8 (each
// invalid byte sequence replaced with U+FFFD) and at most maxCloseReason
// bytes, cut before the first character that would not fit whole, so the
// cut never leaves half a character (which would be invalid UTF-8 again).
// A short, valid reason, the usual case, is returned as is.
func closeReason(reason string) string {
	if len(reason) <= maxCloseReason && utf8.ValidString(reason) {
		return reason
	}
	reason = strings.ToValidUTF8(reason, string(utf8.RuneError))
	if len(reason) <= maxCloseReason {
		return reason
	}
	// Step back from the byte limit to the start of the character that
	// straddles it (at most utf8.UTFMax-1 bytes): reason[:cut] then ends
	// on a whole character.
	cut := maxCloseReason
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut]
}

// runControlHandler calls the app's ping or pong handler with the read side
// handed back for the duration (readSem released, then taken again).
//
// Why: the handler is app code, and may call Close. Close needs the read
// side to wait for the peer's close frame; if ReadMessage kept it across
// the call, a Close made from inside the handler would wait on its own
// goroutine and sit out the full close-handshake timeout. Released, the
// Close reads the peer's answer itself and returns as soon as it arrives.
// Nothing is read between frames, so handing the read side over here never
// splits a frame; when the handler returns, ReadMessage waits for the read
// side again (a Close in progress finishes first) and carries on, failing
// at its next read if the connection was closed meanwhile.
//
// The deferred re-acquire also runs when the handler panics, so the slot
// ReadMessage's own deferred release gives back is always one it holds.
func (ws *WSConn) runControlHandler(h func([]byte) error, data []byte) error {
	<-ws.readSem
	defer func() { ws.readSem <- struct{}{} }()
	return h(data)
}

// checkWriteType refuses a MessageType that WriteMessage / WriteMessages do
// not send (ErrWebSocketBadMessageType, wrapped with the type).
//
// Why a refusal and not a frame: the type used to go to appendFrame as the
// opcode unchecked, so
//
//	CloseMessage       a close frame that bypassed Close: closed not set, so
//	                   data frames could follow it (RFC 6455 §5.5.1 forbids
//	                   any), code and reason unchecked (closeFramePayload
//	                   skipped), and no wait for the peer's answer
//	0                  a continuation frame with no message in progress
//	3–7, 11–15, other  a reserved opcode (or, past 15, bits spilling into
//	                   RSV/FIN of the header byte)
//
// each of which a peer fails with 1002. CloseMessage is refused rather than
// routed to Close because Close needs a code and reason, not a raw payload,
// and blocks up to closeHandshakeTimeout: neither fits a call that writes
// one frame. The error names Close for that case.
func checkWriteType(mt MessageType) error {
	switch mt {
	case TextMessage, BinaryMessage, PingMessage, PongMessage:
		return nil
	case CloseMessage:
		return fmt.Errorf("%w: CloseMessage; use Close(code, reason)", ErrWebSocketBadMessageType)
	}
	return fmt.Errorf("%w: %d", ErrWebSocketBadMessageType, int(mt))
}

// WriteMessage writes a message to the WebSocket connection: TextMessage or
// BinaryMessage, or a PingMessage / PongMessage control frame (at most 125
// bytes, else ErrWebSocketBadControlFrame). Any other type, CloseMessage
// included, returns ErrWebSocketBadMessageType and writes nothing; close
// with Close(code, reason).
//
// On a closed connection it returns ErrWebSocketAlreadyClosed whatever the
// type. That check comes first so that an echo loop handing ReadMessage's
// result back (WriteMessage(msg.Type, msg.Data)) still gets
// ErrWebSocketAlreadyClosed for the peer's CloseMessage, as before the type
// check: ReadMessage has answered that close and closed the connection by
// the time it returns it.
func (ws *WSConn) WriteMessage(messageType MessageType, data []byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed.Load() {
		return ErrWebSocketAlreadyClosed
	}
	if err := checkWriteType(messageType); err != nil {
		return err
	}

	return ws.writeFrame(int(messageType), data)
}

// WriteMessages writes several messages of one type in a single write to the
// socket: each is still its own WebSocket message, but a writer that has a
// queue to drain pays one syscall (and, on TLS, one flush of records) for the
// batch instead of one per message. The types and errors are WriteMessage's;
// a refusal refuses the whole batch, nothing written.
func (ws *WSConn) WriteMessages(messageType MessageType, data ...[]byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed.Load() {
		return ErrWebSocketAlreadyClosed
	}
	if err := checkWriteType(messageType); err != nil {
		return err
	}
	ws.wbuf = ws.wbuf[:0]
	for _, d := range data {
		var err error
		if ws.wbuf, err = ws.appendFrame(ws.wbuf, int(messageType), d); err != nil {
			return err
		}
	}
	return ws.flushFrames()
}

// readFrame reads a single WebSocket frame, returning the opcode, FIN bit, RSV1
// bit, and payload. The FIN bit indicates whether this is the final fragment of
// a message (RFC 6455 §5.2); RSV1 marks a compressed message when
// permessage-deflate is in use. RSV2/RSV3 have no meaning here and fail the read.
//
// Callers hold the read side (readSem), or are a test's only reader.
func (ws *WSConn) readFrame() (opcode int, fin bool, rsv1 bool, payload []byte, err error) {
	if ws.br == nil {
		ws.br = bufio.NewReaderSize(ws.r, wsReadBufferSize)
	}

	// The header is read in two steps into ws.hdr: the two fixed bytes,
	// then, in one read, whatever they say follows before the payload.
	//
	//	 hdr[0]                  hdr[1]           hdr[2:]
	//	┌───┬──────┬──────┐  ┌────┬─────────┐  ┌────────────────────┬───────────────┐
	//	│FIN│RSV1-3│opcode│  │MASK│ len (7) │  │ ext len: 0, 2 or 8 │ mask key: 0/4 │ payload…
	//	└───┴──────┴──────┘  └────┴─────────┘  └────────────────────┴───────────────┘
	//	 ◄──────── step 1: 2 bytes ─────────►   ◄───── step 2: 0 to 12 bytes ──────►
	//
	// len 0-125 is the payload length; 126 and 127 announce a 16- or 64-bit
	// extended length. Both steps are usually copies out of br's buffer.
	hdr := ws.hdr[:]
	if _, err := io.ReadFull(ws.br, hdr[:2]); err != nil {
		return 0, false, false, nil, err
	}

	// Parse first byte — FIN (bit 0), RSV1-3 (bits 1-3) and opcode (bits 4-7)
	fin = (hdr[0] & 0x80) != 0
	rsv1 = (hdr[0] & 0x40) != 0
	if hdr[0]&0x30 != 0 {
		return 0, false, false, nil, ErrWebSocketBadRSV
	}
	opcode = int(hdr[0] & 0x0F)

	// Parse second byte
	masked := (hdr[1] & 0x80) != 0
	payloadLen := int64(hdr[1] & 0x7F)

	// Client frames must be masked, server frames must not be masked
	if ws.isServer && !masked {
		return 0, false, false, nil, ErrWebSocketBadMask
	}
	if !ws.isServer && masked {
		return 0, false, false, nil, ErrWebSocketBadMask
	}

	// Step 2: extended payload length and mask key, together.
	extLen := 0
	switch payloadLen {
	case 126:
		extLen = 2
	case 127:
		extLen = 8
	}
	rest := extLen
	if masked {
		rest += 4
	}
	if rest > 0 {
		if _, err := io.ReadFull(ws.br, hdr[2:2+rest]); err != nil {
			return 0, false, false, nil, err
		}
	}
	switch extLen {
	case 2:
		payloadLen = int64(binary.BigEndian.Uint16(hdr[2:4]))
	case 8:
		payloadLen = int64(binary.BigEndian.Uint64(hdr[2:10]))
		// The 64-bit length's top bit must be 0 (RFC 6455 §5.2). Set, the
		// conversion above makes payloadLen negative, which slips past the
		// size limit below and panics in make.
		if payloadLen < 0 {
			return 0, false, false, nil, ErrWebSocketPayloadTooLarge
		}
	}

	// Control frames (opcode 0x8 and up) are at most 125 bytes and never
	// fragmented (RFC 6455 §5.5). Checked on the header, before the payload
	// is read, so a peer cannot have the server buffer (and, for a ping,
	// echo back) a "control frame" as large as the message limit.
	if opcode >= wsClose && (!fin || payloadLen > 125) {
		return 0, false, false, nil, ErrWebSocketBadControlFrame
	}

	// Check payload size
	if payloadLen > ws.maxMessageSize.Load() {
		return 0, false, false, nil, ErrWebSocketPayloadTooLarge
	}

	// Read payload. Its own allocation: the slice is handed to the app as
	// WSMessage.Data and may be kept, so it cannot be a reused buffer.
	payload = make([]byte, payloadLen)
	if _, err := io.ReadFull(ws.br, payload); err != nil {
		return 0, false, false, nil, err
	}

	// Unmask payload if needed; the key is the last 4 header bytes.
	if masked {
		var key [4]byte
		copy(key[:], hdr[2+extLen:2+extLen+4])
		maskBytes(key, 0, payload)
	}

	return opcode, fin, rsv1, payload, nil
}

// maskBytes XORs b with the mask key (RFC 6455 §5.3) as the part of a
// payload that starts at offset pos: b[i] ^= key[(pos+i)%4]. It returns the
// offset just past b, mod 4, for masking a payload in pieces. Masking and
// unmasking are the same operation.
//
// It works eight bytes at a time. The key, rotated to pos, is repeated into
// a 64-bit word, and each 8-byte chunk of b is loaded, XORed and stored as
// one word; the 0-7 bytes left over go one at a time:
//
//	key (rotated to pos)   k0 k1 k2 k3
//	word                   k0 k1 k2 k3 k0 k1 k2 k3
//	b                      b0 b1 b2 b3 b4 b5 b6 b7 │ b8 … b15 │ … │ tail
//
// Every chunk starts at a multiple of 8 from b's start, so also at a
// multiple of 4: each lines up with the same word, and the tail with the
// key from its start. The loads and stores are little-endian, as is the
// word (k0 in the low byte), so byte j of a chunk meets k[j%4] whatever the
// machine's byte order. binary.LittleEndian compiles to single unaligned
// 8-byte moves on amd64 and arm64; on a platform without those it is byte
// moves, slower but still correct, and no unsafe is needed.
//
// The byte-at-a-time loop this replaces (with i%4 per byte) was most of the
// read cost of a large message: about a third of a 64 KB echo round trip.
func maskBytes(key [4]byte, pos int, b []byte) int {
	if pos&3 != 0 {
		key = [4]byte{key[pos&3], key[(pos+1)&3], key[(pos+2)&3], key[(pos+3)&3]}
	}
	k32 := binary.LittleEndian.Uint32(key[:])
	k64 := uint64(k32) | uint64(k32)<<32

	n := len(b)
	for len(b) >= 8 {
		binary.LittleEndian.PutUint64(b, binary.LittleEndian.Uint64(b)^k64)
		b = b[8:]
	}
	for i := range b {
		b[i] ^= key[i&3]
	}
	return (pos + n) & 3
}

// writeFrame writes one complete message (or control frame) as a single frame,
// in a single write. Callers hold writeMutex.
func (ws *WSConn) writeFrame(opcode int, data []byte) error {
	var err error
	if ws.wbuf, err = ws.appendFrame(ws.wbuf[:0], opcode, data); err != nil {
		return err
	}
	return ws.flushFrames()
}

// flushFrames sends what appendFrame assembled in wbuf, under the write
// deadline, and lets go of an oversized buffer. Callers hold writeMutex.
func (ws *WSConn) flushFrames() error {
	defer func() {
		if cap(ws.wbuf) > maxRetainedWriteBuf {
			ws.wbuf = nil
		} else {
			ws.wbuf = ws.wbuf[:0]
		}
	}()
	if ws.writeErr != nil {
		return ws.writeErr
	}

	ws.applyWriteDeadline()
	n, err := ws.conn.Write(ws.wbuf)
	if err != nil && n > 0 {
		// Part of a frame went out. A write that failed before sending
		// anything (n == 0, e.g. a deadline already past) leaves the stream
		// intact, so, as on a net.Conn, the app may set a new deadline and
		// write again; after a partial frame it may not.
		ws.writeErr = err
	}
	return err
}

// applyWriteDeadline puts this flush's write deadline on the conn:
//
//	explicit SetWriteDeadline(t), t non-zero  -> t, as given, even if past
//	                                             (the write then fails at
//	                                             once, as on a net.Conn)
//	else writeTimeout > 0                     -> now + writeTimeout
//	else                                      -> none (cleared if a previous
//	                                             flush set one)
//
// The explicit deadline used to be applied only while still in the future,
// so an expired one silently stopped applying and writes ran unbounded.
// Callers hold writeMutex.
func (ws *WSConn) applyWriteDeadline() {
	ws.deadlineMu.Lock()
	explicit := ws.writeDeadline
	ws.deadlineMu.Unlock()

	switch {
	case !explicit.IsZero():
		_ = ws.conn.SetWriteDeadline(explicit)
		ws.writeDeadlineSet = true
	case ws.writeTimeout > 0:
		_ = ws.conn.SetWriteDeadline(time.Now().Add(ws.writeTimeout))
		ws.writeDeadlineSet = true
	case ws.writeDeadlineSet:
		_ = ws.conn.SetWriteDeadline(time.Time{})
		ws.writeDeadlineSet = false
	}
}

// appendFrame appends one frame carrying data to buf: header, extended
// length, mask key and (masked) payload, contiguous. Building the frame in one
// buffer is what lets it leave in one write: the header and the payload used
// to be separate writes, which on an unbuffered socket is separate syscalls
// and, with TCP_NODELAY, separate packets.
//
// A text or binary message on a connection that negotiated permessage-deflate
// is compressed first, and marked with RSV1.
//
// A control frame's payload over 125 bytes (RFC 6455 §5.5) is refused with
// ErrWebSocketBadControlFrame and nothing appended: a peer fails the
// connection on such a frame (rweb's own ReadMessage does), so sending it
// would end the connection with 1002 from the other side instead of the
// error going to the app that asked. Every frame this side sends is built
// here, so the check covers WritePing, the pong answer, and WriteMessage /
// WriteMessages with PingMessage or PongMessage alike (they refuse
// CloseMessage before this: checkWriteType). Close never gets here with more
// (closeFramePayload cuts the reason).
func (ws *WSConn) appendFrame(buf []byte, opcode int, data []byte) ([]byte, error) {
	if opcode >= wsClose && len(data) > 125 {
		return buf, fmt.Errorf("%w: a %d-byte payload (at most 125)", ErrWebSocketBadControlFrame, len(data))
	}
	b0 := byte(0x80 | opcode) // FIN = 1, opcode
	if ws.deflate != nil && (opcode == wsText || opcode == wsBinary) {
		compressed, err := ws.deflate.compress(data)
		if err != nil {
			return buf, err
		}
		data = compressed
		b0 |= 0x40 // RSV1: compressed (RFC 7692 §6)
	}

	var b1 byte
	if !ws.isServer {
		b1 = 0x80 // Set mask bit for client frames
	}

	// Determine payload length encoding
	dataLen := len(data)
	switch {
	case dataLen < 126:
		buf = append(buf, b0, b1|byte(dataLen))
	case dataLen <= 65535:
		buf = append(buf, b0, b1|126)
		buf = binary.BigEndian.AppendUint16(buf, uint16(dataLen))
	default:
		buf = append(buf, b0, b1|127)
		buf = binary.BigEndian.AppendUint64(buf, uint64(dataLen))
	}

	if ws.isServer {
		// Server frames are not masked
		return append(buf, data...), nil
	}

	// Client frames: mask key, then the payload masked with it
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return buf, err
	}
	buf = append(buf, mask[:]...)
	start := len(buf)
	buf = append(buf, data...)
	maskBytes(mask, 0, buf[start:])
	return buf, nil
}

// Close closes the WebSocket connection with the given code and reason
//
// It sends the close frame, waits at most closeHandshakeTimeout for the
// peer's answering close frame, then closes the TCP connection. Safe to call
// from any goroutine, including while the connection's handler is blocked in
// ReadMessage; only the first call does anything, later ones return nil.
//
// The wait must not add a second reader to the connection: two goroutines
// reading at once split the byte stream between them, each parsing the
// other's half-frames as headers. So Close waits for the read side
// (readSem) and reads only if it gets it:
//
//	case 1: nobody in ReadMessage            case 2: handler blocked in ReadMessage
//
//	Close             peer                   ReadMessage        Close            peer
//	  │ close frame ───►│                     │ holds readSem     │                │
//	  │ takes readSem   │                     │ (blocked)         │ close frame ──►│
//	  │ reads, dropping │                     │                   │ waits for      │
//	  │ any data, until │                     │◄──────────────────┼────────────────┤ close frame
//	  │◄────────────────┤ close frame         │ closeRcvd = true  │ readSem        │
//	  │ conn.Close()    │                     │ releases readSem ►│ takes it, sees │
//	                                          │ returns           │ closeRcvd      │
//	                                          │ CloseMessage      │ conn.Close()   │
//
// In case 2 the ReadMessage that was blocked returns the peer's close frame
// to the handler, as it would have without the Close. If it returns
// something else first — a data message the peer sent before answering, or
// a read error — Close takes the read side as it is released and carries
// on as in case 1; the handler's next ReadMessage then waits for Close and
// fails on the closed connection. Every path is bounded: the read deadline
// set here ends the reads, and the timer ends the wait even if the app
// moves the read deadline out again.
//
// A code of 1005 ("no status code", what an OnClose handler is given for a
// close frame with no body) sends a close frame with no body, reason
// dropped: 1005 itself may not be sent (RFC 6455 §7.4.1). So does any other
// code that may not be sent (0, 1004, 1006, 1015, 1016–2999, 5000 and up;
// see validCloseCode). A reason is sent as valid UTF-8, invalid bytes
// replaced with U+FFFD, and cut to 123 bytes at a character boundary, so the
// frame stays within a control frame's 125 bytes (closeFramePayload).
func (ws *WSConn) Close(code int, reason string) error {
	ws.closeMutex.Lock()
	if ws.closed.Load() {
		ws.closeMutex.Unlock()
		return nil
	}

	// Send close frame
	data := closeFramePayload(code, reason)

	// Under writeMutex like every other write: frames from two goroutines
	// must not interleave on the socket, and they share wbuf. closed is set
	// before writeMutex is released, so a WriteMessage waiting on the lock
	// sees it and returns ErrWebSocketAlreadyClosed instead of sending a
	// data frame after the close frame (RFC 6455 §5.5.1). It is set whether
	// or not the write succeeded: either way this side is done writing.
	ws.writeMutex.Lock()
	err := ws.writeFrame(wsClose, data)
	ws.closed.Store(true)
	ws.writeMutex.Unlock()
	ws.signalDone()

	// closeMutex only serialises the decision to close. It is released
	// before the wait below, so a second Close, or a peer close frame seen
	// by handleClose meanwhile, returns at once (closed is already set)
	// instead of queueing behind up to closeHandshakeTimeout.
	ws.closeMutex.Unlock()

	if err != nil {
		return ws.conn.Close()
	}

	// Wait for the peer's close frame response using a read deadline
	// instead of a blind sleep. Returns immediately when the frame arrives,
	// or after closeHandshakeTimeout if the peer is unresponsive. Set under
	// deadlineMu, after closed was stored, so the liveness reader cannot
	// push it back out (see extendReadDeadline). It bounds a ReadMessage
	// blocked on another goroutine (case 2) as much as Close's own reads.
	ws.deadlineMu.Lock()
	ws.conn.SetReadDeadline(time.Now().Add(closeHandshakeTimeout))
	ws.deadlineMu.Unlock()
	ws.awaitPeerClose()

	// The connection may already be closed by the time the wait ends: by
	// the HTTP loop, once a handler that saw the close frame returned (case
	// 2). That is the outcome Close is after, not a failure.
	if err := ws.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

// awaitPeerClose is Close's wait for the peer's close frame (see Close for
// the two cases). It takes the read side, at once when nobody is reading,
// else when the ReadMessage in progress returns, and then reads frames
// itself until the close frame or an error (the read deadline Close set
// included) unless the reader it waited for already saw the close frame.
// Frames before the close frame are dropped: after sending a close frame
// this side has nothing left to do with data (RFC 6455 §5.5.1).
//
// The timer bounds the wait for the read side on its own, so a reader that
// does not return — say the app moved the read deadline past Close's — still
// cannot hold Close up; closing the connection then ends that read.
func (ws *WSConn) awaitPeerClose() {
	// The peer's close frame is already in: this Close is the answer to it
	// (made from an OnClose handler, or racing handleClose). Nothing to wait
	// for — and the reader that read it may be this very goroutine, still
	// holding the read side.
	if ws.closeRcvd.Load() {
		return
	}
	timer := time.NewTimer(closeHandshakeTimeout)
	defer timer.Stop()
	select {
	case ws.readSem <- struct{}{}:
	case <-timer.C:
		return
	}
	defer func() { <-ws.readSem }()

	// closeRcvd was stored before readSem was released, so a close frame
	// the previous holder read is visible here.
	for !ws.closeRcvd.Load() {
		opcode, _, _, _, err := ws.readFrame()
		if err != nil {
			return // timeout, EOF, a closed or broken conn: nothing more to wait for
		}
		if opcode == wsClose {
			ws.closeRcvd.Store(true)
		}
	}
}

// goingAway starts the closing handshake from the server's side when the
// server shuts down: a close frame with 1001 (going away), so a well-behaved
// client answers and leaves within the shutdown grace period instead of
// being cut off at its end. It is Close without the two parts that would
// collide with the session still running in the handler's goroutine:
//
//   - it does not read the peer's answering close frame; the handler's own
//     ReadMessage does (handleClose sees closed already set, so it sends no
//     second frame, and ReadMessage returns the CloseMessage);
//   - it does not close the conn; the handler returns and the connection
//     loop closes it, or Shutdown does at its deadline.
//
// What it shares with Close: closeMutex decides once whether to close,
// writeMutex keeps the frame from interleaving with an app write, and closed
// is set under writeMutex so no data frame follows the close frame. The
// wait for the peer's answer is bounded by closeHandshakeTimeout as in
// Close, set under deadlineMu after closed so the liveness reader cannot
// push it back out. Called on its own goroutine (it waits for any app write
// in progress), and safe on a socket already closing: then it does nothing.
func (ws *WSConn) goingAway() {
	ws.closeMutex.Lock()
	if ws.closed.Load() {
		ws.closeMutex.Unlock()
		return
	}
	var data [2]byte
	binary.BigEndian.PutUint16(data[:], wsCloseGoingAway)
	ws.writeMutex.Lock()
	err := ws.writeFrame(wsClose, data[:])
	ws.closed.Store(true)
	ws.writeMutex.Unlock()
	ws.signalDone()
	ws.closeMutex.Unlock()
	if err != nil {
		return // the conn is broken; the handler's next read fails on it
	}

	ws.deadlineMu.Lock()
	_ = ws.conn.SetReadDeadline(time.Now().Add(closeHandshakeTimeout))
	ws.deadlineMu.Unlock()
}

// signalDone closes the Done channel, once, whichever path ends the
// connection first.
func (ws *WSConn) signalDone() {
	ws.doneOnce.Do(func() { close(ws.done) })
}

// handleClose handles an incoming close frame
//
// The close handlers run first, outside closeMutex, and the answer is sent
// after, under it:
//
//	closed already? ── yes ──► return (this frame answers our Close)
//	     │ no
//	run OnClose handlers (no lock held: a handler may call Close)
//	     │
//	closeMutex; closed now? ── yes ──► return (a Close, maybe from a
//	     │ no                           handler, sent the answer)
//	send the answering close frame, closed = true, close the conn
//
// The handlers used to run under closeMutex, so a handler calling Close —
// to answer with a code of its own — deadlocked on it. Now such a Close
// sends the answer itself and, finding the peer's frame already read
// (closeRcvd), does not wait for another.
func (ws *WSConn) handleClose(code int, text string) {
	if ws.closed.Load() {
		return
	}

	// Call close handlers. The slice is copied out under handlersMu and
	// iterated without it, so a handler may register another (OnClose)
	// without deadlocking. Copying the slice header is enough: OnClose only
	// appends, which writes past this copy's length or into a new array,
	// never over the elements read here.
	ws.handlersMu.Lock()
	handlers := ws.closeHandlers
	ws.handlersMu.Unlock()
	for _, handler := range handlers {
		handler(code, text)
	}

	ws.closeMutex.Lock()
	defer ws.closeMutex.Unlock()
	if ws.closed.Load() {
		return
	}

	// Send close response: the peer's code echoed (RFC 6455 §5.5.1: "the
	// endpoint typically echos the status code it received"), without the
	// reason; for a close frame with no body (code 1005), a close frame with
	// no body (closeFramePayload). That case used to put 1005 on the wire,
	// which §7.4.1 forbids, in answer to the most common close there is: a
	// browser's ws.close() with no arguments sends an empty close frame
	// (N-082). As in Close, closed is set under writeMutex together with the
	// close frame, so nothing is written after it.
	data := closeFramePayload(code, "")
	ws.writeMutex.Lock()
	ws.writeFrame(wsClose, data)
	ws.closed.Store(true)
	ws.writeMutex.Unlock()
	ws.signalDone()
	ws.conn.Close()
}

// failConnection is RFC 6455 §7.1.7 "Fail the WebSocket Connection", for a
// peer that broke the protocol: send a close frame with code, then close
// the TCP connection without waiting for the peer's reply (a peer that
// sends malformed frames is not owed a close handshake, and whatever it
// sends next cannot be trusted to parse).
//
// The close frame is best-effort and bounded by closeHandshakeTimeout
// whatever the connection's write settings: a peer that is also not
// reading must not hold the reading goroutine here.
func (ws *WSConn) failConnection(code int) {
	ws.closeMutex.Lock()
	defer ws.closeMutex.Unlock()
	if ws.closed.Load() {
		return
	}

	var data [2]byte
	binary.BigEndian.PutUint16(data[:], uint16(code))
	ws.writeMutex.Lock()
	if frame, err := ws.appendFrame(ws.wbuf[:0], wsClose, data[:]); err == nil {
		_ = ws.conn.SetWriteDeadline(time.Now().Add(closeHandshakeTimeout))
		_, _ = ws.conn.Write(frame)
	}
	ws.wbuf = nil // the connection is done; nothing will reuse it
	ws.closed.Store(true)
	ws.writeMutex.Unlock()
	ws.signalDone()
	_ = ws.conn.Close()
}

// writePong writes a pong frame
func (ws *WSConn) writePong(data []byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed.Load() {
		return ErrWebSocketAlreadyClosed
	}

	return ws.writeFrame(wsPong, data)
}

// WritePing writes a ping frame. data may be at most 125 bytes (RFC 6455
// §5.5); longer, it returns ErrWebSocketBadControlFrame (wrapped) and writes
// nothing, and the connection stays open.
func (ws *WSConn) WritePing(data []byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed.Load() {
		return ErrWebSocketAlreadyClosed
	}

	return ws.writeFrame(wsPing, data)
}

// SetPingHandler sets the handler for ping messages. Safe to call from any
// goroutine, also while ReadMessage runs; a ping already being handled
// finishes with the handler it started with.
func (ws *WSConn) SetPingHandler(handler func([]byte) error) {
	ws.handlersMu.Lock()
	ws.pingHandler = handler
	ws.handlersMu.Unlock()
}

// SetPongHandler sets the handler for pong messages. Safe to call from any
// goroutine, as SetPingHandler.
func (ws *WSConn) SetPongHandler(handler func([]byte) error) {
	ws.handlersMu.Lock()
	ws.pongHandler = handler
	ws.handlersMu.Unlock()
}

// OnClose adds a close handler, run when the peer's close frame arrives.
// Safe to call from any goroutine; a handler added while a close frame is
// being handled may or may not run for it.
//
// code is the peer's status code, or 1005 when its close frame had no body
// (answering with Close(1005, …) sends a close frame with no body). text is
// valid UTF-8: a close frame with a 1-byte body, a code that may not be sent
// or a reason that is not UTF-8 fails the connection instead
// (ErrWebSocketBadClose), and no handler runs for it.
func (ws *WSConn) OnClose(handler func(code int, text string)) {
	ws.handlersMu.Lock()
	ws.closeHandlers = append(ws.closeHandlers, handler)
	ws.handlersMu.Unlock()
}

// SetMaxMessageSize sets the maximum message size. Safe to call from any
// goroutine; a frame already being read is checked against the limit in
// force when its header was parsed.
func (ws *WSConn) SetMaxMessageSize(size int64) {
	ws.maxMessageSize.Store(size)
}

// SetReadDeadline sets the read deadline; the zero time clears it.
//
// With server pings on (the default for WebSocket routes), the connection
// also has a liveness deadline that every received frame pushes out (see
// startKeepalive), and a read fails at whichever of the two comes first:
//
//	app deadline earlier than the liveness window -> the read ends at it,
//	                                                 as on a net.Conn
//	app deadline later (or zero)                  -> a peer that stops
//	                                                 answering pings is still
//	                                                 dropped on time
//
// So an app deadline can shorten reads but not switch off dead-peer
// detection; WSOptions.PingInterval < 0 does that. Without pings this is a
// plain net.Conn read deadline.
//
// The deadline bounds waiting on the socket. Frames are read through a
// buffer, so frames that already arrived with an earlier socket read are
// returned even once it has passed; the first read that has to wait for
// the peer fails.
func (ws *WSConn) SetReadDeadline(t time.Time) error {
	ws.deadlineMu.Lock()
	defer ws.deadlineMu.Unlock()
	ws.readDeadline = t
	if ws.readWindow > 0 && !ws.closed.Load() {
		return ws.conn.SetReadDeadline(ws.effectiveReadDeadline(t))
	}
	return ws.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline, with net.Conn semantics: every
// write fails once t has passed (including a write already blocked when it
// is set, and a write started after t), until a new deadline is set. The
// zero time clears it, and the connection's per-write timeout
// (WSOptions.WriteTimeout) applies again.
//
// It is applied to the conn here as well as on each flush: here so that it
// reaches a write blocked right now (which holds writeMutex, so this must
// not take it), on each flush so the per-write timeout never replaces it.
func (ws *WSConn) SetWriteDeadline(t time.Time) error {
	ws.deadlineMu.Lock()
	ws.writeDeadline = t
	ws.deadlineMu.Unlock()
	if t.IsZero() && ws.writeTimeout > 0 {
		// Clearing the explicit deadline hands a blocked write back to the
		// per-write timeout rather than leaving it unbounded.
		return ws.conn.SetWriteDeadline(time.Now().Add(ws.writeTimeout))
	}
	return ws.conn.SetWriteDeadline(t)
}

// LocalAddr returns the local network address
func (ws *WSConn) LocalAddr() net.Addr {
	return ws.conn.LocalAddr()
}

// Subprotocol returns the subprotocol selected at the handshake from
// WSOptions.Subprotocols, or "" when none was (see performHandshake).
func (ws *WSConn) Subprotocol() string {
	return ws.subprotocol
}

// RemoteAddr returns the remote network address
func (ws *WSConn) RemoteAddr() net.Addr {
	return ws.conn.RemoteAddr()
}

// Done returns a channel that is closed when the WebSocket connection shuts down.
// Use this to stop goroutines tied to the connection (e.g., ping tickers):
//
//	go func() {
//	    ticker := time.NewTicker(20 * time.Second)
//	    defer ticker.Stop()
//	    for {
//	        select {
//	        case <-ws.Done():
//	            return
//	        case <-ticker.C:
//	            ws.WritePing([]byte("ping"))
//	        }
//	    }
//	}()
func (ws *WSConn) Done() <-chan struct{} {
	return ws.done
}
