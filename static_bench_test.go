package rweb

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Benchmarks for a static-file GET, the per-request cost of StaticFiles'
// handler: route tokens, root resolution, the containment check, the open
// and stat, the file headers and the streamed body.
//
// Both run from a working directory the benchmark owns (b.Chdir, which also
// sets $PWD, as a shell-started server has it), because a relative root is
// resolved against it: os.Getwd's cost depends on $PWD matching ".".
//
// Two variants. Synthetic (s.Request) isolates the handler: the body is
// read into memory by materializeBody and nothing touches a socket, so the
// handler's share of the total is large. KeepAlive is the cost a client
// sees on one loopback connection, with the file sent by sendfile.

// staticBenchServer chdirs into a fresh directory holding pub/css/site.css
// and returns a server with that directory mounted relative (StaticFiles) at
// /static/ and absolute (StaticFilesAbs) at /abs/, plus the file's body.
func staticBenchServer(b *testing.B) (*Server, []byte) {
	b.Helper()
	cwd := b.TempDir()
	body := bytes.Repeat([]byte("body{margin:0}\n"), 64) // ~1 KB, a typical small asset
	if err := os.MkdirAll(filepath.Join(cwd, "pub", "css"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "pub", "css", "site.css"), body, 0o644); err != nil {
		b.Fatal(err)
	}
	b.Chdir(cwd)

	s := NewServer()
	s.StaticFiles("/static/", "pub", 1)
	s.StaticFilesAbs("/abs/", filepath.Join(cwd, "pub"), 1)
	return s, body
}

func BenchmarkStaticFileSynthetic(b *testing.B) {
	for _, bc := range []struct{ name, url string }{
		{"relative", "/static/css/site.css"},
		{"absolute", "/abs/css/site.css"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			s, body := staticBenchServer(b)
			if r := s.Request("GET", bc.url, nil, nil); r.Status() != 200 || !bytes.Equal(r.Body(), body) {
				b.Fatalf("GET %s = %d, %d bytes; want 200, %d bytes", bc.url, r.Status(), len(r.Body()), len(body))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.Request("GET", bc.url, nil, nil)
			}
		})
	}
}

// BenchmarkStaticFileKeepAlive is BenchmarkKeepAliveHello for a static file:
// one keep-alive loopback connection, a fixed request, and a response of a
// length learned on the first round trip (the Date and Last-Modified headers
// are fixed-width, so every response is the same length).
func BenchmarkStaticFileKeepAlive(b *testing.B) {
	for _, bc := range []struct{ name, url string }{
		{"relative", "/static/css/site.css"},
		{"absolute", "/abs/css/site.css"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			s, body := staticBenchServer(b)

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

			req := []byte("GET " + bc.url + " HTTP/1.1\r\nHost: bench\r\n\r\n")
			if _, err := conn.Write(req); err != nil {
				b.Fatal(err)
			}
			var first []byte
			buf := make([]byte, 8192)
			for !bytes.HasSuffix(first, body) {
				n, err := conn.Read(buf)
				if err != nil {
					b.Fatal(err)
				}
				first = append(first, buf[:n]...)
			}
			if !bytes.HasPrefix(first, []byte("HTTP/1.1 200")) {
				b.Fatalf("unexpected response: %q", first[:min(len(first), 64)])
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
		})
	}
}
