package rweb_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

func TestGroup(t *testing.T) {
	s := rweb.NewServer()

	// Test basic group
	api := s.Group("/api")
	api.Get("/users", func(ctx rweb.Context) error {
		return ctx.WriteText("users list")
	})
	api.Post("/users", func(ctx rweb.Context) error {
		return ctx.WriteText("user created")
	})

	// Test response
	response := s.Request("GET", "/api/users", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "users list", string(response.Body()))

	response = s.Request("POST", "/api/users", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user created", string(response.Body()))

	// Non-existent route
	response = s.Request("GET", "/users", nil, nil)
	assert.Equal(t, http.StatusNotFound, response.Status())
}

func TestGroupMiddleware(t *testing.T) {
	s := rweb.NewServer()

	// Track middleware execution
	var executionOrder []string

	// Server-level middleware
	s.Use(func(ctx rweb.Context) error {
		executionOrder = append(executionOrder, "server-middleware")
		return ctx.Next()
	})

	// Group with middleware
	api := s.Group("/api", func(ctx rweb.Context) error {
		executionOrder = append(executionOrder, "api-middleware")
		ctx.Response().SetHeader("X-API", "true")
		return ctx.Next()
	})

	api.Get("/test", func(ctx rweb.Context) error {
		executionOrder = append(executionOrder, "handler")
		return ctx.WriteText("test response")
	})

	// Test request
	executionOrder = []string{} // Reset
	response := s.Request("GET", "/api/test", nil, nil)
	
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "test response", string(response.Body()))
	assert.Equal(t, "true", response.Header("X-API"))
	assert.Equal(t, 3, len(executionOrder))
	assert.Equal(t, "server-middleware", executionOrder[0])
	assert.Equal(t, "api-middleware", executionOrder[1])
	assert.Equal(t, "handler", executionOrder[2])
}

func TestNestedGroups(t *testing.T) {
	s := rweb.NewServer()

	// Create nested groups
	api := s.Group("/api")
	v1 := api.Group("/v1")
	v2 := api.Group("/v2")

	v1.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteText("v1 status")
	})

	v2.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteText("v2 status")
	})

	// Test both versions
	response := s.Request("GET", "/api/v1/status", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "v1 status", string(response.Body()))

	response = s.Request("GET", "/api/v2/status", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "v2 status", string(response.Body()))
}

func TestGroupAllMethods(t *testing.T) {
	s := rweb.NewServer()
	api := s.Group("/api")

	// Register all HTTP methods
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

	// Test each method
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	for _, method := range methods {
		response := s.Request(method, "/api/resource", nil, nil)
		assert.Equal(t, http.StatusOK, response.Status())
		assert.Equal(t, method, string(response.Body()))
	}

	// Test HEAD
	response := s.Request("HEAD", "/api/resource", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "HEAD", response.Header("X-Method"))
}

func TestGroupMiddlewareIndependence(t *testing.T) {
	s := rweb.NewServer()

	// Create two groups with different middleware
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

	// Test auth group
	response := s.Request("GET", "/auth/profile", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "auth profile", string(response.Body()))
	assert.Equal(t, "required", response.Header("X-Auth"))
	assert.Equal(t, "", response.Header("X-Public")) // Should not have public header

	// Test public group
	response = s.Request("GET", "/public/info", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "public info", string(response.Body()))
	assert.Equal(t, "true", response.Header("X-Public"))
	assert.Equal(t, "", response.Header("X-Auth")) // Should not have auth header
}

func TestGroupUseMethod(t *testing.T) {
	s := rweb.NewServer()

	var middlewareOrder []string

	api := s.Group("/api")
	
	// Add middleware after group creation
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

	// Test execution order
	middlewareOrder = []string{}
	response := s.Request("GET", "/api/test", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, 3, len(middlewareOrder))
	assert.Equal(t, "first", middlewareOrder[0])
	assert.Equal(t, "second", middlewareOrder[1])
	assert.Equal(t, "handler", middlewareOrder[2])
}

func TestGroupErrorHandling(t *testing.T) {
	s := rweb.NewServer()

	api := s.Group("/api", func(ctx rweb.Context) error {
		// Middleware that might error
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

	// Test successful request
	response := s.Request("GET", "/api/test", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "success", string(response.Body()))

	// Test error in middleware
	response = s.Request("GET", "/api/error", nil, nil)
	assert.Equal(t, http.StatusInternalServerError, response.Status())
	// The default error handler should handle it
	assert.True(t, strings.Contains(string(response.Body()), "Internal Server Error"))
}

func TestGroupStaticFiles(t *testing.T) {
	s := rweb.NewServer()

	// Create a group for assets
	assets := s.Group("/assets")
	
	// This would serve files from the local "static" directory
	// when requests come to /assets/static/*
	// Note: We can't test this fully without actual files
	assets.StaticFiles("/static", "./testdata", 0)

	// Just verify the route is registered properly
	// In a real test, you'd need actual static files to serve
}

func TestGroupProxy(t *testing.T) {
	s := rweb.NewServer()

	// Create API group
	api := s.Group("/api")
	
	// Set up proxy (this would forward /api/external/* to another server)
	// Note: We can't test this fully without a target server
	err := api.Proxy("/external", "http://example.com", 0)
	assert.Nil(t, err)
}

func TestGroupWithParameters(t *testing.T) {
	s := rweb.NewServer()

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

	// Test parameter extraction
	response := s.Request("GET", "/users/123", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user 123", string(response.Body()))

	response = s.Request("GET", "/users/123/posts/456", nil, nil)
	assert.Equal(t, http.StatusOK, response.Status())
	assert.Equal(t, "user 123 post 456", string(response.Body()))
}