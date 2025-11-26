package main

import (
	"fmt"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

func otherMain2() {
	r := rtr.New[string]()

	fmt.Println("=== Scenario 1: Adding /users/:id then /users/:userId/profile ===")
	r.Add(consts.MethodGet, "/users/:id", "Route 1")
	r.Add(consts.MethodGet, "/users/:userId/profile", "Route 2")

	// Test lookups
	data, params := r.Lookup(consts.MethodGet, "/users/123")
	fmt.Printf("Lookup /users/123: data='%s', params=%v\n", data, params)

	data, params = r.Lookup(consts.MethodGet, "/users/456/profile")
	fmt.Printf("Lookup /users/456/profile: data='%s', params=%v\n", data, params)

	fmt.Println("\n=== Scenario 2: Consecutive parameters ===")
	r2 := rtr.New[string]()
	r2.Add(consts.MethodGet, "/posts/:year/:title", "Post 1")
	r2.Add(consts.MethodGet, "/posts/:year/:slug", "Post 2") // Should this conflict?

	data, params = r2.Lookup(consts.MethodGet, "/posts/2024/my-post")
	fmt.Printf("Lookup /posts/2024/my-post: data='%s', params=%v\n", data, params)
}
