package rweb

// Lingering close after an early refusal (N-059). serveConn refuses some
// requests before any handler runs (400, 408, 413, 417, 431, 501), often
// while the client is still sending. It used to write the response and
// close at once; bytes arriving after that close make the kernel send a
// reset, which can reach the client before it has read the response (on
// Linux the response is then lost). Now refuse shuts the write side first
// and drains what the client still sends for up to lingerTimeout
// (lingerClose, conn_limits.go).

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A client that goes on sending its body after a 413 can finish sending,
// reads the whole response and then a clean EOF. Before N-059 its writes
// failed with a broken pipe or a reset once the server had closed.
func TestRefusalDrainsWhileClientSends(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, limitsServer(ServerOptions{MaxRequestBodySize: 1 << 10}), tr)
			conn := dialTest(t, addr, tr)
			writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: t\r\nContent-Length: 67108864\r\n\r\n")

			// The body goes out on its own goroutine, as a client that writes
			// before it reads would send it; 4 MB is far more than the
			// socket buffers hold, so it only gets through if the server
			// reads it.
			sent := make(chan error, 1)
			go func() {
				_, err := io.CopyN(conn, zeroReader{}, 4<<20)
				sent <- err
			}()

			got, _ := readUntilClosed(conn)
			if !strings.HasPrefix(got, "HTTP/1.1 413 ") {
				t.Fatalf("got %q, want a 413", firstLine(got))
			}
			if err := recv(t, sent, "the body write"); err != nil {
				t.Fatalf("writing the rest of the body after the 413: %v", err)
			}
		})
	}
}

// The write side is shut before the draining starts, so a client that has
// stopped sending sees EOF straight after the response rather than after
// lingerTimeout.
func TestRefusalHalfClosesBeforeDraining(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			addr := serveTest(t, limitsServer(ServerOptions{}), tr)
			conn := dialTest(t, addr, tr)
			// Obsolete line folding: refused with 400 (serveConn).
			writeAll(t, conn, "GET / HTTP/1.1\r\nHost: t\r\nX-A: 1\r\n folded\r\n\r\n")
			got, took := readUntilClosed(conn)
			if !strings.HasPrefix(got, "HTTP/1.1 400 ") {
				t.Fatalf("got %q, want a 400", firstLine(got))
			}
			if took >= lingerTimeout/2 {
				t.Fatalf("EOF after %v; the write side should be shut before the drain (lingerTimeout %v)", took, lingerTimeout)
			}
		})
	}
}

// A client that never stops sending is cut off once lingerTimeout has
// passed: the drain is bounded by time, so a refused connection cannot be
// held open by streaming at it.
func TestRefusalLingerIsBounded(t *testing.T) {
	addr := serveTest(t, limitsServer(ServerOptions{MaxRequestBodySize: 1 << 10}), transports[0])
	conn := dialTest(t, addr, transports[0])
	writeAll(t, conn, "POST /echo HTTP/1.1\r\nHost: t\r\nContent-Length: 1099511627776\r\n\r\n")

	start := time.Now()
	sent := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, zeroReader{}) // endless: only an error ends it
		sent <- err
	}()
	got, _ := readUntilClosed(conn)
	if !strings.HasPrefix(got, "HTTP/1.1 413 ") {
		t.Fatalf("got %q, want a 413", firstLine(got))
	}
	err := recv(t, sent, "the endless write to fail")
	took := time.Since(start)
	if err == nil || isTimeout(err) {
		t.Fatalf("write ended with %v; want the server to have closed the connection", err)
	}
	if took < lingerTimeout/2 || took > lingerTimeout+2*time.Second {
		t.Fatalf("server closed after %v, want about lingerTimeout (%v)", took, lingerTimeout)
	}
}

// lingerClose leaves a conn that cannot half-close to the caller's Close at
// once, without reading from it.
func TestLingerCloseWithoutCloseWrite(t *testing.T) {
	c := &noHalfCloseConn{}
	done := make(chan struct{})
	go func() { lingerClose(c); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lingerClose blocked on a conn without CloseWrite")
	}
	if c.reads != 0 {
		t.Fatalf("lingerClose read %d times from a conn it could not half-close", c.reads)
	}
}

// noHalfCloseConn is a net.Conn without CloseWrite that counts reads. The
// embedded interface is nil: lingerClose must call nothing else on it.
type noHalfCloseConn struct {
	net.Conn
	reads int
}

func (c *noHalfCloseConn) Read([]byte) (int, error) {
	c.reads++
	return 0, errors.New("unexpected read")
}
