# RWeb Native Cookie Support Proposal

## Background

Based on analysis of the Church CMS application's cookie usage patterns, this proposal outlines native cookie support that should be added to the RWeb framework to eliminate the need for project-specific cookie implementations.

## Current Cookie Usage Patterns

The application uses cookies for:

1. **Session Management**
   - Storing session keys that reference Redis-backed session data
   - Session cookies (no expiration set, removed on browser close)
   - Path always set to "/"

2. **Flash Messages**
   - Temporary user feedback (info, warning, error messages)
   - Base64-encoded JSON data
   - Get-and-clear pattern (read once then delete)

3. **Security Settings**
   - HttpOnly: true (prevents JavaScript access)
   - SameSite: Lax (CSRF protection)
   - Secure: Should be true in production with HTTPS

## Proposed RWeb Cookie API

### Core Methods

```go
// SetCookie sets a cookie with the given name and value
// Uses secure defaults: HttpOnly=true, SameSite=Lax, Path="/"
func (ctx Context) SetCookie(name, value string) error

// SetCookieWithOptions sets a cookie with custom options
func (ctx Context) SetCookieWithOptions(cookie *Cookie) error

// GetCookie retrieves a cookie value by name
func (ctx Context) GetCookie(name string) (string, error)

// GetCookieAndClear retrieves a cookie value and immediately deletes it
// Useful for flash messages
func (ctx Context) GetCookieAndClear(name string) (string, error)

// DeleteCookie removes a cookie by setting MaxAge=-1 and expired time
func (ctx Context) DeleteCookie(name string) error

// HasCookie checks if a cookie exists
func (ctx Context) HasCookie(name string) bool
```

### Cookie Type Definition

```go
type Cookie struct {
    Name     string
    Value    string
    Path     string        // defaults to "/"
    Domain   string        // defaults to current domain
    Expires  time.Time     // zero value means session cookie
    MaxAge   int           // seconds, 0 means unspecified
    Secure   bool          // defaults based on TLS setting
    HttpOnly bool          // defaults to true
    SameSite SameSiteMode  // defaults to SameSiteLaxMode
}

type SameSiteMode int

const (
    SameSiteDefaultMode SameSiteMode = iota + 1
    SameSiteLaxMode
    SameSiteStrictMode
    SameSiteNoneMode
)
```

### Server-Level Cookie Configuration

```go
type ServerOptions struct {
    // ... existing options ...
    
    Cookie CookieConfig
}

type CookieConfig struct {
    // Default settings for all cookies
    Secure   bool          // auto-detect from TLS if not set
    HttpOnly bool          // default: true
    SameSite SameSiteMode  // default: SameSiteLaxMode
    Path     string        // default: "/"
    
    // Optional encryption for cookie values
    EncryptionKey []byte   // if set, all cookie values are encrypted
}
```

## Usage Examples

### Session Management

```go
// Setting a session cookie
func loginHandler(ctx rweb.Context) error {
    // Generate session key
    sessionKey := generateSessionKey()
    
    // Store session data in Redis
    err := storeSessionInRedis(sessionKey, userData)
    if err != nil {
        return err
    }
    
    // Set session cookie (expires on browser close)
    ctx.SetCookie("session_id", sessionKey)
    
    return ctx.WriteJSON(map[string]string{"status": "logged in"})
}

// Reading session
func protectedHandler(ctx rweb.Context) error {
    sessionKey, err := ctx.GetCookie("session_id")
    if err != nil {
        return ctx.Status(401).WriteString("Not authenticated")
    }
    
    // Validate session in Redis...
}

// Logout
func logoutHandler(ctx rweb.Context) error {
    sessionKey, _ := ctx.GetCookie("session_id")
    
    // Clear session from Redis
    clearSessionFromRedis(sessionKey)
    
    // Delete cookie
    ctx.DeleteCookie("session_id")
    
    return ctx.WriteString("Logged out")
}
```

### Flash Messages

```go
// Setting a flash message
func processForm(ctx rweb.Context) error {
    // Process form...
    
    // Set flash message
    flash := map[string]string{"type": "success", "message": "Form saved!"}
    flashJSON, _ := json.Marshal(flash)
    ctx.SetCookie("flash", base64.StdEncoding.EncodeToString(flashJSON))
    
    // Redirect
    return ctx.Redirect("/dashboard")
}

// Reading and clearing flash message
func dashboardHandler(ctx rweb.Context) error {
    // Get and immediately clear flash message
    flashData, err := ctx.GetCookieAndClear("flash")
    if err == nil {
        // Decode and use flash message
        decoded, _ := base64.StdEncoding.DecodeString(flashData)
        var flash map[string]string
        json.Unmarshal(decoded, &flash)
        // Include flash in page render...
    }
    
    return ctx.WriteHTML(renderDashboard())
}
```

### Custom Cookie Options

```go
// Setting a persistent cookie with custom options
func rememberMeHandler(ctx rweb.Context) error {
    cookie := &rweb.Cookie{
        Name:     "remember_token",
        Value:    generateRememberToken(),
        Path:     "/",
        Expires:  time.Now().Add(30 * 24 * time.Hour), // 30 days
        HttpOnly: true,
        Secure:   true, // Force HTTPS
        SameSite: rweb.SameSiteStrictMode,
    }
    
    ctx.SetCookieWithOptions(cookie)
    return ctx.WriteString("Remember me set")
}
```

## Implementation Considerations

### 1. Automatic Security Settings

- `HttpOnly` should default to `true` to prevent XSS attacks
- `Secure` should automatically be `true` when server is running with TLS
- `SameSite` should default to `Lax` for CSRF protection

### 2. Convenience Features

- `GetCookieAndClear` pattern is common for flash messages
- Session cookies (no expiration) should be easy to create
- Path should default to "/" as it's the most common use case

### 3. Error Handling

- `GetCookie` should return a clear error when cookie doesn't exist
- Similar to how RWeb handles context data with `Has()` and `Get()` pattern

### 4. Middleware Integration

Cookie operations should work seamlessly in middleware:

```go
func authMiddleware(ctx rweb.Context) error {
    if !ctx.HasCookie("session_id") {
        return ctx.Status(401).WriteString("Authentication required")
    }
    
    sessionId, _ := ctx.GetCookie("session_id")
    // Validate session...
    
    return ctx.Next()
}
```

### 5. Testing Support

RWeb's test framework should support cookie operations:

```go
func TestCookieOperations(t *testing.T) {
    s := rweb.NewServer()
    
    s.Get("/set", func(ctx rweb.Context) error {
        ctx.SetCookie("test", "value")
        return ctx.WriteString("Cookie set")
    })
    
    response := s.Request("GET", "/set", nil, nil)
    
    // Should be able to inspect cookies in test response
    cookies := response.Cookies()
    assert.Equal(t, "value", cookies["test"])
}
```

## Benefits

1. **Consistency**: All RWeb applications would handle cookies the same way
2. **Security**: Secure defaults prevent common vulnerabilities
3. **Simplicity**: No need for external cookie packages or custom implementations
4. **Type Safety**: Structured Cookie type prevents errors
5. **Testing**: Built-in test support for cookie operations

## Migration Path

For existing RWeb applications using custom cookie implementations:

1. The new API should be compatible with standard `http.Cookie`
2. Existing `http.SetCookie(ctx.Response(), cookie)` calls can be gradually replaced
3. Helper functions can wrap the new API during transition

## Conclusion

Adding native cookie support to RWeb would eliminate boilerplate code and ensure consistent, secure cookie handling across all RWeb applications. The proposed API covers the common use cases (sessions, flash messages) while remaining flexible for advanced scenarios.