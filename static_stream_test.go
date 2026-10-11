package rweb

// Tests for streamed static files (response.bodyFile): StaticFiles attaches
// the opened file to the response instead of reading it into memory, and
// writeResponse copies it to the connection. They run over raw keep-alive
// connections on both transports, because the two take different copy paths
// — plain TCP hands the file to sendfile, TLS does a buffered copy — and a
// framing mistake (a Content-Length that disagrees with the bytes sent) only
// shows up as the *next* response on the connection parsing wrong.

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
	"testing"

	"github.com/rohanthewiz/rweb/consts"
)

// streamFixture makes a served directory holding big.bin (random bytes,
// larger than any copy buffer or socket buffer, so the copy takes many
// rounds) and small.txt, chdirs into its parent, and returns big.bin's
// contents.
func streamFixture(t *testing.T) (dir string, big []byte) {
	t.Helper()
	cwd := t.TempDir()
	dir = filepath.Join(cwd, "pub")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big = make([]byte, 3<<20+17) // odd size: no accidental buffer alignment
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("small-body"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir, big
}

// roundTrip writes one request on c and parses the response from br.
// http.ReadResponse reads exactly Content-Length body bytes (none for a
// HEAD), so a wrong length desynchronises the following roundTrip.
func roundTrip(t *testing.T, c io.Writer, br *bufio.Reader, method, path string) (*http.Response, []byte) {
	t.Helper()
	if _, err := fmt.Fprintf(c, "%s %s HTTP/1.1\r\nHost: x\r\n\r\n", method, path); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(br, &http.Request{Method: method})
	if err != nil {
		t.Fatalf("%s %s: reading response: %v", method, path, err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: reading body: %v", method, path, err)
	}
	return res, body
}

func TestStaticFileStreamsOverKeepAlive(t *testing.T) {
	_, big := streamFixture(t)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.StaticFiles("/pub/", "pub", 1)
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			// Two big files back to back, then a small one: every response
			// must start exactly where the previous body ended.
			for i := 0; i < 2; i++ {
				res, body := roundTrip(t, c, br, consts.MethodGet, "/pub/big.bin")
				if res.StatusCode != 200 || res.ContentLength != int64(len(big)) {
					t.Fatalf("GET big #%d = %d, Content-Length %d; want 200, %d",
						i, res.StatusCode, res.ContentLength, len(big))
				}
				if !bytes.Equal(body, big) {
					t.Fatalf("GET big #%d: body differs from the file (%d bytes)", i, len(body))
				}
			}
			if _, body := roundTrip(t, c, br, consts.MethodGet, "/pub/small.txt"); string(body) != "small-body" {
				t.Fatalf("GET small after big = %q", body)
			}
		})
	}
}

// The point of the change: the handler leaves the file pending rather than
// reading it, and the response closes it once it has been sent.
func TestStaticFileIsStreamedNotBuffered(t *testing.T) {
	_, big := streamFixture(t)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			files := make(chan *os.File, 1)
			s.Use(func(ctx Context) error {
				err := ctx.Next()
				if ctx.Request().Path() == "/pub/big.bin" {
					res := &ctx.(*context).response
					if len(res.body) != 0 || res.bodyFile == nil || res.bodyFileSize != int64(len(big)) {
						t.Errorf("after the static handler: %d body bytes, file %v, size %d; want 0, pending, %d",
							len(res.body), res.bodyFile != nil, res.bodyFileSize, len(big))
					}
					files <- res.bodyFile
				}
				return err
			})
			s.StaticFiles("/pub/", "pub", 1)
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			if _, body := roundTrip(t, c, br, consts.MethodGet, "/pub/big.bin"); !bytes.Equal(body, big) {
				t.Fatal("GET big: body differs from the file")
			}
			// The server handles a connection's requests in turn, so once
			// the next response arrives the first file has been released.
			roundTrip(t, c, br, consts.MethodGet, "/pub/small.txt")
			if f := <-files; f != nil {
				if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Errorf("body file still open after its response: Stat err = %v", err)
				}
			}
		})
	}
}

// HEAD reports the file's length, sends no body, and never reads the file.
func TestStaticFileHeadSendsLengthOnly(t *testing.T) {
	_, big := streamFixture(t)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.StaticFiles("/pub/", "pub", 1)
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			res, _ := roundTrip(t, c, br, consts.MethodHead, "/pub/big.bin")
			if res.StatusCode != 200 || res.Header.Get("Content-Length") != strconv.Itoa(len(big)) {
				t.Fatalf("HEAD big = %d, Content-Length %q; want 200, %d",
					res.StatusCode, res.Header.Get("Content-Length"), len(big))
			}
			// Had the file been sent after all, this would parse its bytes.
			if _, body := roundTrip(t, c, br, consts.MethodGet, "/pub/small.txt"); string(body) != "small-body" {
				t.Fatalf("GET after HEAD = %q", body)
			}
		})
	}
}

// Middleware that reads or rewrites the body after Next must see the file,
// exactly as when the handler read it into memory, and Content-Length must
// follow whatever the middleware left.
func TestStaticFileBodyVisibleToMiddleware(t *testing.T) {
	streamFixture(t)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.Use(func(ctx Context) error {
				if err := ctx.Next(); err != nil {
					return err
				}
				res := ctx.Response()
				switch ctx.Request().Path() {
				case "/pub/small.txt": // read, then replace
					res.SetBody(bytes.ToUpper(res.Body()))
				case "/x/small.txt": // append after the file
					return ctx.WriteString("+tail")
				}
				return nil
			})
			s.StaticFiles("/pub/", "pub", 1)
			s.StaticFiles("/x/", "pub", 1)
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			for path, want := range map[string]string{
				"/pub/small.txt": "SMALL-BODY",
				"/x/small.txt":   "small-body+tail",
			} {
				res, body := roundTrip(t, c, br, consts.MethodGet, path)
				if string(body) != want || res.ContentLength != int64(len(want)) {
					t.Errorf("GET %s = %q (Content-Length %d), want %q", path, body, res.ContentLength, want)
				}
			}
		})
	}
}

// A synthetic Request hands the response back in memory, so the file must
// end up in Body rather than be streamed to io.Discard.
func TestStaticFileSyntheticRequestHasBody(t *testing.T) {
	_, big := streamFixture(t)
	s := NewServer()
	s.StaticFiles("/pub/", "pub", 1)
	res := s.Request(consts.MethodGet, "/pub/big.bin", nil, nil)
	if res.Status() != 200 || !bytes.Equal(res.Body(), big) {
		t.Fatalf("synthetic GET big = %d, %d bytes; want 200, the file", res.Status(), len(res.Body()))
	}
}

// If the file shrinks between open and send, fewer bytes go out than the
// Content-Length promised. The connection must then be closed: left open,
// the client would wait forever for the rest, or read the next response as
// part of this body.
func TestStaticFileShortCopyClosesConn(t *testing.T) {
	dir, big := streamFixture(t)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			p := filepath.Join(dir, "shrinks.bin")
			if err := os.WriteFile(p, big, 0o644); err != nil {
				t.Fatal(err)
			}
			s := NewServer()
			s.Use(func(ctx Context) error {
				err := ctx.Next()
				// After the size went into the response, before the copy.
				if truncErr := os.Truncate(p, 1000); truncErr != nil {
					t.Error(truncErr)
				}
				return err
			})
			s.StaticFiles("/pub/", "pub", 1)
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			if _, err := fmt.Fprint(c, "GET /pub/shrinks.bin HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			res, err := http.ReadResponse(br, &http.Request{Method: consts.MethodGet})
			if err != nil {
				t.Fatal(err)
			}
			if res.ContentLength != int64(len(big)) {
				t.Fatalf("Content-Length = %d, want the size at open, %d", res.ContentLength, len(big))
			}
			// The body ends early with an error, not a hang (the dial
			// deadline would fail the test as a timeout instead).
			body, err := io.ReadAll(res.Body)
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("reading short body: %d bytes, err %v; want io.ErrUnexpectedEOF", len(body), err)
			}
			if len(body) != 1000 {
				t.Errorf("short body = %d bytes, want the 1000 left in the file", len(body))
			}
			// And nothing more arrives: the server closed the connection.
			if n, err := br.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
				t.Errorf("connection still open after a short body: read %d, err %v", n, err)
			}
		})
	}
}
