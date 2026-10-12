package rweb

// Tests for streamed response bodies (rweb.Stream, SendFile, SendFileAs) on
// top of response.bodyStream. Like static_stream_test.go they run over raw
// keep-alive connections on both transports: a framing mistake (a chunk
// size that disagrees with its payload, a missing last chunk, a stray byte
// after it) only shows up as the *next* response on the connection parsing
// wrong, and plain TCP and TLS take different copy paths.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// trackedReader records whether its stream was read from and closed, so a
// test can tell "streamed", "dropped unread" and "leaked" apart.
type trackedReader struct {
	r      io.Reader
	reads  atomic.Int64
	closed atomic.Bool
}

func (t *trackedReader) Read(p []byte) (int, error) {
	t.reads.Add(1)
	return t.r.Read(p)
}

func (t *trackedReader) Close() error {
	t.closed.Store(true)
	return nil
}

// randBytes returns n random bytes; n is chosen odd by callers so no copy
// buffer or chunk size divides it evenly.
func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// pipeProducer streams data through an io.Pipe in uneven writes from a
// goroutine, the shape a real generated download has (a dump piped from a
// subprocess), and closes the writer with failWith after the data.
func pipeProducer(data []byte, failWith error) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		for rest, step := data, 1; len(rest) > 0; step = step*7%50000 + 1 {
			n := min(step, len(rest))
			if _, err := pw.Write(rest[:n]); err != nil {
				return
			}
			rest = rest[n:]
		}
		pw.CloseWithError(failWith)
	}()
	return pr
}

// A body of unknown size goes out chunked, after whatever was already
// written, and the connection stays usable for the next request.
func TestStreamUnknownSizeIsChunked(t *testing.T) {
	big := randBytes(t, 3<<20+17)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Get("/gen", func(ctx Context) error {
				_ = ctx.WriteString("head:")
				return Stream(ctx, pipeProducer(big, nil), -1)
			})
			s.Get("/empty", func(ctx Context) error {
				return Stream(ctx, strings.NewReader(""), -1)
			})
			s.Get("/small", func(ctx Context) error { return ctx.WriteString("small-body") })
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			want := append([]byte("head:"), big...)
			for i := range 2 {
				res, body := roundTrip(t, c, br, consts.MethodGet, "/gen")
				if res.StatusCode != 200 || len(res.TransferEncoding) != 1 || res.TransferEncoding[0] != "chunked" {
					t.Fatalf("GET gen #%d = %d, Transfer-Encoding %v; want 200, chunked",
						i, res.StatusCode, res.TransferEncoding)
				}
				if res.Header.Get("Content-Length") != "" {
					t.Fatalf("chunked response also carries Content-Length %q", res.Header.Get("Content-Length"))
				}
				if !bytes.Equal(body, want) {
					t.Fatalf("GET gen #%d: body differs (%d bytes, want %d)", i, len(body), len(want))
				}
			}
			if res, body := roundTrip(t, c, br, consts.MethodGet, "/empty"); res.StatusCode != 200 || len(body) != 0 {
				t.Fatalf("GET empty = %d, %q; want 200, empty", res.StatusCode, body)
			}
			if _, body := roundTrip(t, c, br, consts.MethodGet, "/small"); string(body) != "small-body" {
				t.Fatalf("GET small after streams = %q", body)
			}
		})
	}
}

// A sized stream that is not a file takes the Content-Length path, and is
// closed once sent.
func TestStreamKnownSizeReader(t *testing.T) {
	big := randBytes(t, 1<<20+3)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			readers := make(chan *trackedReader, 1)
			s.Get("/sized", func(ctx Context) error {
				// More bytes than declared: only size of them may go out.
				r := &trackedReader{r: io.MultiReader(bytes.NewReader(big), strings.NewReader("EXTRA"))}
				readers <- r
				return Stream(ctx, r, int64(len(big)))
			})
			s.Get("/small", func(ctx Context) error { return ctx.WriteString("small-body") })
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			res, body := roundTrip(t, c, br, consts.MethodGet, "/sized")
			if res.ContentLength != int64(len(big)) || !bytes.Equal(body, big) {
				t.Fatalf("GET sized: Content-Length %d, %d body bytes; want %d of the source",
					res.ContentLength, len(body), len(big))
			}
			if _, body := roundTrip(t, c, br, consts.MethodGet, "/small"); string(body) != "small-body" {
				t.Fatalf("GET small after sized = %q", body)
			}
			if r := <-readers; !r.closed.Load() {
				t.Error("stream not closed after its response")
			}
		})
	}
}

// A producer that fails mid-stream must not yield a complete-looking body:
// the last chunk is withheld and the connection closed.
func TestStreamProducerErrorTruncates(t *testing.T) {
	part := randBytes(t, 100<<10+1)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Get("/fails", func(ctx Context) error {
				return Stream(ctx, pipeProducer(part, errors.New("dump failed")), -1)
			})
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			if _, err := fmt.Fprint(c, "GET /fails HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			res, err := http.ReadResponse(br, &http.Request{Method: consts.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			if err == nil || strings.Contains(err.Error(), "timeout") {
				t.Fatalf("reading failed stream: %d bytes, err %v; want a truncated-body error", len(body), err)
			}
			if !bytes.Equal(body, part) {
				t.Errorf("got %d bytes before the failure, want the %d produced", len(body), len(part))
			}
		})
	}
}

// A client that leaves mid-download stops the producer: its next write to
// the pipe fails instead of it running to the end of a dump nobody reads.
func TestStreamClientGoneStopsProducer(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			stopped := make(chan error, 1)
			s.Get("/endless", func(ctx Context) error {
				pr, pw := io.Pipe()
				go func() {
					chunk := make([]byte, 16<<10)
					for {
						if _, err := pw.Write(chunk); err != nil {
							stopped <- err
							return
						}
					}
				}()
				return Stream(ctx, pr, -1)
			})
			c := dialTest(t, serveTest(t, s, tr), tr)

			if _, err := fmt.Fprint(c, "GET /endless HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(c, make([]byte, 64<<10)); err != nil {
				t.Fatal(err)
			}
			_ = c.Close()

			select {
			case err := <-stopped:
				if !errors.Is(err, io.ErrClosedPipe) {
					t.Errorf("producer stopped with %v, want io.ErrClosedPipe", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("producer still writing 5s after the client left")
			}
		})
	}
}

// HEAD gets the framing headers and no body, and the stream is closed
// without a single read.
func TestStreamHeadNeverReads(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			readers := make(chan *trackedReader, 2)
			s.Get("/gen", func(ctx Context) error {
				r := &trackedReader{r: strings.NewReader("never-sent")}
				readers <- r
				size := int64(-1)
				if ctx.Request().QueryParam("sized") != "" {
					size = int64(len("never-sent"))
				}
				return Stream(ctx, r, size)
			})
			s.Get("/small", func(ctx Context) error { return ctx.WriteString("small-body") })
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			res, _ := roundTrip(t, c, br, consts.MethodHead, "/gen")
			if res.StatusCode != 200 || len(res.TransferEncoding) != 1 || res.TransferEncoding[0] != "chunked" {
				t.Fatalf("HEAD gen = %d, Transfer-Encoding %v; want 200, chunked", res.StatusCode, res.TransferEncoding)
			}
			res, _ = roundTrip(t, c, br, consts.MethodHead, "/gen?sized=1")
			if res.Header.Get("Content-Length") != strconv.Itoa(len("never-sent")) {
				t.Fatalf("HEAD sized gen Content-Length = %q", res.Header.Get("Content-Length"))
			}
			// Had either body been sent after all, this would parse it.
			if _, body := roundTrip(t, c, br, consts.MethodGet, "/small"); string(body) != "small-body" {
				t.Fatalf("GET after HEAD = %q", body)
			}
			for i := range 2 {
				r := <-readers
				if r.reads.Load() != 0 || !r.closed.Load() {
					t.Errorf("HEAD #%d: %d reads, closed %v; want 0, true", i, r.reads.Load(), r.closed.Load())
				}
			}
		})
	}
}

// A synthetic Request hands the response back in memory, so the stream
// ends up in Body, whatever its size class.
func TestStreamSyntheticRequestHasBody(t *testing.T) {
	big := randBytes(t, 1<<20+5)
	s := NewServer()
	s.Get("/gen", func(ctx Context) error { return Stream(ctx, pipeProducer(big, nil), -1) })
	s.Get("/sized", func(ctx Context) error { return Stream(ctx, bytes.NewReader(big), int64(len(big))) })
	for _, path := range []string{"/gen", "/sized"} {
		res := s.Request(consts.MethodGet, path, nil, nil)
		if res.Status() != 200 || !bytes.Equal(res.Body(), big) {
			t.Errorf("synthetic GET %s = %d, %d bytes; want 200, the stream", path, res.Status(), len(res.Body()))
		}
	}
}

// Middleware reading the body after Next sees the stream, and the response
// it leaves is then an ordinary in-memory one with a Content-Length.
func TestStreamBodyVisibleToMiddleware(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Use(func(ctx Context) error {
				if err := ctx.Next(); err != nil {
					return err
				}
				ctx.Response().SetBody(bytes.ToUpper(ctx.Response().Body()))
				return nil
			})
			s.Get("/gen", func(ctx Context) error {
				return Stream(ctx, pipeProducer([]byte("streamed-body"), nil), -1)
			})
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			res, body := roundTrip(t, c, br, consts.MethodGet, "/gen")
			if string(body) != "STREAMED-BODY" || res.ContentLength != int64(len(body)) || len(res.TransferEncoding) != 0 {
				t.Fatalf("GET gen = %q (Content-Length %d, Transfer-Encoding %v); want the rewritten body, sized",
					body, res.ContentLength, res.TransferEncoding)
			}
		})
	}
}

// A handler that fails after attaching a stream gets the error response,
// and the stream is closed unread rather than buffered in front of it.
func TestStreamDroppedOnHandlerError(t *testing.T) {
	s := NewServer()
	readers := make(chan *trackedReader, 1)
	s.Get("/fails", func(ctx Context) error {
		r := &trackedReader{r: strings.NewReader("must-not-appear")}
		readers <- r
		_ = Stream(ctx, r, -1)
		return errors.New("late failure")
	})
	res := s.Request(consts.MethodGet, "/fails", nil, nil)
	if res.Status() != 500 || bytes.Contains(res.Body(), []byte("must-not-appear")) {
		t.Fatalf("GET fails = %d, %q; want 500 without the stream", res.Status(), res.Body())
	}
	if r := <-readers; r.reads.Load() != 0 || !r.closed.Load() {
		t.Errorf("stream after handler error: %d reads, closed %v; want 0, true", r.reads.Load(), r.closed.Load())
	}
}

func TestSendFile(t *testing.T) {
	dir := t.TempDir()
	big := randBytes(t, 2<<20+9)
	dumpPath := filepath.Join(dir, "tmp-83412.bin")
	if err := os.WriteFile(dumpPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(dumpPath, modTime, modTime); err != nil {
		t.Fatal(err)
	}

	// The handler maps a missing file to 404, as SendFile's doc suggests.
	sendOr404 := func(ctx Context, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return ctx.SetStatus(consts.StatusNotFound).WriteString("no such file")
		}
		return err
	}

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Get("/dump", func(ctx Context) error {
				return sendOr404(ctx, SendFileAs(ctx, dumpPath, "db-backup.dump"))
			})
			s.Get("/raw", func(ctx Context) error { return sendOr404(ctx, SendFile(ctx, dumpPath)) })
			s.Get("/missing", func(ctx Context) error {
				return sendOr404(ctx, SendFile(ctx, filepath.Join(dir, "nope.bin")))
			})
			s.Get("/dir", func(ctx Context) error { return sendOr404(ctx, SendFile(ctx, dir)) })
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			res, body := roundTrip(t, c, br, consts.MethodGet, "/dump")
			if res.StatusCode != 200 || res.ContentLength != int64(len(big)) || !bytes.Equal(body, big) {
				t.Fatalf("GET dump = %d, Content-Length %d, %d bytes; want 200, the file",
					res.StatusCode, res.ContentLength, len(body))
			}
			if cd := res.Header.Get(consts.HeaderContentDisposition); cd != "attachment; filename=db-backup.dump" {
				t.Errorf("Content-Disposition = %q, want the SendFileAs name", cd)
			}
			if lm := res.Header.Get(consts.HeaderLastModified); lm != modTime.Format(http.TimeFormat) {
				t.Errorf("Last-Modified = %q, want %q", lm, modTime.Format(http.TimeFormat))
			}

			if res, _ := roundTrip(t, c, br, consts.MethodGet, "/raw"); res.Header.Get(consts.HeaderContentDisposition) != "attachment; filename=tmp-83412.bin" {
				t.Errorf("SendFile Content-Disposition = %q, want the file's own name",
					res.Header.Get(consts.HeaderContentDisposition))
			}

			// A client with a current copy gets a 304 and no body.
			if _, err := fmt.Fprintf(c, "GET /dump HTTP/1.1\r\nHost: x\r\nIf-Modified-Since: %s\r\n\r\n",
				modTime.Format(http.TimeFormat)); err != nil {
				t.Fatal(err)
			}
			res, err := http.ReadResponse(br, &http.Request{Method: consts.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			if res.StatusCode != consts.StatusNotModified {
				t.Errorf("conditional GET = %d, want 304", res.StatusCode)
			}

			for _, path := range []string{"/missing", "/dir"} {
				if res, body := roundTrip(t, c, br, consts.MethodGet, path); res.StatusCode != 404 {
					t.Errorf("GET %s = %d %q, want 404", path, res.StatusCode, body)
				}
			}
		})
	}
}
