// Package rweb_test contains tests for the route groups functionality.
// These tests verify that groups properly organize routes with common prefixes
// and correctly apply middleware to grouped routes.
package rweb_test

import (
	"net/http"
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
// within groups, including consecutive parameters.
func TestGroupWithParameters(t *testing.T) {
	s := rweb.NewServer()

	// Create users group for RESTful user endpoints
	users := s.Group("/users")

	// Consecutive parameters test: /users/:year/:title
	users.Get("/:year/:title", func(ctx rweb.Context) error {
		year := ctx.Request().Param("year")
		title := ctx.Request().Param("title")
		return ctx.WriteText("sermon " + year + " " + title)
	})

	// Nested parameters with static segment: /users/:year/posts/:postId
	// Note: Since this route shares the same first parameter position as /:year/:title,
	// **we must use the same parameter name "year" for the first parameter**
	users.Get("/:year/posts/:postId", func(ctx rweb.Context) error {
		userID := ctx.Request().Param("year")
		postID := ctx.Request().Param("postId")
		return ctx.WriteText("user " + userID + " post " + postID)
	})

	// TO BE NOTED: First Parameter cannot have different names - below does not work
	/*	ser := s.Group("/sermons", authctlr.UseCustomContextRWeb)
		// ser.Get("", sermon_controller.ListSermonsRWeb)
		ser.Get("/:id", sermon_controller.ShowSermonRWeb) //"/:id" -> conflicts with "/:year/:filename") - the lookup algo needs words to determine direction
		ser.Get("/:year/:filename", func(ctx rweb.Context) error {
			year := ctx.Request().Param("year")
			filename := ctx.Request().Param("filename")
			fmt.Println("**->> year:", year, "filename:", filename)
		})
	*/

	// Test consecutive parameters: /users/:year/:title
	response := s.Request("GET", "/users/2024/easter-message", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "sermon 2024 easter-message", string(response.Body()))

	// Test with file extension in parameter
	response = s.Request("GET", "/users/2020/1SAM8-08-16-15.MP3", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "sermon 2020 1SAM8-08-16-15.MP3", string(response.Body()))

	// Test multiple parameters with static segment: /users/:id/posts/:postId
	response = s.Request("GET", "/users/123/posts/456", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user 123 post 456", string(response.Body()))
}
