//go:build unix

package rweb

// Run's signal handling (N-039), split out of graceful_shutdown_test.go so
// the rest of the package's tests build on Windows (N-071). These tests send
// SIGTERM to the test process with syscall.Kill, and TestRunReleasesSignals
// reads a child's syscall.WaitStatus; neither exists on Windows, where there
// is no SIGTERM to deliver to a process. Run's own code (signal.Notify on
// os.Interrupt and syscall.SIGTERM) builds everywhere, so only these tests
// need the tag. Serve/Shutdown, the option and the error path of
// RunWithHttpsRedirect stay in graceful_shutdown_test.go and run on every OS.
//
// As there: each test signals only while its own Run is known to be
// listening (after ReadyChan), and the one that needs a SIGTERM nobody
// handles runs in a child process.

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
