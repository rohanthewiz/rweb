package rweb

import (
	"path"
	"slices"
)

// Group represents a route group with a common prefix and middleware.
// This allows organizing routes under a common URL prefix (e.g., /api/v1)
// and applying middleware that only affects routes within this group.
// Groups can be nested to create hierarchical route structures.
type Group struct {
	// prefix is the URL path prefix for all routes in this group
	prefix string
	// server is a reference to the main server instance for route registration
	server *Server
	// handlers contains middleware functions that will be applied to all routes in this group
	handlers []Handler
}

// Group creates a sub-group with additional prefix and optional middleware.
// The new group inherits all middleware from the parent group and can add its own.
// Example: apiGroup.Group("/users", authMiddleware) creates /api/users with auth.
func (g *Group) Group(prefix string, handlers ...Handler) *Group {
	return &Group{
		// Combine parent and child prefixes using path.Join for proper URL construction
		prefix: path.Join(g.prefix, prefix),
		server: g.server,
		// Inherit parent middleware and append any new middleware.
		// slices.Concat always allocates a fresh backing array. A plain
		// append(g.handlers, handlers...) would reuse the parent's array
		// whenever it has spare capacity (e.g. after a couple of Use calls),
		// so sibling sub-groups would write into the same slots and the
		// first child would silently run the second child's middleware.
		// An own array also keeps a later child.Use from reaching the parent.
		handlers: slices.Concat(g.handlers, handlers),
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
// The group's middleware (as it stands when this is called) runs in front of
// the file handler, as it does for any other route of the group, so files
// under an authenticated group are behind that authentication.
// reqDir is the URL path relative to the group prefix.
// targetDir is the local filesystem directory containing the files.
// nbrOfTokensToStrip removes URL path segments when mapping to filesystem paths.
func (g *Group) StaticFiles(reqDir string, targetDir string, nbrOfTokensToStrip int) {
	fullPath := path.Join(g.prefix, reqDir)
	g.server.staticFiles(fullPath, targetDir, nbrOfTokensToStrip, false, g.wrap)
}

// StaticFilesAbs is StaticFiles for a targetDir given as an absolute
// filesystem path. See Server.StaticFilesAbs for why it is a separate method.
func (g *Group) StaticFilesAbs(reqDir string, targetDir string, nbrOfTokensToStrip int) {
	fullPath := path.Join(g.prefix, reqDir)
	g.server.staticFilesAbs(fullPath, targetDir, nbrOfTokensToStrip, g.wrap)
}

// Proxy sets up a reverse proxy with the group prefix.
// The group's middleware runs in front of the proxy handler, as for any other
// route of the group.
// pathPrefix is the URL path relative to the group prefix.
// targetURL is the backend server URL to proxy requests to.
// prefixTokensToRemove strips URL segments before forwarding the request.
func (g *Group) Proxy(pathPrefix string, targetURL string, prefixTokensToRemove int) error {
	fullPath := path.Join(g.prefix, pathPrefix)
	return g.server.proxy(fullPath, targetURL, prefixTokensToRemove, g.wrap)
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

	g.server.AddMethod(method, fullPath, g.wrap(handler))
}

// wrap returns handler with the group's middleware in front of it.
//
// It is separate from addRoute because not every route of a group is
// registered through addRoute: StaticFiles, StaticFilesAbs and Proxy build
// their handler and their routes (a wildcard, several methods) inside the
// Server. Those used to register straight on the server, so a group's
// middleware silently did not apply to them, and files or an upstream placed
// under an authenticated group were served to anyone. They now take wrap as
// the hook that puts the group's chain in front of whatever they register.
//
// The middleware slice is read here, at registration, so (as with any group
// route) middleware added by a later Use does not apply to routes already
// registered.
func (g *Group) wrap(handler Handler) Handler {
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
			// Create a context wrapper that intercepts Next() calls, so that
			// Next() inside group middleware advances this group chain rather
			// than the server-level chain the route itself sits at the end of.
			//
			// The chain continues only through Next(). Middleware that returns
			// (nil or an error) without calling it ends the request there, the
			// same contract as s.Use middleware. An earlier version continued
			// automatically on a nil return, which let a group auth middleware
			// that wrote a 401 and returned nil still run the route handler.
			wrapper := &contextWrapper{
				Context: ctx,
				next: func() error {
					return nextHandler(ctx)
				},
			}

			return middleware(wrapper)
		}
	}

	return finalHandler
}

// contextWrapper wraps a Context to intercept Next() calls.
// This allows group middleware to properly control the execution chain:
// calling Next() passes control on, returning without it stops the chain.
type contextWrapper struct {
	// Embedded Context provides all standard context methods
	Context
	// next runs the rest of the group chain (later middleware, then the route handler)
	next func() error
}

// Next overrides the Context's Next method to use our custom implementation.
// It is the only way group middleware passes control to the next handler
// in the chain.
func (w *contextWrapper) Next() error {
	return w.next()
}
