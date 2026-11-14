package rweb

// Serving a file from disk: validators (ETag, Last-Modified), conditional
// requests (304 / 412) and single byte ranges (206 / 416). SendFile,
// SendFileAs and every StaticFiles route go through serveFile, so the two
// paths cannot drift apart on caching or range behaviour.
//
// One request, in the order RFC 9110 §13.2.2 evaluates it:
//
//	open file, Stat ──► ETag "<mtime ns hex>-<size hex>", Last-Modified
//	      │
//	      ▼
//	If-Match / If-Unmodified-Since  ── fails ──► 412, no body
//	      │
//	      ▼
//	If-None-Match / If-Modified-Since ─ unchanged ─► 304 (GET/HEAD), 412 (others)
//	      │
//	      ▼
//	GET with Range? ── If-Range stale ──► ignore Range ─┐
//	      │                                             │
//	      ▼                                             │
//	parse "bytes=a-b" ── unsatisfiable ──► 416 + Content-Range: bytes */size
//	      │      └── absent, other unit, list, bad ─────┤
//	      ▼                                             ▼
//	206: seek to a, stream b-a+1 bytes        200: stream the whole file
//	     Content-Range: bytes a-b/size
//
// Both bodies leave as a sized body stream (see Stream), so a span is sent
// by sendfile(2) on plain TCP exactly as a whole file is: the file is
// seeked to the span's start, and writeBodyStream copies `length` bytes
// from the file's current offset.

import (
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// StaticOptions tunes a static file route registered with
// StaticFilesWithOptions or StaticFilesAbsWithOptions (on a Server or a
// Group). The zero value behaves exactly as StaticFiles.
type StaticOptions struct {
	// CacheControl, when not empty, is sent as the Cache-Control header of
	// every response the route makes from a file: 200, 206 and 304 (a 304
	// must repeat it, RFC 9110 §15.4.5). For fingerprinted assets
	// ("app.3f9a1c.js") "public, max-age=31536000, immutable" is usual; for
	// pages, "no-cache" makes the browser revalidate (cheaply, by ETag) on
	// every use. It replaces the "must-revalidate" that download types
	// otherwise carry.
	CacheControl string

	// Index names the file served for a request that names a directory,
	// "index.html" typically. It must be a plain file name, without a path
	// separator. With Index (or SPAFallback) set, the route's own prefix is
	// registered too, so "/docs/" (and "/docs") serves the top directory's
	// index; a route of your own at exactly that path, registered after
	// this one, replaces it.
	//
	// There is no redirect from "/docs/guide" to "/docs/guide/", as
	// net/http's FileServer does: rweb drops trailing slashes from request
	// paths unless URLOptions.KeepTrailingSlashes is set, so the two cannot
	// be told apart and the redirect would loop. Links inside an index page
	// should be root-relative ("/docs/guide/intro.html"), or be visited with
	// the trailing slash, for the browser to resolve them correctly.
	Index string

	// SPAFallback names a file, relative to the directory the route serves
	// (the one its URL prefix maps to), sent with status 200 in place of a
	// 404 whenever a request names no file: a missing path, or a directory
	// with no Index file. That is what a single-page app needs for its
	// client-side routes ("/app/users/42" gets index.html, whose script then
	// reads the URL). Requests refused for safety (a ".." segment, a bad
	// escape, a link out of the root under StaticContainSymlinks) still get
	// 404.
	//
	// Every miss gets the fallback, a missing "/app/logo.png" included, as
	// with Caddy's try_files or Netlify's "/* /index.html 200". To keep real
	// 404s for assets, serve them from their own route (say "/app/assets/",
	// with a long CacheControl), which takes precedence for its paths, and
	// give the SPA route a "no-cache" CacheControl so the shell page is
	// revalidated on each visit.
	SPAFallback string
}

// fileETag is the entity tag of a file: its modification time in
// nanoseconds and its size, both in hex, quoted. nginx and Apache derive
// theirs the same way. Hashing the content would be exact but costs a full
// read of the file on every request (or a cache to invalidate); this costs
// nothing beyond the Stat already made.
//
// The tag is strong, not weak (W/"…"). A weak tag can never satisfy
// If-Range (RFC 9110 §13.1.5 requires strong comparison there), so a
// download resumed by a client that kept our ETag would restart from byte
// zero every time, and a cache could not combine ranges. The cost of strong:
// two different contents with the same size and the same mtime, to the
// nanosecond the filesystem keeps, would share a tag. That happens when a
// build stamps every file with a fixed time (reproducible builds,
// SOURCE_DATE_EPOCH) and a file keeps its size across releases; then
// Last-Modified is equally blind, and fingerprinted file names or
// "Cache-Control: no-store" are the fix.
func fileETag(modTime time.Time, size int64) string {
	b := make([]byte, 0, 40)
	b = append(b, '"')
	b = strconv.AppendInt(b, modTime.UnixNano(), 16)
	b = append(b, '-')
	b = strconv.AppendInt(b, size, 16)
	b = append(b, '"')
	return string(b)
}

// serveFile sends the open file f, which info describes, as the response:
// a 200 with the whole file, a 206 with one span of it, or a bodiless 304,
// 412 or 416 (see the diagram at the top of this file). name picks the
// Content-Type and, for download types, the offered file name.
// cacheControl, when not empty, becomes the Cache-Control header.
//
// The response owns f from here on: it is either handed to Stream, which
// closes it once sent, or closed before returning.
func serveFile(ctx Context, f *os.File, info os.FileInfo, name, cacheControl string) error {
	res := ctx.Response()
	req := ctx.Request()
	modTime, size := info.ModTime(), info.Size()
	etag := fileETag(modTime, size)

	switch checkPreconditions(ctx, etag, modTime) {
	case consts.StatusNotModified:
		_ = f.Close()
		// A 304 carries what a cache needs to refresh its stored copy
		// (RFC 9110 §15.4.5): Date, ETag, and Cache-Control when the 200
		// would have had one. Last-Modified is left out: with an ETag
		// present it adds nothing, and the RFC asks for no other metadata.
		res.SetHeader(consts.HeaderDate, time.Now().UTC().Format(http.TimeFormat))
		res.SetHeader(consts.HeaderETag, etag)
		if cacheControl != "" {
			res.SetHeader(consts.HeaderCacheControl, cacheControl)
		}
		ctx.SetStatus(consts.StatusNotModified)
		return nil
	case consts.StatusPreconditionFailed:
		_ = f.Close()
		ctx.SetStatus(consts.StatusPreconditionFailed)
		return nil
	}

	// Range is defined for GET alone (RFC 9110 §14.2); a HEAD, routed to
	// the same handler, gets the 200 headers whatever it sends. If-Range
	// is checked first: a client whose copy is stale gets the whole file,
	// not a span of the new one to splice onto its old bytes.
	start, length, ranged := int64(0), size, rangeIgnored
	if req.Method() == consts.MethodGet {
		if rh := req.Header(consts.HeaderRange); rh != "" &&
			ifRangeHolds(req.Header(consts.HeaderIfRange), etag, modTime) {
			start, length, ranged = parseByteRange(rh, size)
		}
	}
	if ranged == rangeUnsatisfiable {
		_ = f.Close()
		// The size lets the client form a satisfiable request (RFC 9110
		// §15.5.17). No body: writeResponse sends Content-Length: 0.
		res.SetHeader(consts.HeaderContentRange, "bytes */"+strconv.FormatInt(size, 10))
		res.SetHeader(consts.HeaderAcceptRanges, "bytes")
		ctx.SetStatus(consts.StatusRangeNotSatisfiable)
		return nil
	}

	setFileHeaders(ctx, name, modTime)
	res.SetHeader(consts.HeaderETag, etag)
	// Advertised on every file response, 200 included, so that a media
	// player or download manager knows it may seek or resume.
	res.SetHeader(consts.HeaderAcceptRanges, "bytes")
	if cacheControl != "" {
		res.SetHeader(consts.HeaderCacheControl, cacheControl)
	}

	if ranged == rangeSatisfiable {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			_ = f.Close()
			return err
		}
		res.SetHeader(consts.HeaderContentRange, "bytes "+strconv.FormatInt(start, 10)+"-"+
			strconv.FormatInt(start+length-1, 10)+"/"+strconv.FormatInt(size, 10))
		ctx.SetStatus(consts.StatusPartialContent)
	}
	return Stream(ctx, f, length)
}

// checkPreconditions evaluates the request's conditional headers against
// the file's validators, in the order and with the precedence of RFC 9110
// §13.2.2, and returns the status that ends the request there: 412, 304,
// or 0 to go on and send the file.
//
//   - If-Match (strong comparison) or, only when it is absent,
//     If-Unmodified-Since: a failed condition is 412. These guard
//     state-changing requests; on a GET they are rare but cheap to honour.
//   - If-None-Match (weak comparison) or, only when it is absent,
//     If-Modified-Since: an unchanged file is 304 for GET and HEAD. For
//     other methods a matching If-None-Match is 412, and If-Modified-Since
//     does not apply.
//
// Dates compare at one-second grain, the resolution of an HTTP date.
func checkPreconditions(ctx Context, etag string, modTime time.Time) int {
	req := ctx.Request()
	if im := req.Header(consts.HeaderIfMatch); im != "" {
		if !etagListMatches(im, etag, true) {
			return consts.StatusPreconditionFailed
		}
	} else if ius := req.Header(consts.HeaderIfUnmodifiedSince); ius != "" {
		// An unparseable date is ignored, as RFC 9110 §13.1.4 directs.
		if t, err := http.ParseTime(ius); err == nil && modTime.Truncate(time.Second).After(t) {
			return consts.StatusPreconditionFailed
		}
	}

	method := req.Method()
	getOrHead := method == consts.MethodGet || method == consts.MethodHead
	if inm := req.Header(consts.HeaderIfNoneMatch); inm != "" {
		if etagListMatches(inm, etag, false) {
			if getOrHead {
				return consts.StatusNotModified
			}
			return consts.StatusPreconditionFailed
		}
	} else if getOrHead && notModifiedSince(ctx, modTime) {
		return consts.StatusNotModified
	}
	return 0
}

// etagListMatches reports whether the If-Match / If-None-Match value
// header lists etag, or is "*" (any current representation, and a file
// that was opened has one). strong selects strong comparison (If-Match): a
// weak tag in the list never matches. Otherwise (If-None-Match) W/"x"
// matches "x".
//
// The value is a comma-separated list of entity tags, each `"opaque"` or
// `W/"opaque"`, where the opaque part may itself hold commas or spaces, so
// it is scanned tag by tag rather than split on commas. A malformed list
// stops the scan and counts as no match: for If-None-Match that sends the
// file, for If-Match it is a 412, both the safe outcome.
func etagListMatches(header, etag string, strong bool) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for {
		header = strings.TrimLeft(header, " \t,")
		if header == "" {
			return false
		}
		weak := strings.HasPrefix(header, "W/")
		if weak {
			header = header[2:]
		}
		if header == "" || header[0] != '"' {
			return false
		}
		end := strings.IndexByte(header[1:], '"')
		if end < 0 {
			return false
		}
		tag := header[:end+2] // with its quotes, as etag is
		header = header[end+2:]
		if tag == etag && !(strong && weak) {
			return true
		}
	}
}

// ifRangeHolds reports whether a Range request may be answered with a
// span: true when there is no If-Range, or when its validator still
// describes the file. An entity tag must match strongly, so a weak one
// never does; a date must equal Last-Modified exactly (RFC 9110 §13.1.5).
// When it does not hold, the Range is ignored and the whole file is sent.
func ifRangeHolds(ir, etag string, modTime time.Time) bool {
	ir = strings.TrimSpace(ir)
	if ir == "" {
		return true
	}
	if ir[0] == '"' || strings.HasPrefix(ir, "W/") {
		return ir == etag
	}
	t, err := http.ParseTime(ir)
	return err == nil && modTime.Truncate(time.Second).Equal(t)
}

// rangeResult is what parseByteRange makes of a Range header.
type rangeResult int

const (
	rangeIgnored       rangeResult = iota // send the whole file, 200
	rangeSatisfiable                      // send the span, 206
	rangeUnsatisfiable                    // 416
)

// parseByteRange reads a Range header for a file of size bytes and returns
// the span to send as its first byte and length.
//
// One range is served: "bytes=a-b", "bytes=a-" (a to the end) or
// "bytes=-n" (the last n bytes). A last position past the end is clamped
// to it. A first position at or past the end, or a suffix of zero, is
// unsatisfiable (416).
//
// Everything else is ignored and the whole file is sent with 200, which
// RFC 9110 §14.2 allows a server to do with any Range: another unit, a
// syntax error, a last position before the first, and a list of ranges.
// A list would need a multipart/byteranges body; browsers and media
// players ask for one range, and a client that asks for several can take
// the whole file.
//
// An empty file ignores the Range too, as net/http does: some clients add
// "Range: bytes=0-" to every request, and a 416 for an empty file would
// fail them where the plain empty 200 succeeds.
func parseByteRange(h string, size int64) (start, length int64, res rangeResult) {
	unit, set, ok := strings.Cut(h, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return 0, size, rangeIgnored
	}
	set = strings.TrimSpace(set)
	if strings.IndexByte(set, ',') >= 0 || size == 0 {
		return 0, size, rangeIgnored
	}
	first, last, ok := strings.Cut(set, "-")
	if !ok {
		return 0, size, rangeIgnored
	}
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)

	if first == "" { // suffix: the last n bytes
		n, ok := parseRangePos(last)
		if !ok {
			return 0, size, rangeIgnored
		}
		if n == 0 {
			return 0, size, rangeUnsatisfiable
		}
		n = min(n, size)
		return size - n, n, rangeSatisfiable
	}

	a, ok := parseRangePos(first)
	if !ok {
		return 0, size, rangeIgnored
	}
	b := size - 1
	if last != "" {
		v, ok := parseRangePos(last)
		if !ok || v < a {
			return 0, size, rangeIgnored // a syntax error, not a 416
		}
		b = min(v, b)
	}
	if a >= size {
		return 0, size, rangeUnsatisfiable
	}
	return a, b - a + 1, rangeSatisfiable
}

// parseRangePos parses a byte position: ASCII digits only, no sign or
// space (strconv.ParseInt alone would take "+5"). A position too large for
// an int64 is still a position, past the end of any file, so it saturates
// rather than failing: "bytes=99999999999999999999-" is then a 416 and
// "bytes=0-99999999999999999999" the whole file, as their meaning says.
func parseRangePos(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return math.MaxInt64, true
		}
		return 0, false
	}
	return v, true
}

// contentDisposition builds the Content-Disposition value offering name as
// a download.
//
// An ASCII name goes through mime.FormatMediaType, which quotes it when it
// holds spaces or separators and escapes quotes and backslashes:
// `attachment; filename="Q3 report.pdf"`. It used to be passed through
// url.QueryEscape, which no browser decodes there, so a space was saved
// as "+".
//
// A name with other characters is sent twice (RFC 6266 §4.3): filename*
// in RFC 8187 form (utf-8 and percent-encoded, which is what
// FormatMediaType emits for such a value), which every current browser
// prefers, and before it a plain filename with each non-ASCII or control
// character replaced by "_", for clients that only read that one.
func contentDisposition(name string) string {
	enc := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if enc == "" { // only for an invalid type or parameter name; not expected
		return "attachment"
	}
	if !strings.Contains(enc, "filename*=") {
		return enc
	}
	// Per rune, so "é" becomes one "_", not one per UTF-8 byte; an invalid
	// byte comes through strings.Map as U+FFFD and is replaced likewise.
	fallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r >= 0x7f {
			return '_'
		}
		return r
	}, name)
	plain := mime.FormatMediaType("attachment", map[string]string{"filename": fallback})
	return plain + strings.TrimPrefix(enc, "attachment")
}

// headerFileName encodes name for the x-filename header, which a page's
// script reads (it is listed in Access-Control-Expose-Headers) to name a
// download it fetched. The form is encodeURIComponent's, so
// decodeURIComponent in the browser and url.QueryUnescape in Go both give
// the name back: url.QueryEscape alone writes a space as "+", which
// decodeURIComponent keeps, and it escapes a literal "+" as "%2B", so
// turning its "+" into "%20" is exact.
func headerFileName(name string) string {
	return strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
}
