package rtr

// Router is a high-performance router.
type Router[T any] struct {
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
func New[T any]() *Router[T] {
	return &Router[T]{}
}

// Add registers a new handler for the given method and path.
func (router *Router[T]) Add(method string, path string, handler T) {
	tree := router.selectTree(method)
	tree.Add(path, handler)
}

// Lookup finds the handler and parameters for the given route.
func (router *Router[T]) Lookup(method string, path string) (T, []Parameter) {
	if method[0] == 'G' {
		return router.get.Lookup(path)
	}

	tree := router.selectTree(method)
	return tree.Lookup(path)
}

// LookupNoAlloc finds the handler and parameters for the given route without using any memory allocations.
func (router *Router[T]) LookupNoAlloc(method string, path string, addParameter func(string, string)) T {
	if method[0] == 'G' {
		return router.get.LookupNoAlloc(path, addParameter)
	}

	tree := router.selectTree(method)
	return tree.LookupNoAlloc(path, addParameter)
}

// Map traverses all trees and calls the given function on every node.
func (router *Router[T]) Map(transform func(T) T) {
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
func (router *Router[T]) selectTree(method string) *Tree[T] {
	switch method {
	case "GET":
		return &router.get
	case "POST":
		return &router.post
	case "DELETE":
		return &router.delete
	case "PUT":
		return &router.put
	case "PATCH":
		return &router.patch
	case "HEAD":
		return &router.head
	case "CONNECT":
		return &router.connect
	case "TRACE":
		return &router.trace
	case "OPTIONS":
		return &router.options
	default:
		return nil
	}
}
