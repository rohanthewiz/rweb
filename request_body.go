package rweb

import (
	"errors"
	"io"
	"net"
	"slices"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// The request body, read on demand (N-057).
//
// serveConn used to read every body into memory, up to the 100 MB default
// cap, before any handler ran: for a route that would answer 404, for a
// handler that rejects the request on its headers, and for an upload that
// only needed to be copied to disk. Now serveConn parses and checks the
// head, records how the body is framed (lazyBody) and reads none of it. The
// body comes off the connection when something asks for it:
//
//	serveConn               handler chain                    handleRequest, after the chain
//	─────────               ─────────────                    ──────────────────────────────
//	head parsed and         Body()         all of it,        settleBody looks at what is left:
//	checked; framing        GetPostValue   into memory,        read to its end   → keep-alive
//	recorded in lazyBody;   PostArgs       once                failed            → 400 / 408 / 413 /
//	no body byte read                                                              431, close
//	                        BodyReader()   as it arrives,      small remainder   → drained (≤ 256 KB,
//	                        multipart      never held whole                        ≤ 1 s), keep-alive
//	                        form parse                         large remainder,  → response with
//	                                                           or 100 Continue     Connection: close,
//	                        (nobody)       stays on the wire   never sent          then a lingering close
//
// What this buys:
//   - an upload read through BodyReader (or parsed as a multipart form) is
//     never held in memory whole;
//   - a request nobody reads the body of (an unrouted path, a failed auth
//     check) costs at most the small drain, and with "Expect: 100-continue"
//     the client is never told to send it at all;
//   - a cap can be set per route (MaxBody), since the cap is applied while
//     reading rather than before routing;
//   - Proxy can pass a large upload to its upstream as it arrives.
//
// What stays as it was:
//   - the framing rules (request_framing.go) and the up-front checks: a
//     Content-Length over the server-wide cap is still refused with 413
//     before any handler runs, and before 100 Continue;
//   - Body() still returns the whole body, and global middleware still sees
//     the body of an unrouted request: only the moment of reading moved;
//   - a body that cannot be read (malformed chunk, over the cap, too slow,
//     client gone) is still answered by the server, whatever the handler
//     wrote: see "A body that cannot be read" below.
//
// The disconnect watch behind Request().Context() (conn_reader.go) reads the
// connection one byte ahead, so it cannot run while body bytes are still to
// come: it starts when the body has been read to its end (net/http does the
// same). A handler that never reads its request's body gets no disconnect
// notice for that request.

// A body that cannot be read, now that a handler is already running when
// that is found out.
//
// Before the body was lazy, a request whose body was malformed, over the
// cap, too slow or cut off never reached a handler: serveConn refused it.
// The refusal is kept: whatever the handler does, the server answers such a
// request itself (400, 408, 413 or 431; nothing for a client that left) and
// closes the connection (settleBody, refuseBody). What is new is that the
// handler has run, so it has to be told:
//
//	accessor                              on a body that could not be read
//	────────                              ────────────────────────────────
//	BodyReader().Read                     returns the error
//	ParseMultipartForm, GetFormFile(s)    return the error
//	Body                                  returns nil (never a cut-short body)
//	GetPostValue, PostArgs, FormValue     return "" / no args
//	BodyErr                               returns the error, nil when all is well
//
// The accessors in the lower half have no error result, so a handler whose
// work must not happen on a missing body (it stores the body, say) checks
// BodyErr after them, or reads through BodyReader:
//
//	data := ctx.Request().Body()
//	if err := ctx.Request().BodyErr(); err != nil {
//	    return err // the server answers 400/408/413 itself
//	}
//
// Design choice: nil plus BodyErr, not a panic that unwinds the handler.
// A panic would guarantee no handler carries on with a body it did not get,
// but it runs through the application's own frames: code after the call is
// skipped, a lock released without defer stays held, and recovery
// middleware has to know to let it pass. A server should not do that to the
// code it hosts over a client's broken upload. The failure is handled as
// data instead: an empty result the handler can check, the server's own
// answer on the wire, and a log line. That line is a warning when a handler
// was given an empty result and never asked BodyErr (lazyBody.unseen): it
// may have acted on a body it did not have, which is worth an operator's
// attention. In every other case the failure is the client's doing and the
// handler knew or never looked, so it is a Debug line, like the refusals
// serveConn writes before a handler runs.

// ErrBodyTooLarge is the error a body read returns when the body is larger
// than the cap in force: ServerOptions.MaxRequestBodySize, or a lower one set
// by MaxBody. The request is answered 413 and the connection closed.
var ErrBodyTooLarge = errors.New("rweb: request body too large")

// ErrBodyReadAfterHandler is returned by a BodyReader that is read after its
// request's handlers have returned. The body belongs to the server from then
// on (it drains or drops what is left), and the connection's next bytes are
// another request's.
var ErrBodyReadAfterHandler = errors.New("rweb: request body read after its handler returned")

// errBodyAbandoned marks a body the server stopped reading on its own
// account, with nothing wrong on the client's side: the after-handler drain
// ran out of time, or Proxy's upstream call was cut off mid-upload. The
// stream position is then unknown, so the connection closes after the
// response, but no refusal is due.
var errBodyAbandoned = errors.New("rweb: request body abandoned")

const (
	// postHandlerDrainMax is the most of an unread body that is read and
	// discarded after the handlers return, to keep the connection usable
	// for the next request. A larger remainder is not worth its bandwidth:
	// the response goes out with "Connection: close" instead. The value is
	// net/http's (maxPostHandlerReadBytes).
	postHandlerDrainMax = 256 << 10

	// postHandlerDrainTimeout bounds that drain in time. The response is
	// held back while draining, so a client trickling a small body must
	// not be able to delay it for long; past this the connection is
	// closed after the response, as for a large remainder.
	postHandlerDrainTimeout = time.Second

	// bodyGrowStep is how much room Body() reserves at a time when no cap
	// is in force (MaxRequestBodySize negative): the buffer then grows with
	// the bytes that actually arrive, so a declared length alone, which the
	// client chooses, cannot make the server allocate. With a cap, the
	// declared length is within it and is reserved at once, saving the
	// regrow copies.
	bodyGrowStep = 1 << 20

	// bodyMinRead is the least room given to one read of a chunked body
	// whose next chunk size is not known yet.
	bodyMinRead = 512
)

// bodyKind is how a request's body is delimited on the wire.
type bodyKind uint8

const (
	// bodyNone: nothing to read from the connection. Either the request
	// has no body, or it was dealt with before the handler (the discarded
	// body of a HEAD or TRACE), or it is already in request.body (a
	// synthetic Server.Request). The zero value, so a cleaned context or
	// one built outside serveConn reads as "no body".
	bodyNone bodyKind = iota
	// bodySized: Content-Length bytes.
	bodySized
	// bodyChunked: chunked transfer coding, ended by the last chunk and
	// its trailer section.
	bodyChunked
)

// lazyBody is the reading state of one request's body. It is set up by
// serveConn once the head is parsed and zeroed by Clean.
//
// err is sticky and tells the three end states apart:
//
//	nil               more may be read (or kind is bodyNone)
//	io.EOF            read to its end: the connection is at the next request
//	errBodyAbandoned  the server gave up reading; close after the response
//	anything else     the body could not be read; the server refuses the
//	                  request (bodyRefusal) and closes
type lazyBody struct {
	kind bodyKind
	err  error

	// remain is, for a sized body, the bytes not yet read; for a chunked
	// one, the data bytes left in the current chunk (0 between chunks).
	remain int64
	// read counts the data bytes read off the connection so far, which is
	// what the cap is compared against for a chunked body.
	read int64
	// declared is the Content-Length, or -1 for a chunked body.
	declared int64
	// max is the cap in force, <= 0 for none: the server-wide cap to begin
	// with, which MaxBody can lower and never raise.
	max int64

	// Chunked decoding state (see nextChunk in request_framing.go):
	// inChunk means the CRLF that closes the current chunk's data is still
	// to be read; excess is the framing-overhead allowance used up.
	inChunk bool
	excess  int64

	// continueOwed: the client sent "Expect: 100-continue" and is waiting
	// to be told to send the body. The interim response goes out with the
	// first read, so a request whose body nobody asks for never has it
	// sent.
	continueOwed bool
	// started: the first read has begun (100 Continue sent if owed, the
	// read deadline armed).
	started bool
	// deadline: a read deadline of the body's is set on the conn and must
	// be cleared when the body ends. Cleared before the disconnect watch
	// may start, which takes a deadline error it did not cause for a
	// vanished client.
	deadline bool
	// eofSeen: the end of the body has been acted on (afterBodyRead), so
	// the watch is offered its start only once.
	eofSeen bool
	// logged: the Debug line with the buffered body has been written.
	logged bool
	// unseen: an accessor with no error result (Body, the form accessors)
	// returned empty because the body could not be read, and the handler
	// has not asked BodyErr since. It decides the level of refuseBody's log
	// line: a handler that may have acted on a body it did not have.
	unseen bool
	// linger: after the response, the connection is to be closed with
	// lingerClose rather than at once, because the client may still be
	// sending body bytes nobody will read (see settleBody).
	linger bool

	// lease is set while Proxy's upstream request is reading the body from
	// another goroutine (proxyRequestBody, proxy.go). While it is set, the
	// fields above belong to that goroutine and are only looked at through
	// the lease.
	lease *proxyRequestBody
}

// bodyComplete reports whether nothing of the body is left on the
// connection: there was none, or it has been read to its end.
func (req *request) bodyComplete() bool {
	lb := &req.lazy
	if lb.err != nil {
		return lb.err == io.EOF
	}
	return lb.kind == bodyNone
}

// bodyFailed reports whether the body could not be read for a reason that
// is the request's (malformed, too large, too slow, client gone), as opposed
// to the server giving up on it (errBodyAbandoned).
func (req *request) bodyFailed() bool {
	err := req.lazy.err
	return err != nil && err != io.EOF && err != errBodyAbandoned
}

// declaredBodyLen is the body's length as far as it is known without
// reading: the Content-Length, -1 for a chunked body, or the length of what
// is already buffered when nothing is on the wire.
func (req *request) declaredBodyLen() int64 {
	if req.lazy.kind == bodyNone {
		return int64(len(req.body))
	}
	return req.lazy.declared
}

// bodyReadTimeout is ServerOptions.ReadTimeout resolved, 0 for none. A
// request built outside NewServer has no limits.
func (req *request) bodyReadTimeout() time.Duration {
	if req.lim == nil {
		return 0
	}
	return req.lim.read
}

// startBody runs once, before the first byte of the body is read: it sends
// the 100 Continue the client is waiting for, and arms the body's read
// deadline.
//
// The deadline is ReadTimeout from this moment, for the whole body, as
// before; only its start moved from the end of the head to the first read.
// It could not stay at the end of the head: the time a handler takes before
// it asks for the body is not the client's.
func (req *request) startBody() error {
	lb := &req.lazy
	lb.started = true
	conn := req.cr.netConn()
	if conn == nil {
		return net.ErrClosed
	}
	if lb.continueOwed {
		lb.continueOwed = false
		if _, err := conn.Write(consts.BytResponseContinue); err != nil {
			return err
		}
	}
	if d := req.bodyReadTimeout(); d > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(d))
		lb.deadline = true
	}
	return nil
}

// clearBodyDeadline removes the body's read deadline from the conn, if one
// is set.
func (req *request) clearBodyDeadline() {
	if !req.lazy.deadline {
		return
	}
	req.lazy.deadline = false
	if conn := req.cr.netConn(); conn != nil {
		_ = conn.SetReadDeadline(time.Time{})
	}
}

// finishBody records that the body has been read to its end.
func (req *request) finishBody() {
	req.lazy.err = io.EOF
	req.clearBodyDeadline()
}

// failBody records why the body could not be read and returns that error.
// An EOF from the connection in the middle of a body is the client leaving,
// not the body's end, and must not look like it: io.EOF here would have
// Body() hand a cut-short body to the handler as if it were whole.
func (req *request) failBody(err error) error {
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	req.lazy.err = err
	req.clearBodyDeadline()
	return err
}

// readBodyRaw reads body bytes off the connection into p, undoing the
// framing: at most what is left of a sized body, or of the current chunk.
// It returns io.EOF once the body has been read to its end, and from then
// on; a failure is sticky too.
//
// A sized body is marked complete by the read that takes its last byte,
// without waiting for a further call, so its deadline is cleared and the
// connection known to be at the next request as early as possible. A chunked
// body's end is only found by reading the last-chunk line, on the call after
// its last data byte.
//
// Reads go through the connection's bufio.Reader, which for a p at least as
// large as its buffer reads straight into p: a large upload is not copied
// through the 4 KB buffer.
func (req *request) readBodyRaw(p []byte) (int, error) {
	lb := &req.lazy
	if lb.err != nil {
		return 0, lb.err
	}
	if lb.kind == bodyNone {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if !lb.started {
		if err := req.startBody(); err != nil {
			return 0, req.failBody(err)
		}
	}
	if lb.kind == bodyChunked && lb.remain == 0 {
		last, err := req.nextChunk()
		if err != nil {
			return 0, req.failBody(err)
		}
		if last {
			req.finishBody()
			return 0, io.EOF
		}
	}
	if int64(len(p)) > lb.remain {
		p = p[:lb.remain]
	}
	n, err := req.reader.Read(p)
	lb.remain -= int64(n)
	lb.read += int64(n)
	if err != nil {
		return n, req.failBody(err)
	}
	if lb.remain == 0 && lb.kind == bodySized {
		req.finishBody()
	}
	return n, nil
}

// readBody is readBodyRaw for a read made on the handler's goroutine: once
// the body is at its end, the disconnect watch gets its chance to start.
func (req *request) readBody(p []byte) (int, error) {
	n, err := req.readBodyRaw(p)
	req.afterBodyRead()
	return n, err
}

// afterBodyRead starts the disconnect watch when a read has just brought the
// body to its end and the request's context is in use: Context() could not
// start it while body bytes were still to come (see startWatchIfIdle).
func (req *request) afterBodyRead() {
	lb := &req.lazy
	if lb.err == io.EOF && !lb.eofSeen {
		lb.eofSeen = true
		req.startWatchIfIdle()
	}
}

// bufferBody reads what is left of the body into request.body, so that
// Body() can return it. It is a no-op when nothing is left.
func (req *request) bufferBody() error {
	_, err := req.bufferBodyUpTo(-1)
	return err
}

// bufferBodyUpTo is bufferBody that stops once request.body holds limit
// bytes (no limit when negative). It reports whether the body is now
// complete, i.e. wholly in request.body apart from anything a BodyReader
// took earlier.
func (req *request) bufferBodyUpTo(limit int64) (complete bool, err error) {
	lb := &req.lazy
	for {
		if req.bodyComplete() {
			req.logBufferedBody()
			return true, nil
		}
		if lb.err != nil {
			return false, lb.err
		}
		have := int64(len(req.body))
		if limit >= 0 && have >= limit {
			return false, nil
		}

		// Room for the next read: all that is known to be coming when a
		// cap bounds it, a step at a time otherwise (see bodyGrowStep).
		want := lb.remain
		if lb.max <= 0 && want > bodyGrowStep {
			want = bodyGrowStep
		}
		if want < bodyMinRead {
			want = bodyMinRead
		}
		if limit >= 0 && want > limit-have {
			want = limit - have
		}
		req.body = slices.Grow(req.body, int(want))
		n, _ := req.readBody(req.body[have : have+want]) // an error is sticky: seen at the top of the loop
		req.body = req.body[:have+int64(n)]
	}
}

// logBufferedBody writes the Debug line with the request's body, once, when
// the body has been buffered whole. A body read as a stream is not logged:
// logging it would mean holding it.
func (req *request) logBufferedBody() {
	if req.debugLog == nil || req.lazy.logged || len(req.body) == 0 {
		return
	}
	req.lazy.logged = true
	req.debugLog.Debug("rweb: request body", "path", req.path, "body", string(req.body))
}

// Body returns the whole request body, reading it off the connection on the
// first call; later calls return the same bytes. The slice is only valid
// until the handler returns.
//
// Bytes already taken through BodyReader (or by a multipart form parsed
// straight from the connection) are not part of it: a body is read off the
// wire once. Call Body first to have both: readers then replay what it
// holds.
//
// If the body cannot be read (malformed, over the cap, timed out, client
// gone), Body returns nil: never the part that did arrive, which a handler
// could take for the whole. BodyErr then says why. The server answers such a
// request itself (400, 408, 413) and closes the connection, so the handler
// only has to stop: check BodyErr before acting on the body.
func (req *request) Body() []byte {
	if err := req.bufferBody(); err != nil {
		req.lazy.unseen = true // an empty result with no error beside it
		return nil
	}
	return req.body
}

// BodyErr reports why the request body could not be read, or nil: the body
// is fine so far, has not been asked for, or there is none. It is the error
// result that Body, GetPostValue, PostArgs and FormValue do not have:
//
//	data := ctx.Request().Body()
//	if err := ctx.Request().BodyErr(); err != nil {
//	    return err
//	}
//
// The error is ErrBodyTooLarge for a body over the cap, and otherwise
// whatever stopped the read (a malformed chunk, a timeout, the client
// leaving). Returning it from the handler is enough: the server answers the
// request with the matching status in any case, and does not send the error
// to the error handler.
func (req *request) BodyErr() error {
	if !req.bodyFailed() {
		return nil
	}
	req.lazy.unseen = false // the handler has been told
	return req.lazy.err
}

// BodyReader returns the request body as a stream, for a handler that wants
// to process it as it arrives instead of holding it in memory: copy an
// upload to disk, decode JSON incrementally, hash it.
//
//	f, _ := os.Create(dst)
//	defer f.Close()
//	if _, err := io.Copy(f, ctx.Request().BodyReader()); err != nil {
//	    return err // the server answers 400/408/413 for a body it could not read
//	}
//
// The reader yields first whatever Body() has already buffered, from its
// start, and then the bytes still on the connection. Without an earlier
// Body() call the body is a one-shot stream: bytes read here are gone, and a
// later Body() returns only what was left.
//
// Read returns io.EOF at the body's end, ErrBodyTooLarge past the cap in
// force, and otherwise the error that stopped the read. After any such
// failure the server replaces the handler's response with its own (400,
// 408, 413 or 431) and closes the connection, so a handler need not map
// these errors to a status itself.
//
// The reader must be used on the handler's goroutine and not after the
// handler returns (ErrBodyReadAfterHandler). What a handler leaves unread
// is drained or dropped by the server.
func (req *request) BodyReader() io.Reader {
	return &bodyHandle{req: req, gen: req.bodyGen}
}

// bodyHandle is the io.Reader BodyReader hands out.
//
// gen ties it to one request: the request struct is pooled and serves the
// connection's next request too, so a handle kept past its handler would
// otherwise read another request's body. off is how far this handle has
// replayed request.body.
type bodyHandle struct {
	req *request
	gen uint32
	off int
}

func (h *bodyHandle) Read(p []byte) (int, error) {
	req := h.req
	if h.gen != req.bodyGen {
		return 0, ErrBodyReadAfterHandler
	}
	if len(p) == 0 {
		return 0, nil
	}
	// What Body() buffered comes first. When it has buffered anything it
	// has buffered everything that was left, so the connection holds no
	// more and the read below then reports the end.
	if h.off < len(req.body) {
		n := copy(p, req.body[h.off:])
		h.off += n
		return n, nil
	}
	return req.readBody(p)
}

// limitBody applies a body cap of n bytes to this request (MaxBody). n <= 0
// leaves the cap as it is, and so does an n above the cap already in force:
// a cap is only ever lowered, so the server-wide one stays the ceiling and,
// of several MaxBody in a chain, the lowest counts.
//
// It fails the body at once, with ErrBodyTooLarge, when the request is
// already known to be over: a Content-Length above n, or more than n bytes
// read or buffered. A chunked body's size is not known ahead, so it is held
// to the cap as it is read.
func (req *request) limitBody(n int64) error {
	lb := &req.lazy
	if n <= 0 || lb.lease != nil {
		return nil
	}
	if req.bodyFailed() {
		return lb.err
	}
	if lb.max > 0 && n > lb.max {
		n = lb.max
	}
	lb.max = n

	var size int64
	switch lb.kind {
	case bodySized:
		size = lb.read + lb.remain // the whole Content-Length
	case bodyChunked:
		size = lb.read
	default:
		size = int64(len(req.body)) // a synthetic request's body
	}
	if size > n {
		// Deliberately not failBody: the wire may be in any state here
		// (even read to its end), and the request is refused either way.
		lb.err = ErrBodyTooLarge
		req.clearBodyDeadline()
		return ErrBodyTooLarge
	}
	return nil
}

// MaxBody returns middleware that caps the request body at n bytes for the
// routes it is in front of: a group's (s.Group("/api", rweb.MaxBody(1<<20)))
// or, through s.Use, every route's.
//
// A request over the cap is answered 413 and its connection closed. A
// Content-Length above n is refused here, before the rest of the chain
// runs and, for a client that sent "Expect: 100-continue", before any of
// the body is sent. A chunked body is cut off when it passes n: the read
// fails with ErrBodyTooLarge (Body() ends the handler).
//
// MaxBody only ever lowers the cap. ServerOptions.MaxRequestBodySize stays
// the ceiling, checked before any handler, and of several MaxBody in one
// chain (a group inside a group, s.Use and a group) the lowest counts. To
// give one route more room than the rest, set MaxRequestBodySize to that
// route's limit and put MaxBody on the others. n <= 0 leaves the cap
// unchanged.
//
// Design choice: lowering only, and refusing a known-oversize request on
// the spot, over "the last MaxBody in the chain decides". The second would
// let a global default be raised for one group, but only by putting off
// the refusal until something reads the body, with the rest of the chain
// already run; a cap that an outer layer set and an inner one can undo is
// also the weaker guarantee.
//
// Design choice: the cap lives in the request's reading state rather than in
// a wrapping reader, so every way of reading the body (Body, BodyReader,
// form parsing, Proxy's upload) is held to it, and the server knows to
// answer 413 whichever of them met it.
func MaxBody(n int64) Handler {
	return func(ctx Context) error {
		if req, ok := ctx.Request().(*request); ok {
			if req.limitBody(n) != nil {
				// Refused: the chain stops here, and settleBody writes
				// the 413 once this returns.
				return nil
			}
		}
		return ctx.Next()
	}
}

// drainBody reads and discards what is left of the body.
//
// With maxBytes >= 0 and a timeout (the after-handler drain) it gives up
// past either bound, leaving the body unfinished; running out of time marks
// it abandoned, since a timeout of the server's own choosing is not the
// client's fault and earns no 408. With maxBytes < 0 and no timeout it reads
// to the end under the ordinary body rules (ReadTimeout, the cap).
//
// A failure found on the way (a malformed chunk, the cap) is recorded as for
// any read.
func (req *request) drainBody(maxBytes int64, timeout time.Duration) {
	lb := &req.lazy
	if maxBytes >= 0 && lb.kind == bodySized && lb.remain > maxBytes {
		return // known to be more than is worth reading
	}
	if timeout > 0 {
		conn := req.cr.netConn()
		if conn == nil {
			return
		}
		// The drain's own deadline replaces the body's. started is set so
		// the first read does not arm ReadTimeout over it; no 100 Continue
		// can be owed here (settleBody closes instead of draining then).
		lb.started = true
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		lb.deadline = true
	}

	bp := chunkBufPool.Get().(*[]byte)
	defer chunkBufPool.Put(bp)
	buf := *bp

	// One byte past the budget is asked for, so that a body of exactly
	// maxBytes is still read to its end: a chunked body's end is only
	// found by the read after its last data byte.
	budget := maxBytes
	for lb.err == nil {
		p := buf
		if maxBytes >= 0 {
			if budget < 0 {
				break
			}
			if int64(len(p)) > budget+1 {
				p = p[:budget+1]
			}
		}
		n, _ := req.readBodyRaw(p)
		budget -= int64(n)
	}

	if timeout > 0 {
		if req.bodyFailed() && isTimeout(lb.err) {
			lb.err = errBodyAbandoned
		}
		req.clearBodyDeadline()
	}
}

// bodyRefusal maps a body read failure to the server's answer: the canned
// response for the wire and its status code. Both are zero for a failure
// that gets no answer because nobody is left to read one (the client closed
// or reset the connection mid-body).
func bodyRefusal(err error) (wire string, status int) {
	switch {
	case errors.Is(err, ErrBodyTooLarge):
		return consts.HTTPPayloadTooLarge, consts.StatusRequestEntityTooLarge
	case errors.Is(err, errBadChunk):
		return consts.HTTPBadRequest, consts.StatusBadRequest
	case errors.Is(err, errHeaderTooLarge): // a chunked body's trailer section
		return consts.HTTPHeaderTooLarge, consts.StatusRequestHeaderFieldsTooLarge
	case isTimeout(err):
		return consts.HTTPRequestTimeout, consts.StatusRequestTimeout
	}
	return "", 0
}

// settleBody decides, once the handlers have returned, what becomes of the
// request body and so of the connection. It reports whether the handler's
// response is to be written; when not, it has answered (or dropped) the
// request itself and marked the connection to close.
//
//	body state after the chain          response            connection
//	──────────────────────────          ────────            ──────────
//	none, or read to its end            the handler's       kept
//	could not be read                   400/408/413/431,    lingering close
//	                                    or none (client
//	                                    gone)
//	Proxy still uploading it            the handler's,      lingering close
//	                                    Connection: close   after the response
//	100 Continue never sent             the handler's,      lingering close
//	                                    Connection: close
//	unread, event stream to follow      the stream          (read to its end first,
//	                                                        as before N-057)
//	unread, ≤ 256 KB arriving in 1 s    the handler's       kept (drained)
//	unread, more or slower              the handler's,      lingering close
//	                                    Connection: close
//
// Why drain at all: the bytes of an unread body sit between this request and
// the next on the connection, so the connection is only reusable once they
// are gone. Why not always: reading 100 MB to save a TCP handshake is a bad
// trade, and the handler's answer would wait behind it. Why the response
// goes out before the close when the body is left: the handler's verdict
// (401, 404, 413 from its own check) is what the client needs, and the
// lingering close (lingerClose, conn_limits.go) keeps a client that is still
// sending from losing it to a reset.
//
// The failure row is what keeps the old guarantee that a request with a bad
// body is never answered by a handler: whatever the handler wrote, having
// seen a read error or not, is replaced.
func (s *Server) settleBody(ctx *context, respWriter io.Writer) (respond bool) {
	req := &ctx.request
	lb := &req.lazy

	// Proxy's upstream request may still be reading the body on another
	// goroutine. If it has read it all, the lease is simply returned. If
	// not, the upload goes on while the response is relayed (the upstream
	// may answer early and still want the rest, or not), and is stopped
	// after it; lazyBody is not ours to look at until then.
	if lb.lease != nil {
		if !lb.lease.finished() {
			req.connClose = true
			lb.linger = true
			return true
		}
		req.endBodyLease()
	}

	switch {
	case req.bodyFailed():
		s.refuseBody(ctx, respWriter)
		return false
	case req.bodyComplete():
		return true
	case lb.err == errBodyAbandoned:
		req.connClose = true
		lb.linger = true
		return true
	}

	// Part or all of the body is still on the wire, unread.

	// An event stream's disconnect detection reads the connection
	// (sendSSE), and would take a body byte arriving later for a sign of
	// life it is not. Such a request keeps the old behaviour: the body is
	// read to its end, whatever its size, before the stream starts.
	if ctx.sseEventsChan != nil && req.method != consts.MethodHead {
		req.drainBody(-1, 0)
		if req.bodyFailed() {
			s.refuseBody(ctx, respWriter)
			return false
		}
		return true
	}

	// The client was told to wait for 100 Continue and never got it. It
	// may send the body anyway or not (RFC 9110 §10.1.1), so the only way
	// to know where the next request starts is not to have one.
	if lb.continueOwed {
		req.connClose = true
		lb.linger = true
		return true
	}

	req.drainBody(postHandlerDrainMax, postHandlerDrainTimeout)
	switch {
	case req.bodyComplete():
		return true
	case req.bodyFailed():
		s.refuseBody(ctx, respWriter)
		return false
	}
	req.connClose = true
	lb.linger = true
	return true
}

// dropResponse discards whatever the handlers prepared, for a request that
// is not going to be answered by them, and marks the connection to close.
// A pending body stream is released, and an event stream's cleanup run (an
// SSEHub registration made by the handler is undone), as writeResponse
// would have done for them.
func (s *Server) dropResponse(ctx *context) {
	ctx.response.closeBodyStream()
	if ctx.sseCleanup != nil {
		ctx.sseCleanup()
		ctx.sseCleanup = nil
	}
	ctx.request.connClose = true
}

// refuseBody answers a request whose body could not be read, in place of
// whatever the handlers prepared: the canned response serveConn has always
// sent for that failure, then a lingering close, since the client is likely
// still sending. A client that is gone gets nothing.
//
// A synthetic request has no connection: its Response carries the status
// instead, with an empty body.
//
// The log line (levels as in the Logger policy table, logging.go):
//
//	Warn, always   a handler got an empty Body() or form value for this
//	               request and never asked BodyErr: it could not tell the
//	               body was missing, and may have acted on that. The line
//	               names the route to fix.
//	Debug          every other case: the handler was given the error
//	               (BodyReader, the multipart accessors, BodyErr), or never
//	               touched the body (MaxBody refused it, the drain found
//	               it). The client's doing, as for a refusal before the
//	               handler.
//
// status is 0 in the line when no response is sent (the client left).
func (s *Server) refuseBody(ctx *context, respWriter io.Writer) {
	err := ctx.request.lazy.err
	s.dropResponse(ctx)
	wire, status := bodyRefusal(err)
	if ctx.request.lazy.unseen {
		s.log.Warn("rweb: request body could not be read, and the handler was given an empty body without checking Request().BodyErr(); its response is replaced",
			"method", ctx.request.method, "path", ctx.request.path, "status", status, "error", err)
	} else if s.options.Debug {
		s.log.Debug("rweb: request body could not be read; the handler's response is replaced",
			"method", ctx.request.method, "path", ctx.request.path, "status", status, "error", err)
	}
	if ctx.conn == nil {
		if status != 0 {
			ctx.response.headers = ctx.response.headers[:0]
			ctx.response.body = ctx.response.body[:0]
			ctx.response.SetStatus(status)
		}
		return
	}
	if wire != "" {
		_, _ = io.WriteString(respWriter, wire)
		lingerClose(ctx.conn)
	}
}

// endBodyLease stops Proxy's upstream request from reading the body any
// further and waits until it has stopped; lazyBody is the handler
// goroutine's again on return. A no-op without a lease.
func (req *request) endBodyLease() {
	if l := req.lazy.lease; l != nil {
		l.detach()
		req.lazy.lease = nil
	}
}
