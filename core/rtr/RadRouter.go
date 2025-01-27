package rtr

import (
	"github.com/rohanthewiz/rweb/consts"
)

// RadixRouter is a high-performance router.
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
func New[T any]() *RadixRouter[T] {
	return &RadixRouter[T]{}
}

// Add registers a new handler for the given method and path.
func (router *RadixRouter[T]) Add(method string, path string, handler T) {
	tree := router.selectTree(method)
	tree.Add(path, handler)
}

// Lookup finds the handler and parameters for the given route.
func (router *RadixRouter[T]) Lookup(method string, path string) (T, []Parameter) {
	if method[0] == 'G' {
		return router.get.Lookup(path)
	}

	tree := router.selectTree(method)
	return tree.Lookup(path)
}

// LookupNoAlloc finds the handler and parameters for the given route without using any memory allocations.
func (router *RadixRouter[T]) LookupNoAlloc(method string, path string, addParameter func(string, string)) T {
	if method[0] == 'G' {
		return router.get.LookupNoAlloc(path, addParameter)
	}

	tree := router.selectTree(method)
	return tree.LookupNoAlloc(path, addParameter)
}

// Map traverses all trees and calls the given function on every node.
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
