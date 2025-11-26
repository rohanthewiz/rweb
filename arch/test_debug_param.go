package main

import (
	"fmt"

	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

func otherMain() {
	r := rtr.New[string]()

	fmt.Println("Adding first route: /users/:id")
	r.Add(consts.MethodGet, "/users/:id", "Route 1")

	fmt.Println("\nAdding second route: /users/:userId/profile")
	r.Add(consts.MethodGet, "/users/:userId/profile", "Route 2")

	fmt.Println("\nDone - no panic occurred")

	// Test lookups
	data, params := r.Lookup(consts.MethodGet, "/users/123")
	fmt.Printf("\nLookup /users/123: data=%s, params=%v\n", data, params)

	data, params = r.Lookup(consts.MethodGet, "/users/456/profile")
	fmt.Printf("Lookup /users/456/profile: data=%s, params=%v\n", data, params)
}
