# Cookie Architecture in RWeb

## Overview

This document describes the architectural design and implementation details of the native cookie support in RWeb. The cookie feature was designed to provide a simple, secure API while maintaining compatibility with Go's standard `net/http` cookie handling.

## Design Principles

1. **Security by Default** - Cookies are created with secure defaults (HttpOnly=true, SameSite=Lax)
2. **Simplicity** - Common operations like setting/getting cookies require minimal code
3. **Compatibility** - Full compatibility with Go's `net/http.Cookie` for easy migration
4. **Performance** - Lazy parsing and efficient storage to minimize overhead
5. **Flexibility** - Support for both simple and advanced use cases

## Architecture Components

### 1. Cookie Type System (`Cookie.go`)

The cookie implementation centers around two main types:

```go
type Cookie struct {
    Name     string
    Value    string
    Path     string
    Domain   string
    Expires  time.Time
    MaxAge   int
    Secure   bool
    HttpOnly bool
    SameSite SameSiteMode
}

type SameSiteMode int
```

**Key Design Decisions:**
- Custom `SameSiteMode` enum instead of using `http.SameSite` directly for cleaner API
- Conversion methods to/from `net/http.Cookie` for compatibility
- No cookie value encryption in core (can be added at application level)

### 2. Context Interface Extensions

Cookie methods were added to the `Context` interface to provide request-scoped cookie operations:

```go
type Context interface {
    // ... existing methods ...
    
    // Cookie operations
    SetCookie(name, value string) error
    SetCookieWithOptions(cookie *Cookie) error
    GetCookie(name string) (string, error)
    GetCookieAndClear(name string) (string, error)
    DeleteCookie(name string) error
    HasCookie(name string) bool
}
```

**Design Rationale:**
- Methods on Context rather than Request/Response for consistency with RWeb patterns
- Separate simple (`SetCookie`) and advanced (`SetCookieWithOptions`) methods
- `GetCookieAndClear` for the common flash message pattern

### 3. Implementation Details

#### Cookie Storage in Context

```go
type context struct {
    // ... existing fields ...
    parsedCookies map[string]*Cookie  // Lazy-loaded cookie cache
    cookiesParsed bool                 // Parsing flag
}
```

**Performance Optimizations:**
- Cookies are parsed only once per request (lazy loading)
- Parsed cookies are cached in a map for O(1) lookups
- Cookie map is reused across requests (cleared, not reallocated)

#### Cookie Parsing Flow

```
Request Headers → parseCookies() → parsedCookies map → GetCookie()
                      ↑
                      |
                 (lazy, on first access)
```

#### Cookie Setting Flow

```
SetCookie() → Cookie struct → ToStdCookie() → response.AddHeader("Set-Cookie", ...)
                  ↑
                  |
           (applies defaults)
```

### 4. Server-Level Configuration

```go
type ServerOptions struct {
    // ... existing options ...
    Cookie CookieConfig
}

type CookieConfig struct {
    Secure   bool
    HttpOnly bool
    SameSite SameSiteMode
    Path     string
    EncryptionKey []byte  // Reserved for future use
}
```

**Configuration Hierarchy:**
1. Explicit cookie attributes (highest priority)
2. Server-level defaults
3. Built-in secure defaults (lowest priority)

### 5. Response Header Handling

A new `AddHeader` method was added to the response interface to support multiple Set-Cookie headers:

```go
func (res *response) AddHeader(key string, value string) {
    res.headers = append(res.headers, Header{Key: key, Value: value})
}
```

This allows multiple cookies to be set in a single response, as required by the HTTP specification.

## Implementation Challenges and Solutions

### Challenge 1: Multiple Set-Cookie Headers

**Problem:** HTTP allows multiple Set-Cookie headers, but RWeb's response only supported one value per header key.

**Solution:** Added `AddHeader` method specifically for headers that can appear multiple times.

### Challenge 2: Middleware Cookie Access

**Problem:** Middleware needs to read/write cookies before the main handler executes.

**Solution:** Cookie operations work seamlessly in middleware since they operate on the same context instance passed through the chain.

### Challenge 3: Test Infrastructure

**Problem:** Testing cookie operations requires simulating full HTTP request/response cycles.

**Solution:** Leveraged RWeb's existing `s.Request()` test helper, though it only returns the first Set-Cookie header.

## Security Considerations

### Default Security Settings

- **HttpOnly=true** - Prevents XSS attacks by blocking JavaScript access
- **SameSite=Lax** - Provides CSRF protection while allowing top-level navigation
- **Secure=auto** - Automatically enabled when TLS is detected
- **Path="/"** - Scoped to entire site by default

### Security Decision Tree

```
Is TLS enabled? → Yes → Secure=true
                → No  → Secure=false (unless explicitly set)

Is SameSite=None? → Yes → Force Secure=true (required by spec)
                  → No  → Use configured/default value
```

## Performance Characteristics

### Time Complexity
- Parse cookies: O(n) where n = number of cookies (once per request)
- Get cookie: O(1) after parsing
- Set cookie: O(1)
- Delete cookie: O(1)

### Space Complexity
- Memory per request: O(n) where n = number of cookies
- Context reuse: Cookie map is cleared but not deallocated between requests

## Integration Points

### 1. With Request Processing
- Cookies are parsed from the "Cookie" request header
- Uses Go's standard `http.Request.Cookies()` for compatibility

### 2. With Response Writing
- Cookies are written as "Set-Cookie" response headers
- Uses Go's standard `http.Cookie.String()` for formatting

### 3. With Middleware
- Cookie operations are available at any point in the middleware chain
- Common pattern: Auth middleware reading session cookies

### 4. With Groups
- Cookie-based auth works seamlessly with route groups
- Group middleware can enforce cookie requirements

## Future Enhancements

### Planned Features
1. **Cookie Encryption** - The `EncryptionKey` field is reserved for automatic cookie value encryption
2. **Cookie Signing** - HMAC-based signatures for tamper detection
3. **SameSite Heuristics** - Auto-detect when to use Strict vs Lax based on route

### Extension Points
- Cookie parsing/formatting can be customized by replacing the conversion methods
- Server-wide cookie defaults can be extended with more options
- Cookie storage backend could be abstracted for distributed systems

## Best Practices

### For Framework Users
1. Use `SetCookie` for simple session cookies
2. Use `SetCookieWithOptions` only when you need non-default attributes
3. Always check errors from `GetCookie` 
4. Use `GetCookieAndClear` for flash messages
5. Let the framework handle security defaults

### For Framework Maintainers
1. Maintain compatibility with `net/http.Cookie`
2. Keep the simple API simple (SetCookie with just name/value)
3. Ensure security defaults stay current with web standards
4. Document any breaking changes to cookie behavior

## Testing Strategy

### Unit Tests
- Test each cookie operation in isolation
- Verify security defaults are applied correctly
- Test edge cases (empty names, special characters)

### Integration Tests
- Test cookies in middleware chains
- Test cookies with route groups
- Test multiple cookies in single response
- Test cookie lifecycle (set, read, delete)

### Example Test Pattern
```go
func TestCookieOperation(t *testing.T) {
    s := rweb.NewServer()
    s.Get("/test", handler)
    
    // Set cookie
    response := s.Request("GET", "/test", nil, nil)
    
    // Verify cookie in response
    cookie := response.Header("Set-Cookie")
    assert.Contains(t, cookie, "expected=value")
}
```

## Conclusion

The cookie implementation in RWeb provides a clean, secure, and performant solution for HTTP cookie management. By building on Go's standard library while providing a simpler API, it achieves the goal of making common tasks easy while keeping advanced use cases possible.