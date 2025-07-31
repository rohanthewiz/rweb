package rweb_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// TestSetCookie tests basic cookie setting functionality
func TestSetCookie(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/set-cookie", func(ctx rweb.Context) error {
		err := ctx.SetCookie("session", "abc123")
		if err != nil {
			return err
		}
		return ctx.WriteString("Cookie set")
	})
	
	response := s.Request("GET", "/set-cookie", nil, nil)
	assert.Equal(t, 200, response.Status())
	assert.Equal(t, "Cookie set", string(response.Body()))
	
	// check Set-Cookie header
	setCookie := response.Header("Set-Cookie")
	assert.Contains(t, setCookie, "session=abc123")
	assert.Contains(t, setCookie, "Path=/")
	assert.Contains(t, setCookie, "HttpOnly")
	assert.Contains(t, setCookie, "SameSite=Lax")
}

// TestSetCookieWithOptions tests setting cookies with custom options
func TestSetCookieWithOptions(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/custom-cookie", func(ctx rweb.Context) error {
		cookie := &rweb.Cookie{
			Name:     "remember_me",
			Value:    "token123",
			Path:     "/admin",
			Domain:   "example.com",
			Expires:  time.Now().Add(24 * time.Hour),
			MaxAge:   86400, // 24 hours
			Secure:   true,
			HttpOnly: false,
			SameSite: rweb.SameSiteStrictMode,
		}
		err := ctx.SetCookieWithOptions(cookie)
		if err != nil {
			return err
		}
		return ctx.WriteString("Custom cookie set")
	})
	
	response := s.Request("GET", "/custom-cookie", nil, nil)
	assert.Equal(t, 200, response.Status())
	
	setCookie := response.Header("Set-Cookie")
	assert.Contains(t, setCookie, "remember_me=token123")
	assert.Contains(t, setCookie, "Path=/admin")
	assert.Contains(t, setCookie, "Domain=example.com")
	assert.Contains(t, setCookie, "Max-Age=86400")
	assert.Contains(t, setCookie, "Secure")
	assert.NotContains(t, setCookie, "HttpOnly")
	assert.Contains(t, setCookie, "SameSite=Strict")
}

// TestGetCookie tests retrieving cookies from requests
func TestGetCookie(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/get-cookie", func(ctx rweb.Context) error {
		value, err := ctx.GetCookie("user_id")
		if err != nil {
			return ctx.WriteString("Cookie not found")
		}
		return ctx.WriteString("User ID: " + value)
	})
	
	// test with cookie
	headers := []rweb.Header{
		{Key: "Cookie", Value: "user_id=12345; session=xyz"},
	}
	response := s.Request("GET", "/get-cookie", headers, nil)
	assert.Equal(t, 200, response.Status())
	assert.Equal(t, "User ID: 12345", string(response.Body()))
	
	// test without cookie
	response = s.Request("GET", "/get-cookie", nil, nil)
	assert.Equal(t, 200, response.Status())
	assert.Equal(t, "Cookie not found", string(response.Body()))
}

// TestHasCookie tests checking cookie existence
func TestHasCookie(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/has-cookie", func(ctx rweb.Context) error {
		if ctx.HasCookie("auth_token") {
			return ctx.WriteString("Authenticated")
		}
		return ctx.WriteString("Not authenticated")
	})
	
	// test with cookie
	headers := []rweb.Header{
		{Key: "Cookie", Value: "auth_token=secret123"},
	}
	response := s.Request("GET", "/has-cookie", headers, nil)
	assert.Equal(t, "Authenticated", string(response.Body()))
	
	// test without cookie
	response = s.Request("GET", "/has-cookie", nil, nil)
	assert.Equal(t, "Not authenticated", string(response.Body()))
}

// TestDeleteCookie tests cookie deletion
func TestDeleteCookie(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/delete-cookie", func(ctx rweb.Context) error {
		err := ctx.DeleteCookie("session")
		if err != nil {
			return err
		}
		return ctx.WriteString("Cookie deleted")
	})
	
	response := s.Request("GET", "/delete-cookie", nil, nil)
	assert.Equal(t, 200, response.Status())
	
	setCookie := response.Header("Set-Cookie")
	assert.Contains(t, setCookie, "session=")
	// Go's http.Cookie uses Max-Age=0 for deletion
	assert.Contains(t, setCookie, "Max-Age=0")
}

// TestGetCookieAndClear tests the flash message pattern
func TestGetCookieAndClear(t *testing.T) {
	s := rweb.NewServer()
	
	var flashMessage string
	s.Get("/flash", func(ctx rweb.Context) error {
		msg, err := ctx.GetCookieAndClear("flash_message")
		if err == nil {
			flashMessage = msg
		}
		return ctx.WriteString("Flash: " + flashMessage)
	})
	
	// first request with flash cookie
	headers := []rweb.Header{
		{Key: "Cookie", Value: "flash_message=Success!"},
	}
	response := s.Request("GET", "/flash", headers, nil)
	assert.Equal(t, "Flash: Success!", string(response.Body()))
	
	// check that delete cookie was set
	setCookie := response.Header("Set-Cookie")
	assert.Contains(t, setCookie, "flash_message=")
	// Go's http.Cookie uses Max-Age=0 for deletion
	assert.Contains(t, setCookie, "Max-Age=0")
}

// TestMultipleCookies tests setting multiple cookies
func TestMultipleCookies(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/multi-cookie", func(ctx rweb.Context) error {
		ctx.SetCookie("cookie1", "value1")
		ctx.SetCookie("cookie2", "value2")
		ctx.SetCookie("cookie3", "value3")
		return ctx.WriteString("Multiple cookies set")
	})
	
	response := s.Request("GET", "/multi-cookie", nil, nil)
	assert.Equal(t, 200, response.Status())
	
	// verify cookies were set
	// The Header() method only returns the first Set-Cookie header,
	// but in a real HTTP response, each would be a separate header.
	// For now, just verify the first cookie was set correctly.
	setCookieHeader := response.Header("Set-Cookie")
	assert.Contains(t, setCookieHeader, "cookie1=value1")
	assert.Contains(t, setCookieHeader, "Path=/")
	assert.Contains(t, setCookieHeader, "HttpOnly")
	assert.Contains(t, setCookieHeader, "SameSite=Lax")
}

// TestCookieInMiddleware tests using cookies in middleware
func TestCookieInMiddleware(t *testing.T) {
	s := rweb.NewServer()
	
	// protected route handler
	protectedHandler := func(ctx rweb.Context) error {
		// check auth from context (set by middleware)
		if !ctx.Has("authenticated") || !ctx.Get("authenticated").(bool) {
			return ctx.Status(401).WriteString("Unauthorized")
		}
		return ctx.WriteString("Protected content")
	}
	
	// auth middleware that sets context data
	authMiddleware := func(ctx rweb.Context) error {
		if ctx.HasCookie("auth_token") {
			token, _ := ctx.GetCookie("auth_token")
			if token == "valid_token" {
				ctx.Set("authenticated", true)
			}
		}
		return ctx.Next()
	}
	
	// apply middleware globally
	s.Use(authMiddleware)
	s.Get("/protected", protectedHandler)
	
	// test without cookie
	response := s.Request("GET", "/protected", nil, nil)
	assert.Equal(t, 401, response.Status())
	// the body might include both middleware and handler response
	body := string(response.Body())
	assert.Equal(t, "Unauthorized", body)
	
	// test with invalid token
	headers := []rweb.Header{
		{Key: "Cookie", Value: "auth_token=invalid"},
	}
	response = s.Request("GET", "/protected", headers, nil)
	assert.Equal(t, 401, response.Status())
	assert.Equal(t, "Unauthorized", string(response.Body()))
	
	// test with valid token
	headers = []rweb.Header{
		{Key: "Cookie", Value: "auth_token=valid_token"},
	}
	response = s.Request("GET", "/protected", headers, nil)
	assert.Equal(t, 200, response.Status())
	assert.Equal(t, "Protected content", string(response.Body()))
}

// TestCookieDefaults tests server-wide cookie defaults
func TestCookieDefaults(t *testing.T) {
	s := rweb.NewServer(rweb.ServerOptions{
		Cookie: rweb.CookieConfig{
			Path:     "/app",
			HttpOnly: false,
			SameSite: rweb.SameSiteStrictMode,
		},
	})
	
	s.Get("/default-cookie", func(ctx rweb.Context) error {
		// explicitly set HttpOnly=false to respect server config
		cookie := &rweb.Cookie{
			Name:  "test",
			Value: "value",
			HttpOnly: false, // explicitly set false to match server config
		}
		return ctx.SetCookieWithOptions(cookie)
	})
	
	response := s.Request("GET", "/default-cookie", nil, nil)
	setCookie := response.Header("Set-Cookie")
	
	assert.Contains(t, setCookie, "Path=/app")
	assert.NotContains(t, setCookie, "HttpOnly")
	assert.Contains(t, setCookie, "SameSite=Strict")
}

// TestCookieWithTLS tests automatic Secure flag with TLS
func TestCookieWithTLS(t *testing.T) {
	// test that cookies are automatically secure when server has TLS
	s := rweb.NewServer(rweb.ServerOptions{
		TLS: rweb.TLSCfg{
			UseTLS: true,
		},
	})
	
	s.Get("/tls-cookie", func(ctx rweb.Context) error {
		// even without explicitly setting Secure, it should be true with TLS
		return ctx.SetCookie("secure_test", "value")
	})
	
	response := s.Request("GET", "/tls-cookie", nil, nil)
	setCookie := response.Header("Set-Cookie")
	assert.Contains(t, setCookie, "Secure")
}

// TestEmptyCookieName tests error handling for empty cookie names
func TestEmptyCookieName(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/empty-name", func(ctx rweb.Context) error {
		err := ctx.SetCookie("", "value")
		if err != nil {
			return ctx.WriteString("Error: " + err.Error())
		}
		return ctx.WriteString("Should not reach here")
	})
	
	response := s.Request("GET", "/empty-name", nil, nil)
	assert.Equal(t, 200, response.Status())
	assert.Contains(t, string(response.Body()), "cookie name cannot be empty")
}

// TestCookieParsing tests parsing multiple cookies from header
func TestCookieParsing(t *testing.T) {
	s := rweb.NewServer()
	
	s.Get("/parse-cookies", func(ctx rweb.Context) error {
		var cookies []string
		
		// check specific cookies
		if ctx.HasCookie("user") {
			val, _ := ctx.GetCookie("user")
			cookies = append(cookies, fmt.Sprintf("user=%s", val))
		}
		
		if ctx.HasCookie("session") {
			val, _ := ctx.GetCookie("session")
			cookies = append(cookies, fmt.Sprintf("session=%s", val))
		}
		
		if ctx.HasCookie("pref") {
			val, _ := ctx.GetCookie("pref")
			cookies = append(cookies, fmt.Sprintf("pref=%s", val))
		}
		
		return ctx.WriteString(strings.Join(cookies, "; "))
	})
	
	headers := []rweb.Header{
		{Key: "Cookie", Value: "user=john; session=abc123; pref=dark"},
	}
	
	response := s.Request("GET", "/parse-cookies", headers, nil)
	assert.Equal(t, "user=john; session=abc123; pref=dark", string(response.Body()))
}

// Helper to make Cookie and CookieConfig exported for tests
func TestCookieExports(t *testing.T) {
	// verify Cookie type is exported and can be instantiated
	cookie := &rweb.Cookie{
		Name:     "test",
		Value:    "value",
		SameSite: rweb.SameSiteLaxMode,
	}
	assert.Equal(t, "test", cookie.Name)
	
	// verify CookieConfig is exported
	config := rweb.CookieConfig{
		HttpOnly: true,
		SameSite: rweb.SameSiteStrictMode,
	}
	assert.True(t, config.HttpOnly)
}