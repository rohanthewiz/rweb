package rweb

import (
	"testing"
)

// BenchmarkKeepAliveHello measures one small GET on a keep-alive loopback
// connection: request parsing, routing, a 5-byte body and the response write.
// It is the per-request cost the connection hardening (header budget,
// read/idle deadlines, keep-alive rules) must not make worse.
//
// The server side is driven through handleConnection on a listener the
// benchmark owns, rather than through Run, so no signal is needed to stop it.
// The client writes a fixed request and reads a response of a length learned
// on the first round trip, into buffers allocated once, so the allocations
// reported are the server's (b.ReportAllocs counts every goroutine's).
//
// The harness is benchKeepAlive (bench_test.go), shared with the TLS, JSON
// and browser-like variants there; this is its plain-TCP hello case, kept
// under its original name so earlier numbers stay comparable.
func BenchmarkKeepAliveHello(b *testing.B) {
	benchKeepAlive(b, false, benchHelloRoutes, "GET / HTTP/1.1\r\nHost: bench\r\n\r\n")
}
