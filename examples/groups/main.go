// Package main demonstrates how to use route groups in rweb for organizing
// routes with common prefixes and middleware.
// This example shows API versioning, authentication, and role-based access control.
package main

import (
	"fmt"
	"log"

	"github.com/rohanthewiz/rweb"
)

func main() {
	// Create server with verbose logging to see request handling
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})

	// Global middleware - applies to all routes in the server
	s.Use(rweb.RequestInfo) // Logs all requests

	// Public routes - accessible without authentication
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the API</h1><p>Visit /api/v1/status for API status</p>")
	})

	// API group - all routes under this group will have /api prefix
	api := s.Group("/api")

	// API v1 - nested group creates /api/v1 prefix
	// This pattern allows easy API versioning
	v1 := api.Group("/v1")
	// Status endpoint: GET /api/v1/status
	v1.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]string{
			"status":  "ok",
			"version": "1.0",
		})
	})

	// Users group - all routes require authentication via authMiddleware
	// Creates routes like: GET /api/v1/users, GET /api/v1/users/:id, etc.
	users := v1.Group("/users", authMiddleware)
	users.Get("/", listUsers)        // GET /api/v1/users
	users.Get("/:id", getUser)       // GET /api/v1/users/:id
	users.Post("/", createUser)      // POST /api/v1/users
	users.Put("/:id", updateUser)    // PUT /api/v1/users/:id
	users.Delete("/:id", deleteUser) // DELETE /api/v1/users/:id

	// Admin routes - require both authentication AND admin privileges
	// Middleware executes in order: authMiddleware -> adminMiddleware -> handler
	admin := s.Group("/admin", authMiddleware, adminMiddleware)
	// Admin dashboard: GET /admin/dashboard
	admin.Get("/dashboard", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Admin Dashboard</h1><p>Welcome, admin!</p>")
	})
	// Admin user list with role information: GET /admin/users
	admin.Get("/users", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]interface{}{
			"users": []map[string]string{
				{"id": "1", "name": "John", "role": "admin"},
				{"id": "2", "name": "Jane", "role": "user"},
			},
		})
	})
	// Admin user deletion: DELETE /admin/users/:id
	admin.Delete("/users/:id", func(ctx rweb.Context) error {
		id := ctx.Request().PathParam("id")
		return ctx.WriteJSON(map[string]string{
			"message": fmt.Sprintf("User %s deleted by admin", id),
		})
	})

	// Static files group - demonstrates grouping non-API routes
	static := s.Group("/static")
	static.Get("/version", func(ctx rweb.Context) error {
		return ctx.WriteText("Static files version 1.0")
	})
	// In production, you might serve actual static files:
	// static.StaticFiles("/assets", "./public/assets", 0)

	// Print usage instructions
	fmt.Println("Server starting on http://localhost:8080")
	fmt.Println("Try these endpoints:")
	fmt.Println("  GET  /")
	fmt.Println("  GET  /api/v1/status")
	fmt.Println("  GET  /api/v1/users (requires auth header)")
	fmt.Println("  GET  /admin/dashboard (requires auth + admin)")

	if err := s.Run(); err != nil {
		log.Fatal(err)
	}
}

// authMiddleware validates authentication tokens.
// In a real application, this would validate JWT tokens or session cookies.
// For this demo, it expects "Authorization: Bearer valid-token" header.
func authMiddleware(ctx rweb.Context) error {
	// Check for auth token in Authorization header
	authHeader := ctx.Request().Header("Authorization")
	if authHeader == "" {
		return ctx.SetStatus(401).WriteJSON(map[string]string{
			"error": "Authentication required",
		})
	}

	// Simple token validation for demo purposes
	// Real implementation would decode/verify JWT or lookup session
	if authHeader != "Bearer valid-token" {
		return ctx.SetStatus(401).WriteJSON(map[string]string{
			"error": "Invalid token",
		})
	}

	// Store user info in context for use by handlers
	// This data is available to all subsequent middleware and handlers
	ctx.Set("userId", "123")
	ctx.Set("isAuthenticated", true)

	// Continue to next middleware/handler
	return ctx.Next()
}

// adminMiddleware checks if authenticated user has admin privileges.
// This runs after authMiddleware, so we know the user is authenticated.
// For demo purposes, it checks for "X-Admin: true" header.
// Real implementation would check user roles from database or JWT claims.
func adminMiddleware(ctx rweb.Context) error {
	// Check if user is admin (in real app, check from DB or JWT claims)
	// For demo, we'll check a specific header
	if ctx.Request().Header("X-Admin") != "true" {
		return ctx.SetStatus(403).WriteJSON(map[string]string{
			"error": "Admin access required",
		})
	}

	// Mark user as admin in context
	ctx.Set("isAdmin", true)
	return ctx.Next()
}

// listUsers handles GET /api/v1/users
// Returns a list of users (mock data for demo)
func listUsers(ctx rweb.Context) error {
	return ctx.WriteJSON(map[string]interface{}{
		"users": []map[string]string{
			{"id": "1", "name": "John Doe"},
			{"id": "2", "name": "Jane Smith"},
		},
	})
}

// getUser handles GET /api/v1/users/:id
// Returns details for a specific user
func getUser(ctx rweb.Context) error {
	// Extract URL parameter
	id := ctx.Request().PathParam("id")
	return ctx.WriteJSON(map[string]string{
		"id":    id,
		"name":  "John Doe",
		"email": "john@example.com",
	})
}

// createUser handles POST /api/v1/users
// Creates a new user (mock implementation)
func createUser(ctx rweb.Context) error {
	// In real app, you would:
	// 1. Parse request body with ctx.Request().Body()
	// 2. Validate the data
	// 3. Save to database
	return ctx.WriteJSON(map[string]string{
		"id":      "3",
		"message": "User created successfully",
	})
}

// updateUser handles PUT /api/v1/users/:id
// Updates an existing user
func updateUser(ctx rweb.Context) error {
	id := ctx.Request().PathParam("id")
	// In real app: parse body, validate, update database
	return ctx.WriteJSON(map[string]string{
		"id":      id,
		"message": "User updated successfully",
	})
}

// deleteUser handles DELETE /api/v1/users/:id
// Deletes a user (mock implementation)
func deleteUser(ctx rweb.Context) error {
	id := ctx.Request().PathParam("id")
	// In real app: check permissions, delete from database
	return ctx.WriteJSON(map[string]string{
		"id":      id,
		"message": "User deleted successfully",
	})
}
