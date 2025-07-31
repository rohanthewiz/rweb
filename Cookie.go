package rweb

import (
	"net/http"
	"time"
)

// SameSiteMode represents the SameSite cookie attribute for CSRF protection.
// This attribute controls when cookies are sent with cross-site requests.
type SameSiteMode int

const (
	// SameSiteDefaultMode leaves the SameSite attribute unset (browser-dependent behavior)
	SameSiteDefaultMode SameSiteMode = iota + 1
	// SameSiteLaxMode allows cookies on top-level navigation (recommended default)
	SameSiteLaxMode
	// SameSiteStrictMode prevents cookies on all cross-site requests
	SameSiteStrictMode
	// SameSiteNoneMode allows cookies on all cross-site requests (requires Secure=true)
	SameSiteNoneMode
)

// Cookie represents an HTTP cookie with all standard attributes.
// It's designed to be compatible with net/http.Cookie while providing
// a simpler API for common use cases in RWeb applications.
type Cookie struct {
	// Name is the cookie name (required)
	Name string
	// Value is the cookie value (can be empty string)
	Value string
	// Path specifies the URL path prefix where the cookie is valid (default: "/")
	Path string
	// Domain specifies the domain where the cookie is valid (default: current domain)
	Domain string
	// Expires specifies when the cookie expires (zero value = session cookie)
	Expires time.Time
	// MaxAge specifies cookie lifetime in seconds (0 = unspecified, <0 = delete now)
	MaxAge int
	// Secure limits cookie transmission to HTTPS only (auto-set based on TLS)
	Secure bool
	// HttpOnly prevents JavaScript access to the cookie (default: true)
	HttpOnly bool
	// SameSite controls cross-site request behavior (default: SameSiteLaxMode)
	SameSite SameSiteMode
}

// ToStdCookie converts the RWeb Cookie to a standard net/http.Cookie.
// This ensures compatibility with existing Go HTTP infrastructure.
func (c *Cookie) ToStdCookie() *http.Cookie {
	// apply secure defaults if not explicitly set
	cookie := &http.Cookie{
		Name:     c.Name,
		Value:    c.Value,
		Path:     c.Path,
		Domain:   c.Domain,
		Expires:  c.Expires,
		MaxAge:   c.MaxAge,
		Secure:   c.Secure,
		HttpOnly: c.HttpOnly,
	}
	
	// convert SameSiteMode to http.SameSite
	switch c.SameSite {
	case SameSiteLaxMode:
		cookie.SameSite = http.SameSiteLaxMode
	case SameSiteStrictMode:
		cookie.SameSite = http.SameSiteStrictMode
	case SameSiteNoneMode:
		cookie.SameSite = http.SameSiteNoneMode
	default:
		cookie.SameSite = http.SameSiteDefaultMode
	}
	
	return cookie
}

// newCookieFromStd creates an RWeb Cookie from a standard net/http.Cookie.
// Used internally for parsing cookies from request headers.
func newCookieFromStd(c *http.Cookie) *Cookie {
	cookie := &Cookie{
		Name:     c.Name,
		Value:    c.Value,
		Path:     c.Path,
		Domain:   c.Domain,
		Expires:  c.Expires,
		MaxAge:   c.MaxAge,
		Secure:   c.Secure,
		HttpOnly: c.HttpOnly,
	}
	
	// convert http.SameSite to SameSiteMode
	switch c.SameSite {
	case http.SameSiteLaxMode:
		cookie.SameSite = SameSiteLaxMode
	case http.SameSiteStrictMode:
		cookie.SameSite = SameSiteStrictMode
	case http.SameSiteNoneMode:
		cookie.SameSite = SameSiteNoneMode
	default:
		cookie.SameSite = SameSiteDefaultMode
	}
	
	return cookie
}

// CookieConfig holds server-wide default settings for cookies.
// These defaults are applied to all cookies unless overridden.
type CookieConfig struct {
	// Secure sets the default Secure flag for all cookies.
	// If not set, it's auto-detected based on whether TLS is enabled.
	Secure bool
	// HttpOnly sets the default HttpOnly flag (default: true).
	// This prevents JavaScript access to cookies for security.
	HttpOnly bool
	// SameSite sets the default SameSite mode (default: SameSiteLaxMode).
	// This provides CSRF protection by default.
	SameSite SameSiteMode
	// Path sets the default path for all cookies (default: "/").
	// This makes cookies available to the entire site by default.
	Path string
	// EncryptionKey enables automatic cookie value encryption if set.
	// Must be 16, 24, or 32 bytes for AES-128, AES-192, or AES-256.
	EncryptionKey []byte
}

