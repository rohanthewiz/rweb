package rtr

import (
	"github.com/rohanthewiz/rweb/consts"
)

// RadixRouter is a high-performance router using radix trees (compressed tries) for route matching.
// It supports dynamic segments (:param) and wildcards (*path) while maintaining O(log n) lookup time.
//
// Key features:
// - Memory efficient through prefix compression
// - Fast lookups even with thousands of routes
// - Parameter extraction without regex or string splitting
// - Zero-allocation lookup option for maximum performance
//
// Architecture:
// - Each HTTP method has its own radix tree to eliminate method checking during lookup
// - Trees are lazily initialized (zero value is usable)
// - Generic type T allows any handler type
type RadixRouter[T any] struct {
	get     Tree[T]
	post    Tree[T]
	delete  Tree[T]
	put     Tree[T]
	patch   Tree[T]
	head    Tree[T]
	connect Tree[T]
	trace   Tree[T]
	options Tree[T]
}

// New creates a new router containing trees for every HTTP method.
// Trees are not pre-allocated, relying on Go's zero-value initialization.
// This makes the router lightweight until routes are actually added.
func New[T any]() *RadixRouter[T] {
	return &RadixRouter[T]{}
}

// Add registers a new handler for the given method and path.
// Paths can contain:
//   - Static segments: /users/profile
//   - Parameters: /users/:id (captures "id")
//   - Wildcards: /files/*path (captures everything after /files/)
//
// Routes are automatically optimized during insertion for fastest possible lookup.
func (router *RadixRouter[T]) Add(method string, path string, handler T) {
	tree := router.selectTree(method)
	tree.Add(path, handler)
}

// Lookup finds the handler and parameters for the given route.
// Returns the handler and a slice of extracted parameters.
//
// Performance note:
// - GET requests are optimized with a fast path (most common method)
// - Allocates memory for parameter slice if route has parameters
// - Use LookupNoAlloc for zero-allocation lookups
func (router *RadixRouter[T]) Lookup(method string, path string) (T, []Parameter) {
	if method[0] == 'G' {
		return router.get.Lookup(path)
	}

	tree := router.selectTree(method)
	return tree.Lookup(path)
}

// LookupNoAlloc finds the handler and parameters for the given route without using any memory allocations.
// Parameters are passed to the callback function instead of being collected in a slice.
//
// This is ideal for high-performance scenarios where:
// - You need to minimize GC pressure
// - Parameters can be processed immediately
// - You're handling thousands of requests per second
//
// The addParameter callback is called for each parameter found in order.
func (router *RadixRouter[T]) LookupNoAlloc(method string, path string, addParameter func(string, string)) T {
	if method[0] == 'G' {
		return router.get.LookupNoAlloc(path, addParameter)
	}

	tree := router.selectTree(method)
	return tree.LookupNoAlloc(path, addParameter)
}

// Map traverses all trees and calls the given function on every node.
// This allows bulk transformation of all handlers in the router.
//
// Common use cases:
// - Wrapping all handlers with middleware
// - Adding instrumentation or logging
// - Replacing handlers for testing
//
// The transform function receives each handler and should return the new handler.
func (router *RadixRouter[T]) Map(transform func(T) T) {
	router.get.Map(transform)
	router.post.Map(transform)
	router.delete.Map(transform)
	router.put.Map(transform)
	router.patch.Map(transform)
	router.head.Map(transform)
	router.connect.Map(transform)
	router.trace.Map(transform)
	router.options.Map(transform)
}

// selectTree returns the tree by the given HTTP method.
// Returns nil for unknown methods to allow graceful handling.
//
// Implementation note:
// - Returns pointer to embedded tree struct (not a copy)
// - Switch compiles to jump table for O(1) selection
// - Method constants ensure consistency across the codebase
func (router *RadixRouter[T]) selectTree(method string) *Tree[T] {
	switch method {
	case consts.MethodGet:
		return &router.get
	case consts.MethodPost:
		return &router.post
	case consts.MethodDelete:
		return &router.delete
	case consts.MethodPut:
		return &router.put
	case consts.MethodPatch:
		return &router.patch
	case consts.MethodHead:
		return &router.head
	case consts.MethodConnect:
		return &router.connect
	case consts.MethodTrace:
		return &router.trace
	case consts.MethodOptions:
		return &router.options
	default:
		return nil
	}
}
