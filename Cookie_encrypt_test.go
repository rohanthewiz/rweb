package rweb_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// 32 bytes: AES-256.
var testCookieKey = []byte("0123456789abcdef0123456789abcdef")

// newEncryptingServer has a /set route that sets name=value and a /get route
// that reports GetCookie(name) as "ok:<value>" or "err:<error>".
func newEncryptingServer(t *testing.T, key []byte) *rweb.Server {
	t.Helper()
	s := rweb.NewServerWithOptions(rweb.WithCookie(rweb.CookieConfig{EncryptionKey: key}))
	s.Get("/set", func(ctx rweb.Context) error {
		if err := ctx.SetCookie("session", "user=42;admin"); err != nil {
			return ctx.WriteString("set-err:" + err.Error())
		}
		return ctx.WriteString("set")
	})
	s.Get("/get", func(ctx rweb.Context) error {
		v, err := ctx.GetCookie("session")
		if err != nil {
			if errors.Is(err, rweb.ErrCookieDecrypt) {
				return ctx.WriteString("err:decrypt")
			}
			return ctx.WriteString("err:" + err.Error())
		}
		return ctx.WriteString("ok:" + v)
	})
	return s
}

// sealedValue pulls the value of the first Set-Cookie header.
func sealedValue(t *testing.T, setCookie string) string {
	t.Helper()
	_, rest, ok := strings.Cut(setCookie, "=")
	if !ok {
		t.Fatalf("malformed Set-Cookie %q", setCookie)
	}
	v, _, _ := strings.Cut(rest, ";")
	return v
}

func getWithCookie(s *rweb.Server, cookie string) string {
	resp := s.Request("GET", "/get", []rweb.Header{{Key: "Cookie", Value: cookie}}, nil)
	return string(resp.Body())
}

// The value on the wire is not the plaintext, and reads back as it.
func TestCookieEncryptionRoundTrip(t *testing.T) {
	s := newEncryptingServer(t, testCookieKey)

	resp := s.Request("GET", "/set", nil, nil)
	assert.Equal(t, "set", string(resp.Body()))
	setCookie := resp.Header("Set-Cookie")
	assert.NotContains(t, setCookie, "user=42")
	assert.Contains(t, setCookie, "HttpOnly") // attributes are untouched

	sealed := sealedValue(t, setCookie)
	assert.Equal(t, "ok:user=42;admin", getWithCookie(s, "session="+sealed))
}

// Two seals of the same value differ (fresh nonce each time), so the cookie
// does not reveal when a value repeats.
func TestCookieEncryptionNonceIsFresh(t *testing.T) {
	s := newEncryptingServer(t, testCookieKey)
	a := sealedValue(t, s.Request("GET", "/set", nil, nil).Header("Set-Cookie"))
	b := sealedValue(t, s.Request("GET", "/set", nil, nil).Header("Set-Cookie"))
	if a == b {
		t.Fatalf("two seals of one value are identical: %q", a)
	}
}

// Anything that is not a value this server sealed under this name and key
// fails with ErrCookieDecrypt.
func TestCookieEncryptionRejects(t *testing.T) {
	s := newEncryptingServer(t, testCookieKey)
	sealed := sealedValue(t, s.Request("GET", "/set", nil, nil).Header("Set-Cookie"))

	// Flip one character in the middle (ciphertext, not nonce).
	mid := len(sealed) / 2
	flip := byte('A')
	if sealed[mid] == 'A' {
		flip = 'B'
	}
	tampered := sealed[:mid] + string(flip) + sealed[mid+1:]

	// Same value sealed for a different cookie name.
	other := rweb.NewServerWithOptions(rweb.WithCookie(rweb.CookieConfig{EncryptionKey: testCookieKey}))
	other.Get("/set", func(ctx rweb.Context) error {
		return ctx.SetCookie("role", "user=42;admin")
	})
	swapped := sealedValue(t, other.Request("GET", "/set", nil, nil).Header("Set-Cookie"))

	// Same name and value sealed under another key.
	otherKey := newEncryptingServer(t, []byte("fedcba9876543210fedcba9876543210"))
	wrongKey := sealedValue(t, otherKey.Request("GET", "/set", nil, nil).Header("Set-Cookie"))

	cases := map[string]string{
		"tampered":     tampered,
		"truncated":    sealed[:10],
		"name swapped": swapped,
		"wrong key":    wrongKey,
		"plaintext":    "user42",
		"empty":        "",
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, "err:decrypt", getWithCookie(s, "session="+v))
		})
	}

	// A missing cookie is still "not found", not a decrypt error.
	assert.Equal(t, "err:cookie not found", getWithCookie(s, "other=x"))
}

// A key of the wrong length must not fall back to plaintext: setting and
// getting both return the key error.
func TestCookieEncryptionBadKeyFailsClosed(t *testing.T) {
	s := newEncryptingServer(t, []byte("too-short"))

	resp := s.Request("GET", "/set", nil, nil)
	assert.Contains(t, string(resp.Body()), "set-err:")
	assert.Contains(t, string(resp.Body()), "EncryptionKey")
	assert.Equal(t, "", resp.Header("Set-Cookie"))

	assert.Contains(t, getWithCookie(s, "session=anything"), "EncryptionKey")
}

// SetCookieWithOptions seals only the wire copy: the caller's struct keeps
// its plaintext, so reusing it does not seal an already-sealed value.
func TestCookieEncryptionReusedStruct(t *testing.T) {
	s := rweb.NewServerWithOptions(rweb.WithCookie(rweb.CookieConfig{EncryptionKey: testCookieKey}))
	s.Get("/set", func(ctx rweb.Context) error {
		c := &rweb.Cookie{Name: "session", Value: "plain"}
		_ = ctx.SetCookieWithOptions(c)
		_ = ctx.SetCookieWithOptions(c)
		return ctx.WriteString(c.Value)
	})
	s.Get("/get", func(ctx rweb.Context) error {
		v, err := ctx.GetCookie("session")
		if err != nil {
			return ctx.WriteString("err")
		}
		return ctx.WriteString(v)
	})

	// The struct still holds plaintext after both calls, so the second call
	// sealed "plain" too rather than the first call's ciphertext.
	resp := s.Request("GET", "/set", nil, nil)
	assert.Equal(t, "plain", string(resp.Body()))
	assert.Equal(t, "plain", getWithCookie(s, "session="+sealedValue(t, resp.Header("Set-Cookie"))))
}

// Deleting works with encryption on, and GetCookieAndClear returns the
// plaintext before clearing.
func TestCookieEncryptionDeleteAndClear(t *testing.T) {
	s := newEncryptingServer(t, testCookieKey)
	s.Get("/clear", func(ctx rweb.Context) error {
		v, err := ctx.GetCookieAndClear("session")
		if err != nil {
			return ctx.WriteString("err:" + err.Error())
		}
		return ctx.WriteString("ok:" + v)
	})
	sealed := sealedValue(t, s.Request("GET", "/set", nil, nil).Header("Set-Cookie"))

	resp := s.Request("GET", "/clear", []rweb.Header{{Key: "Cookie", Value: "session=" + sealed}}, nil)
	assert.Equal(t, "ok:user=42;admin", string(resp.Body()))
	assert.Contains(t, resp.Header("Set-Cookie"), "session=;")
	assert.Contains(t, resp.Header("Set-Cookie"), "Max-Age=0")
}
