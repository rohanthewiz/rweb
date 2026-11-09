package rweb

import (
	"bufio"
	"errors"
	"io"
	"math"
	"slices"
	"strings"

	"github.com/rohanthewiz/rweb/consts"
)

// Request framing: where a request's body ends (RFC 9112 §6).
//
// rweb is often deployed behind a proxy, and the proxy and rweb each decide
// independently where a request ends. If they can disagree, a client can
// make the bytes the proxy took for the tail of one body start a second
// request for rweb, one the proxy never inspected (request smuggling). So
// the rules here refuse every message whose length two reasonable parsers
// could read differently, rather than picking one reading:
//
//	Content-Length and Transfer-Encoding together     400  (RFC 9112 §6.1)
//	Content-Length not all digits, or two that differ 400  (§6.3)
//	Transfer-Encoding without chunked last            400  (§6.3)
//	Transfer-Encoding with another coding too         501  (§6.1)
//	header name not a token ("Name :" included)       400  (§5.1)
//	obsolete line folding (line starts with SP/HT)    400  (§5.2)
//	header line with no colon                         400
//	chunk data not followed by CRLF                   400  (§7.1)
//
// Each is answered and the connection closed: after a framing error the
// position of the next request is unknown.

// parseContentLength parses a Content-Length value: one or more ASCII digits
// and nothing else. strconv.ParseInt alone would also take "+5" and "-5";
// a list such as "5, 5" is refused rather than collapsed, since a parser
// that keeps the first and one that keeps the last must not both be served.
func parseContentLength(v string) (int64, bool) {
	if v == "" || len(v) > 19 { // 19 digits is the most an int64 holds
		return 0, false
	}
	var n int64
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int64(c - '0')
		if n > (math.MaxInt64-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

// transferEncodingReply returns the response for a request's
// Transfer-Encoding value, or "" when it is exactly "chunked" (in any case),
// the only coding rweb decodes.
//
// RFC 9112 §6.3 makes "chunked not the final coding" a 400 that MUST close
// the connection, because the body's length then cannot be known; a list
// that ends in chunked but also names a coding rweb does not implement
// (gzip, chunked) has a known length but cannot be decoded, which is 501.
func transferEncodingReply(v string) string {
	last := v
	if i := strings.LastIndexByte(v, ','); i >= 0 {
		last = v[i+1:]
	}
	if !strings.EqualFold(strings.TrimSpace(last), "chunked") {
		return consts.HTTPBadRequest
	}
	if strings.IndexByte(v, ',') >= 0 {
		return consts.HTTPNotImplemented
	}
	return ""
}

// errBadChunk reports a malformed chunked body; it is answered 400.
var errBadChunk = errors.New("rweb: malformed chunked request body")

// errChunkTooLarge reports a chunked body over the size cap; it is answered
// 413.
var errChunkTooLarge = errors.New("rweb: chunked request body too large")

// parseChunkSizeLine returns the size from a chunk-size line:
//
//	chunk-size [ BWS ";" chunk-ext ] CRLF
//
// The extension, if any, is ignored (no extension has a defined meaning).
// The size must be 1 to 16 hex digits and nothing else: strconv.ParseInt
// would also take a sign, and TrimSpace before it would take leading
// whitespace, both of which another parser may read differently.
func parseChunkSizeLine(line string) (int64, error) {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimRight(line, " \t") // BWS before ";"
	if line == "" || len(line) > 16 {
		return 0, errBadChunk
	}
	var n uint64
	for i := 0; i < len(line); i++ {
		c := line[i]
		var d byte
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, errBadChunk
		}
		n = n<<4 | uint64(d)
	}
	if n > math.MaxInt64 {
		return 0, errBadChunk
	}
	return int64(n), nil
}

// chunkExcessMax is how far chunk framing may run ahead of chunk data. See
// readChunkedBody.
const chunkExcessMax = 16 << 10

// readChunkedBody reads a chunked body (RFC 9112 §7.1) off r and appends it
// to *body:
//
//	chunk-size [ chunk-ext ] CRLF   chunk-data CRLF   ...   "0" [ chunk-ext ] CRLF
//	trailer-field CRLF ...   CRLF
//
// lr reads the size and trailer lines: each size line within chunkLineMax,
// the trailer section within maxTrailer (the head's MaxHeaderBytes, since
// trailer fields are header fields; they are read and discarded). maxBody is
// the resolved body cap, <= 0 for none.
//
// Extensions are allowed but bounded by an overhead allowance taken from
// net/http's chunked reader: framing bytes accrue as excess, each chunk
// earns back 16 bytes plus twice its data, and excess past chunkExcessMax is
// refused. One-byte chunks ("1\r\nX\r\n", 5 bytes of framing per data byte)
// stay within it; a long extension on every small chunk does not, so the
// cap on body size cannot be dodged by sending framing instead of data.
//
// Errors: errBadChunk (400), errChunkTooLarge (413), errHeaderTooLarge from
// the trailers (431), or the connection's own error (a deadline, EOF).
func readChunkedBody(r *bufio.Reader, lr *lineReader, body *[]byte, maxBody int64, maxTrailer int) error {
	var excess int64
	var crlf [2]byte
	for {
		lr.reset(chunkLineMax)
		line, err := lr.readLine()
		if err != nil {
			if errors.Is(err, errHeaderTooLarge) {
				return errBadChunk // a size line no client would send
			}
			return err
		}
		size, err := parseChunkSizeLine(line)
		if err != nil {
			return err
		}

		excess += int64(len(line)) + 2            // the size line, and the CRLF after the data
		excess -= 16 + 2*min(size, math.MaxInt32) // min: 2*size must not overflow
		if excess < 0 {
			excess = 0
		}
		if excess > chunkExcessMax {
			return errBadChunk
		}

		if size == 0 { // last chunk: then trailers, up to an empty line
			lr.reset(maxTrailer)
			for {
				line, err := lr.readLine()
				if err != nil {
					return err
				}
				if line == consts.CRLF {
					return nil
				}
			}
		}

		// The cap is checked as "size fits in what is left" rather than
		// "len + size <= cap", which a size near MaxInt64 would overflow
		// into a negative sum and pass. With no cap, a size that cannot be
		// added to the body at all is malformed.
		have := int64(len(*body))
		if maxBody > 0 {
			if size > maxBody-have {
				return errChunkTooLarge
			}
		} else if size > math.MaxInt-have {
			return errBadChunk
		}

		// Read chunk data directly into the pooled body buffer
		start := len(*body)
		*body = slices.Grow(*body, int(size))[:start+int(size)]
		if _, err := io.ReadFull(r, (*body)[start:]); err != nil {
			return err
		}

		// The data must end in exactly CRLF. Skipping to the next LF, as
		// before, let "5\r\nhelloXX..." pass, with the bytes before the LF
		// read by rweb as junk and by another parser as the next chunk.
		if _, err := io.ReadFull(r, crlf[:]); err != nil {
			return err
		}
		if crlf != [2]byte{'\r', '\n'} {
			return errBadChunk
		}
	}
}
