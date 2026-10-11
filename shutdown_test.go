package rweb

// Shutdown and request contexts: Run cancels the server's base context on
// SIGINT/SIGTERM, which every request context derives from. These tests call
// cancelBaseContext directly, the step Run takes after closing its listener,
// rather than signalling the test process: a signal would reach every server
// other tests left running in Run, and one sent before Run registers its
// handler would kill the process. Connections are served through serveTest
// (request_context_test.go).

import (
	"bufio"
	stdctx "context"
	"errors"
	"io"
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

			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)
			writeAll(t, c, getReq("/slow"))
			recv(t, entered, "handler to start")

			s.cancelBaseContext()
			if err := recv(t, result, "handler result"); !errors.Is(err, stdctx.Canceled) {
				t.Fatalf("got %v, want context.Canceled", err)
			}

			// The in-flight request is still answered...
			if status, body := readBody(t, br); status != 200 || body != "bye" {
				t.Fatalf("got %d %q, want 200 \"bye\"", status, body)
			}
			// ...and then the server closes the connection.
			_ = c.SetReadDeadline(time.Now().Add(waitFor))
			if _, err := br.ReadByte(); err != io.EOF {
				t.Fatalf("read after the last response: %v, want io.EOF", err)
			}
		})
	}
}

// A Run after a shutdown must not hand out contexts that are already
// cancelled: renewBaseContext, which Run calls first, replaces the base.
func TestShutdownBaseContextRenewed(t *testing.T) {
	s := NewServer()
	errs := make(chan error, 1)
	s.Get("/", func(ctx Context) error {
		errs <- ctx.Request().Context().Err()
		return nil
	})
	addr := serveTest(t, s, transports[0])

	s.cancelBaseContext()
	s.renewBaseContext()

	c := dialTest(t, addr, transports[0])
	writeAll(t, c, getReq("/"))
	if err := recv(t, errs, "handler"); err != nil {
		t.Fatalf("request context after renew: %v, want live", err)
	}
	readBody(t, bufio.NewReader(c))
}

// Synthetic requests derive from the same base, so a test can observe
// shutdown through Server.Request as well.
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

	s.cancelBaseContext()
	s.Request("GET", "/", nil, nil)
	if !errors.Is(got, stdctx.Canceled) {
		t.Fatalf("after shutdown: %v, want context.Canceled", got)
	}
}
