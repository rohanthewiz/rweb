package rtr

import "github.com/rohanthewiz/rweb/consts"

// Tree represents a radix tree (compressed trie) for efficient route storage and lookup.
// The tree compresses common prefixes to minimize memory usage and traversal time.
//
// Structure example for routes /user, /users, /user/:id:
//   root
//    └── "user"  (data: handler for /user)
//         ├── "s" (data: handler for /users)
//         └── ":" (parameter node)
//              └── "id" (data: handler for /user/:id)
//
// Zero value is ready to use - the root node is embedded, not a pointer.
type Tree[T any] struct {
	root treeNode[T]
}

// Add adds a new element to the tree.
// The algorithm walks the tree and finds the optimal insertion point,
// splitting nodes when necessary to maintain the radix tree properties.
//
// Algorithm overview:
// 1. Walk down the tree matching prefixes
// 2. Split nodes when paths diverge
// 3. Create new nodes for remaining path segments
// 4. Handle special nodes (parameters and wildcards)
//
// The implementation modifies the tree in-place for efficiency.
func (tree *Tree[T]) Add(path string, data T) {
	// Search tree for equal parts until we can no longer proceed
	i := 0      // Current position in the path string
	offset := 0 // Start of the current node's prefix in the path
	node := &tree.root

	for {
	begin:
		switch node.kind {
		case consts.RuneColon:
			// This only occurs when the same parameter based route is added twice.
			// Example:
			//   node: /post/:id|
			//   path: /post/:id|
			// Simply update the handler data.
			if i == len(path) {
				node.data = data
				return
			}

			// When we hit a separator after a parameter, we need to find
			// the next child node to continue traversal.
			// Example: /user/:id/posts where we're at the / after :id
			if path[i] == consts.RuneFwdSlash {
				node, offset, _ = node.end(path, data, i, offset)
				goto next
			}

		default:
			if i == len(path) {
				// Case 1: Exact match - path already exists
				// Example:
				//   node: /blog|
				//   path: /blog|
				if i-offset == len(node.prefix) {
					node.data = data
					return
				}

				// Case 2: Path is shorter than node prefix - need to split
				// Example:
				//   node: /blog|feed
				//   path: /blog|
				// Result: /blog| -> feed
				node.split(i-offset, "", data)
				return
			}

			// Case 3: Node prefix is fully matched, continue to children
			// Example:
			//   node: /|
			//   path: /|blog
			if i-offset == len(node.prefix) {
				var control flow
				node, offset, control = node.end(path, data, i, offset)

				switch control {
				case flowStop:
					return
				case flowBegin:
					goto begin
				case flowNext:
					goto next
				}
			}

			// Case 4: Paths diverge - need to split at the conflict point
			// Example:
			//   node: /b|ag
			//   path: /b|riefcase
			// Result: /b| -> ag, riefcase
			if path[i] != node.prefix[i-offset] {
				node.split(i-offset, path[i:], data)
				return
			}
		}

	next:
		i++
	}
}

// Lookup finds the data for the given path.
// This is a convenience wrapper around LookupNoAlloc that collects parameters into a slice.
//
// The allocation for the parameter slice only occurs if the route actually has parameters.
// For static routes, this performs identically to LookupNoAlloc.
func (tree *Tree[T]) Lookup(path string) (T, []Parameter) {
	var params []Parameter

	data := tree.LookupNoAlloc(path, func(key string, value string) {
		params = append(params, Parameter{key, value})
	})

	return data, params
}

// LookupNoAlloc finds the data for the given path without using any memory allocations.
// This is the core lookup algorithm optimized for maximum performance.
//
// Algorithm features:
// - No allocations (parameters passed via callback)
// - Optimized character indexing for child lookup
// - Wildcard fallback for catch-all routes
// - Early termination on mismatches
//
// The implementation uses several micro-optimizations:
// - Unsigned integers for bounds checking
// - Character range indexing instead of maps
// - Goto statements to avoid function call overhead
func (tree *Tree[T]) LookupNoAlloc(path string, addParameter func(key string, value string)) T {
	var (
		i            uint            // Current position in path (unsigned for faster bounds checks)
		wildcardPath string          // Saved path suffix for wildcard fallback
		wildcard     *treeNode[T]    // Saved wildcard node for fallback
		node         = &tree.root     // Current node in traversal
	)

	// Optimization: Skip the first loop iteration if the starting characters are equal
	// This is a common case (e.g., all routes starting with "/") and saves one iteration
	if len(path) > 0 && len(node.prefix) > 0 && path[0] == node.prefix[0] {
		i = 1
	}

begin:
	// Search tree for equal parts until we can no longer proceed
	for i < uint(len(path)) {
		// The node prefix is fully matched, look for child nodes
		// Example:
		//   node: /|
		//   path: /|blog
		if i == uint(len(node.prefix)) {
			// Save wildcard node as fallback if no exact match is found later
			if node.wildcard != nil {
				wildcard = node.wildcard
				wildcardPath = path[i:]
			}

			char := path[i]

			// Fast child lookup using character indexing
			// The indices array maps characters to child array positions
			if char >= node.startIndex && char < node.endIndex {
				index := node.indices[char-node.startIndex]

				if index != 0 {
					node = node.children[index]
					path = path[i:]
					i = 1
					continue
				}
			}

			// Check for parameter node
			// Example:
			//   node: /|:id
			//   path: /|123
			if node.parameter != nil {
				node = node.parameter
				path = path[i:]
				i = 1

				// Extract parameter value until next slash or end of path
				for i < uint(len(path)) {
					// Parameter followed by more path segments
					// Example:
					//   node: /:id|/posts
					//   path: /123|/posts
					if path[i] == consts.RuneFwdSlash {
						addParameter(node.prefix, path[:i])
						index := node.indices[consts.RuneFwdSlash-node.startIndex]
						node = node.children[index]
						path = path[i:]
						i = 1
						goto begin
					}

					i++
				}

				addParameter(node.prefix, path[:i])
				return node.data
			}

			// No matching child found, try wildcard fallback
			// Example:
			//   node: /|*filepath
			//   path: /|static/image.png
			goto notFound
		}

		// Character mismatch - paths diverge
		// Example:
		//   node: /b|ag
		//   path: /b|riefcase
		if path[i] != node.prefix[i] {
			goto notFound
		}

		i++
	}

	// Exact match found
	// Example:
	//   node: /blog|
	//   path: /blog|
	if i == uint(len(node.prefix)) {
		return node.data
	}

	// No exact match found, use wildcard if available
	// Example:
	//   wildcard: /*filepath
	//   path: /static/css/main.css
	//   captures: filepath="static/css/main.css"
notFound:
	if wildcard != nil {
		addParameter(wildcard.prefix, wildcardPath)
		return wildcard.data
	}

	var empty T
	return empty
}

// Map binds all handlers to a new one provided by the callback.
// This traverses the entire tree and applies the transformation to each node's data.
//
// Use cases:
// - Adding middleware wrapper to all routes
// - Converting handler types
// - Adding debugging or monitoring
//
// The transformation is applied in-place, modifying the existing tree.
func (tree *Tree[T]) Map(transform func(T) T) {
	tree.root.each(func(node *treeNode[T]) {
		node.data = transform(node.data)
	})
}
