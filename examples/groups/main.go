package main

import (
	"fmt"
	"log"

	"github.com/rohanthewiz/rweb"
)

func main() {
	// Create server
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})

	// Global middleware
	s.Use(rweb.RequestInfo) // Logs all requests

	// Public routes (no auth required)
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the API</h1><p>Visit /api/v1/status for API status</p>")
	})

	// API group with versioning
	api := s.Group("/api")

	// API v1
	v1 := api.Group("/v1")
	v1.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]string{
			"status":  "ok",
			"version": "1.0",
		})
	})

	// Users endpoints with authentication
	users := v1.Group("/users", authMiddleware)
	users.Get("/", listUsers)
	users.Get("/:id", getUser)
	users.Post("/", createUser)
	users.Put("/:id", updateUser)
	users.Delete("/:id", deleteUser)

	// Admin routes with additional authorization
	admin := s.Group("/admin", authMiddleware, adminMiddleware)
	admin.Get("/dashboard", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Admin Dashboard</h1><p>Welcome, admin!</p>")
	})
	admin.Get("/users", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]interface{}{
			"users": []map[string]string{
				{"id": "1", "name": "John", "role": "admin"},
				{"id": "2", "name": "Jane", "role": "user"},
			},
		})
	})
	admin.Delete("/users/:id", func(ctx rweb.Context) error {
		id := ctx.Request().Param("id")
		return ctx.WriteJSON(map[string]string{
			"message": fmt.Sprintf("User %s deleted by admin", id),
		})
	})

	// Static files group
	static := s.Group("/static")
	static.Get("/version", func(ctx rweb.Context) error {
		return ctx.WriteText("Static files version 1.0")
	})
	// In production, you might use:
	// static.StaticFiles("/assets", "./public/assets", 0)

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

// Middleware functions
func authMiddleware(ctx rweb.Context) error {
	// Check for auth token
	authHeader := ctx.Request().Header("Authorization")
	if authHeader == "" {
		return ctx.Status(401).WriteJSON(map[string]string{
			"error": "Authentication required",
		})
	}

	// In real app, validate the token
	if authHeader != "Bearer valid-token" {
		return ctx.Status(401).WriteJSON(map[string]string{
			"error": "Invalid token",
		})
	}

	// Store user info in context
	ctx.Set("userId", "123")
	ctx.Set("isAuthenticated", true)

	return ctx.Next()
}

func adminMiddleware(ctx rweb.Context) error {
	// Check if user is admin (in real app, check from DB or JWT claims)
	// For demo, we'll check a specific header
	if ctx.Request().Header("X-Admin") != "true" {
		return ctx.Status(403).WriteJSON(map[string]string{
			"error": "Admin access required",
		})
	}

	ctx.Set("isAdmin", true)
	return ctx.Next()
}

// Handler functions for users
func listUsers(ctx rweb.Context) error {
	return ctx.WriteJSON(map[string]interface{}{
		"users": []map[string]string{
			{"id": "1", "name": "John Doe"},
			{"id": "2", "name": "Jane Smith"},
		},
	})
}

func getUser(ctx rweb.Context) error {
	id := ctx.Request().Param("id")
	return ctx.WriteJSON(map[string]string{
		"id":    id,
		"name":  "John Doe",
		"email": "john@example.com",
	})
}

func createUser(ctx rweb.Context) error {
	// In real app, parse request body
	return ctx.WriteJSON(map[string]string{
		"id":      "3",
		"message": "User created successfully",
	})
}

func updateUser(ctx rweb.Context) error {
	id := ctx.Request().Param("id")
	return ctx.WriteJSON(map[string]string{
		"id":      id,
		"message": "User updated successfully",
	})
}

func deleteUser(ctx rweb.Context) error {
	id := ctx.Request().Param("id")
	return ctx.WriteJSON(map[string]string{
		"id":      id,
		"message": "User deleted successfully",
	})
}