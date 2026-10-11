package rweb

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
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
	//
	// When set, every cookie written through SetCookie/SetCookieWithOptions
	// is sealed with AES-GCM, and GetCookie/GetCookieAndClear open it again,
	// so handlers only ever see plaintext. GCM authenticates as well as
	// encrypts: a value the client edited, or one sealed under another key,
	// fails GetCookie with ErrCookieDecrypt rather than reading as garbage.
	//
	// It applies to all cookies the server reads, so a cookie this server did
	// not seal (one set by client-side JS, or written before the key was
	// configured) also fails GetCookie with ErrCookieDecrypt. HasCookie only
	// reports presence and does not decrypt.
	//
	// A key of the wrong length does not fall back to plaintext: NewServer
	// logs the problem, and every cookie set or get returns the key error.
	EncryptionKey []byte
}

// ErrCookieDecrypt is returned by GetCookie when CookieConfig.EncryptionKey
// is set and the cookie's value does not open under it: tampered, truncated,
// sealed under another key, or never encrypted at all.
var ErrCookieDecrypt = errors.New("rweb: cookie value failed to decrypt")

// newCookieAEAD builds the AES-GCM cipher for CookieConfig.EncryptionKey.
// An empty key means encryption is off and returns (nil, nil). It runs once
// in NewServer rather than per cookie: key expansion is the expensive part of
// AES setup, and a cipher.AEAD from crypto/cipher is safe for concurrent use.
func newCookieAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) == 0 {
		return nil, nil
	}
	block, err := aes.NewCipher(key) // rejects any length but 16, 24 or 32
	if err != nil {
		return nil, fmt.Errorf("rweb: invalid CookieConfig.EncryptionKey (%d bytes; need 16, 24 or 32): %w", len(key), err)
	}
	return cipher.NewGCM(block)
}

// sealCookieValue encrypts a cookie value. The wire form is
//
//	base64url( nonce[12] || ciphertext || tag[16] )
//
// unpadded, so it uses only [A-Za-z0-9-_] and needs no cookie quoting.
// Overhead is 28 bytes before base64, about 38 characters after.
//
// The cookie name is passed as GCM additional data: it is authenticated but
// not encrypted, which binds the value to its name. Without it a client could
// copy the sealed value of one cookie (say "role") into another ("plan") and
// the server would accept it as authentic.
//
// A fresh random nonce per seal is safe here: with 96-bit random nonces the
// collision bound only matters after ~2^32 seals under one key, far beyond
// what a cookie key sees between rotations.
func sealCookieValue(aead cipher.AEAD, name, value string) (string, error) {
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(value)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// Seal appends to nonce's backing array, giving nonce||ciphertext||tag in
	// one allocation.
	sealed := aead.Seal(nonce, nonce, []byte(value), []byte(name))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openCookieValue reverses sealCookieValue. Every failure (bad base64, too
// short, authentication failure) maps to ErrCookieDecrypt: the caller cannot
// act differently on them, and detail would only help someone probing.
func openCookieValue(aead cipher.AEAD, name, sealedValue string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealedValue)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", ErrCookieDecrypt
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return "", ErrCookieDecrypt
	}
	return string(plain), nil
}
