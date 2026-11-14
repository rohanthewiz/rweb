package rweb

// Tests for N-052: byte ranges, validators and conditional requests on
// files served from disk (StaticFiles, SendFile), StaticOptions (Cache-
// Control, Index, SPAFallback, a root mount), unknown extensions asked of
// the mime package, and download-name encoding.
//
// Ranges run over raw keep-alive connections on both transports, as in
// static_stream_test.go: plain TCP sends a span by sendfile from a seeked
// file, TLS by a buffered copy, and a span whose Content-Length disagrees
// with the bytes sent shows up as the next response parsing wrong.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// roundTripH is roundTrip with extra request header lines ("Key: value").
func roundTripH(t *testing.T, c io.Writer, br *bufio.Reader, method, path string, hdrs ...string) (*http.Response, []byte) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: x\r\n", method, path)
	for _, h := range hdrs {
		b.WriteString(h + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(c, b.String()); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(br, &http.Request{Method: method})
	if err != nil {
		t.Fatalf("%s %s %v: reading response: %v", method, path, hdrs, err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s %v: reading body: %v", method, path, hdrs, err)
	}
	return res, body
}

func TestParseByteRange(t *testing.T) {
	const size = 1000
	type want struct {
		start, length int64
		res           rangeResult
	}
	ok := func(a, n int64) want { return want{a, n, rangeSatisfiable} }
	whole := want{0, size, rangeIgnored}
	bad := want{0, size, rangeUnsatisfiable}

	for h, w := range map[string]want{
		"bytes=0-99":                   ok(0, 100),
		"bytes=100-":                   ok(100, 900),
		"bytes=-1":                     ok(999, 1),
		"bytes=-1000":                  ok(0, 1000),
		"bytes=-5000":                  ok(0, 1000), // suffix longer than the file: all of it
		"bytes=990-5000":               ok(990, 10), // last position clamped
		"bytes=999-999":                ok(999, 1),
		"BYTES=0-0":                    ok(0, 1), // the unit is case-insensitive
		" bytes = 5-9 ":                ok(5, 5),
		"bytes=0-99999999999999999999": ok(0, 1000), // overflow saturates

		"bytes=1000-":                 bad, // first position at the end
		"bytes=1000-2000":             bad,
		"bytes=-0":                    bad,
		"bytes=99999999999999999999-": bad,

		"bytes=0-1,5-6": whole, // a list: the whole file
		"items=0-1":     whole, // another unit
		"bytes=5-1":     whole, // last before first: a syntax error
		"bytes=":        whole,
		"bytes=-":       whole,
		"bytes=a-b":     whole,
		"bytes=+1-2":    whole,
		"bytes=1":       whole,
		"0-1":           whole,
	} {
		a, n, res := parseByteRange(h, size)
		if (want{a, n, res}) != w {
			t.Errorf("parseByteRange(%q) = %d, %d, %d; want %d, %d, %d", h, a, n, res, w.start, w.length, w.res)
		}
	}

	// An empty file ignores any range rather than answering 416.
	if _, _, res := parseByteRange("bytes=0-", 0); res != rangeIgnored {
		t.Errorf("empty file: %d, want rangeIgnored", res)
	}
}

func TestETagListMatches(t *testing.T) {
	const tag = `"5f-a"`
	for _, c := range []struct {
		header       string
		weak, strong bool // want, by comparison kind
	}{
		{`"5f-a"`, true, true},
		{`W/"5f-a"`, true, false},
		{`*`, true, true},
		{` "x", "5f-a" `, true, true},
		{`"x",W/"5f-a"`, true, false},
		{`"a,b", "5f-a"`, true, true}, // a comma inside an opaque tag
		{`"x"`, false, false},
		{`5f-a`, false, false}, // unquoted: malformed
		{`"5f-a`, false, false},
		{``, false, false},
	} {
		if got := etagListMatches(c.header, tag, false); got != c.weak {
			t.Errorf("weak %q = %v, want %v", c.header, got, c.weak)
		}
		if got := etagListMatches(c.header, tag, true); got != c.strong {
			t.Errorf("strong %q = %v, want %v", c.header, got, c.strong)
		}
	}
}

// Spans of a file larger than any copy buffer, back to back on one
// keep-alive connection, each followed by a whole response that must parse.
func TestStaticRangeRequests(t *testing.T) {
	_, big := streamFixture(t)
	n := len(big)
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			s := NewServer()
			s.StaticFiles("/pub/", "pub", 1)
			c := dialTest(t, serveTest(t, s, tr), tr)
			br := bufio.NewReader(c)

			for _, rc := range []struct {
				rng        string
				start, end int // inclusive
			}{
				{"bytes=0-99", 0, 99},
				{"bytes=12345-1061920", 12345, 1061920}, // a megabyte from inside the file
				{"bytes=" + strconv.Itoa(n-500) + "-", n - 500, n - 1},
				{"bytes=-777", n - 777, n - 1},
				{"bytes=" + strconv.Itoa(n-10) + "-" + strconv.Itoa(n+1000), n - 10, n - 1},
			} {
				res, body := roundTripH(t, c, br, consts.MethodGet, "/pub/big.bin", "Range: "+rc.rng)
				wantCR := fmt.Sprintf("bytes %d-%d/%d", rc.start, rc.end, n)
				if res.StatusCode != 206 || res.Header.Get("Content-Range") != wantCR {
					t.Fatalf("%s: %d, Content-Range %q; want 206, %q", rc.rng, res.StatusCode, res.Header.Get("Content-Range"), wantCR)
				}
				if !bytes.Equal(body, big[rc.start:rc.end+1]) || res.ContentLength != int64(len(body)) {
					t.Fatalf("%s: %d bytes (Content-Length %d), want big[%d:%d]", rc.rng, len(body), res.ContentLength, rc.start, rc.end+1)
				}
				if res.Header.Get("Accept-Ranges") != "bytes" || res.Header.Get("ETag") == "" {
					t.Errorf("%s: Accept-Ranges %q, ETag %q", rc.rng, res.Header.Get("Accept-Ranges"), res.Header.Get("ETag"))
				}
				if _, body := roundTrip(t, c, br, consts.MethodGet, "/pub/small.txt"); string(body) != "small-body" {
					t.Fatalf("after %s: GET small = %q", rc.rng, body)
				}
			}

			// Unsatisfiable: 416, the size, no body.
			for _, rng := range []string{"bytes=" + strconv.Itoa(n) + "-", "bytes=-0"} {
				res, body := roundTripH(t, c, br, consts.MethodGet, "/pub/big.bin", "Range: "+rng)
				if res.StatusCode != 416 || res.Header.Get("Content-Range") != "bytes */"+strconv.Itoa(n) || len(body) != 0 {
					t.Errorf("%s: %d, Content-Range %q, %d bytes; want 416, bytes */%d, none",
						rng, res.StatusCode, res.Header.Get("Content-Range"), len(body), n)
				}
			}

			// Ignored: the whole file with 200.
			for _, rng := range []string{"bytes=0-1,5-6", "items=0-1", "bytes=9-1"} {
				res, body := roundTripH(t, c, br, consts.MethodGet, "/pub/big.bin", "Range: "+rng)
				if res.StatusCode != 200 || !bytes.Equal(body, big) || res.Header.Get("Content-Range") != "" {
					t.Errorf("%s: %d, %d bytes, Content-Range %q; want 200, the file, none",
						rng, res.StatusCode, len(body), res.Header.Get("Content-Range"))
				}
			}

			// HEAD ignores Range and reports the whole length.
			res, _ := roundTripH(t, c, br, consts.MethodHead, "/pub/big.bin", "Range: bytes=0-9")
			if res.StatusCode != 200 || res.Header.Get("Content-Length") != strconv.Itoa(n) ||
				res.Header.Get("Accept-Ranges") != "bytes" {
				t.Errorf("HEAD with Range: %d, Content-Length %q, Accept-Ranges %q; want 200, %d, bytes",
					res.StatusCode, res.Header.Get("Content-Length"), res.Header.Get("Accept-Ranges"), n)
			}
			if _, body := roundTrip(t, c, br, consts.MethodGet, "/pub/small.txt"); string(body) != "small-body" {
				t.Fatalf("GET small after HEAD = %q", body)
			}
		})
	}
}

// A synthetic request reads the span into Body.
func TestStaticRangeSynthetic(t *testing.T) {
	_, big := streamFixture(t)
	s := NewServer()
	s.StaticFiles("/pub/", "pub", 1)
	res := s.Request(consts.MethodGet, "/pub/big.bin", []Header{{Key: "Range", Value: "bytes=10-19"}}, nil)
	if res.Status() != 206 || !bytes.Equal(res.Body(), big[10:20]) {
		t.Fatalf("synthetic range = %d, %q; want 206, big[10:20]", res.Status(), res.Body())
	}
}

// staticGet makes a synthetic GET (or other method) with header pairs.
func staticGet(s *Server, method, path string, kv ...string) Response {
	var hs []Header
	for i := 0; i+1 < len(kv); i += 2 {
		hs = append(hs, Header{Key: kv[i], Value: kv[i+1]})
	}
	return s.Request(method, path, hs, nil)
}

func TestStaticConditionalRequests(t *testing.T) {
	dir, _ := streamFixture(t)
	p := filepath.Join(dir, "small.txt")
	modTime := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := os.Chtimes(p, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	lastMod := modTime.Format(http.TimeFormat)
	before := modTime.Add(-time.Hour).Format(http.TimeFormat)
	after := modTime.Add(time.Hour).Format(http.TimeFormat)

	s := NewServer()
	s.StaticFiles("/pub/", "pub", 1)
	const path = "/pub/small.txt"

	res := staticGet(s, "GET", path)
	etag := res.Header("ETag")
	wantTag := fmt.Sprintf(`"%x-%x"`, modTime.UnixNano(), len("small-body"))
	if res.Status() != 200 || etag != wantTag || res.Header("Last-Modified") != lastMod ||
		res.Header("Accept-Ranges") != "bytes" {
		t.Fatalf("GET = %d, ETag %q, Last-Modified %q, Accept-Ranges %q; want 200, %s, %s, bytes",
			res.Status(), etag, res.Header("Last-Modified"), res.Header("Accept-Ranges"), wantTag, lastMod)
	}

	for _, c := range []struct {
		name   string
		method string
		kv     []string
		status int
	}{
		{"If-None-Match exact", "GET", []string{"If-None-Match", etag}, 304},
		{"If-None-Match weak form matches weakly", "GET", []string{"If-None-Match", "W/" + etag}, 304},
		{"If-None-Match in a list", "GET", []string{"If-None-Match", `"other", ` + etag}, 304},
		{"If-None-Match star", "GET", []string{"If-None-Match", "*"}, 304},
		{"If-None-Match other", "GET", []string{"If-None-Match", `"other"`}, 200},
		{"If-None-Match on HEAD", "HEAD", []string{"If-None-Match", etag}, 304},
		// If-None-Match takes precedence: a current date does not save a
		// changed tag.
		{"If-None-Match beats If-Modified-Since", "GET", []string{"If-None-Match", `"other"`, "If-Modified-Since", after}, 200},
		{"If-Modified-Since current", "GET", []string{"If-Modified-Since", lastMod}, 304},
		{"If-Modified-Since stale", "GET", []string{"If-Modified-Since", before}, 200},
		{"If-Match exact", "GET", []string{"If-Match", etag}, 200},
		{"If-Match other", "GET", []string{"If-Match", `"other"`}, 412},
		{"If-Match weak never matches", "GET", []string{"If-Match", "W/" + etag}, 412},
		{"If-Match star", "GET", []string{"If-Match", "*"}, 200},
		{"If-Unmodified-Since before", "GET", []string{"If-Unmodified-Since", before}, 412},
		{"If-Unmodified-Since current", "GET", []string{"If-Unmodified-Since", lastMod}, 200},
		{"If-Unmodified-Since bad date ignored", "GET", []string{"If-Unmodified-Since", "yesterday"}, 200},
		// If-Match is evaluated before If-None-Match.
		{"If-Match fails first", "GET", []string{"If-Match", `"other"`, "If-None-Match", etag}, 412},
	} {
		res := staticGet(s, c.method, path, c.kv...)
		if res.Status() != c.status {
			t.Errorf("%s: %d, want %d", c.name, res.Status(), c.status)
			continue
		}
		if c.status != 200 && len(res.Body()) != 0 {
			t.Errorf("%s: body %q, want none", c.name, res.Body())
		}
		if c.status == 304 && (res.Header("ETag") != etag || res.Header("Date") == "") {
			t.Errorf("%s: 304 with ETag %q, Date %q; want the tag and a date", c.name, res.Header("ETag"), res.Header("Date"))
		}
	}

	// If-Range: a current validator gets the span, a stale one the file.
	for _, c := range []struct {
		ifRange string
		status  int
	}{
		{etag, 206},
		{lastMod, 206},
		{`"stale"`, 200},
		{"W/" + etag, 200}, // weak: never satisfies If-Range
		{before, 200},
		{"garbage", 200},
	} {
		res := staticGet(s, "GET", path, "Range", "bytes=0-4", "If-Range", c.ifRange)
		wantBody := map[int]string{206: "small", 200: "small-body"}[c.status]
		if res.Status() != c.status || string(res.Body()) != wantBody {
			t.Errorf("If-Range %q: %d %q, want %d %q", c.ifRange, res.Status(), res.Body(), c.status, wantBody)
		}
	}

	// A change to the file changes the tag, so the old one no longer
	// revalidates.
	if err := os.WriteFile(p, []byte("small-BODY"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := staticGet(s, "GET", path, "If-None-Match", etag); res.Status() != 200 || string(res.Body()) != "small-BODY" {
		t.Errorf("after the file changed: %d %q, want 200 and the new body", res.Status(), res.Body())
	}
}

func TestStaticCacheControlOption(t *testing.T) {
	dir, _ := streamFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "a.zip"), []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	const cc = "public, max-age=31536000, immutable"
	s := NewServer()
	s.StaticFilesWithOptions("/pub/", "pub", 1, StaticOptions{CacheControl: cc})
	s.StaticFiles("/plain/", "pub", 1)

	res := staticGet(s, "GET", "/pub/small.txt")
	etag := res.Header("ETag")
	for name, r := range map[string]Response{
		"200": res,
		"206": staticGet(s, "GET", "/pub/small.txt", "Range", "bytes=0-1"),
		"304": staticGet(s, "GET", "/pub/small.txt", "If-None-Match", etag),
		// A download type's "must-revalidate" gives way to the option.
		"zip": staticGet(s, "GET", "/pub/a.zip"),
	} {
		if got := r.Header("Cache-Control"); got != cc {
			t.Errorf("%s: Cache-Control %q, want %q", name, got, cc)
		}
	}
	if got := staticGet(s, "GET", "/plain/small.txt").Header("Cache-Control"); got != "" {
		t.Errorf("without the option: Cache-Control %q, want none", got)
	}
}

// siteFixture makes a served tree for the Index and SPA tests:
//
//	site/index.html          "root index"
//	site/docs/index.html     "docs index"
//	site/docs/guide.html     "guide"
//	site/empty/              (no index)
func siteFixture(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	site := filepath.Join(cwd, "site")
	for _, d := range []string{"docs", "empty"} {
		if err := os.MkdirAll(filepath.Join(site, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"index.html":      "root index",
		"docs/index.html": "docs index",
		"docs/guide.html": "guide",
	} {
		if err := os.WriteFile(filepath.Join(site, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return site
}

// expectGet checks a synthetic GET's status and body.
func expectGet(t *testing.T, s *Server, path string, status int, body string) Response {
	t.Helper()
	res := s.Request(consts.MethodGet, path, nil, nil)
	if res.Status() != status || string(res.Body()) != body {
		t.Errorf("GET %s = %d %q, want %d %q", path, res.Status(), res.Body(), status, body)
	}
	return res
}

func TestStaticIndexOption(t *testing.T) {
	siteFixture(t)
	for _, contain := range []bool{false, true} {
		t.Run(fmt.Sprintf("contain=%v", contain), func(t *testing.T) {
			s := NewServer(ServerOptions{StaticContainSymlinks: contain})
			s.StaticFilesWithOptions("/site/", "site", 1, StaticOptions{Index: "index.html"})
			s.StaticFiles("/plain/", "site", 1)

			res := expectGet(t, s, "/site/docs", 200, "docs index")
			if ct := res.Header("Content-Type"); ct != consts.MIMEHTMLUTF8 {
				t.Errorf("index Content-Type %q, want %q (from the index file's name)", ct, consts.MIMEHTMLUTF8)
			}
			expectGet(t, s, "/site/docs/", 200, "docs index")
			expectGet(t, s, "/site/docs/guide.html", 200, "guide")
			expectGet(t, s, "/site/", 200, "root index") // the prefix route
			expectGet(t, s, "/site", 200, "root index")
			expectGet(t, s, "/site/empty", 404, "") // a directory without an index
			expectGet(t, s, "/site/nope.html", 404, "")

			// Without the option a directory stays a 404, and the prefix is
			// not routed.
			expectGet(t, s, "/plain/docs", 404, "")
			expectGet(t, s, "/plain/", 404, "")
		})
	}
}

func TestStaticOptionsRefusedAtRegistration(t *testing.T) {
	siteFixture(t)
	s := NewServer()
	s.StaticFilesWithOptions("/a/", "site", 1, StaticOptions{Index: "docs/index.html"})
	s.StaticFilesWithOptions("/b/", "site", 1, StaticOptions{Index: ".."})
	s.StaticFilesWithOptions("/c/", "site", 1, StaticOptions{SPAFallback: "../index.html"})
	s.StaticFilesWithOptions("/d/", "site", 1, StaticOptions{SPAFallback: "/etc/passwd"})
	for _, p := range []string{"/a/docs/guide.html", "/b/docs/guide.html", "/c/docs/guide.html", "/d/docs/guide.html"} {
		expectGet(t, s, p, 404, "") // the route was not registered at all
	}
}

func TestStaticSPAFallback(t *testing.T) {
	site := siteFixture(t)
	for _, contain := range []bool{false, true} {
		t.Run(fmt.Sprintf("contain=%v", contain), func(t *testing.T) {
			s := NewServer(ServerOptions{StaticContainSymlinks: contain})
			s.StaticFilesWithOptions("/app/", "site", 1, StaticOptions{SPAFallback: "index.html", CacheControl: "no-cache"})
			s.StaticFilesWithOptions("/nested/", "site", 1, StaticOptions{SPAFallback: "docs/guide.html"})
			s.StaticFilesWithOptions("/broken/", "site", 1, StaticOptions{SPAFallback: "missing.html"})

			res := expectGet(t, s, "/app/users/42", 200, "root index")
			if res.Header("Content-Type") != consts.MIMEHTMLUTF8 || res.Header("Cache-Control") != "no-cache" {
				t.Errorf("fallback headers: Content-Type %q, Cache-Control %q", res.Header("Content-Type"), res.Header("Cache-Control"))
			}
			expectGet(t, s, "/app/docs/guide.html", 200, "guide") // a real file wins
			expectGet(t, s, "/app/empty", 200, "root index")      // a directory with no index
			expectGet(t, s, "/app/", 200, "root index")           // the prefix route
			expectGet(t, s, "/app", 200, "root index")
			expectGet(t, s, "/nested/x", 200, "guide")
			expectGet(t, s, "/broken/x", 404, "")

			// Refusals stay 404s, not the fallback.
			expectGet(t, s, "/app/%2e%2e/secret", 404, "")
			expectGet(t, s, "/app/%zz", 404, "")

			// The fallback answers conditional requests with its own tag.
			etag := s.Request(consts.MethodGet, "/app/a", nil, nil).Header("ETag")
			if r := staticGet(s, "GET", "/app/b", "If-None-Match", etag); r.Status() != 304 {
				t.Errorf("fallback revalidation = %d, want 304", r.Status())
			}
		})
	}

	// A link out of the root is refused under StaticContainSymlinks: 404,
	// not the fallback.
	if runtime.GOOS == "windows" {
		return
	}
	outside := filepath.Join(filepath.Dir(site), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(site, "leak.txt")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	s := NewServer(ServerOptions{StaticContainSymlinks: true})
	s.StaticFilesWithOptions("/app/", "site", 1, StaticOptions{SPAFallback: "index.html"})
	expectGet(t, s, "/app/leak.txt", 404, "")
}

// "/" mounts a directory at the site root; other routes keep their paths.
func TestStaticRootMount(t *testing.T) {
	siteFixture(t)
	s := NewServer()
	s.Get("/api/ping", func(ctx Context) error { return ctx.WriteString("pong") })
	s.StaticFilesWithOptions("/", "site", 0, StaticOptions{Index: "index.html", SPAFallback: "index.html"})

	expectGet(t, s, "/", 200, "root index")
	expectGet(t, s, "/docs/guide.html", 200, "guide")
	expectGet(t, s, "/docs", 200, "docs index")
	expectGet(t, s, "/some/client/route", 200, "root index")
	expectGet(t, s, "/api/ping", 200, "pong")

	// Plain StaticFiles at "/" works too (it used to be refused).
	s2 := NewServer()
	s2.StaticFiles("/", "site", 0)
	expectGet(t, s2, "/docs/guide.html", 200, "guide")
	expectGet(t, s2, "/nope", 404, "")
}

func TestGroupStaticFilesWithOptions(t *testing.T) {
	siteFixture(t)
	s := NewServer()
	g := s.Group("/g")
	var hits int
	g.Use(func(ctx Context) error { hits++; return ctx.Next() })
	g.StaticFilesWithOptions("/site/", "site", 2, StaticOptions{Index: "index.html"})
	abs, err := filepath.Abs("site")
	if err != nil {
		t.Fatal(err)
	}
	g.StaticFilesAbsWithOptions("/abs/", abs, 2, StaticOptions{SPAFallback: "index.html"})

	expectGet(t, s, "/g/site/docs", 200, "docs index")
	expectGet(t, s, "/g/site", 200, "root index")
	expectGet(t, s, "/g/abs/anything", 200, "root index")
	if hits != 3 {
		t.Errorf("group middleware ran %d times, want 3", hits)
	}
}

// SendFile shares the range and validator handling, and encodes download
// names for both headers.
func TestSendFileRangeAndNames(t *testing.T) {
	dir := t.TempDir()
	data := []byte("0123456789abcdefghij")
	p := filepath.Join(dir, "x.zip")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.Get("/plain", func(ctx Context) error { return SendFileAs(ctx, p, "Q3 report.zip") })
	s.Get("/intl", func(ctx Context) error { return SendFileAs(ctx, p, "café ✓.zip") })
	s.Get("/quote", func(ctx Context) error { return SendFileAs(ctx, p, `a"b+c.zip`) })

	res := staticGet(s, "GET", "/plain", "Range", "bytes=10-")
	if res.Status() != 206 || string(res.Body()) != "abcdefghij" || res.Header("Content-Range") != "bytes 10-19/20" {
		t.Fatalf("SendFile range = %d %q %q", res.Status(), res.Body(), res.Header("Content-Range"))
	}
	if r := staticGet(s, "GET", "/plain", "If-None-Match", res.Header("ETag")); r.Status() != 304 {
		t.Errorf("SendFile If-None-Match = %d, want 304", r.Status())
	}

	for path, c := range map[string]struct{ disposition, xname, decoded string }{
		"/plain": {`attachment; filename="Q3 report.zip"`, "Q3%20report.zip", "Q3 report.zip"},
		"/intl": {`attachment; filename="caf_ _.zip"; filename*=utf-8''caf%C3%A9%20%E2%9C%93.zip`,
			"caf%C3%A9%20%E2%9C%93.zip", "café ✓.zip"},
		"/quote": {`attachment; filename="a\"b+c.zip"`, "a%22b%2Bc.zip", `a"b+c.zip`},
	} {
		res := s.Request(consts.MethodGet, path, nil, nil)
		cd := res.Header("Content-Disposition")
		if cd != c.disposition {
			t.Errorf("%s: Content-Disposition %q, want %q", path, cd, c.disposition)
		}
		if _, params, err := mime.ParseMediaType(cd); err != nil || params["filename"] != c.decoded {
			t.Errorf("%s: parsed filename %q (%v), want %q", path, params["filename"], err, c.decoded)
		}
		if x := res.Header("x-filename"); x != c.xname {
			t.Errorf("%s: x-filename %q, want %q", path, x, c.xname)
		}
	}
}

// An extension missing from the built-in switch is looked up with
// mime.TypeByExtension before falling back to a forced download.
func TestFileTypeFromMimePackage(t *testing.T) {
	if err := mime.AddExtensionType(".rwebtest", "application/x-rweb-test"); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.Get("/known", func(ctx Context) error { return File(ctx, "a.rwebtest", []byte("x")) })
	s.Get("/unknown", func(ctx Context) error { return File(ctx, "a.rwebnope", []byte("x")) })

	res := s.Request(consts.MethodGet, "/known", nil, nil)
	if res.Header("Content-Type") != "application/x-rweb-test" || res.Header("Content-Disposition") != "" {
		t.Errorf("known to mime: Content-Type %q, Content-Disposition %q; want the type, inline",
			res.Header("Content-Type"), res.Header("Content-Disposition"))
	}
	res = s.Request(consts.MethodGet, "/unknown", nil, nil)
	if res.Header("Content-Type") != consts.MIMEOctetStream || res.Header("Content-Disposition") == "" {
		t.Errorf("unknown: Content-Type %q, Content-Disposition %q; want octet-stream, a download",
			res.Header("Content-Type"), res.Header("Content-Disposition"))
	}
}
