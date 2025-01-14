package rweb

// Header is used to store HTTP headers.
type Header struct {
	Key   string
	Value string
}

/*type RequestHeader struct {
	noCopy noCopy

	contentLengthBytes []byte

	method      []byte
	requestURI  []byte
	proto       []byte
	host        []byte
	contentType []byte
	userAgent   []byte
	mulHeader   [][]byte

	h       []argsKV
	trailer []argsKV

	cookies []argsKV

	// stores an immutable copy of headers as they were received from the
	// wire.
	rawHeaders []byte
	bufK       []byte
	bufV       []byte

	contentLength int

	disableNormalizing bool
	// noHTTP11             bool
	connectionClose      bool
	noDefaultContentType bool
	disableSpecialHeader bool

	// These two fields have been moved close to other bool fields
	// for reducing RequestHeader object size.
	cookiesCollected bool

	secureErrorLogMessage bool
}
*/

/*// MultipartFormBoundary returns boundary part
// from 'multipart/form-data; boundary=...' Content-Type.
func (h *RequestHeader) MultipartFormBoundary() []byte {
	b := h.ContentType()
	if !bytes.HasPrefix(b, consts.StrMultipartFormData) {
		return nil
	}
	b = b[len(consts.StrMultipartFormData):]
	if len(b) == 0 || b[0] != ';' {
		return nil
	}

	var n int
	for len(b) > 0 {
		n++
		for len(b) > n && b[n] == ' ' {
			n++
		}
		b = b[n:]
		if !bytes.HasPrefix(b, consts.StrMultipartFormData) {
			if n = bytes.IndexByte(b, ';'); n < 0 {
				return nil
			}
			continue
		}

		b = b[len(consts.StrMultipartFormData):]
		if len(b) == 0 || b[0] != '=' {
			return nil
		}
		b = b[1:]
		if n = bytes.IndexByte(b, ';'); n >= 0 {
			b = b[:n]
		}
		if len(b) > 1 && b[0] == '"' && b[len(b)-1] == '"' {
			b = b[1 : len(b)-1]
		}
		return b
	}
	return nil
}

func (h *RequestHeader) peek(key []byte) []byte {
	switch string(key) {
	case consts.HeaderHost:
		return h.Host()
	case consts.HeaderContentType:
		return h.ContentType()
	case consts.HeaderUserAgent:
		return h.UserAgent()
	case consts.HeaderConnection:
		if h.ConnectionClose() {
			return consts.StrClose
		}
		return peekArgBytes(h.h, key)
	case consts.HeaderContentLength:
		return h.contentLengthBytes
	case consts.HeaderCookie:
		if h.cookiesCollected {
			return appendRequestCookieBytes(nil, h.cookies)
		}
		return peekArgBytes(h.h, key)
	case consts.HeaderTrailer:
		return appendArgsKeyBytes(nil, h.trailer, consts.StrCommaSpace)
	default:
		return peekArgBytes(h.h, key)
	}
}
*/
/*
// PeekAll returns all header value for the given key.
//
// The returned value is valid until the request is released,
// either though ReleaseRequest or your request handler returning.
// Any future calls to the Peek* will modify the returned value.
// Do not store references to returned value. Make copies instead.
func (h *RequestHeader) PeekAll(key string) [][]byte {
	h.bufK = getHeaderKeyBytes(h.bufK, key, h.disableNormalizing)
	return h.peekAll(h.bufK)
}

func (h *RequestHeader) peekAll(key []byte) [][]byte {
	h.mulHeader = h.mulHeader[:0]
	switch string(key) {
	case consts.HeaderHost:
		if host := h.Host(); len(host) > 0 {
			h.mulHeader = append(h.mulHeader, host)
		}
	case consts.HeaderContentType:
		if contentType := h.ContentType(); len(contentType) > 0 {
			h.mulHeader = append(h.mulHeader, contentType)
		}
	case consts.HeaderUserAgent:
		if ua := h.UserAgent(); len(ua) > 0 {
			h.mulHeader = append(h.mulHeader, ua)
		}
	case consts.HeaderConnection:
		if h.ConnectionClose() {
			h.mulHeader = append(h.mulHeader, consts.StrClose)
		} else {
			h.mulHeader = peekAllArgBytesToDst(h.mulHeader, h.h, key)
		}
	case consts.HeaderContentLength:
		h.mulHeader = append(h.mulHeader, h.contentLengthBytes)
	case consts.HeaderCookie:
		if h.cookiesCollected {
			h.mulHeader = append(h.mulHeader, appendRequestCookieBytes(nil, h.cookies))
		} else {
			h.mulHeader = peekAllArgBytesToDst(h.mulHeader, h.h, key)
		}
	case consts.HeaderTrailer:
		h.mulHeader = append(h.mulHeader, appendArgsKeyBytes(nil, h.trailer, consts.StrCommaSpace))
	default:
		h.mulHeader = peekAllArgBytesToDst(h.mulHeader, h.h, key)
	}
	return h.mulHeader
}

// ContentType returns Content-Type header value.
func (h *RequestHeader) ContentType() []byte {
	if h.disableSpecialHeader {
		return peekArgBytes(h.h, consts.StrContentType)
	}
	return h.contentType
}

// SetContentType sets Content-Type header value.
func (h *RequestHeader) SetContentType(contentType string) {
	h.contentType = append(h.contentType[:0], contentType...)
}

// SetContentTypeBytes sets Content-Type header value.
func (h *RequestHeader) SetContentTypeBytes(contentType []byte) {
	h.contentType = append(h.contentType[:0], contentType...)
}

// Host returns Host header value.
func (h *RequestHeader) Host() []byte {
	if h.disableSpecialHeader {
		return peekArgBytes(h.h, []byte(consts.HeaderHost))
	}
	return h.host
}

// ConnectionClose returns true if 'Connection: close' header is set.
func (h *RequestHeader) ConnectionClose() bool {
	return h.connectionClose
}

// SetConnectionClose sets 'Connection: close' header.
func (h *RequestHeader) SetConnectionClose() {
	h.connectionClose = true
}

// ResetConnectionClose clears 'Connection: close' header if it exists.
func (h *RequestHeader) ResetConnectionClose() {
	if h.connectionClose {
		h.connectionClose = false
		h.h = delAllArgsBytes(h.h, consts.StrConnection)
	}
}

// UserAgent returns User-Agent header value.
func (h *RequestHeader) UserAgent() []byte {
	if h.disableSpecialHeader {
		return peekArgBytes(h.h, []byte(consts.HeaderUserAgent))
	}
	return h.userAgent
}

// SetUserAgent sets User-Agent header value.
func (h *RequestHeader) SetUserAgent(userAgent string) {
	h.userAgent = append(h.userAgent[:0], userAgent...)
}

// SetUserAgentBytes sets User-Agent header value.
func (h *RequestHeader) SetUserAgentBytes(userAgent []byte) {
	h.userAgent = append(h.userAgent[:0], userAgent...)
}

// Referer returns Referer header value.
func (h *RequestHeader) Referer() []byte {
	return peekArgBytes(h.h, consts.StrReferer)
}

func appendArgsKeyBytes(dst []byte, args []argsKV, sep []byte) []byte {
	for i, n := 0, len(args); i < n; i++ {
		kv := &args[i]
		dst = append(dst, kv.key...)
		if i+1 < n {
			dst = append(dst, sep...)
		}
	}
	return dst
}

func getHeaderKeyBytes(bufK []byte, key string, disableNormalizing bool) []byte {
	bufK = append(bufK[:0], key...)
	normalizeHeaderKey(bufK, disableNormalizing)
	return bufK
}

func normalizeHeaderValue(ov, ob []byte, headerLength int) (nv, nb []byte, nhl int) {
	nv = ov
	length := len(ov)
	if length <= 0 {
		return
	}
	write := 0
	shrunk := 0
	once := false
	lineStart := false
	for read := 0; read < length; read++ {
		c := ov[read]
		switch {
		case c == consts.RuneCR || c == consts.RuneNewLine:
			shrunk++
			if c == consts.RuneNewLine {
				lineStart = true
				once = false
			}
			continue
		case lineStart && (c == '\t' || c == ' '):
			if !once {
				c = ' '
				once = true
			} else {
				shrunk++
				continue
			}
		default:
			lineStart = false
		}
		nv[write] = c
		write++
	}

	nv = nv[:write]
	copy(ob[write:], ob[write+shrunk:])

	// Check if we need to skip \r\n or just \n
	skip := 0
	if ob[write] == consts.RuneCR {
		if ob[write+1] == consts.RuneNewLine {
			skip += 2
		} else {
			skip++
		}
	} else if ob[write] == consts.RuneNewLine {
		skip++
	}

	nb = ob[write+skip : len(ob)-shrunk]
	nhl = headerLength - shrunk
	return
}

func normalizeHeaderKey(b []byte, disableNormalizing bool) {
	if disableNormalizing {
		return
	}

	n := len(b)
	if n == 0 {
		return
	}

	b[0] = toUpperTable[b[0]]
	for i := 1; i < n; i++ {
		p := &b[i]
		if *p == '-' {
			i++
			if i < n {
				b[i] = toUpperTable[b[i]]
			}
			continue
		}
		*p = toLowerTable[*p]
	}
}
*/
