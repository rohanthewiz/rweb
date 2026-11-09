package rweb

import (
	"bufio"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"time"
)

// Connection limits: how much of a request head is read, and how long any
// read or write on a connection may wait.
//
// Deadlines across the life of a keep-alive connection, as set by
// serveConn (read side) and writeResponse (write side). Shutdown may also
// set a past read deadline on a connection in the wait phase, to wake and
// close it (see shutdown.go):
//
//	accept
//	  │
//	  ▼  ┌──────────────────────────────── per request ────────────────────────────────┐
//	  ├─►│ wait   read deadline = now+IdleTimeout   (first request: ReadHeaderTimeout,  │
//	  │  │        which also bounds the TLS handshake, run inside this first read)    │
//	  │  │        Peek(1): timeout or EOF here → close silently, nothing was asked    │
//	  │  │ head   read deadline = now+ReadHeaderTimeout, from the first byte          │
//	  │  │        request line + headers, charged to the MaxHeaderBytes budget        │
//	  │  │        timeout → 408, over budget → 431, then close                       │
//	  │  │ body   read deadline = now+ReadTimeout, or none   (only if there is one)   │
//	  │  │        timeout → 408, then close                                          │
//	  │  │ run    read deadline cleared ──────────────────────────────────────────┐   │
//	  │  │        handler; disconnect watch, WebSocket and GetConn read the conn  │   │
//	  │  │        with no deadline of ours in the way (the watch's own stopWatch  │   │
//	  │  │        sets and clears a past deadline, see conn_reader.go)            │   │
//	  │  │ write  write deadline = now+WriteTimeout before every write of ≤ 1 MB  │   │
//	  │  │        (SSE: SSECfg.WriteTimeout before every flush); cleared at the   │   │
//	  │  │        end, so a WebSocket upgrade on the next request writes freely   │   │
//	  │  └──────────────────────────────────────────────────────────────────────────┘
//	  └──── next request (keep-alive)
//
// Every deadline is armed from "now" when its phase starts rather than once
// per connection, so a phase's budget does not depend on how long the
// previous one took. Read deadlines are cleared before the handler because
// what reads the conn from there on (the disconnect watch above all, which
// takes any deadline error it did not cause for a vanished client) is not
// part of reading a request.

const (
	defaultMaxHeaderBytes    = 1 << 20 // 1 MB
	defaultReadHeaderTimeout = 30 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultSSEWriteTimeout   = 30 * time.Second

	// writeSlice is the most a single write is given under a write deadline:
	// a larger write (an in-memory body, a streamed file) is split, with the
	// deadline renewed before each piece. WriteTimeout thus means "no piece
	// of this size may stall longer than that", i.e. a floor on throughput of
	// writeSlice per WriteTimeout (35 KB/s at 1 MB and 30 s), rather than a
	// cap on the whole response. 1 MB keeps that floor low enough not to
	// trip on a slow but live client, while a sendfile slice is still large
	// enough that the extra syscalls do not show.
	writeSlice = 1 << 20

	// chunkLineMax caps one chunk-size line of a chunked request body
	// (hex size, optional extensions, CRLF). Real ones are a few bytes.
	chunkLineMax = 4 << 10
)

// connLimits is ServerOptions' limits with defaults applied. Zero means
// "none" in every field, so callers test `> 0` and need no sentinel.
type connLimits struct {
	maxHeaderBytes int
	readHeader     time.Duration
	idle           time.Duration
	read           time.Duration
	write          time.Duration
	sseWrite       time.Duration
}

// resolveConnLimits applies the option convention: zero selects the
// default, a negative value disables (stored as zero), anything else is
// taken as given. ReadTimeout and WriteTimeout default to off.
func resolveConnLimits(o ServerOptions) connLimits {
	return connLimits{
		maxHeaderBytes: resolveLimit(o.MaxHeaderBytes, defaultMaxHeaderBytes),
		readHeader:     resolveLimit(o.ReadHeaderTimeout, defaultReadHeaderTimeout),
		idle:           resolveLimit(o.IdleTimeout, defaultIdleTimeout),
		read:           resolveLimit(o.ReadTimeout, 0),
		write:          resolveLimit(o.WriteTimeout, 0),
		sseWrite:       resolveLimit(o.SSECfg.WriteTimeout, defaultSSEWriteTimeout),
	}
}

func resolveLimit[T int | time.Duration](v, def T) T {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

// errHeaderTooLarge is returned by lineReader when a line would take the
// request past its byte budget.
var errHeaderTooLarge = errors.New("rweb: request header too large")

// lineReader reads the lines of a request head (and of a chunked body's
// framing) off a connection's bufio.Reader, charging each to a byte budget.
//
// bufio.Reader.ReadString, used before, grows its result without bound, so a
// single endless header line was buffered whole. ReadSlice instead returns
// at most one buffer's worth (4 KB) at a time, with ErrBufferFull for a
// longer line; the pieces of such a line are gathered in scratch, and the
// budget is checked before each piece is kept, so no more than the budget
// plus one buffer is ever held.
//
// Each line is returned as a fresh string, which is the one allocation per
// line ReadString also made: a line within one buffer (nearly all of them)
// is converted straight from bufio's buffer, and scratch is only touched for
// the long ones. scratch lives as long as the connection, so a client
// sending long lines reuses one buffer rather than allocating per line.
type lineReader struct {
	r       *bufio.Reader
	budget  int // bytes the current request head may still use
	scratch []byte
}

// reset starts a new budget. max <= 0 means no limit.
func (lr *lineReader) reset(max int) {
	if max <= 0 {
		max = math.MaxInt
	}
	lr.budget = max
}

// readLine returns the next line, line ending included, and charges its
// length to the budget. Over budget it returns errHeaderTooLarge; an error
// from the connection (EOF, a deadline) mid-line is returned as is.
func (lr *lineReader) readLine() (string, error) {
	line, err := lr.r.ReadSlice('\n')
	if err == nil { // the common case: the whole line was in the buffer
		if len(line) > lr.budget {
			return "", errHeaderTooLarge
		}
		lr.budget -= len(line)
		return string(line), nil
	}

	buf := lr.scratch[:0]
	for {
		if len(line) > lr.budget-len(buf) {
			lr.scratch = buf
			return "", errHeaderTooLarge
		}
		buf = append(buf, line...)
		if err != bufio.ErrBufferFull {
			break
		}
		line, err = lr.r.ReadSlice('\n')
	}
	lr.scratch = buf
	if err != nil {
		return "", err
	}
	lr.budget -= len(buf)
	return string(buf), nil
}

// isTimeout reports whether err is a deadline running out, as opposed to the
// client closing or resetting the connection.
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// readDeadline arms and clears a connection's read deadline for the request
// loop. It remembers whether one is set, so that with the timeouts disabled,
// or between two phases that both have none, clearing costs nothing.
type readDeadline struct {
	conn  net.Conn
	armed bool
}

// set arms the read deadline d from now, or clears it when d is zero.
func (rd *readDeadline) set(d time.Duration) {
	if d > 0 {
		_ = rd.conn.SetReadDeadline(time.Now().Add(d))
		rd.armed = true
	} else if rd.armed {
		_ = rd.conn.SetReadDeadline(time.Time{})
		rd.armed = false
	}
}

// deadlineWriter renews conn's write deadline before every write to w and
// splits writes larger than writeSlice, so that each piece gets the full
// timeout. writeResponse puts it under everything it writes for a request
// with a write timeout, except a sized body stream, which writeBodyStream
// slices itself to keep the sendfile path (a wrapped writer would hide the
// *net.TCPConn that sendfile needs).
type deadlineWriter struct {
	conn net.Conn
	w    io.Writer
	d    time.Duration
}

func (dw *deadlineWriter) Write(p []byte) (n int, err error) {
	for len(p) > 0 {
		k := min(len(p), writeSlice)
		_ = dw.conn.SetWriteDeadline(time.Now().Add(dw.d))
		m, err := dw.w.Write(p[:k])
		n += m
		if err != nil {
			return n, err
		}
		p = p[k:]
	}
	return n, nil
}
