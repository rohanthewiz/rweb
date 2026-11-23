package rweb

// Tests for the lazy request body (N-057, request_body.go): nothing of a
// body is read before a handler asks; Body() reads it whole, BodyReader()
// streams it; a body that cannot be read ends the handler and is answered by
// the server; what nobody read is drained or the connection closed; MaxBody
// caps a route.
//
// They talk to the server over raw connections (serveTest / dialTest), since
// most of what is checked is what goes over the wire and when.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// readResp reads one response off br, body included.
func readResp(t *testing.T, br *bufio.Reader) (*http.Response, string) {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return resp, string(b)
}

// chunkedBody encodes data as a chunked body in pieces of size n.
func chunkedBody(data []byte, n int) []byte {
	var b bytes.Buffer
	for len(data) > 0 {
		k := min(n, len(data))
		fmt.Fprintf(&b, "%x\r\n", k)
		b.Write(data[:k])
		b.WriteString("\r\n")
		data = data[k:]
	}
	b.WriteString("0\r\n\r\n")
	return b.Bytes()
}

// bufferedLen is how much of the request body the server holds in memory
// for ctx's request.
func bufferedLen(ctx Context) int {
	return len(ctx.Request().(*request).body)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// expectConnClosed fails unless the server closes the connection (EOF, or a
// reset) within waitFor, with nothing more sent.
func expectConnClosed(t *testing.T, c net.Conn, br *bufio.Reader) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(waitFor))
	if b, err := io.ReadAll(br); isTimeout(err) {
		t.Fatalf("connection still open after the response (read %q)", b)
	}
}

// The handler runs before the body has arrived: the client sends the head
// and half the body, waits until the handler says it has started, and only
// then sends the rest. With the body read ahead of the handler, as it was,
// the handler would never start and the test would time out.
func TestBodyIsNotReadBeforeTheHandler(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			started := make(chan struct{})
			s := NewServer()
			s.Post("/up", func(ctx Context) error {
				close(started)
				return ctx.WriteString(string(ctx.Request().Body()))
			})
			c := dialTest(t, serveTest(t, s, tr), tr)
			writeAll(t, c, "POST /up HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\nhello")
			recv(t, started, "handler start before the body is complete")
			writeAll(t, c, "world")
			if code, body := readBody(t, bufio.NewReader(c)); code != 200 || body != "helloworld" {
				t.Fatalf("got %d %q", code, body)
			}
		})
	}
}

// BodyReader hands a large body to the handler without the server holding
// it: the handler hashes 3 MB as it arrives and request.body stays empty.
// Both framings, both transports, and the connection serves another request
// afterwards (the body was read to its end, so it is at the next request).
func TestBodyReaderStreamsWithoutBuffering(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 3<<20/16)
	want := sum(data)

	for _, tr := range transports {
		for _, framing := range []string{"content-length", "chunked"} {
			t.Run(tr.name+"/"+framing, func(t *testing.T) {
				var held atomic.Int64
				s := NewServer()
				s.Post("/hash", func(ctx Context) error {
					h := sha256.New()
					n, err := io.Copy(h, ctx.Request().BodyReader())
					if err != nil {
						return err
					}
					held.Store(int64(bufferedLen(ctx)))
					return ctx.WriteString(fmt.Sprintf("%d %s", n, hex.EncodeToString(h.Sum(nil))))
				})
				s.Get("/next", func(ctx Context) error { return ctx.WriteString("next") })

				c := dialTest(t, serveTest(t, s, tr), tr)
				br := bufio.NewReader(c)
				head := "POST /hash HTTP/1.1\r\nHost: test\r\n"
				body := data
				if framing == "chunked" {
					head += "Transfer-Encoding: chunked\r\n\r\n"
					body = chunkedBody(data, 100_000)
				} else {
					head += fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
				}
				go func() {
					_, _ = io.WriteString(c, head)
					_, _ = c.Write(body)
				}()

				code, got := readBody(t, br)
				if code != 200 || got != fmt.Sprintf("%d %s", len(data), want) {
					t.Fatalf("got %d %q", code, got)
				}
				if held.Load() != 0 {
					t.Fatalf("server buffered %d bytes of a streamed body", held.Load())
				}

				writeAll(t, c, getReq("/next"))
				if code, got := readBody(t, br); code != 200 || got != "next" {
					t.Fatalf("next request on the connection: %d %q", code, got)
				}
			})
		}
	}
}

// Body() and BodyReader() together: Body first makes the body replayable,
// every reader then starting from its beginning; a reader first takes its
// bytes for good, and Body() returns what was left.
func TestBodyAndBodyReaderTogether(t *testing.T) {
	s := NewServer()
	s.Post("/body-first", func(ctx Context) error {
		whole := string(ctx.Request().Body())
		a, _ := io.ReadAll(ctx.Request().BodyReader())
		b, _ := io.ReadAll(ctx.Request().BodyReader())
		return ctx.WriteString(whole + "|" + string(a) + "|" + string(b) + "|" + string(ctx.Request().Body()))
	})
	s.Post("/reader-first", func(ctx Context) error {
		r := ctx.Request().BodyReader()
		first := make([]byte, 4)
		if _, err := io.ReadFull(r, first); err != nil {
			return err
		}
		rest := string(ctx.Request().Body())
		// The first reader carries on into what Body() buffered.
		again, _ := io.ReadAll(r)
		return ctx.WriteString(string(first) + "|" + rest + "|" + string(again))
	})
	addr := serveTest(t, s, transports[0])

	for path, want := range map[string]string{
		"/body-first":   "0123456789|0123456789|0123456789|0123456789",
		"/reader-first": "0123|456789|456789",
	} {
		c := dialTest(t, addr, transports[0])
		writeAll(t, c, "POST "+path+" HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\n0123456789")
		if code, got := readBody(t, bufio.NewReader(c)); code != 200 || got != want {
			t.Errorf("%s: got %d %q, want %q", path, code, got, want)
		}
	}
}

// A small body nobody reads is drained after the handler, so the connection
// carries the next request: an unrouted POST (the N-046 case), and a routed
// handler that ignores its body. Chunked too, where the drain has to walk
// the framing. The second request is sent in the same write as the first, so
// a body left on the wire would be parsed as its request line.
func TestUnreadSmallBodyIsDrained(t *testing.T) {
	s := NewServer()
	s.Post("/ignore", func(ctx Context) error { return ctx.WriteString("ignored") })
	s.Get("/next", func(ctx Context) error { return ctx.WriteString("next") })
	addr := serveTest(t, s, transports[0])

	payload := strings.Repeat("x", 5000)
	for _, tc := range []struct {
		name, req string
		code      int
	}{
		{"unrouted", "POST /nowhere HTTP/1.1\r\nHost: test\r\nContent-Length: 5000\r\n\r\n" + payload, 404},
		{"ignored", "POST /ignore HTTP/1.1\r\nHost: test\r\nContent-Length: 5000\r\n\r\n" + payload, 200},
		{"chunked", "POST /ignore HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n" +
			string(chunkedBody([]byte(payload), 700)), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dialTest(t, addr, transports[0])
			br := bufio.NewReader(c)
			writeAll(t, c, tc.req+getReq("/next"))
			resp, _ := readResp(t, br)
			if resp.StatusCode != tc.code || resp.Close {
				t.Fatalf("first response: %d, Connection: close = %v", resp.StatusCode, resp.Close)
			}
			if code, got := readBody(t, br); code != 200 || got != "next" {
				t.Fatalf("request behind the unread body: %d %q", code, got)
			}
		})
	}
}

// A large body nobody reads is not read: the response goes out at once with
// "Connection: close", while the client has sent only a sliver of the 50 MB
// it announced, and the connection then closes. Before, the server waited
// for all 50 MB before it even looked up the route.
func TestUnreadLargeBodyClosesAfterResponse(t *testing.T) {
	s := NewServer()
	addr := serveTest(t, s, transports[0])
	c := dialTest(t, addr, transports[0])
	br := bufio.NewReader(c)

	start := time.Now()
	writeAll(t, c, "POST /nowhere HTTP/1.1\r\nHost: test\r\nContent-Length: 52428800\r\n\r\n"+strings.Repeat("x", 1000))
	resp, _ := readResp(t, br)
	if resp.StatusCode != 404 || !resp.Close {
		t.Fatalf("got %d, Connection: close = %v; want 404 and close", resp.StatusCode, resp.Close)
	}
	if d := time.Since(start); d > waitFor {
		t.Fatalf("response took %v: the server waited on the body", d)
	}
	expectConnClosed(t, c, br)
}

// A handler that turns a request down without reading its body never has the
// body sent by a client that asked first: no 100 Continue goes out, only the
// handler's answer, and the connection closes (the client may or may not
// send the body now, so the next request's start is unknowable).
func TestNoContinueForABodyNobodyReads(t *testing.T) {
	s := NewServer()
	s.Post("/guarded", func(ctx Context) error {
		return ctx.SetStatus(401).WriteString("no")
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	writeAll(t, c, "POST /guarded HTTP/1.1\r\nHost: test\r\nContent-Length: 1000000\r\nExpect: 100-continue\r\n\r\n")

	resp, body := readResp(t, br) // http.ReadResponse would skip a 100; the status below is what counts
	if resp.StatusCode != 401 || body != "no" || !resp.Close {
		t.Fatalf("got %d %q close=%v; want the handler's 401 and close", resp.StatusCode, body, resp.Close)
	}
	expectConnClosed(t, c, br)
}

// The same exchange read byte for byte: the first thing on the wire is the
// 401, not an interim response.
func TestNoContinueOnTheWire(t *testing.T) {
	s := NewServer()
	s.Post("/guarded", func(ctx Context) error { return ctx.SetStatus(401).WriteString("no") })
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	writeAll(t, c, "POST /guarded HTTP/1.1\r\nHost: test\r\nContent-Length: 1000000\r\nExpect: 100-continue\r\n\r\n")
	got, _ := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 401 ") {
		t.Fatalf("first line %q, want the 401", firstLine(got))
	}
}

// A body that cannot be read ends the handler at Body(): nothing after the
// call runs, so a handler cannot act on a body it did not get, and the
// server answers as it did when such a request never reached a handler.
func TestUnreadableBodyEndsTheHandler(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ServerOptions
		req  string
		// close the client's write side after sending (a client that left)
		hangUp bool
		want   string // status-line prefix; "" for no response at all
	}{
		{name: "malformed chunk",
			req:  "POST /store HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhelloXX\r\n0\r\n\r\n",
			want: "HTTP/1.1 400 "},
		{name: "chunked over the cap", opts: ServerOptions{MaxRequestBodySize: 8},
			req:  "POST /store HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n5\r\nworld\r\n0\r\n\r\n",
			want: "HTTP/1.1 413 "},
		{name: "too slow", opts: ServerOptions{ReadTimeout: 150 * time.Millisecond},
			req:  "POST /store HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\nhello",
			want: "HTTP/1.1 408 "},
		{name: "client left mid-body", hangUp: true,
			req: "POST /store HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\nhello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entered, stored atomic.Bool
			done := make(chan struct{})
			s := NewServer(tc.opts)
			s.Use(func(ctx Context) error {
				defer close(done) // deferred functions still run when a handler is ended
				return ctx.Next()
			})
			s.Post("/store", func(ctx Context) error {
				entered.Store(true)
				body := ctx.Request().Body()
				stored.Store(true) // stands for "wrote body to the database"
				return ctx.WriteString(string(body))
			})
			c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
			writeAll(t, c, tc.req)
			if tc.hangUp {
				_ = c.(*net.TCPConn).CloseWrite()
			}
			got, _ := readUntilClosed(c)
			recv(t, done, "handler chain to unwind")

			if !entered.Load() {
				t.Fatal("handler never ran: the test did not reach the lazy read")
			}
			if stored.Load() {
				t.Fatal("handler carried on past Body() with a body that could not be read")
			}
			if tc.want == "" {
				if got != "" {
					t.Fatalf("response to a client that left: %q", got)
				}
			} else if !strings.HasPrefix(got, tc.want) {
				t.Fatalf("got %q, want %s", firstLine(got), tc.want)
			}
		})
	}
}

// A handler reading through BodyReader gets the error back instead. Whatever
// it does with it, the client gets the server's answer for the failure: here
// the handler swallows the error and reports success, and the client still
// sees 413, never a 200 for an upload that did not arrive.
func TestBodyReaderErrorIsAnsweredByTheServer(t *testing.T) {
	var readErr atomic.Value
	s := NewServer(ServerOptions{MaxRequestBodySize: 8})
	s.Post("/up", func(ctx Context) error {
		_, err := io.Copy(io.Discard, ctx.Request().BodyReader())
		readErr.Store(err)
		return ctx.WriteString("stored") // wrong, and overruled
	})
	// A handler that passes the error on is not sent to the error handler:
	// its page would be thrown away, and its log line misplaced.
	var errorHandlerRan atomic.Bool
	s.Post("/up-err", func(ctx Context) error {
		_, err := io.Copy(io.Discard, ctx.Request().BodyReader())
		return err
	})
	s.SetErrorHandler(func(ctx Context, err error) {
		errorHandlerRan.Store(true)
		_ = ctx.SetStatus(500).WriteString("oops")
	})
	addr := serveTest(t, s, transports[0])

	for _, path := range []string{"/up", "/up-err"} {
		c := dialTest(t, addr, transports[0])
		writeAll(t, c, "POST "+path+" HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n5\r\nworld\r\n0\r\n\r\n")
		got, _ := readUntilClosed(c)
		if !strings.HasPrefix(got, "HTTP/1.1 413 ") || strings.Contains(got, "stored") {
			t.Fatalf("%s: got %q, want the server's 413", path, got)
		}
	}
	if err, _ := readErr.Load().(error); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("BodyReader error = %v, want ErrBodyTooLarge", err)
	}
	if errorHandlerRan.Load() {
		t.Fatal("error handler ran for a request the server refuses itself")
	}
}

// MaxBody caps the body for the routes behind it: a Content-Length over it
// is refused before the handler (and before 100 Continue), a chunked body
// when it passes the cap; other routes keep the server-wide cap; and a
// MaxBody further down the chain cannot raise the cap again.
func TestMaxBody(t *testing.T) {
	var smallRan atomic.Int32
	s := NewServer()
	echo := func(ctx Context) error { return ctx.WriteString(string(ctx.Request().Body())) }
	small := s.Group("/small", MaxBody(8))
	small.Post("/echo", func(ctx Context) error {
		smallRan.Add(1)
		return echo(ctx)
	})
	// A group inside asks for more: the lower cap still counts.
	small.Group("/roomy", MaxBody(64)).Post("/echo", echo)
	s.Post("/open/echo", echo)
	addr := serveTest(t, s, transports[0])

	twenty := strings.Repeat("y", 20)
	for _, tc := range []struct {
		name, req, want, body string
	}{
		{"under the cap", "POST /small/echo HTTP/1.1\r\nHost: test\r\nConnection: close\r\nContent-Length: 5\r\n\r\nhello",
			"HTTP/1.1 200 ", "hello"},
		{"content-length over", "POST /small/echo HTTP/1.1\r\nHost: test\r\nContent-Length: 20\r\n\r\n" + twenty,
			"HTTP/1.1 413 ", ""},
		{"expect, over: no 100", "POST /small/echo HTTP/1.1\r\nHost: test\r\nContent-Length: 20\r\nExpect: 100-continue\r\n\r\n",
			"HTTP/1.1 413 ", ""},
		{"chunked over", "POST /small/echo HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n" +
			string(chunkedBody([]byte(twenty), 6)), "HTTP/1.1 413 ", ""},
		{"other route unaffected", "POST /open/echo HTTP/1.1\r\nHost: test\r\nConnection: close\r\nContent-Length: 20\r\n\r\n" + twenty,
			"HTTP/1.1 200 ", twenty},
		{"inner MaxBody cannot raise", "POST /small/roomy/echo HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n" +
			string(chunkedBody([]byte(twenty), 6)), "HTTP/1.1 413 ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := smallRan.Load()
			c := dialTest(t, addr, transports[0])
			writeAll(t, c, tc.req)
			got, _ := readUntilClosed(c)
			if !strings.HasPrefix(got, tc.want) {
				t.Fatalf("got %q, want %s", firstLine(got), tc.want)
			}
			if tc.body != "" && !strings.HasSuffix(got, "\r\n\r\n"+tc.body) {
				t.Fatalf("body not echoed: %q", got)
			}
			// A Content-Length over the cap never reaches the handler.
			if strings.Contains(tc.name, "content-length over") || strings.Contains(tc.name, "expect") {
				if smallRan.Load() != before {
					t.Fatal("handler ran for a Content-Length over MaxBody")
				}
			}
		})
	}
}

// MaxBody cannot raise the cap past the server's: the server-wide check
// runs before any handler.
func TestMaxBodyDoesNotExceedServerCap(t *testing.T) {
	s := NewServer(ServerOptions{MaxRequestBodySize: 8})
	s.Use(MaxBody(1 << 20))
	s.Post("/echo", func(ctx Context) error { return ctx.WriteString(string(ctx.Request().Body())) })
	addr := serveTest(t, s, transports[0])

	for _, req := range []string{
		"POST /echo HTTP/1.1\r\nHost: test\r\nContent-Length: 20\r\n\r\n" + strings.Repeat("y", 20),
		"POST /echo HTTP/1.1\r\nHost: test\r\nTransfer-Encoding: chunked\r\n\r\n" + string(chunkedBody(bytes.Repeat([]byte("y"), 20), 6)),
	} {
		c := dialTest(t, addr, transports[0])
		writeAll(t, c, req)
		if got, _ := readUntilClosed(c); !strings.HasPrefix(got, "HTTP/1.1 413 ") {
			t.Fatalf("got %q, want 413", firstLine(got))
		}
	}
}

// A BodyReader kept past its handler reads nothing of the connection's next
// request: the pooled request it points into has moved on.
func TestBodyReaderIsStaleAfterItsHandler(t *testing.T) {
	var kept io.Reader
	s := NewServer()
	s.Post("/keep", func(ctx Context) error {
		kept = ctx.Request().BodyReader()
		return ctx.WriteString("kept")
	})
	s.Post("/probe", func(ctx Context) error {
		n, err := kept.Read(make([]byte, 16))
		if n != 0 || !errors.Is(err, ErrBodyReadAfterHandler) {
			return fmt.Errorf("stale reader read %d bytes, err %v", n, err)
		}
		return ctx.WriteString(string(ctx.Request().Body()))
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	writeAll(t, c, "POST /keep HTTP/1.1\r\nHost: test\r\nContent-Length: 3\r\n\r\nabc")
	if code, got := readBody(t, br); code != 200 || got != "kept" {
		t.Fatalf("first: %d %q", code, got)
	}
	writeAll(t, c, "POST /probe HTTP/1.1\r\nHost: test\r\nContent-Length: 6\r\n\r\nsecret")
	if code, got := readBody(t, br); code != 200 || got != "secret" {
		t.Fatalf("second: %d %q", code, got)
	}
}

// A multipart upload is parsed straight off the connection: the file ends up
// readable through GetFormFile while the request body is never buffered.
func TestMultipartFormStreamsFromTheConnection(t *testing.T) {
	file := bytes.Repeat([]byte("multipart!"), 200_000) // 2 MB
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "big.bin")
	_, _ = fw.Write(file)
	_ = w.WriteField("name", "lazy")
	_ = w.Close()

	t.Setenv("TMPDIR", t.TempDir()) // the spill file goes with the test
	var held atomic.Int64
	held.Store(-1)
	s := NewServer(ServerOptions{MultipartMaxMemory: 64 << 10})
	s.Post("/upload", func(ctx Context) error {
		f, _, err := ctx.Request().GetFormFile("file")
		if err != nil {
			return err
		}
		defer f.Close()
		got, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		held.Store(int64(bufferedLen(ctx)))
		return ctx.WriteString(ctx.Request().FormValue("name") + " " + sum(got))
	})
	s.Get("/next", func(ctx Context) error { return ctx.WriteString("next") })

	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	go func() {
		_, _ = fmt.Fprintf(c, "POST /upload HTTP/1.1\r\nHost: test\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n",
			w.FormDataContentType(), buf.Len())
		_, _ = c.Write(buf.Bytes())
	}()
	if code, got := readBody(t, br); code != 200 || got != "lazy "+sum(file) {
		t.Fatalf("got %d %q", code, got)
	}
	if held.Load() != 0 {
		t.Fatalf("server buffered %d bytes of the multipart body", held.Load())
	}
	writeAll(t, c, getReq("/next"))
	if code, got := readBody(t, br); code != 200 || got != "next" {
		t.Fatalf("next request on the connection: %d %q", code, got)
	}
}

// A urlencoded form is parsed on first use and leaves the body available.
func TestURLEncodedFormParsedOnUse(t *testing.T) {
	s := NewServer()
	s.Post("/form", func(ctx Context) error {
		req := ctx.Request()
		return ctx.WriteString(req.GetPostValue("a") + "," + req.FormValue("b") + "," + string(req.Body()))
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	writeAll(t, c, "POST /form HTTP/1.1\r\nHost: test\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 7\r\n\r\na=1&b=2")
	if code, got := readBody(t, bufio.NewReader(c)); code != 200 || got != "1,2,a=1&b=2" {
		t.Fatalf("got %d %q", code, got)
	}
}

// The disconnect watch waits for the body: Context() taken before the body
// is read starts no watch (it would eat a body byte), the read to the end
// starts it, and a client that then leaves cancels the context.
func TestRequestContextWatchStartsAfterBody(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			bodyRead := make(chan string, 1)
			cancelled := make(chan struct{})
			s := NewServer()
			s.Post("/work", func(ctx Context) error {
				reqCtx := ctx.Request().Context() // before the body: no watch yet
				bodyRead <- string(ctx.Request().Body())
				<-reqCtx.Done()
				close(cancelled)
				return nil
			})
			c := dialTest(t, serveTest(t, s, tr), tr)
			writeAll(t, c, "POST /work HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\nhello")
			time.Sleep(settle) // a watch started too early would take the 'w' below
			writeAll(t, c, "world")
			if got := recv(t, bodyRead, "body"); got != "helloworld" {
				t.Fatalf("body %q: a byte went missing", got)
			}
			time.Sleep(settle)
			_ = c.Close()
			recv(t, cancelled, "context cancelled by the client leaving")
		})
	}
}

// An application may end a request itself with ErrAbortHandler: no response
// is written and the connection closes.
func TestAbortHandlerByTheApplication(t *testing.T) {
	s := NewServer()
	s.Get("/drop", func(ctx Context) error {
		_ = ctx.WriteString("never sent")
		panic(ErrAbortHandler)
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	writeAll(t, c, getReq("/drop"))
	if got, _ := readUntilClosed(c); got != "" {
		t.Fatalf("got %q, want the connection closed with no response", got)
	}
}

// A synthetic request's body is in memory from the start: Body and
// BodyReader both serve it, and MaxBody answers 413 in the Response.
func TestSyntheticRequestBody(t *testing.T) {
	s := NewServer()
	s.Post("/echo", func(ctx Context) error {
		streamed, _ := io.ReadAll(ctx.Request().BodyReader())
		return ctx.WriteString(string(ctx.Request().Body()) + "|" + string(streamed))
	})
	s.Group("/small", MaxBody(4)).Post("/echo", func(ctx Context) error {
		return ctx.WriteString(string(ctx.Request().Body()))
	})

	res := s.Request("POST", "/echo", nil, strings.NewReader("hello"))
	if res.Status() != 200 || string(res.Body()) != "hello|hello" {
		t.Fatalf("echo: %d %q", res.Status(), res.Body())
	}
	res = s.Request("POST", "/small/echo", nil, strings.NewReader("hello"))
	if res.Status() != 413 || len(res.Body()) != 0 {
		t.Fatalf("over MaxBody: %d %q, want 413 and no body", res.Status(), res.Body())
	}
	res = s.Request("POST", "/small/echo", nil, strings.NewReader("hi"))
	if res.Status() != 200 || string(res.Body()) != "hi" {
		t.Fatalf("under MaxBody: %d %q", res.Status(), res.Body())
	}
}

// An event stream answering a POST whose body the handler left unread: the
// body is read to its end before the stream starts, as it always was, so
// the stream's disconnect detection does not take a late body byte for the
// client leaving.
func TestSSEAfterUnreadBody(t *testing.T) {
	s := NewServer()
	s.Post("/events", func(ctx Context) error {
		ch := make(chan any, 2)
		ch <- "one"
		ch <- SSEClose
		return ctx.SetSSE(ch, "tick")
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	body := strings.Repeat("z", 600_000) // more than the after-handler drain takes
	go func() {
		_, _ = io.WriteString(c, fmt.Sprintf("POST /events HTTP/1.1\r\nHost: test\r\nContent-Length: %d\r\n\r\n", len(body)))
		_, _ = io.WriteString(c, body)
	}()
	got, _ := readUntilClosed(c)
	if !strings.HasPrefix(got, "HTTP/1.1 200 ") || !strings.Contains(got, "data: one") {
		t.Fatalf("stream not delivered: %q", got)
	}
}

// A WebSocket handshake that carries a body: the body is buffered before the
// connection is taken over, so the first frame is read as a frame and not
// from the middle of the body.
func TestWebSocketUpgradeWithBody(t *testing.T) {
	s := NewServer()
	s.WebSocket("/ws", func(ws *WSConn) error {
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				return err
			}
			if err := ws.WriteMessage(msg.Type, msg.Data); err != nil {
				return err
			}
		}
	})
	c := dialTest(t, serveTest(t, s, transports[0]), transports[0])
	br := bufio.NewReader(c)
	writeAll(t, c, "GET /ws HTTP/1.1\r\nHost: test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nContent-Length: 5\r\n\r\nhello")
	ws := wsClient(t, c, br)
	if err := ws.WriteMessage(TextMessage, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	expectEcho(t, ws, "frame")
}
