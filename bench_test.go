package rweb

// Keep-alive benchmarks for the request/response hot path, over loopback,
// plain TCP and TLS. They are the yardstick for the performance items
// (N-042 onwards): each measures one request and its whole response on a
// connection that stays open, so the per-request cost is what shows, not
// connection setup or the TLS handshake.
//
//	              hello (5 B text)   JSON (~200 B)      browser-like head    body at coalesceBodyMax
//	plain TCP     KeepAliveHello     KeepAliveJSON      KeepAliveBrowser     KeepAliveBodyCap
//	TLS           KeepAliveHelloTLS  KeepAliveJSONTLS   KeepAliveBrowserTLS  KeepAliveBodyCapTLS
//
// The browser-like request carries the eight or so headers a real browser
// sends (User-Agent, Accept*, Cookie, Referer...), about 700 bytes of head,
// so it is the one that shows the cost of reading a request head.
//
// Each reports cpu-ns/op beside ns/op where the platform allows: the CPU
// the process spent per request, which a busy machine disturbs far less
// than the round-trip time (see benchKeepAlive).
//
// To compare a change, build a test binary on each side (go test -c) and
// run them in turns, comparing medians rather than one run of each:
// loopback timings drift by tens of percent from one run to the next on a
// busy machine, and taking turns spreads that drift over both sides.
//
//	for i in $(seq 8); do
//		./old.test -test.run=NONE -test.bench=KeepAlive -test.benchtime=1s
//		./new.test -test.run=NONE -test.bench=KeepAlive -test.benchtime=1s
//	done

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
)

// benchBrowserRequest is a request as a desktop browser sends it for a page
// navigation on a site it has visited before: a parameterised route, a query
// string and the usual headers, cookies included.
const benchBrowserRequest = "GET /products/42?ref=home HTTP/1.1\r\n" +
	"Host: shop.example.com\r\n" +
	"User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36\r\n" +
	"Accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8\r\n" +
	"Accept-Language: en-US,en;q=0.9\r\n" +
	"Accept-Encoding: gzip, deflate, br, zstd\r\n" +
	"Connection: keep-alive\r\n" +
	"Cookie: session=6f1c2b9e8a7d4c3b2a19f0e8d7c6b5a4; theme=dark; _ga=GA1.1.1234567890.1731300000; consent=1\r\n" +
	"Referer: https://shop.example.com/\r\n" +
	"Upgrade-Insecure-Requests: 1\r\n" +
	"\r\n"

// benchProduct is the JSON variant's payload: a handful of fields of the
// kinds an API returns, about 200 bytes encoded.
type benchProduct struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Price    float64  `json:"price"`
	InStock  bool     `json:"in_stock"`
	Tags     []string `json:"tags"`
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
}

var benchProductValue = benchProduct{
	ID: 42, Name: "Espresso machine", Price: 349.99, InStock: true,
	Tags: []string{"kitchen", "coffee", "appliance"}, Category: "home",
	Summary: "A compact pump espresso machine with a steam wand.",
}

func BenchmarkKeepAliveHelloTLS(b *testing.B) {
	benchKeepAlive(b, true, benchHelloRoutes, "GET / HTTP/1.1\r\nHost: bench\r\n\r\n")
}

func BenchmarkKeepAliveJSON(b *testing.B) {
	benchKeepAlive(b, false, benchJSONRoutes, "GET /api/product HTTP/1.1\r\nHost: bench\r\n\r\n")
}

func BenchmarkKeepAliveJSONTLS(b *testing.B) {
	benchKeepAlive(b, true, benchJSONRoutes, "GET /api/product HTTP/1.1\r\nHost: bench\r\n\r\n")
}

func BenchmarkKeepAliveBrowser(b *testing.B) {
	benchKeepAlive(b, false, benchBrowserRoutes, benchBrowserRequest)
}

func BenchmarkKeepAliveBrowserTLS(b *testing.B) {
	benchKeepAlive(b, true, benchBrowserRoutes, benchBrowserRequest)
}

// The BodyCap variants send a body of exactly coalesceBodyMax bytes, the
// largest that writeResponse copies into the header block's write: they
// are where that copy costs most against the syscall (and TLS record) it
// saves, so a change to the cap should be judged on them.
func BenchmarkKeepAliveBodyCap(b *testing.B) {
	benchKeepAlive(b, false, benchBodyCapRoutes, "GET /cap HTTP/1.1\r\nHost: bench\r\n\r\n")
}

func BenchmarkKeepAliveBodyCapTLS(b *testing.B) {
	benchKeepAlive(b, true, benchBodyCapRoutes, "GET /cap HTTP/1.1\r\nHost: bench\r\n\r\n")
}

func benchBodyCapRoutes(s *Server) {
	body := bytes.Repeat([]byte("x"), coalesceBodyMax)
	s.Get("/cap", func(ctx Context) error { return ctx.Bytes(body) })
}

func benchHelloRoutes(s *Server) {
	s.Get("/", func(ctx Context) error { return ctx.WriteString("hello") })
}

func benchJSONRoutes(s *Server) {
	s.Get("/api/product", func(ctx Context) error { return ctx.WriteJSON(benchProductValue) })
}

// The browser route does what a page handler typically does with such a
// request before rendering: reads a path param and a header or two.
func benchBrowserRoutes(s *Server) {
	s.Get("/products/:id", func(ctx Context) error {
		id := ctx.Request().Param("id")
		lang := ctx.Request().Header("Accept-Language")
		if id == "" || lang == "" {
			return ctx.SetStatus(400).WriteString("bad request")
		}
		return ctx.WriteHTML("<p>product</p>")
	})
}

// benchKeepAlive serves the routes that setup registers on a loopback
// listener of its own, through handleConnection (the per-connection path
// Run and Serve use, without their signal handling), and times req and its
// response on one keep-alive connection, plain or over TLS.
//
// The client side is kept out of the numbers as far as it can be: req is
// written as is, and the response, whose length is learned on a first
// untimed round trip, is read with io.ReadFull into a buffer allocated
// once. b.ReportAllocs counts every goroutine's allocations, so what it
// reports is the server's, plus whatever crypto/tls allocates per record on
// the client (nothing, in steady state).
func benchKeepAlive(b *testing.B, useTLS bool, setup func(*Server), req string) {
	s := NewServer()
	setup(s)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	if useTLS {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{testCert(b)}})
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

	var conn net.Conn
	if useTLS {
		conn, err = tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	} else {
		conn, err = net.Dial("tcp", ln.Addr().String())
	}
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	reqBytes := []byte(req)

	// First round trip (untimed; it also runs the TLS handshake): learn the
	// response's length.
	if _, err := conn.Write(reqBytes); err != nil {
		b.Fatal(err)
	}
	resp := make([]byte, readFirstResponse(b, conn))

	b.ReportAllocs()
	cpuStart, cpuOK := processCPUTime()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(reqBytes); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(conn, resp); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	// cpu-ns/op is the CPU the whole process (server, client, runtime)
	// spent per request, user and system time together. ns/op is a
	// round-trip latency, and on a busy machine most of it can be waiting
	// to be scheduled, which swamps a change of a few percent; CPU time
	// counts only work done, a syscall or a wake-up saved included, so it
	// is the steadier figure to compare changes on.
	if cpuEnd, ok := processCPUTime(); cpuOK && ok {
		b.ReportMetric(float64(cpuEnd-cpuStart)/float64(b.N), "cpu-ns/op")
	}
}

// readFirstResponse reads one Content-Length-framed response off conn and
// returns its length on the wire. It insists on a 200, so a benchmark
// request that the server refuses (a 400 that closes the connection, say)
// fails loudly rather than timing an error path.
func readFirstResponse(b *testing.B, conn net.Conn) int {
	b.Helper()
	var got []byte
	buf := make([]byte, 4096)
	for {
		if end := bytes.Index(got, []byte("\r\n\r\n")); end >= 0 {
			head := string(got[:end])
			if !strings.HasPrefix(head, "HTTP/1.1 200 ") {
				b.Fatalf("benchmark request not answered 200:\n%s", head)
			}
			length := -1
			for _, line := range strings.Split(head, "\r\n")[1:] {
				k, v, _ := strings.Cut(line, ":")
				if strings.EqualFold(k, "Content-Length") {
					length, _ = strconv.Atoi(strings.TrimSpace(v))
				}
			}
			if length < 0 {
				b.Fatalf("response has no Content-Length:\n%s", head)
			}
			if total := end + 4 + length; len(got) >= total {
				if len(got) > total {
					b.Fatalf("more than one response arrived (%d bytes, expected %d)", len(got), total)
				}
				return total
			}
		}
		n, err := conn.Read(buf)
		if err != nil {
			b.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
}
