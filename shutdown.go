package rweb

import (
	stdctx "context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// Server lifecycle: Serve, Shutdown, and the connection registry they share.
//
// Shutdown, in order:
//
//	Shutdown(ctx)
//	  │
//	  ├─ 1. inShutdown = true          new conns are refused (trackConn), idle
//	  │                                loops stop (connIdle), listeners close,
//	  │                                Serve returns ErrServerClosed
//	  ├─ 2. cancel the base context    handlers, SSE producers and outbound
//	  │                                calls tied to Request().Context() are
//	  │                                told; sendSSE ends its stream
//	  ├─ 3. for each tracked conn:
//	  │       idle   → claim it (idle → closing) and set a past read deadline:
//	  │                its loop, parked in Peek, wakes at once and closes it
//	  │       active → left to finish its response, then its loop closes it
//	  │                (it never goes back to waiting for a request);
//	  │                a WebSocket is also sent a close frame, 1001 going away
//	  ├─ 4. wait until the registry is empty ……………… return nil
//	  └─ 5. or ctx is done first: close every remaining conn
//	        (WebSockets, SSE streams and stuck handlers included) … return ctx.Err()
//
// A connection's state, as the request loop (serveConn) moves it:
//
//	           accept
//	              │
//	              ▼
//	  ┌────────► idle ── Shutdown claims it ──► closing ──► closed
//	  │           │       (CAS idle→closing,     (Peek fails on the past
//	  │           │        past read deadline)    deadline, or the loop's own
//	  │           │                               CAS idle→active fails)
//	  │           │ first byte of a request (CAS idle→active)
//	  │           ▼
//	  └──────── active ── response written, and the conn is kept ─┐
//	              │       (Store idle, then check inShutdown) ─────┘ (back to idle)
//	              │
//	              ├── WebSocket upgrade: still active; the WSConn is recorded
//	              │   so Shutdown can send it a close frame (hijacked)
//	              ├── SSE: still active; the stream ends on the base context
//	              └── closes (Connection: close, an error, the stream or
//	                  WebSocket ended, or inShutdown) ──────────────► closed
//
// Why a registry: N-020 chose a server-wide base context over tracking
// connections, and the base context is still how shutdown reaches handlers.
// But only a registry can wake a connection parked in a read with nothing to
// do (an idle keep-alive conn would otherwise hold on until IdleTimeout), and
// only a registry knows when every connection is done. Its cost is kept off
// the hot path: the mutex is taken when a connection opens and closes, and a
// request pays two atomic operations (idle → active, active → idle) plus one
// atomic load; nothing per read or write.
//
// Why the idle/active handoff is race-free. Both sides use sequentially
// consistent atomics:
//
//	loop (between requests)          Shutdown
//	  rd.set(wait)                     inShutdown = true
//	  state = idle                     for each conn: CAS(idle → closing)
//	  if inShutdown → close              ok → SetReadDeadline(past)
//	  Peek(1)
//	  CAS(idle → active) or close
//
// Either the loop's inShutdown load sees true and it closes, or its idle
// store came first and Shutdown's CAS sees idle. The read deadline is armed
// before the store, so the loop never overwrites Shutdown's past deadline
// with its own; and once Shutdown has claimed a conn the loop's CAS fails,
// so it never starts a request on a conn Shutdown has given up on.
//
// Locking: lifeMu guards the registry maps and is only ever held for map
// work and atomic operations. Shutdown copies what it needs under it and
// then closes, wakes and waits with it released, so a connection finishing
// at the same moment (untrackConn) never waits on a Shutdown in progress.
// Lock order where both are taken: lifeMu, then baseMu (beginServing).

// ErrServerClosed is returned by Serve after Shutdown (and by a Serve that
// starts while a Shutdown is still in progress), as net/http's is.
var ErrServerClosed = errors.New("rweb: Server closed")

// defaultShutdownTimeout is how long Run waits for in-flight requests after
// a signal when ServerOptions.ShutdownTimeout is zero.
const defaultShutdownTimeout = 10 * time.Second

// Accept retry backoff (see nextAcceptDelay).
const (
	acceptDelayMin = 5 * time.Millisecond
	acceptDelayMax = time.Second
)

// Connection states for trackedConn.state.
const (
	// connIdle: a new connection, or a keep-alive one between requests,
	// waiting for the first byte of a request. Zero, so a new trackedConn
	// starts idle.
	connIdle int32 = iota
	// connActive: a request is being read, handled or answered. A WebSocket
	// session or an event stream stays active for as long as it lasts.
	connActive
	// connClosing: Shutdown claimed the connection while it was idle; its
	// loop must close it rather than start another request.
	connClosing
)

// trackedConn is one connection in the server's registry.
type trackedConn struct {
	conn  net.Conn
	state atomic.Int32
	// ws is the WebSocket the connection was upgraded to, if any: Shutdown
	// sends it a close frame. Set once per upgrade, not per frame.
	ws atomic.Pointer[WSConn]
}

// WithShutdownTimeout sets how long Run waits, after SIGINT or SIGTERM, for
// in-flight requests to finish before closing their connections. 0 selects
// the default (10 s); a negative value closes everything at once.
func WithShutdownTimeout(d time.Duration) ServerOption {
	return func(opts *ServerOptions) {
		opts.ShutdownTimeout = d
	}
}

// shutdownTimeout is ServerOptions.ShutdownTimeout with the option
// convention applied: zero selects the default, negative means "do not
// wait" and is returned as zero.
func (s *Server) shutdownTimeout() time.Duration {
	return resolveLimit(s.options.ShutdownTimeout, defaultShutdownTimeout)
}

// nextAcceptDelay is the wait before retrying Accept after an error, given
// the previous wait (zero after a success): 5 ms, doubling, capped at 1 s,
// as net/http does. Without it an error that persists (EMFILE: out of file
// descriptors) would spin a core retrying, and starve the very handlers whose
// finishing would free descriptors.
func nextAcceptDelay(d time.Duration) time.Duration {
	if d == 0 {
		return acceptDelayMin
	}
	return min(2*d, acceptDelayMax)
}

// Serve accepts connections on l and serves each in its own goroutine. It
// returns ErrServerClosed after Shutdown, or the error that stopped the
// listener if something else closed it. The listener is closed when Serve
// returns.
//
// l may be any net.Listener: TLS is the caller's business (wrap it with
// tls.NewListener), as are systemd sockets, Unix sockets and the like.
// ServerOptions.Address and TLS are not consulted. ReadyChan is signalled
// and GetListenAddr reports l's address once Serve is accepting.
//
// Serve does not handle signals; that is Run's job. Pair it with Shutdown:
//
//	go func() {
//	    if err := s.Serve(ln); !errors.Is(err, rweb.ErrServerClosed) {
//	        log.Println(err)
//	    }
//	}()
//	...
//	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
//	defer cancel()
//	err := s.Shutdown(ctx)
//
// A server can serve again after a Shutdown has returned. A Shutdown that
// returns before Serve has started has nothing to stop, so wait for ReadyChan
// before shutting down a Serve started in a goroutine.
func (s *Server) Serve(l net.Listener) error {
	s.beginServing()
	return s.serve(l)
}

// beginServing re-arms a server that an earlier Shutdown stopped: request
// contexts derive from a live base context again and connections are
// accepted. It does nothing while a Shutdown is still running, so a Serve
// racing with one is refused (serve returns ErrServerClosed) rather than
// left running after it.
//
// Run calls it before starting serve in a goroutine, not from inside that
// goroutine: a signal that arrives before the goroutine runs leads to a
// Shutdown, and the serve that follows must see it and refuse.
func (s *Server) beginServing() {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.shutdowns > 0 {
		return
	}
	s.renewBaseContext()
	s.inShutdown.Store(false)
}

// serve is Serve's accept loop, without the re-arming.
func (s *Server) serve(l net.Listener) error {
	defer func() { _ = l.Close() }()
	if !s.trackListener(l) {
		return ErrServerClosed
	}
	defer s.untrackListener(l)

	if s.options.Verbose {
		protocol := consts.HTTP
		if s.options.TLS.UseTLS {
			protocol = consts.HTTPS
		}
		fmt.Printf("Serving at %s://%s\n", protocol, l.Addr())
	}

	if s.options.ReadyChan != nil { // remember to nil check!
		select {
		// Let the caller know we are running
		case s.options.ReadyChan <- struct{}{}: // attempt to send to channel
		default: // Don't block if out can't receive for some reason
		}
	}

	var delay time.Duration // current accept backoff; zero after a success
	for {
		conn, err := l.Accept() // accept next client connection
		if err != nil {
			// Shutdown closed the listener (or the error is incidental to
			// shutting down): the expected way out.
			if s.inShutdown.Load() {
				return ErrServerClosed
			}
			// Closed by someone else: nothing more will ever be accepted.
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Anything else (EMFILE, ECONNABORTED, a TLS listener's
			// failure) may pass: retry, but back off rather than spin.
			delay = nextAcceptDelay(delay)
			if s.options.Debug {
				fmt.Printf("Error accepting connection: %v; retrying in %v\n", err, delay)
			}
			time.Sleep(delay)
			continue
		}
		delay = 0
		// fmt.Printf("** Connection established: %s <-- %s\n", conn.LocalAddr(), conn.RemoteAddr())

		// Tracked here, before its goroutine starts, so that Shutdown's view
		// of the registry includes every connection Accept has returned.
		tc := s.trackConn(conn)
		if tc == nil { // Shutdown started since Accept returned
			_ = conn.Close()
			continue
		}
		// Each connection separately bc a copy is passed in
		go s.serveConn(tc)
	}
}

// trackListener records a listener for Shutdown to close, and its address
// for GetListenAddr. It refuses once a Shutdown has started.
func (s *Server) trackListener(l net.Listener) bool {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.inShutdown.Load() {
		return false
	}
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
	}
	s.listeners[l] = struct{}{}
	s.listenAddr = l.Addr().String()
	return true
}

func (s *Server) untrackListener(l net.Listener) {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	delete(s.listeners, l)
}

// trackConn adds a connection to the registry, idle. It returns nil once a
// Shutdown has started; the caller then closes the connection unserved.
func (s *Server) trackConn(conn net.Conn) *trackedConn {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.inShutdown.Load() {
		return nil
	}
	if s.conns == nil {
		s.conns = make(map[*trackedConn]struct{})
	}
	tc := &trackedConn{conn: conn}
	s.conns[tc] = struct{}{}
	return tc
}

// untrackConn removes a finished connection and, when it was the last one,
// wakes the Shutdowns waiting for the registry to empty.
func (s *Server) untrackConn(tc *trackedConn) {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	delete(s.conns, tc)
	if len(s.conns) == 0 && s.connsGone != nil {
		close(s.connsGone)
		s.connsGone = nil
	}
}

// connIdle marks a connection as waiting for its next request and reports
// whether it may wait: false once a Shutdown has started, in which case the
// loop closes the connection instead. The store comes before the load (see
// the handoff note at the top of this file).
func (s *Server) connIdle(tc *trackedConn) bool {
	tc.state.Store(connIdle)
	return !s.inShutdown.Load()
}

// trackWebSocket records the WebSocket a connection was upgraded to. If a
// Shutdown is already running it may have scanned the registry before the
// WebSocket was recorded, so the close frame is sent from here too;
// goingAway sends it at most once whichever side gets there first.
func (s *Server) trackWebSocket(tc *trackedConn, ws *WSConn) {
	tc.ws.Store(ws)
	if s.inShutdown.Load() {
		go ws.goingAway()
	}
}

// Shutdown stops the server gracefully: it closes the listeners, cancels the
// base context (so every Request().Context() is done), closes idle
// keep-alive connections at once, and lets requests in progress finish, each
// connection closing after its response. WebSockets are sent a close frame
// (1001, going away) and event streams end. It then waits until every
// connection has closed, returning nil, or until ctx is done, in which case
// it closes the connections that remain and returns ctx.Err().
//
// Handlers are not interrupted except through their request context: one
// that ignores it keeps running after its connection is closed. Shutdown may
// be called more than once and concurrently, from any goroutine, including
// while Run or Serve is starting. Serve returns ErrServerClosed as soon as
// Shutdown begins; Run, when it is Shutdown and not a signal that stops it,
// returns nil. The server can be run or served again once Shutdown has
// returned.
func (s *Server) Shutdown(ctx stdctx.Context) error {
	s.lifeMu.Lock()
	s.inShutdown.Store(true)
	s.shutdowns++
	listeners := make([]net.Listener, 0, len(s.listeners))
	for l := range s.listeners {
		listeners = append(listeners, l)
	}
	s.lifeMu.Unlock()
	defer func() {
		s.lifeMu.Lock()
		s.shutdowns--
		s.lifeMu.Unlock()
	}()

	var lnErr error
	for _, l := range listeners {
		if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) && lnErr == nil {
			lnErr = err
		}
	}

	s.cancelBaseContext()
	s.wakeConns()

	for {
		s.lifeMu.Lock()
		if len(s.conns) == 0 {
			s.lifeMu.Unlock()
			return lnErr
		}
		// One channel shared by every waiting Shutdown, closed by the
		// untrackConn that empties the registry. No connection can be added
		// meanwhile (trackConn refuses), so one wake-up is final; the loop
		// only re-checks.
		if s.connsGone == nil {
			s.connsGone = make(chan struct{})
		}
		gone := s.connsGone
		s.lifeMu.Unlock()

		select {
		case <-gone:
		case <-ctx.Done():
			s.closeConns()
			return ctx.Err()
		}
	}
}

// wakeConns is step 3 of Shutdown: idle connections are claimed and woken,
// WebSockets are told the server is going away. The registry is only read
// under the lock; deadlines are set and frames sent after releasing it.
func (s *Server) wakeConns() {
	var idle []net.Conn
	var sockets []*WSConn
	s.lifeMu.Lock()
	for tc := range s.conns {
		if tc.state.CompareAndSwap(connIdle, connClosing) {
			idle = append(idle, tc.conn)
		} else if ws := tc.ws.Load(); ws != nil {
			sockets = append(sockets, ws)
		}
	}
	s.lifeMu.Unlock()

	// The loop of an idle connection is parked in Peek (or about to be):
	// a deadline in the past makes that read return at once with a timeout,
	// which the loop takes as "nothing was asked" and closes silently. A
	// deadline rather than Close: it never blocks (a TLS Close writes an
	// alert), and the loop's own defers do the closing and the cleanup.
	for _, c := range idle {
		_ = c.SetReadDeadline(aLongTimeAgo)
	}
	// Each in its own goroutine: the close frame waits for any write the
	// app has in progress on that socket, and one slow peer must not hold
	// up the others or Shutdown itself.
	for _, ws := range sockets {
		go ws.goingAway()
	}
}

// closeConns is Shutdown's last resort: close every connection still
// registered. Their loops see the closed conn on their next read or write
// and finish, removing themselves from the registry.
func (s *Server) closeConns() {
	s.lifeMu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for tc := range s.conns {
		conns = append(conns, tc.conn)
	}
	s.lifeMu.Unlock()

	for _, c := range conns {
		// A past write deadline first: a TLS Close otherwise sends a
		// close_notify alert and may wait up to 5 s on a peer that is not
		// reading. Nobody is owed a clean TLS close at this point.
		_ = c.SetWriteDeadline(aLongTimeAgo)
		_ = c.Close()
	}
}

// shutdownContext is the context Run gives Shutdown: ShutdownTimeout from
// now, or one already done when the timeout is negative (do not wait).
func (s *Server) shutdownContext() (stdctx.Context, stdctx.CancelFunc) {
	if d := s.shutdownTimeout(); d > 0 {
		return stdctx.WithTimeout(stdctx.Background(), d)
	}
	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()
	return ctx, cancel
}
