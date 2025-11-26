// Package main demonstrates parameter name validation in the RWeb router.
//
// The router enforces that routes sharing the same parameter position
// must use the same parameter name. This is a fundamental requirement
// of the radix tree structure.
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

	// Example 1: Valid routes with consistent parameter names
	fmt.Println("✓ Registering valid routes with consistent parameter names...")
	s.Get("/users/:id", func(ctx rweb.Context) error {
		id := ctx.Request().Param("id")
		return ctx.WriteString(fmt.Sprintf("User ID: %s\n", id))
	})

	s.Get("/users/:id/profile", func(ctx rweb.Context) error {
		id := ctx.Request().Param("id")
		return ctx.WriteString(fmt.Sprintf("Profile for user ID: %s\n", id))
	})

	s.Get("/users/:id/posts", func(ctx rweb.Context) error {
		id := ctx.Request().Param("id")
		return ctx.WriteString(fmt.Sprintf("Posts for user ID: %s\n", id))
	})

	// Example 2: Consecutive parameters with consistent names
	fmt.Println("✓ Registering routes with consecutive parameters...")
	s.Get("/posts/:year/:title", func(ctx rweb.Context) error {
		year := ctx.Request().Param("year")
		title := ctx.Request().Param("title")
		return ctx.WriteString(fmt.Sprintf("Post: %s/%s\n", year, title))
	})

	s.Get("/posts/:year/archive", func(ctx rweb.Context) error {
		year := ctx.Request().Param("year")
		return ctx.WriteString(fmt.Sprintf("Archive for year: %s\n", year))
	})

	// Example 3: Valid - different branches can use different parameter names
	fmt.Println("✓ Registering routes on different branches...")
	s.Get("/admin/:userId", func(ctx rweb.Context) error {
		userId := ctx.Request().Param("userId")
		return ctx.WriteString(fmt.Sprintf("Admin user ID: %s\n", userId))
	})

	s.Get("/customer/:customerId", func(ctx rweb.Context) error {
		customerId := ctx.Request().Param("customerId")
		return ctx.WriteString(fmt.Sprintf("Customer ID: %s\n", customerId))
	})

	// Example 4: UNCOMMENT to see validation error
	// This will panic because :userId conflicts with :id at the same position
	// fmt.Println("✗ Attempting to register conflicting route...")
	// s.Get("/users/:userId/settings", func(ctx rweb.Context) error {
	//     userId := ctx.Request().Param("userId")
	//     return ctx.WriteString(fmt.Sprintf("Settings for user ID: %s\n", userId))
	// })
	// Expected panic message:
	// "radix tree router: conflicting parameter names at the same position.
	//  Existing parameter 'id' conflicts with new parameter 'userId'.
	//  Routes sharing the same parameter position must use the same parameter
	//  name because they share the same node in the tree structure."

	fmt.Println("\n✓ All routes registered successfully!")
	fmt.Println("\nTry these URLs:")
	fmt.Println("  http://localhost:8080/users/123")
	fmt.Println("  http://localhost:8080/users/123/profile")
	fmt.Println("  http://localhost:8080/users/123/posts")
	fmt.Println("  http://localhost:8080/posts/2024/hello-world")
	fmt.Println("  http://localhost:8080/posts/2024/archive")
	fmt.Println("  http://localhost:8080/admin/admin123")
	fmt.Println("  http://localhost:8080/customer/cust456")
	fmt.Println()

	log.Fatal(s.Run())
}
