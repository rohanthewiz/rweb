package rweb

import (
	"path"
)

// Group represents a route group with a common prefix and middleware
type Group struct {
	prefix   string
	server   *Server
	handlers []Handler
}

// Group creates a sub-group with additional prefix and optional middleware
func (g *Group) Group(prefix string, handlers ...Handler) *Group {
	return &Group{
		prefix:   path.Join(g.prefix, prefix),
		server:   g.server,
		handlers: append(g.handlers, handlers...),
	}
}

// Use adds middleware to the group
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

// StaticFiles serves static files with the group prefix
func (g *Group) StaticFiles(reqDir string, targetDir string, nbrOfTokensToStrip int) {
	fullPath := path.Join(g.prefix, reqDir)
	g.server.StaticFiles(fullPath, targetDir, nbrOfTokensToStrip)
}

// Proxy sets up a reverse proxy with the group prefix
func (g *Group) Proxy(pathPrefix string, targetURL string, prefixTokensToRemove int) error {
	fullPath := path.Join(g.prefix, pathPrefix)
	return g.server.Proxy(fullPath, targetURL, prefixTokensToRemove)
}

// SSEHandler returns a handler that sets up Server-Sent Events with the group prefix
func (g *Group) SSEHandler(eventsChan <-chan any, eventName ...string) Handler {
	return g.server.SSEHandler(eventsChan, eventName...)
}

// addRoute is a helper that adds a route with the group prefix and middleware
func (g *Group) addRoute(method, routePath string, handler Handler) {
	fullPath := path.Join("/", g.prefix, routePath)
	
	// Build the middleware chain
	finalHandler := handler
	
	// Wrap in reverse order so they execute in the order they were added
	for i := len(g.handlers) - 1; i >= 0; i-- {
		// Capture the middleware and next handler in the closure
		middleware := g.handlers[i]
		nextHandler := finalHandler
		
		finalHandler = func(ctx Context) error {
			// Track if Next() was called
			nextCalled := false
			
			// Create a wrapper that tracks Next() calls
			wrapper := &contextWrapper{
				Context: ctx,
				next: func() error {
					nextCalled = true
					return nextHandler(ctx)
				},
			}
			
			// Execute the middleware with our wrapper
			err := middleware(wrapper)
			
			// If middleware didn't call Next() and no error, call next handler
			if err == nil && !nextCalled {
				err = nextHandler(ctx)
			}
			
			return err
		}
	}
	
	g.server.AddMethod(method, fullPath, finalHandler)
}

// contextWrapper wraps a Context to intercept Next() calls
type contextWrapper struct {
	Context
	next func() error
}

// Next overrides the Context's Next method
func (w *contextWrapper) Next() error {
	return w.next()
}