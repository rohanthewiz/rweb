package rweb

// Tests for the server lifecycle: Serve on a caller's listener, Shutdown
// (drain, then force), Run's signal handling, the accept loop's backoff and
// RunWithHttpsRedirect. Connections are real loopback sockets, plain and TLS,
// as in request_context_test.go, whose helpers these tests share.
//
// Most tests stop their server with Shutdown, not a signal. The few that
// test Run's signal handling send SIGTERM to the test process only while
// their own Run is known to be listening for it (after ReadyChan, which Run
// fires only once its signal handler is in place); the one that needs a
// SIGTERM nobody handles runs in a child process.

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	stdctx "context"
)

// serveShutdownTest serves s through Serve on a loopback listener (TLS for
// the tls transport) and returns the address and a channel that receives
// Serve's result. Whatever is still open when the test ends is closed by a
// Shutdown that does not wait.
func serveShutdownTest(t *testing.T, s *Server, tr transport) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tr.tls {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{testCert(t)}})
	}
	errc := make(chan error, 1)
	go func() { errc <- s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Shutdown(cancelledContext()) })
	return ln.Addr().String(), errc
}

// cancelledContext is a context that is already done: Shutdown with it
// closes everything at once.
func cancelledContext() stdctx.Context {
	ctx, cancel := stdctx.WithCancel(stdctx.Background())
	cancel()
	return ctx
}

// shutdownAsync runs Shutdown with timeout d in a goroutine.
func shutdownAsync(s *Server, d time.Duration) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := stdctx.WithTimeout(stdctx.Background(), d)
		defer cancel()
		done <- s.Shutdown(ctx)
	}()
	return done
}

// expectClosed fails unless the server closes c within waitFor: the read
// ends with EOF or a reset, not a timeout. Any bytes still buffered are
// skipped.
func expectClosed(t *testing.T, c net.Conn, r io.Reader) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(waitFor))
	_, err := io.Copy(io.Discard, r)
	if isTimeout(err) {
		t.Fatalf("connection still open after shutdown: %v", err)
	}
}

// A request in progress when Shutdown is called is answered in full, and
// Shutdown waits for it; the connection then closes rather than waiting for
// another request.
func TestShutdownWaitsForSlowHandler(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			entered, release := make(chan struct{}), make(chan struct{})
			body := strings.Repeat("x", 256<<10) // more than one write's worth
			s.Get("/slow", func(ctx Context) error {
				close(entered)
				<-release
				return ctx.WriteString(body)
			})
			addr, serveErr := serveShutdownTest(t, s, tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)
			writeAll(t, c, getReq("/slow"))
			recv(t, entered, "handler to start")

			shut := shutdownAsync(s, waitFor)
			if err := recv(t, serveErr, "Serve to return"); !errors.Is(err, ErrServerClosed) {
				t.Fatalf("Serve: %v, want ErrServerClosed", err)
			}
			select {
			case err := <-shut:
				t.Fatalf("Shutdown returned (%v) while a handler was still running", err)
			case <-time.After(settle):
			}

			close(release)
			if status, got := readBody(t, br); status != 200 || got != body {
				t.Fatalf("got %d and %d bytes, want 200 and %d bytes", status, len(got), len(body))
			}
			if err := recv(t, shut, "Shutdown"); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			expectClosed(t, c, br)
		})
	}
}

// Keep-alive connections waiting for their next request, and a new one that
// has sent nothing yet, are closed as soon as Shutdown starts, not after
// IdleTimeout (120 s by default).
func TestShutdownClosesIdleConnections(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Get("/", func(ctx Context) error { return ctx.WriteString("ok") })
			addr, _ := serveShutdownTest(t, s, tr)

			kept := dialTest(t, addr, tr)
			br := bufio.NewReader(kept)
			writeAll(t, kept, getReq("/"))
			if status, _ := readBody(t, br); status != 200 {
				t.Fatalf("status %d", status)
			}
			fresh := dialTest(t, addr, tr)
			time.Sleep(settle) // let the server accept fresh and wait on it

			start := time.Now()
			if err := recv(t, shutdownAsync(s, waitFor), "Shutdown"); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("Shutdown took %v with only idle connections", d)
			}
			expectClosed(t, kept, br)
			expectClosed(t, fresh, fresh)
		})
	}
}

// A handler that outlives the shutdown context has its connection closed,
// and Shutdown returns the context's error.
func TestShutdownForcesAfterTimeout(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			entered, release := make(chan struct{}), make(chan struct{})
			t.Cleanup(func() { close(release) })
			s.Get("/stuck", func(ctx Context) error {
				close(entered)
				<-release // ignores its request context on purpose
				return nil
			})
			addr, _ := serveShutdownTest(t, s, tr)
			c := dialTest(t, addr, tr)
			writeAll(t, c, getReq("/stuck"))
			recv(t, entered, "handler to start")

			start := time.Now()
			err := recv(t, shutdownAsync(s, 100*time.Millisecond), "Shutdown")
			if !errors.Is(err, stdctx.DeadlineExceeded) {
				t.Fatalf("Shutdown: %v, want context.DeadlineExceeded", err)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("Shutdown took %v with a 100ms context", d)
			}
			expectClosed(t, c, c)
		})
	}
}

// An event stream ends at shutdown: the base context is one of the things
// sendSSE waits on, so the stream is flushed and the connection closed even
// though its events channel stays open.
func TestShutdownEndsSSEStream(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			cleaned := make(chan struct{})
			s.Get("/events", func(ctx Context) error {
				ctx.(*context).sseCleanup = func() { close(cleaned) }
				ch := make(chan any, 1)
				ch <- "first"
				return ctx.SetSSE(ch, "tick") // ch is never closed
			})
			addr, _ := serveShutdownTest(t, s, tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)
			writeAll(t, c, "GET /events HTTP/1.1\r\nHost: test\r\nAccept: text/event-stream\r\n\r\n")
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(line, "data: first") {
					break
				}
			}

			if err := recv(t, shutdownAsync(s, waitFor), "Shutdown"); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			expectClosed(t, c, br)
			recv(t, cleaned, "SSE cleanup")
		})
	}
}

// readCloseFrame reads one server frame off r and returns its close code;
// it fails unless the frame is a close frame.
func readCloseFrame(t *testing.T, r io.Reader) int {
	t.Helper()
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatalf("reading close frame: %v", err)
	}
	if head[0] != 0x80|wsClose || head[1] < 2 {
		t.Fatalf("frame header % x, want a close frame with a code", head[:2])
	}
	return int(binary.BigEndian.Uint16(head[2:]))
}

// A WebSocket open at shutdown is sent a close frame with 1001 (going away)
// at once. A client that answers it ends the session, and Shutdown returns
// without waiting for its timeout.
func TestShutdownClosesWebSocket(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := wsServer(make(chan stdctx.Context, 1), nil)
			addr, _ := serveShutdownTest(t, s, tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)
			writeAll(t, c, wsUpgradeReq)
			ws := wsClient(t, c, br)
			if err := ws.WriteMessage(TextMessage, []byte("hi")); err != nil {
				t.Fatal(err)
			}
			expectEcho(t, ws, "hi")

			shut := shutdownAsync(s, waitFor)
			_ = c.SetReadDeadline(time.Now().Add(waitFor))
			// The client WSConn answers a close frame and closes its side.
			msg, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("waiting for the server's close frame: %v", err)
			}
			if msg.Type != CloseMessage || len(msg.Data) < 2 ||
				binary.BigEndian.Uint16(msg.Data) != wsCloseGoingAway {
				t.Fatalf("got %v % x, want a close frame with 1001", msg.Type, msg.Data)
			}
			if err := recv(t, shut, "Shutdown"); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
		})
	}
}

// A WebSocket client that never answers the close frame is cut off when the
// shutdown context ends.
func TestShutdownForcesWebSocket(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := wsServer(make(chan stdctx.Context, 1), nil)
			addr, _ := serveShutdownTest(t, s, tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)
			writeAll(t, c, wsUpgradeReq)
			wsClient(t, c, br) // reads the 101; the client then goes quiet

			err := recv(t, shutdownAsync(s, 200*time.Millisecond), "Shutdown")
			if !errors.Is(err, stdctx.DeadlineExceeded) {
				t.Fatalf("Shutdown: %v, want context.DeadlineExceeded", err)
			}
			_ = c.SetReadDeadline(time.Now().Add(waitFor))
			if code := readCloseFrame(t, br); code != wsCloseGoingAway {
				t.Fatalf("close code %d, want 1001", code)
			}
			expectClosed(t, c, br)
		})
	}
}

// Serve accepts on a caller's listener, plain or TLS; ReadyChan,
// GetListenAddr and GetListenPort report on it; Shutdown makes Serve return
// ErrServerClosed and closes the listener; and the server can serve again.
func TestServeCallerListener(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			ready := make(chan struct{}, 1)
			s := NewServer(ServerOptions{ReadyChan: ready})
			s.Get("/", func(ctx Context) error {
				return ctx.WriteString(fmt.Sprint("live=", ctx.Request().Context().Err() == nil))
			})

			for round := 1; round <= 2; round++ {
				addr, serveErr := serveShutdownTest(t, s, tr)
				recv(t, ready, "ReadyChan")
				if got := s.GetListenAddr(); got != addr {
					t.Fatalf("GetListenAddr %q, want %q", got, addr)
				}
				if _, port, _ := net.SplitHostPort(addr); s.GetListenPort() != port {
					t.Fatalf("GetListenPort %q, want %q", s.GetListenPort(), port)
				}

				c := dialTest(t, addr, tr)
				writeAll(t, c, getReq("/"))
				if status, body := readBody(t, bufio.NewReader(c)); status != 200 || body != "live=true" {
					t.Fatalf("round %d: got %d %q", round, status, body)
				}

				if err := recv(t, shutdownAsync(s, waitFor), "Shutdown"); err != nil {
					t.Fatalf("Shutdown: %v", err)
				}
				if err := recv(t, serveErr, "Serve to return"); !errors.Is(err, ErrServerClosed) {
					t.Fatalf("Serve: %v, want ErrServerClosed", err)
				}
				if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
					_ = c.Close()
					t.Fatal("listener still accepting after Shutdown")
				}
			}
		})
	}
}

// Shutdown is safe to call more than once, concurrently, and on a server
// that is not serving.
func TestShutdownRepeated(t *testing.T) {
	s := NewServer()
	if err := s.Shutdown(stdctx.Background()); err != nil {
		t.Fatalf("Shutdown before Serve: %v", err)
	}
	s.Get("/", func(ctx Context) error { return ctx.WriteString("ok") })
	addr, serveErr := serveShutdownTest(t, s, transports[0])
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, getReq("/"))
	readBody(t, bufio.NewReader(c))

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Shutdown(stdctx.Background()); err != nil {
				t.Errorf("concurrent Shutdown: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := recv(t, serveErr, "Serve to return"); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve: %v, want ErrServerClosed", err)
	}
}

// --- accept loop ---------------------------------------------------------------

// scriptedListener is a listener whose Accept plays a script: an error for
// each nil entry, a conn otherwise; then it blocks until closed. It records
// when each Accept was called.
type scriptedListener struct {
	mu     sync.Mutex
	script []net.Conn
	calls  []time.Time
	closed chan struct{}
	once   sync.Once
}

var errAcceptFailed = errors.New("accept failed (scripted)")

func newScriptedListener(script ...net.Conn) *scriptedListener {
	return &scriptedListener{script: script, closed: make(chan struct{})}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	l.calls = append(l.calls, time.Now())
	if len(l.script) > 0 {
		c := l.script[0]
		l.script = l.script[1:]
		l.mu.Unlock()
		if c == nil {
			return nil, errAcceptFailed
		}
		return c, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *scriptedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *scriptedListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func (l *scriptedListener) callTimes() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]time.Time(nil), l.calls...)
}

// Accept errors are retried after a delay that starts at 5 ms and doubles,
// not in a tight loop; the delay starts over after an accept succeeds.
func TestAcceptErrorBackoff(t *testing.T) {
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	// 4 errors (waits 5, 10, 20, 40 ms), a conn, then 2 errors: after the
	// conn the first wait is 5 ms again, not 80.
	ln := newScriptedListener(nil, nil, nil, nil, srv, nil, nil)
	s := NewServer()
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(ln) }()

	deadline := time.Now().Add(waitFor)
	for len(ln.callTimes()) < 8 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	calls := ln.callTimes()
	if len(calls) < 8 {
		t.Fatalf("%d Accept calls, want 8", len(calls))
	}
	gaps := make([]time.Duration, len(calls)-1)
	for i := range gaps {
		gaps[i] = calls[i+1].Sub(calls[i])
	}
	// gaps[0..3]: after the 4 errors; gaps[3] is the 40 ms wait.
	if gaps[0] < 4*time.Millisecond || gaps[3] < 35*time.Millisecond {
		t.Fatalf("no backoff between failed accepts: gaps %v", gaps)
	}
	// gaps[4]: the conn, no wait. gaps[5]: first error after it, 5 ms again.
	if gaps[5] > 30*time.Millisecond {
		t.Fatalf("backoff not reset after a successful accept: gaps %v", gaps)
	}

	if err := s.Shutdown(cancelledContext()); !errors.Is(err, stdctx.Canceled) && err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := recv(t, serveErr, "Serve to return"); !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve: %v, want ErrServerClosed", err)
	}
}

// The delay sequence itself: 5 ms doubling, capped at 1 s.
func TestAcceptBackoffSequence(t *testing.T) {
	var d time.Duration
	var got []time.Duration
	for range 10 {
		d = nextAcceptDelay(d)
		got = append(got, d)
	}
	var want []time.Duration
	for _, ms := range []time.Duration{5, 10, 20, 40, 80, 160, 320, 640, 1000, 1000} {
		want = append(want, ms*time.Millisecond)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delays %v, want %v", got, want)
		}
	}
}

// --- Run and signals -------------------------------------------------------------

// runForTest starts Run in a goroutine with the given options and waits until
// it is serving (and so listening for SIGTERM). The returned channel gets
// Run's result.
func runForTest(t *testing.T, s *Server, ready chan struct{}) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(waitFor):
		t.Fatal("Run not ready")
	}
	return done
}

// Run, SIGTERM, Run again: the second Run serves with live request contexts
// on a new port, and the second SIGTERM stops it too.
func TestRunAgainAfterSignal(t *testing.T) {
	ready := make(chan struct{}, 1)
	s := NewServer(ServerOptions{Address: "127.0.0.1:0", ReadyChan: ready})
	s.Get("/", func(ctx Context) error {
		return ctx.WriteString(fmt.Sprint("live=", ctx.Request().Context().Err() == nil))
	})
	get := func() string {
		c := dialTest(t, s.GetListenAddr(), transports[0])
		writeAll(t, c, getReq("/"))
		_, body := readBody(t, bufio.NewReader(c))
		return body
	}

	for round := 1; round <= 2; round++ {
		done := runForTest(t, s, ready)
		if body := get(); body != "live=true" {
			t.Fatalf("round %d: %q", round, body)
		}
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		if err := recv(t, done, "Run to return after SIGTERM"); err != nil {
			t.Fatalf("round %d: Run: %v", round, err)
		}
	}
}

// After SIGTERM, Run drains: a request in flight is answered in full before
// Run returns, within ShutdownTimeout.
func TestRunDrainsOnSignal(t *testing.T) {
	ready := make(chan struct{}, 1)
	s := NewServerWithOptions(WithAddress("127.0.0.1:0"), WithReadyChan(ready), WithShutdownTimeout(waitFor))
	entered, release := make(chan struct{}), make(chan struct{})
	s.Get("/slow", func(ctx Context) error {
		close(entered)
		<-release
		return ctx.WriteString("done")
	})
	done := runForTest(t, s, ready)
	c := dialTest(t, s.GetListenAddr(), transports[0])
	br := bufio.NewReader(c)
	writeAll(t, c, getReq("/slow"))
	recv(t, entered, "handler to start")

	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case err := <-done:
		t.Fatalf("Run returned (%v) with a request in flight", err)
	case <-time.After(settle):
	}
	close(release)
	if status, body := readBody(t, br); status != 200 || body != "done" {
		t.Fatalf("got %d %q", status, body)
	}
	if err := recv(t, done, "Run to return"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// A negative ShutdownTimeout makes Run close everything at once on a signal.
func TestRunNegativeShutdownTimeout(t *testing.T) {
	ready := make(chan struct{}, 1)
	s := NewServerWithOptions(WithAddress("127.0.0.1:0"), WithReadyChan(ready), WithShutdownTimeout(-1))
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.Get("/stuck", func(ctx Context) error {
		close(entered)
		<-release
		return nil
	})
	done := runForTest(t, s, ready)
	c := dialTest(t, s.GetListenAddr(), transports[0])
	writeAll(t, c, getReq("/stuck"))
	recv(t, entered, "handler to start")

	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if err := recv(t, done, "Run to return"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	expectClosed(t, c, c)
}

// WithShutdownTimeout sets the option, WithOptions copies it, and Run's
// timeout resolves zero to the 10 s default.
func TestShutdownTimeoutOption(t *testing.T) {
	var o ServerOptions
	WithShutdownTimeout(3 * time.Second)(&o)
	if o.ShutdownTimeout != 3*time.Second {
		t.Fatalf("WithShutdownTimeout: %v", o.ShutdownTimeout)
	}
	var copied ServerOptions
	WithOptions(o)(&copied)
	if copied.ShutdownTimeout != o.ShutdownTimeout {
		t.Fatalf("WithOptions dropped ShutdownTimeout: %v", copied.ShutdownTimeout)
	}
	if d := NewServer().shutdownTimeout(); d != 10*time.Second {
		t.Fatalf("default shutdown timeout %v, want 10s", d)
	}
}

// signalHelperEnv marks the child process TestRunReleasesSignals starts.
const signalHelperEnv = "RWEB_TEST_SIGNAL_HELPER"

// Once Run has returned, SIGTERM is the process's business again: rweb no
// longer swallows it. Checking that means letting a SIGTERM reach a process
// with no handler for it, which kills it, so the check runs in a child
// process (this test binary, re-run for this test only). The child runs Run
// in a goroutine, stops it with one SIGTERM, then sends a second; the parent
// expects the child to die of that second one. Before Run called
// signal.Stop the child survived it, which is how a process running
// RunWithHttpsRedirect outlived SIGTERM.
func TestRunReleasesSignals(t *testing.T) {
	if os.Getenv(signalHelperEnv) == "1" {
		signalHelper()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunReleasesSignals$", "-test.count=1")
	cmd.Env = append(os.Environ(), signalHelperEnv+"=1")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("child: %v, want it killed by SIGTERM\n%s", err, out)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("child: %v, want it killed by SIGTERM\n%s", err, out)
	}
	if !strings.Contains(string(out), "run returned") {
		t.Fatalf("child died before Run returned\n%s", out)
	}
}

// signalHelper is the child side of TestRunReleasesSignals. Its exit codes
// say what went wrong; a pass is death by the second SIGTERM.
func signalHelper() {
	ready := make(chan struct{}, 1)
	s := NewServer(ServerOptions{Address: "127.0.0.1:0", ReadyChan: ready})
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		os.Exit(3)
	}
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		os.Exit(4)
	}
	fmt.Println("run returned")
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	time.Sleep(time.Second)
	fmt.Println("survived the second SIGTERM")
	os.Exit(5)
}

// --- RunWithHttpsRedirect ------------------------------------------------------

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// SIGTERM stops both sides of RunWithHttpsRedirect: the HTTPS server and
// the plain-HTTP redirector. It used to stop only the HTTPS side, leaving the
// process serving redirects.
func TestRunWithHttpsRedirectStopsOnSignal(t *testing.T) {
	ready := make(chan struct{}, 1)
	redirectAddr := freeAddr(t)
	s := NewServer(ServerOptions{
		Address:   redirectAddr,
		ReadyChan: ready,
		TLS: TLSCfg{UseTLS: true, TLSAddr: "127.0.0.1:0",
			Config: &tls.Config{Certificates: []tls.Certificate{testCert(t)}}},
	})
	s.Get("/", func(ctx Context) error { return ctx.WriteString("secure") })

	done := make(chan error, 1)
	go func() { done <- s.RunWithHttpsRedirect() }()
	recv(t, ready, "ReadyChan")

	// The redirector answers 301 to the same path over https.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	var resp *http.Response
	var err error
	for range 50 { // the redirector may start a moment after ReadyChan
		if resp, err = client.Get("http://" + redirectAddr + "/a?b=1"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "https://"+redirectAddr+"/a?b=1" {
		t.Fatalf("redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if err := recv(t, done, "RunWithHttpsRedirect to return"); err != nil {
		t.Fatalf("RunWithHttpsRedirect: %v", err)
	}
	for _, addr := range []string{redirectAddr, s.GetListenAddr()} {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			t.Fatalf("%s still accepting after SIGTERM", addr)
		}
	}
}

// An error on either side stops both: here the HTTPS side cannot start (no
// certificate), and RunWithHttpsRedirect returns that error instead of
// serving redirects to nowhere.
func TestRunWithHttpsRedirectStopsOnError(t *testing.T) {
	redirectAddr := freeAddr(t)
	s := NewServer(ServerOptions{
		Address: redirectAddr,
		TLS: TLSCfg{UseTLS: true, TLSAddr: "127.0.0.1:0",
			CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"},
	})
	done := make(chan error, 1)
	go func() { done <- s.RunWithHttpsRedirect() }()
	if err := recv(t, done, "RunWithHttpsRedirect to return"); err == nil {
		t.Fatal("RunWithHttpsRedirect: nil error, want the certificate error")
	}
	if c, err := net.DialTimeout("tcp", redirectAddr, time.Second); err == nil {
		_ = c.Close()
		t.Fatal("redirector still accepting after the HTTPS side failed")
	}
}
