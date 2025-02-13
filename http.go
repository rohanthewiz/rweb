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
	if queryPos != -1 && queryPos < len(url)+1 /* we will go one past the question sign below */ {
		path = url[:queryPos]
		query = url[queryPos+1:] // check above ensures we don't go past the end of the string
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

	// If the host is empty, set it to "localhost"
	if host == "" {
		host = consts.Localhost
	}

	return
}
