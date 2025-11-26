package main

import (
	"fmt"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

func main() {
	r := rtr.New[string]()

	// Add routes in same order as failing test
	fmt.Println("Adding route 1: /users/:year/:title")
	r.Add(consts.MethodGet, "/users/:year/:title", "Handler 1: year/title")

	fmt.Println("Adding route 2: /users/:id/posts/:postId")
	r.Add(consts.MethodGet, "/users/:id/posts/:postId", "Handler 2: id/posts/postId")

	// Test the problematic path
	path := "/users/123/posts/456"
	fmt.Printf("\nLooking up: %s\n", path)

	data, params := r.Lookup(consts.MethodGet, path)
	fmt.Printf("Handler: %s\n", data)
	fmt.Printf("Parameters:\n")
	for i, p := range params {
		fmt.Printf("  [%d] %s = %s\n", i, p.Key, p.Value)
	}

	// Also test the first route
	path2 := "/users/2024/easter-message"
	fmt.Printf("\nLooking up: %s\n", path2)

	data2, params2 := r.Lookup(consts.MethodGet, path2)
	fmt.Printf("Handler: %s\n", data2)
	fmt.Printf("Parameters:\n")
	for i, p := range params2 {
		fmt.Printf("  [%d] %s = %s\n", i, p.Key, p.Value)
	}
}
