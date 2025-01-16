package rtr

import (
	"github.com/rohanthewiz/rweb/consts"
)

// HashRouter is a fast lookup router.
type HashRouter[T any] struct {
	get     map[string]T
	post    map[string]T
	delete  map[string]T
	put     map[string]T
	patch   map[string]T
	head    map[string]T
	connect map[string]T
	trace   map[string]T
	options map[string]T
}

// NewHashRouter creates a new router containing initialized hashmaps for every HTTP method.
// It is important to use this method when a new hash router is needed
func NewHashRouter[T any]() *HashRouter[T] {
	hr := &HashRouter[T]{
		get:     make(map[string]T),
		post:    make(map[string]T),
		delete:  make(map[string]T),
		put:     make(map[string]T),
		patch:   make(map[string]T),
		head:    make(map[string]T),
		connect: make(map[string]T),
		trace:   make(map[string]T),
		options: make(map[string]T),
	}
	return hr
}

// Add registers a new handler for the given method and path.
func (hr *HashRouter[T]) Add(method string, path string, handler T) {
	hashMap := hr.SelectMethodMap(method)
	hashMap[path] = handler
	// Debug
	// for k, h := range hashMap {
	// 	fmt.Printf("Added. Now - method: %q, route key: %q, handler: %v\n", method, k, h)
	// }

}

// Lookup finds the handler for the given route.
func (hr *HashRouter[T]) Lookup(method string, path string) T {
	if method[0] == 'G' {
		return hr.get[path]
	}

	hashMap := hr.SelectMethodMap(method)
	return hashMap[path]
}

// SelectMethodMap returns the map based on the given HTTP method.
func (hr *HashRouter[T]) SelectMethodMap(method string) map[string]T {
	switch method {
	case consts.MethodGet:
		return hr.get
	case consts.MethodPost:
		return hr.post
	case consts.MethodDelete:
		return hr.delete
	case consts.MethodPut:
		return hr.put
	case consts.MethodPatch:
		return hr.patch
	case consts.MethodHead:
		return hr.head
	case consts.MethodConnect:
		return hr.connect
	case consts.MethodTrace:
		return hr.trace
	case consts.MethodOptions:
		return hr.options
	default:
		return nil
	}
}
