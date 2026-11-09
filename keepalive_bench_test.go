package rweb

import (
	"bytes"
	"io"
	"net"
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
func BenchmarkKeepAliveHello(b *testing.B) {
	s := NewServer()
	s.Get("/", func(ctx Context) error { return ctx.WriteString("hello") })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConnection(c)
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	req := []byte("GET / HTTP/1.1\r\nHost: bench\r\n\r\n")

	// First round trip: learn the response length (head + "hello").
	if _, err := conn.Write(req); err != nil {
		b.Fatal(err)
	}
	var first []byte
	buf := make([]byte, 4096)
	for !bytes.HasSuffix(first, []byte("\r\n\r\nhello")) {
		n, err := conn.Read(buf)
		if err != nil {
			b.Fatal(err)
		}
		first = append(first, buf[:n]...)
	}
	resp := make([]byte, len(first))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(req); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(conn, resp); err != nil {
			b.Fatal(err)
		}
	}
}
