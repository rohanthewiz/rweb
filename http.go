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

// parseURL parses a request target and returns the scheme, host, path and query.
// Though we could have used the standard URL package we wanted to maintain fine control.
//
// The target arrives in one of the forms of RFC 9112 §3.2:
//
//	origin-form     /path?query                   nearly every request
//	absolute-form   scheme://host/path?query      proxies, some clients
//	                scheme://host?query           (empty path: "/")
//	authority-form  host:port                     CONNECT; kept as the path
//	asterisk-form   *                             OPTIONS *; kept as the path
//
// Only an absolute-form target yields a scheme and host, and they are looked
// for only before the first "?". The query is data: a target such as
// "/redirect?url=http://evil.com/admin" used to be split at the "://" inside
// the query, which routed it to /admin with host "evil.com" — a different
// route from the one any proxy in front, applying path rules, had seen.
func parseURL(url string, urlOpts URLOptions) (scheme string, host string, path string, query string) {
	// A target starting with "/" is origin-form and cannot carry a scheme or
	// host, so the scan for "://" is skipped altogether. This is the hot
	// path, and also the one the misrouting above came through.
	if !strings.HasPrefix(url, "/") {
		beforeQuery := url
		if q := strings.IndexByte(url, consts.RuneQuestion); q != -1 {
			beforeQuery = url[:q]
		}

		if schemeEndPos := strings.Index(beforeQuery, consts.SchemeDelimiter); schemeEndPos != -1 {
			scheme = url[:schemeEndPos]
			url = url[schemeEndPos+len(consts.SchemeDelimiter):]

			// The authority runs to the first "/" or "?", whichever comes
			// first. Stopping only at "/" would read
			// "http://a.com?u=http://b/c" as host "a.com?u=http:".
			// With neither, the whole rest is the host and the path is
			// empty (fixed up to "/" below).
			if authEnd := strings.IndexAny(url, "/?"); authEnd != -1 {
				host = url[:authEnd]
				url = url[authEnd:]
			} else {
				host = url
				url = ""
			}
		}
		// No scheme: authority-form ("host:port") or asterisk-form ("*"), or a
		// malformed target. None of these names a host for routing purposes,
		// so the target is left whole as the path. In particular a target
		// such as "evil.com/admin" is not split into host and path: a host
		// is taken only from an absolute-form target.
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
