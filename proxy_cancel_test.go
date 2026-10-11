package rweb

// Proxy and the client's request context: a client that disconnects while
// the upstream call is in flight must cancel that call, and the abandoned
// request must not reach the error handler. Internal test so it can serve
// through handleConnection (serveTest, request_context_test.go) over a real
// socket the client can close.

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rohanthewiz/rweb/consts"
)

func TestProxyCancelsUpstreamWhenClientLeaves(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			// The upstream parks until its request is cancelled, so the only
			// way it returns promptly is the cancellation under test.
			entered := make(chan struct{}, 1)
			upstreamGone := make(chan error, 1)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-r.Context().Done()
				upstreamGone <- r.Context().Err()
			}))
			// Cleanups run last-in first-out: the client connections are
			// closed first, which cancels a still-parked upstream handler
			// (on a failed run) so that Close does not wait on it.
			t.Cleanup(up.Close)
			t.Cleanup(up.CloseClientConnections)

			s := NewServer()
			errHandlerCalls := make(chan error, 1)
			s.errorHandler = func(_ Context, err error) { errHandlerCalls <- err }

			// Middleware runs ahead of the route, so it sees what the proxy
			// handler returned and the status it left behind.
			type outcome struct {
				err    error
				status int
			}
			handlerDone := make(chan outcome, 1)
			s.Use(func(ctx Context) error {
				err := ctx.Next()
				handlerDone <- outcome{err, ctx.Response().Status()}
				return err
			})
			if err := s.Proxy("/up", up.URL, 1); err != nil {
				t.Fatal(err)
			}

			c := dialTest(t, serveTest(t, s, tr), tr)
			writeAll(t, c, getReq("/up/slow"))
			recv(t, entered, "upstream to receive the proxied request")

			_ = c.Close()
			if err := recv(t, upstreamGone, "upstream request to be cancelled"); err == nil {
				t.Fatal("upstream context ended without an error")
			}

			got := recv(t, handlerDone, "proxy handler to return")
			if got.err != nil {
				t.Fatalf("proxy handler returned %v, want nil for a departed client", got.err)
			}
			if got.status != consts.StatusBadGateway {
				t.Fatalf("status = %d, want %d", got.status, consts.StatusBadGateway)
			}
			select {
			case err := <-errHandlerCalls:
				t.Fatalf("error handler called for a departed client: %v", err)
			default:
			}
		})
	}
}

// The quiet path is only for a departed client: an upstream that fails while
// the client is still connected still goes to the error handler.
func TestProxyUpstreamFailureStillReported(t *testing.T) {
	// A server that is closed straight away gives an address that refuses
	// connections.
	up := httptest.NewServer(http.NotFoundHandler())
	deadURL := up.URL
	up.Close()

	s := NewServer()
	errHandlerCalls := make(chan error, 1)
	s.errorHandler = func(ctx Context, err error) {
		errHandlerCalls <- err
		ctx.SetStatus(consts.StatusInternalServerError)
	}
	if err := s.Proxy("/up", deadURL, 1); err != nil {
		t.Fatal(err)
	}

	tr := transports[0]
	c := dialTest(t, serveTest(t, s, tr), tr)
	writeAll(t, c, getReq("/up/x"))
	status, _ := readBody(t, bufio.NewReader(c))
	if status != consts.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if err := recv(t, errHandlerCalls, "error handler call"); err == nil {
		t.Fatal("error handler got a nil error")
	}
}
