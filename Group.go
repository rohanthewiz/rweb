package rweb

import (
	"path"
)

// Group represents a route group with a common prefix and middleware.
// This allows organizing routes under a common URL prefix (e.g., /api/v1)
// and applying middleware that only affects routes within this group.
// Groups can be nested to create hierarchical route structures.
type Group struct {
	// prefix is the URL path prefix for all routes in this group
	prefix   string
	// server is a reference to the main server instance for route registration
	server   *Server
	// handlers contains middleware functions that will be applied to all routes in this group
	handlers []Handler
}

// Group creates a sub-group with additional prefix and optional middleware.
// The new group inherits all middleware from the parent group and can add its own.
// Example: apiGroup.Group("/users", authMiddleware) creates /api/users with auth.
func (g *Group) Group(prefix string, handlers ...Handler) *Group {
	return &Group{
		// Combine parent and child prefixes using path.Join for proper URL construction
		prefix:   path.Join(g.prefix, prefix),
		server:   g.server,
		// Inherit parent middleware and append any new middleware
		handlers: append(g.handlers, handlers...),
	}
}

// Use adds middleware to the group.
// These middleware functions will be executed for all routes registered after this call.
// Middleware is executed in the order it was added.
func (g *Group) Use(handlers ...Handler) {
	g.handlers = append(g.handlers, handlers...)
}

// Get registers a GET route with the group prefix
func (g *Group) Get(path string, handler Handler) {
	g.addRoute("GET", path, handler)
}

// Post registers a POST route with the group prefix
func (g *Group) Post(path string, handler Handler) {
	g.addRoute("POST", path, handler)
}

// Put registers a PUT route with the group prefix
func (g *Group) Put(path string, handler Handler) {
	g.addRoute("PUT", path, handler)
}

// Patch registers a PATCH route with the group prefix
func (g *Group) Patch(path string, handler Handler) {
	g.addRoute("PATCH", path, handler)
}

// Delete registers a DELETE route with the group prefix
func (g *Group) Delete(path string, handler Handler) {
	g.addRoute("DELETE", path, handler)
}

// Head registers a HEAD route with the group prefix
func (g *Group) Head(path string, handler Handler) {
	g.addRoute("HEAD", path, handler)
}

// Options registers an OPTIONS route with the group prefix
func (g *Group) Options(path string, handler Handler) {
	g.addRoute("OPTIONS", path, handler)
}

// Connect registers a CONNECT route with the group prefix
func (g *Group) Connect(path string, handler Handler) {
	g.addRoute("CONNECT", path, handler)
}

// Trace registers a TRACE route with the group prefix
func (g *Group) Trace(path string, handler Handler) {
	g.addRoute("TRACE", path, handler)
}

// StaticFiles serves static files with the group prefix.
// reqDir is the URL path relative to the group prefix.
// targetDir is the local filesystem directory containing the files.
// nbrOfTokensToStrip removes URL path segments when mapping to filesystem paths.
func (g *Group) StaticFiles(reqDir string, targetDir string, nbrOfTokensToStrip int) {
	fullPath := path.Join(g.prefix, reqDir)
	g.server.StaticFiles(fullPath, targetDir, nbrOfTokensToStrip)
}

// Proxy sets up a reverse proxy with the group prefix.
// pathPrefix is the URL path relative to the group prefix.
// targetURL is the backend server URL to proxy requests to.
// prefixTokensToRemove strips URL segments before forwarding the request.
func (g *Group) Proxy(pathPrefix string, targetURL string, prefixTokensToRemove int) error {
	fullPath := path.Join(g.prefix, pathPrefix)
	return g.server.Proxy(fullPath, targetURL, prefixTokensToRemove)
}

// SSEHandler returns a handler that sets up Server-Sent Events with the group prefix.
// Note: The group prefix doesn't affect the SSE handler itself, but the route
// it's registered on will include the group prefix.
func (g *Group) SSEHandler(eventsChan <-chan any, eventName ...string) Handler {
	return g.server.SSEHandler(eventsChan, eventName...)
}

// addRoute is a helper that adds a route with the group prefix and middleware.
// It constructs the full path by combining the group prefix with the route path,
// then wraps the handler with all group middleware before registering it.
func (g *Group) addRoute(method, routePath string, handler Handler) {
	// Construct the full URL path by joining group prefix and route path
	// The leading "/" ensures proper path formatting
	fullPath := path.Join("/", g.prefix, routePath)
	
	// Build the middleware chain - start with the route handler as the final handler
	finalHandler := handler
	
	// Wrap handlers in reverse order to ensure they execute in the order they were added.
	// This creates a chain where each middleware wraps the next one.
	for i := len(g.handlers) - 1; i >= 0; i-- {
		// Capture the current middleware and next handler in the closure
		// to avoid closure variable issues in the loop
		middleware := g.handlers[i]
		nextHandler := finalHandler
		
		finalHandler = func(ctx Context) error {
			// Track whether the middleware called Next() to continue the chain.
			// This allows middleware to optionally stop the chain (e.g., for auth failures)
			nextCalled := false
			
			// Create a context wrapper that intercepts Next() calls.
			// This allows us to track when middleware explicitly passes control
			// to the next handler in the chain.
			wrapper := &contextWrapper{
				Context: ctx,
				next: func() error {
					nextCalled = true
					return nextHandler(ctx)
				},
			}
			
			// Execute the middleware with our wrapper context
			err := middleware(wrapper)
			
			// If middleware didn't call Next() and didn't return an error,
			// automatically continue to the next handler.
			// This allows middleware to work without explicitly calling Next().
			if err == nil && !nextCalled {
				err = nextHandler(ctx)
			}
			
			return err
		}
	}
	
	g.server.AddMethod(method, fullPath, finalHandler)
}

// contextWrapper wraps a Context to intercept Next() calls.
// This allows group middleware to properly track and control the execution chain,
// ensuring that middleware can stop the chain or pass control as needed.
type contextWrapper struct {
	// Embedded Context provides all standard context methods
	Context
	// next is our custom Next() implementation that tracks calls
	next func() error
}

// Next overrides the Context's Next method to use our custom implementation.
// This allows the group to track when middleware explicitly passes control
// to the next handler in the chain.
func (w *contextWrapper) Next() error {
	return w.next()
}