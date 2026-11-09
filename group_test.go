// Package rweb_test contains tests for the route groups functionality.
// These tests verify that groups properly organize routes with common prefixes
// and correctly apply middleware to grouped routes.
package rweb_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// TestGroup verifies basic group functionality including route registration
// with prefixes and proper routing to group handlers.
func TestGroup(t *testing.T) {
	s := rweb.NewServer()

	// Create a group with /api prefix
	// All routes registered on this group will have /api prepended
	api := s.Group("/api")
	api.Get("/users", func(ctx rweb.Context) error {
		return ctx.WriteText("users list")
	})
	api.Post("/users", func(ctx rweb.Context) error {
		return ctx.WriteText("user created")
	})

	// Verify GET /api/users works correctly
	response := s.Request("GET", "/api/users", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "users list", string(response.Body()))

	// Verify POST /api/users works correctly
	response = s.Request("POST", "/api/users", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user created", string(response.Body()))

	// Verify that /users without the /api prefix returns 404
	response = s.Request("GET", "/users", nil, nil)
	assert.Equal(t, http.StatusNotFound, response.Status())
}

// TestGroupMiddleware verifies that middleware applied to groups
// executes correctly and in the proper order.
func TestGroupMiddleware(t *testing.T) {
	s := rweb.NewServer()

	// Track middleware execution order to verify correct chaining
	var executionOrder []string

	// Server-level middleware applies to all routes
	s.Use(func(ctx rweb.Context) error {
		executionOrder = append(executionOrder, "server-middleware")
		return ctx.Next()
	})

	// Group with middleware that only applies to routes in this group
	// Middleware can modify response headers or perform auth checks
	api := s.Group("/api", func(ctx rweb.Context) error {
		executionOrder = append(executionOrder, "api-middleware")
		ctx.Response().SetHeader("X-API", "true")
		return ctx.Next()
	})

	api.Get("/test", func(ctx rweb.Context) error {
		executionOrder = append(executionOrder, "handler")
		return ctx.WriteText("test response")
	})

	// Test request to verify middleware execution order
	executionOrder = []string{} // Reset
	response := s.Request("GET", "/api/test", nil, nil)

	// Verify response is correct
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "test response", string(response.Body()))
	assert.Equal(t, "true", response.Header("X-API"))

	// Verify middleware executed in correct order:
	// server -> group -> handler
	assert.Equal(t, 3, len(executionOrder))
	assert.Equal(t, "server-middleware", executionOrder[0])
	assert.Equal(t, "api-middleware", executionOrder[1])
	assert.Equal(t, "handler", executionOrder[2])
}

// TestNestedGroups verifies that groups can be nested to create
// hierarchical route structures like API versioning.
func TestNestedGroups(t *testing.T) {
	s := rweb.NewServer()

	// Create nested groups for API versioning
	// api group creates /api prefix
	api := s.Group("/api")
	// v1 and v2 groups create /api/v1 and /api/v2 prefixes
	v1 := api.Group("/v1")
	v2 := api.Group("/v2")

	v1.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteText("v1 status")
	})

	v2.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteText("v2 status")
	})

	// Verify v1 endpoint: GET /api/v1/status
	response := s.Request("GET", "/api/v1/status", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "v1 status", string(response.Body()))

	// Verify v2 endpoint: GET /api/v2/status
	response = s.Request("GET", "/api/v2/status", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "v2 status", string(response.Body()))
}

// TestGroupAllMethods verifies that groups support all HTTP methods
// (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS, etc.).
func TestGroupAllMethods(t *testing.T) {
	s := rweb.NewServer()
	api := s.Group("/api")

	// Register handlers for all supported HTTP methods
	api.Get("/resource", func(ctx rweb.Context) error {
		return ctx.WriteText("GET")
	})
	api.Post("/resource", func(ctx rweb.Context) error {
		return ctx.WriteText("POST")
	})
	api.Put("/resource", func(ctx rweb.Context) error {
		return ctx.WriteText("PUT")
	})
	api.Patch("/resource", func(ctx rweb.Context) error {
		return ctx.WriteText("PATCH")
	})
	api.Delete("/resource", func(ctx rweb.Context) error {
		return ctx.WriteText("DELETE")
	})
	api.Head("/resource", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("X-Method", "HEAD")
		return nil
	})
	api.Options("/resource", func(ctx rweb.Context) error {
		return ctx.WriteText("OPTIONS")
	})

	// Test each method (except HEAD which doesn't return body)
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	for _, method := range methods {
		response := s.Request(method, "/api/resource", nil, nil)
		assert.Equal(t, http.StatusOK, response.Status())
		assert.Equal(t, method, string(response.Body()))
	}

	// Test HEAD separately as it only sets headers, no body
	response := s.Request("HEAD", "/api/resource", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "HEAD", response.Header("X-Method"))
}

// TestGroupMiddlewareIndependence verifies that middleware applied to one group
// doesn't affect other groups, ensuring proper isolation.
func TestGroupMiddlewareIndependence(t *testing.T) {
	s := rweb.NewServer()

	// Create two groups with different middleware to test isolation
	auth := s.Group("/auth", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("X-Auth", "required")
		return ctx.Next()
	})

	public := s.Group("/public", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("X-Public", "true")
		return ctx.Next()
	})

	auth.Get("/profile", func(ctx rweb.Context) error {
		return ctx.WriteText("auth profile")
	})

	public.Get("/info", func(ctx rweb.Context) error {
		return ctx.WriteText("public info")
	})

	// Test auth group - should only have X-Auth header
	response := s.Request("GET", "/auth/profile", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "auth profile", string(response.Body()))
	assert.Equal(t, "required", response.Header("X-Auth"))
	assert.Equal(t, "", response.Header("X-Public")) // Should not have public header

	// Test public group - should only have X-Public header
	response = s.Request("GET", "/public/info", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "public info", string(response.Body()))
	assert.Equal(t, "true", response.Header("X-Public"))
	assert.Equal(t, "", response.Header("X-Auth")) // Should not have auth header
}

// TestGroupUseMethod verifies that the Use() method on groups correctly
// adds middleware that applies to all routes in the group.
func TestGroupUseMethod(t *testing.T) {
	s := rweb.NewServer()

	var middlewareOrder []string

	api := s.Group("/api")

	// Add middleware after group creation using Use() method
	api.Use(func(ctx rweb.Context) error {
		middlewareOrder = append(middlewareOrder, "first")
		return ctx.Next()
	})

	api.Use(func(ctx rweb.Context) error {
		middlewareOrder = append(middlewareOrder, "second")
		return ctx.Next()
	})

	api.Get("/test", func(ctx rweb.Context) error {
		middlewareOrder = append(middlewareOrder, "handler")
		return ctx.WriteText("done")
	})

	// Verify middleware executes in the order it was added
	middlewareOrder = []string{}
	response := s.Request("GET", "/api/test", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, 3, len(middlewareOrder))
	assert.Equal(t, "first", middlewareOrder[0])
	assert.Equal(t, "second", middlewareOrder[1])
	assert.Equal(t, "handler", middlewareOrder[2])
}

// TestGroupErrorHandling verifies that errors in group middleware
// are properly handled and stop the middleware chain.
func TestGroupErrorHandling(t *testing.T) {
	s := rweb.NewServer()

	// Group with middleware that returns error for specific path
	api := s.Group("/api", func(ctx rweb.Context) error {
		// Simulate error condition for /api/error path
		if ctx.Request().Path() == "/api/error" {
			return ctx.Error("middleware error")
		}
		return ctx.Next()
	})

	api.Get("/test", func(ctx rweb.Context) error {
		return ctx.WriteText("success")
	})

	api.Get("/error", func(ctx rweb.Context) error {
		return ctx.WriteText("should not reach here")
	})

	// Test successful request - middleware passes through
	response := s.Request("GET", "/api/test", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "success", string(response.Body()))

	// Test error in middleware - handler should not be reached
	response = s.Request("GET", "/api/error", nil, nil)
	assert.Equal(t, http.StatusInternalServerError, response.Status())
	// The default error handler should handle it
	assert.True(t, strings.Contains(string(response.Body()), "Internal Server Error"))
}

// TestGroupStaticFiles verifies that groups can serve static files
// with the group prefix applied.
func TestGroupStaticFiles(t *testing.T) {
	s := rweb.NewServer()

	// Create a group for serving static assets
	assets := s.Group("/assets")

	// Register static file handler - this would serve files from ./testdata
	// when requests come to /assets/static/*
	// The 0 parameter means no path segments are stripped
	assets.StaticFiles("/static", "./testdata", 0)

	// Note: Full testing would require actual static files
	// This test just verifies the route registration doesn't panic
}

// TestGroupProxy verifies that groups can set up reverse proxies
// with the group prefix applied.
func TestGroupProxy(t *testing.T) {
	s := rweb.NewServer()

	// Create API group for proxying external services
	api := s.Group("/api")

	// Set up proxy - this would forward /api/external/* to http://example.com
	// The 0 parameter means no path segments are stripped before forwarding
	err := api.Proxy("/external", "http://example.com", 0)
	assert.Nil(t, err)

	// Note: Full testing would require a target server to proxy to
}

// TestGroupWithParameters verifies that route parameters work correctly
// within groups, including nested parameters.
func TestGroupWithParameters(t *testing.T) {
	s := rweb.NewServer()

	// Create users group for RESTful user endpoints
	users := s.Group("/users")

	users.Get("/:id", func(ctx rweb.Context) error {
		id := ctx.Request().Param("id")
		return ctx.WriteText("user " + id)
	})

	users.Get("/:id/posts/:postId", func(ctx rweb.Context) error {
		userID := ctx.Request().Param("id")
		postID := ctx.Request().Param("postId")
		return ctx.WriteText("user " + userID + " post " + postID)
	})

	// Test single parameter: /users/:id
	response := s.Request("GET", "/users/123", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user 123", string(response.Body()))

	// Test multiple parameters: /users/:id/posts/:postId
	response = s.Request("GET", "/users/123/posts/456", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user 123 post 456", string(response.Body()))
}

// TestSiblingSubGroupsKeepOwnMiddleware verifies that two sub-groups created
// from the same parent each keep their own middleware. The parent is given
// spare slice capacity (mw + two Use calls: len 3, cap 4) so that a child
// built with append(parent, ...) would share the parent's backing array and
// the second child's middleware would overwrite the first child's.
func TestSiblingSubGroupsKeepOwnMiddleware(t *testing.T) {
	s := rweb.NewServer()

	// mark returns a middleware that appends its name to X-Trace
	mark := func(name string) rweb.Handler {
		return func(ctx rweb.Context) error {
			ctx.Response().SetHeader("X-Trace", ctx.Response().Header("X-Trace")+name+";")
			return ctx.Next()
		}
	}

	g := s.Group("/api", mark("mw"))
	g.Use(mark("u1"))
	g.Use(mark("u2"))

	a := g.Group("/a", mark("A"))
	b := g.Group("/b", mark("B"))

	a.Get("/x", func(ctx rweb.Context) error { return ctx.WriteText("a") })
	b.Get("/x", func(ctx rweb.Context) error { return ctx.WriteText("b") })

	response := s.Request("GET", "/api/a/x", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "a", string(response.Body()))
	assert.Equal(t, "mw;u1;u2;A;", response.Header("X-Trace"))

	response = s.Request("GET", "/api/b/x", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "b", string(response.Body()))
	assert.Equal(t, "mw;u1;u2;B;", response.Header("X-Trace"))

	// Use on a child must not leak into the parent or a sibling either
	a.Use(mark("A2"))
	g.Get("/y", func(ctx rweb.Context) error { return ctx.WriteText("g") })
	b.Get("/y", func(ctx rweb.Context) error { return ctx.WriteText("b") })

	response = s.Request("GET", "/api/y", nil, nil)
	assert.Equal(t, "mw;u1;u2;", response.Header("X-Trace"))
	response = s.Request("GET", "/api/b/y", nil, nil)
	assert.Equal(t, "mw;u1;u2;B;", response.Header("X-Trace"))
}

// TestServerGroupDoesNotAliasCallerSlice verifies that s.Group copies the
// middleware slice it is given. Two groups built from the same caller slice
// with spare capacity would otherwise share it, and Use on one would
// overwrite the other's appended middleware.
func TestServerGroupDoesNotAliasCallerSlice(t *testing.T) {
	s := rweb.NewServer()

	mark := func(name string) rweb.Handler {
		return func(ctx rweb.Context) error {
			ctx.Response().SetHeader("X-Trace", ctx.Response().Header("X-Trace")+name+";")
			return ctx.Next()
		}
	}

	mws := make([]rweb.Handler, 1, 4)
	mws[0] = mark("mw")

	g1 := s.Group("/one", mws...)
	g2 := s.Group("/two", mws...)
	g1.Use(mark("U1"))
	g2.Use(mark("U2"))

	g1.Get("/x", func(ctx rweb.Context) error { return ctx.WriteText("1") })
	g2.Get("/x", func(ctx rweb.Context) error { return ctx.WriteText("2") })

	response := s.Request("GET", "/one/x", nil, nil)
	assert.Equal(t, "mw;U1;", response.Header("X-Trace"))
	response = s.Request("GET", "/two/x", nil, nil)
	assert.Equal(t, "mw;U2;", response.Header("X-Trace"))
}

// TestGroupMiddlewareStopsChain verifies that group middleware which returns
// nil without calling Next() ends the request there, as s.Use middleware
// does. A denying auth middleware must not let the route handler run.
func TestGroupMiddlewareStopsChain(t *testing.T) {
	s := rweb.NewServer()

	handlerRan := false
	secured := s.Group("/secure", func(ctx rweb.Context) error {
		ctx.SetStatus(http.StatusUnauthorized)
		return ctx.WriteString("denied;")
	})
	secured.Get("/data", func(ctx rweb.Context) error {
		handlerRan = true
		return ctx.WriteString("SECRET")
	})

	response := s.Request("GET", "/secure/data", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, response.Status())
	assert.Equal(t, "denied;", string(response.Body()))
	assert.False(t, handlerRan)

	// A later middleware in the same group must not run either
	laterRan := false
	g := s.Group("/two", func(ctx rweb.Context) error {
		return nil // stop without writing
	})
	g.Use(func(ctx rweb.Context) error {
		laterRan = true
		return ctx.Next()
	})
	g.Get("/x", func(ctx rweb.Context) error {
		handlerRan = true
		return ctx.WriteString("x")
	})

	response = s.Request("GET", "/two/x", nil, nil)
	assert.False(t, laterRan)
	assert.False(t, handlerRan)
	assert.Equal(t, "", string(response.Body()))
}

// TestGroupMiddlewareCoversStaticAndProxy verifies that a group's middleware
// runs in front of the routes StaticFiles, StaticFilesAbs and Proxy register.
// Those are built inside the Server rather than through Group.addRoute, and
// used to be registered bare: files and an upstream mounted on an
// authenticated group were served to anyone.
func TestGroupMiddlewareCoversStaticAndProxy(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("SECRET"), 0o600))

	// The upstream records whether the proxy ever reached it.
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		_, _ = w.Write([]byte("UPSTREAM"))
	}))
	defer upstream.Close()

	s := rweb.NewServer()

	// allow is flipped by the test; the middleware denies by returning
	// without Next(), the way an auth check does.
	allow := false
	admin := s.Group("/admin", func(ctx rweb.Context) error {
		if !allow {
			return ctx.SetStatus(http.StatusUnauthorized).WriteString("denied")
		}
		return ctx.Next()
	})
	admin.StaticFilesAbs("/files/", dir, 2)
	assert.Nil(t, admin.Proxy("/px", upstream.URL, 2))

	// Denied: neither the file nor the upstream is reached.
	r := s.Request("GET", "/admin/files/secret.txt", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, r.Status())
	assert.Equal(t, "denied", string(r.Body()))

	for _, p := range []string{"/admin/px", "/admin/px/anything"} {
		r = s.Request("POST", p, nil, nil)
		assert.Equal(t, http.StatusUnauthorized, r.Status())
		assert.Equal(t, "denied", string(r.Body()))
	}
	assert.Equal(t, false, upstreamHit)

	// Allowed: the middleware passes control on and both work as before.
	allow = true
	r = s.Request("GET", "/admin/files/secret.txt", nil, nil)
	assert.Equal(t, http.StatusOK, r.Status())
	assert.Equal(t, "SECRET", string(r.Body()))

	r = s.Request("GET", "/admin/px/anything", nil, nil)
	assert.Equal(t, http.StatusOK, r.Status())
	assert.Equal(t, "UPSTREAM", string(r.Body()))
	assert.Equal(t, true, upstreamHit)
}
