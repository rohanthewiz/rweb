package main

import (
	"fmt"
	"log"

	"github.com/rohanthewiz/rweb"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})

	// Authentication middleware
	s.Use(func(ctx rweb.Context) error {
		// Simulate checking auth token from header
		authHeader := ctx.Request().Header("Authorization")

		if authHeader == "Bearer admin-token" {
			ctx.Set("isLoggedIn", true)
			ctx.Set("userId", "123")
			ctx.Set("username", "admin")
			ctx.Set("isAdmin", true)
		} else if authHeader == "Bearer user-token" {
			ctx.Set("isLoggedIn", true)
			ctx.Set("userId", "456")
			ctx.Set("username", "john")
			ctx.Set("isAdmin", false)
		}

		return ctx.Next()
	})

	// Session middleware
	s.Use(func(ctx rweb.Context) error {
		// Add session data if user is logged in
		if ctx.Has("isLoggedIn") && ctx.Get("isLoggedIn").(bool) {
			ctx.Set("session", map[string]any{
				"theme": "dark",
				"lang":  "en",
			})
		}
		return ctx.Next()
	})

	// Public route
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Welcome to the API")
	})

	// Protected route
	s.Get("/profile", func(ctx rweb.Context) error {
		if !ctx.Has("isLoggedIn") || !ctx.Get("isLoggedIn").(bool) {
			return ctx.SetStatus(401).WriteString("Unauthorized")
		}

		username := ctx.Get("username").(string)
		userId := ctx.Get("userId").(string)

		return ctx.WriteJSON(map[string]any{
			"userId":   userId,
			"username": username,
			"session":  ctx.Get("session"),
		})
	})

	// Admin-only route
	s.Get("/admin", func(ctx rweb.Context) error {
		if !ctx.Has("isAdmin") || !ctx.Get("isAdmin").(bool) {
			return ctx.SetStatus(403).WriteString("Forbidden")
		}

		return ctx.WriteString("Welcome to admin panel!")
	})

	// Route that modifies context data
	s.Post("/settings", func(ctx rweb.Context) error {
		if !ctx.Has("isLoggedIn") || !ctx.Get("isLoggedIn").(bool) {
			return ctx.SetStatus(401).WriteString("Unauthorized")
		}

		// Update session data
		if session, ok := ctx.Get("session").(map[string]any); ok {
			session["theme"] = "light" // Update theme
			ctx.Set("session", session)
		}

		return ctx.WriteJSON(map[string]any{
			"message": "Settings updated",
			"session": ctx.Get("session"),
		})
	})

	// Route demonstrating deletion of context data
	s.Post("/logout", func(ctx rweb.Context) error {
		// Clear all auth-related data
		ctx.Delete("isLoggedIn")
		ctx.Delete("userId")
		ctx.Delete("username")
		ctx.Delete("isAdmin")
		ctx.Delete("session")

		return ctx.WriteString("Logged out successfully")
	})

	fmt.Println("Server running on :8080")
	fmt.Println("Try these commands:")
	fmt.Println("  curl http://localhost:8080/")
	fmt.Println("  curl http://localhost:8080/profile")
	fmt.Println("  curl -H 'Authorization: Bearer user-token' http://localhost:8080/profile")
	fmt.Println("  curl -H 'Authorization: Bearer admin-token' http://localhost:8080/admin")

	log.Fatal(s.Run())
}
