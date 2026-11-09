package rweb

// Shutdown and request contexts: Shutdown (which Run calls on SIGINT/SIGTERM)
// cancels the server's base context, which every request context derives
// from. These tests call Shutdown directly rather than signalling the test
// process, so no other test's server is disturbed. Connections are served
// through Serve (serveShutdownTest, graceful_shutdown_test.go); the rest of
// Shutdown's behaviour (draining, idle connections, the deadline, SSE and
// WebSockets) is tested there.

import (
	"bufio"
	stdctx "context"
	"errors"
	"testing"
	"time"
)

// A handler blocked on its request context is cancelled by shutdown, and the
// keep-alive connection closes after that response instead of waiting for
// another request.
func TestShutdownCancelsInFlightRequests(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			entered := make(chan struct{}, 1)
			result := make(chan error, 1)
			s.Get("/slow", func(ctx Context) error {
				rc := ctx.Request().Context()
				entered <- struct{}{}
				select {
				case <-rc.Done():
					result <- rc.Err()
				case <-time.After(5 * time.Second):
					result <- errors.New("not cancelled")
				}
				return ctx.WriteString("bye")
			})

			addr, _ := serveShutdownTest(t, s, tr)
			c := dialTest(t, addr, tr)
			br := bufio.NewReader(c)
			writeAll(t, c, getReq("/slow"))
			recv(t, entered, "handler to start")

			shut := shutdownAsync(s, waitFor)
			if err := recv(t, result, "handler result"); !errors.Is(err, stdctx.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}

			// The in-flight request is still answered...
			if status, body := readBody(t, br); status != 200 || body != "bye" {
				t.Fatalf("got %d %q, want 200 \"bye\"", status, body)
			}
			// ...and then the server closes the connection.
			expectClosed(t, c, br)
			if err := recv(t, shut, "Shutdown"); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
		})
	}
}

// Serving again after a shutdown must not hand out contexts that are already
// cancelled: Serve (and Run) renew the base context first.
func TestShutdownBaseContextRenewed(t *testing.T) {
	ready := make(chan struct{}, 1)
	s := NewServer(ServerOptions{ReadyChan: ready})
	errs := make(chan error, 1)
	s.Get("/", func(ctx Context) error {
		errs <- ctx.Request().Context().Err()
		return nil
	})
	serveShutdownTest(t, s, transports[0])
	recv(t, ready, "first Serve")
	if err := s.Shutdown(stdctx.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	addr, _ := serveShutdownTest(t, s, transports[0])
	recv(t, ready, "second Serve")
	c := dialTest(t, addr, transports[0])
	writeAll(t, c, getReq("/"))
	if err := recv(t, errs, "handler"); err != nil {
		t.Fatalf("request context after serving again: %v, want live", err)
	}
	readBody(t, bufio.NewReader(c))
}

// Synthetic requests derive from the same base, so a test can observe
// shutdown through Server.Request as well; Shutdown works on a server that
// is not serving.
func TestShutdownSyntheticRequest(t *testing.T) {
	s := NewServer()
	var got error
	s.Get("/", func(ctx Context) error {
		got = ctx.Request().Context().Err()
		return nil
	})

	s.Request("GET", "/", nil, nil)
	if got != nil {
		t.Fatalf("before shutdown: %v, want live", got)
	}

	if err := s.Shutdown(stdctx.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	s.Request("GET", "/", nil, nil)
	if !errors.Is(got, stdctx.Canceled) {
		t.Fatalf("after shutdown: %v, want context.Canceled", got)
	}
}
