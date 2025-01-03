package rtr

// Tree represents a radix tree.
type Tree[T any] struct {
	root treeNode[T]
}

// Add adds a new element to the tree.
func (tree *Tree[T]) Add(path string, data T) {
	// Search tree for equal parts until we can no longer proceed
	i := 0
	offset := 0
	node := &tree.root

	for {
	begin:
		switch node.kind {
		case parameter:
			// This only occurs when the same parameter based route is added twice.
			// node: /post/:id|
			// path: /post/:id|
			if i == len(path) {
				node.data = data
				return
			}

			// When we hit a separator, we'll search for a fitting child.
			if path[i] == separator {
				node, offset, _ = node.end(path, data, i, offset)
				goto next
			}

		default:
			if i == len(path) {
				// The path already exists.
				// node: /blog|
				// path: /blog|
				if i-offset == len(node.prefix) {
					node.data = data
					return
				}

				// The path ended but the node prefix is longer.
				// node: /blog|feed
				// path: /blog|
				node.split(i-offset, "", data)
				return
			}

			// The node we just checked is entirely included in our path.
			// node: /|
			// path: /|blog
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

			// We got a conflict.
			// node: /b|ag
			// path: /b|riefcase
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
func (tree *Tree[T]) Lookup(path string) (T, []Parameter) {
	var params []Parameter

	data := tree.LookupNoAlloc(path, func(key string, value string) {
		params = append(params, Parameter{key, value})
	})

	return data, params
}

// LookupNoAlloc finds the data for the given path without using any memory allocations.
func (tree *Tree[T]) LookupNoAlloc(path string, addParameter func(key string, value string)) T {
	var (
		i            uint
		wildcardPath string
		wildcard     *treeNode[T]
		node         = &tree.root
	)

	// Skip the first loop iteration if the starting characters are equal
	if len(path) > 0 && len(node.prefix) > 0 && path[0] == node.prefix[0] {
		i = 1
	}

begin:
	// Search tree for equal parts until we can no longer proceed
	for i < uint(len(path)) {
		// The node we just checked is entirely included in our path.
		// node: /|
		// path: /|blog
		if i == uint(len(node.prefix)) {
			if node.wildcard != nil {
				wildcard = node.wildcard
				wildcardPath = path[i:]
			}

			char := path[i]

			if char >= node.startIndex && char < node.endIndex {
				index := node.indices[char-node.startIndex]

				if index != 0 {
					node = node.children[index]
					path = path[i:]
					i = 1
					continue
				}
			}

			// node: /|:id
			// path: /|blog
			if node.parameter != nil {
				node = node.parameter
				path = path[i:]
				i = 1

				for i < uint(len(path)) {
					// node: /:id|/posts
					// path: /123|/posts
					if path[i] == separator {
						addParameter(node.prefix, path[:i])
						index := node.indices[separator-node.startIndex]
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

			// node: /|*any
			// path: /|image.png
			goto notFound
		}

		// We got a conflict.
		// node: /b|ag
		// path: /b|riefcase
		if path[i] != node.prefix[i] {
			goto notFound
		}

		i++
	}

	// node: /blog|
	// path: /blog|
	if i == uint(len(node.prefix)) {
		return node.data
	}

	// node: /|*any
	// path: /|image.png
notFound:
	if wildcard != nil {
		addParameter(wildcard.prefix, wildcardPath)
		return wildcard.data
	}

	var empty T
	return empty
}

// Map binds all handlers to a new one provided by the callback.
func (tree *Tree[T]) Map(transform func(T) T) {
	tree.root.each(func(node *treeNode[T]) {
		node.data = transform(node.data)
	})
}
