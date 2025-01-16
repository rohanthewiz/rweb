package rtr

import (
	"fmt"

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
		get:     make(map[string]T, 16),
		post:    make(map[string]T, 8),
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
	hashMap := hr.selectMethodMap(method)
	hashMap[path] = handler
	// Debug
	// for k, h := range hashMap {
	// 	fmt.Printf("Added. Now - method: %q, route key: %q, handler: %v\n", method, k, h)
	// }

}

func (hr *HashRouter[T]) ListRoutes() (routes []RouteList) {
	for k, h := range hr.get {
		routes = append(routes, RouteList{Method: consts.MethodGet, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.post {
		routes = append(routes, RouteList{Method: consts.MethodPost, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.put {
		routes = append(routes, RouteList{Method: consts.MethodPut, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.patch {
		routes = append(routes, RouteList{Method: consts.MethodPatch, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.delete {
		routes = append(routes, RouteList{Method: consts.MethodDelete, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.head {
		routes = append(routes, RouteList{Method: consts.MethodHead, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.connect {
		routes = append(routes, RouteList{Method: consts.MethodConnect, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.trace {
		routes = append(routes, RouteList{Method: consts.MethodTrace, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	for k, h := range hr.options {
		routes = append(routes, RouteList{Method: consts.MethodOptions, Path: k, HandlerRef: fmt.Sprintf("%v", h)})
	}
	return
}

// Lookup finds the handler for the given route.
func (hr *HashRouter[T]) Lookup(method string, path string) T {
	if method[0] == 'G' {
		return hr.get[path]
	}

	hashMap := hr.selectMethodMap(method)
	return hashMap[path]
}

// selectMethodMap returns the map based on the given HTTP method.
func (hr *HashRouter[T]) selectMethodMap(method string) map[string]T {
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
