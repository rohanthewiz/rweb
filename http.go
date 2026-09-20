package rweb

import (
	"strings"

	"github.com/rohanthewiz/rweb/consts"
)

// isValidRequestMethod returns true if the given string is a valid HTTP request method.
func isValidRequestMethod(method string) bool {
	switch method {
	case consts.MethodGet, consts.MethodHead, consts.MethodPost, consts.MethodPut,
		consts.MethodDelete, consts.MethodConnect, consts.MethodOptions, consts.MethodTrace, consts.MethodPatch:
		return true
	default:
		return false
	}
}

// parseURL parses a URL and returns the scheme, host, path and query.
// The URL is expected to be in the format "scheme://host/path?query"
// Though we could have used the standard URL package we wanted to maintain fine control.
func parseURL(url string, urlOpts URLOptions) (scheme string, host string, path string, query string) {
	schemeEndPos := strings.Index(url, consts.SchemeDelimiter)
	if schemeEndPos != -1 {
		scheme = url[:schemeEndPos]
		url = url[schemeEndPos+len(consts.SchemeDelimiter):]
	}

	pathStartPos := strings.IndexByte(url, consts.RuneFwdSlash)
	if pathStartPos != -1 {
		host = url[:pathStartPos]
		url = url[pathStartPos:]
	}

	queryPos := strings.IndexByte(url, consts.RuneQuestion)
	if queryPos != -1 {
		path = url[:queryPos]
		query = url[queryPos+1:] // safe even when '?' is the last char (yields "")
	} else {
		path = url
	}

	// FIXUPS

	if lnPath := len(path); lnPath == 0 {
		path = "/"
	} else { // Trailing slash removal
		if !urlOpts.KeepTrailingSlashes && lnPath > 1 && strings.HasSuffix(path, "/") {
			path = path[:lnPath-1]
		}
	}

	// host is left empty when the URL has none, which is the usual case: a
	// browser sends the origin-form target ("GET /path") and names the host
	// in the Host header instead. The caller resolves that (see
	// Server.handleRequest); defaulting here would hide the difference
	// between "the request line said localhost" and "it said nothing".

	return
}

// isValidHostHeader reports whether v is an acceptable Host header value:
//
//	Host = uri-host [ ":" port ]        (RFC 9110 §7.2, RFC 3986 §3.2.2)
//
//	example.com          reg-name
//	example.com:8080     reg-name + port
//	127.0.0.1:8080       IPv4 (a subset of the reg-name alphabet)
//	[::1]:8080           IP-literal + port
//	""                   legal: the target URI has no authority
//
// This is a character-class check, not a resolver: it does not decide whether
// the name is a plausible DNS name, only that it cannot carry anything that
// would change meaning when echoed — whitespace, control bytes, "/", "\",
// "?", "#", "@", or a stray ":" or bracket. It is hand-rolled rather than
// delegated to net/url because it runs on every request and allocates nothing.
func isValidHostHeader(v string) bool {
	if v == "" {
		return true
	}

	host, port := v, ""
	if v[0] == '[' {
		// IP-literal: everything up to the closing bracket is the address;
		// what follows may only be ":port". The address alphabet is hex
		// digits, ":" and "." (for an embedded IPv4 tail). Zone IDs ("%eth0")
		// are not valid in a Host header and so are not admitted.
		end := strings.IndexByte(v, ']')
		if end < 2 { // no "]" at all, or the empty literal "[]"
			return false
		}
		for i := 1; i < end; i++ {
			c := v[i]
			isHex := c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
			if !isHex && c != ':' && c != '.' {
				return false
			}
		}
		rest := v[end+1:]
		if rest == "" {
			return true
		}
		if rest[0] != ':' {
			return false
		}
		host, port = "", rest[1:]
	} else if colon := strings.IndexByte(v, ':'); colon != -1 {
		// Outside brackets the first ":" must be the port delimiter, so any
		// further ":" ends up in the port and fails the digit check below.
		host, port = v[:colon], v[colon+1:]
		if host == "" {
			return false // ":8080" — a port with no host
		}
	}

	// port = *DIGIT — an empty port ("example.com:") is legal per the grammar.
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}

	// reg-name = *( unreserved / pct-encoded / sub-delims )
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("-._~%!$&'()*+,;=", c) != -1:
		default:
			return false
		}
	}
	return true
}
