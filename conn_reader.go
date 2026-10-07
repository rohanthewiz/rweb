package rweb

import (
	stdctx "context"
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// aLongTimeAgo is a read deadline already in the past: setting it makes a
// Read parked on the connection return at once with os.ErrDeadlineExceeded.
var aLongTimeAgo = time.Unix(1, 0)

// errReadDuringWatch is returned if the connection loop tries to read while a
// disconnect watch is still parked on the conn. handleRequest always stops the
// watch first, so this means a lifecycle bug, not a client fault; failing the
// read (which closes the connection) beats two readers splitting the stream.
var errReadDuringWatch = errors.New("rweb: connection read while a disconnect watch is running")

// connReader is the io.Reader under a connection's bufio.Reader (ctx.reader).
// Normally it is a pass-through to the conn. While a handler holds a live
// request context (Request().Context()), it also runs the *disconnect watch*:
// one goroutine parked in a 1-byte conn.Read, so the moment the client closes
// the connection the read returns and the request context is cancelled.
//
//	conn ──► connReader ──► bufio.Reader ──► request parsing (connection loop)
//	           │
//	           └─ watch goroutine, only while a request context is live:
//	                conn.Read(1 byte) returns
//	                  error (EOF, reset)    → client gone: cancel the context
//	                  1 byte                → client sent its next request
//	                                          (pipelining): keep the byte, which
//	                                          the next Read returns first, and
//	                                          stop watching without cancelling
//	                  deadline, by stopWatch → expected; nothing happened
//
// Why a background read and not something cheaper: a closed TCP connection is
// only observable by reading from it (or by a write failing, and rweb writes
// nothing until the handler returns). This is the same design as net/http's
// connReader (startBackgroundRead / abortPendingRead), which is how
// http.Request.Context() learns the client has gone.
//
// Why the watch lives *under* bufio rather than beside it: the 1 byte the
// watch may read is the first byte of the next request. Returning it from
// this Read puts it back in stream order without touching bufio's internals.
//
// The watch only starts when the request has nothing buffered (no pipelined
// request already queued behind it) and something asked for the context, so
// a handler that never calls Request().Context() costs one uncontended mutex
// per bufio fill and nothing else.
type connReader struct {
	conn net.Conn

	mu      sync.Mutex
	inRead  bool              // the watch goroutine is parked in conn.Read
	aborted bool              // stopWatch is ending that read; a deadline error is expected
	hasByte bool              // the watch read byteBuf[0]; it belongs to the next request
	byteBuf [1]byte           // written by the watch goroutine before it takes mu
	done    chan struct{}     // closed when the current watch goroutine has returned
	cancel  stdctx.CancelFunc // cancels the request context if the client goes away
}

// reset points the reader at a new connection. A pooled context is reused
// across connections, so nothing from the previous one (least of all a
// stashed byte) may survive. Fields are reset one by one rather than by
// assigning a fresh struct, which would copy the mutex.
func (cr *connReader) reset(conn net.Conn) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.conn = conn
	cr.inRead, cr.aborted, cr.hasByte = false, false, false
	cr.done, cr.cancel = nil, nil
}

// Read implements io.Reader for the connection's bufio.Reader.
func (cr *connReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	cr.mu.Lock()
	if cr.inRead {
		cr.mu.Unlock()
		return 0, errReadDuringWatch
	}
	if cr.hasByte {
		// Hand back the byte the watch took off the wire. Returning just the
		// one byte (a short read) is fine: bufio calls again for the rest.
		p[0] = cr.byteBuf[0]
		cr.hasByte = false
		cr.mu.Unlock()
		return 1, nil
	}
	conn := cr.conn
	cr.mu.Unlock()
	if conn == nil { // a synthetic Server.Request context has no connection
		return 0, net.ErrClosed
	}
	return conn.Read(p)
}

// startWatch starts the disconnect watch; cancel is called if the client goes
// away while it runs. It does nothing when there is no connection, a watch is
// already running, or a byte of the next request is already in hand — in that
// last case the client is demonstrably alive and pipelining, and the byte must
// not be overwritten.
func (cr *connReader) startWatch(cancel stdctx.CancelFunc) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.conn == nil || cr.inRead || cr.hasByte {
		return
	}
	cr.inRead, cr.aborted, cr.cancel = true, false, cancel
	cr.done = make(chan struct{})
	go cr.watch(cr.conn, cr.done)
}

// watch is the watch goroutine. It reads at most one byte, records what that
// read means (see the type doc), and exits. It never re-arms itself: after a
// byte arrives the next request is already on its way and the connection loop
// will read it; after an error the connection is finished.
func (cr *connReader) watch(conn net.Conn, done chan struct{}) {
	defer close(done) // runs after the Unlock below (defers are LIFO)

	n, err := conn.Read(cr.byteBuf[:])

	cr.mu.Lock()
	defer cr.mu.Unlock()
	if n == 1 {
		cr.hasByte = true
	}
	// Any error other than the deadline stopWatch set means the connection
	// is gone (EOF on close, ECONNRESET, a TLS close_notify, ...). A deadline
	// error that stopWatch did not cause also counts: nothing in rweb sets
	// read deadlines on this path, so the conn's state is no longer known.
	if err != nil && !(cr.aborted && errors.Is(err, os.ErrDeadlineExceeded)) {
		cr.cancel()
	}
	cr.inRead, cr.aborted, cr.cancel = false, false, nil
}

// stopWatch ends a running watch and waits for its goroutine to exit, so that
// on return nothing else is reading the conn: the connection loop, a WebSocket
// or sendSSE can take over. It is idempotent and cheap when no watch runs.
//
// The parked Read is interrupted with a past read deadline, which is then
// cleared so the conn is fully usable again (keep-alive, WebSocket frames).
// Both plain TCP and TLS conns survive this: tls.Conn treats a deadline error
// as temporary and keeps any partially read record for the next Read.
func (cr *connReader) stopWatch() {
	cr.mu.Lock()
	if !cr.inRead {
		cr.mu.Unlock()
		return
	}
	cr.aborted = true
	conn, done := cr.conn, cr.done
	_ = conn.SetReadDeadline(aLongTimeAgo)
	cr.mu.Unlock()

	<-done // the goroutine needs mu to finish, hence waiting outside it
	_ = conn.SetReadDeadline(time.Time{})
}

// takeByte returns the byte a watch read ahead, if any, and forgets it. Used
// when the connection leaves HTTP (a WebSocket upgrade), so the byte can go to
// the new protocol's reader instead of the next HTTP request.
func (cr *connReader) takeByte() (b byte, ok bool) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if !cr.hasByte {
		return 0, false
	}
	cr.hasByte = false
	return cr.byteBuf[0], true
}
