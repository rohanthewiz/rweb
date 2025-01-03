package rweb

import (
	"strings"

	"github.com/rohanthewiz/rweb/consts"
)

// isRequestMethod returns true if the given string is a valid HTTP request method.
func isRequestMethod(method string) bool {
	switch method {
	case consts.MethodGet, consts.MethodHead, consts.MethodPost, consts.MethodPut, consts.MethodDelete, consts.MethodConnect, consts.MethodOptions, consts.MethodTrace, consts.MethodPatch:
		return true
	default:
		return false
	}
}

// parseURL parses a URL and returns the scheme, host, path and query.
func parseURL(url string) (scheme string, host string, path string, query string) {
	schemePos := strings.Index(url, "://")

	if schemePos != -1 {
		scheme = url[:schemePos]
		url = url[schemePos+len("://"):]
	}

	pathPos := strings.IndexByte(url, '/')

	if pathPos != -1 {
		host = url[:pathPos]
		url = url[pathPos:]
	}

	queryPos := strings.IndexByte(url, '?')

	if queryPos != -1 {
		path = url[:queryPos]
		query = url[queryPos+1:]
		return
	}

	path = url
	return
}
