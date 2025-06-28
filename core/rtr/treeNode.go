package rtr

import (
	"strings"

	"github.com/rohanthewiz/rweb/consts"
)

// Node type constants for clarity in the code.
// These correspond to the special characters used in route definitions.
const (
// separator = '/'  // Path segment separator
// parameter = ':'  // Parameter prefix (e.g., :id)
// wildcard  = '*'  // Wildcard prefix (e.g., *filepath)
)

// treeNode represents a radix tree node.
// Each node stores a prefix and can have multiple types of children:
// regular children (for static paths), a parameter child, and a wildcard child.
//
// Memory layout optimizations:
//   - indices array provides O(1) child lookup by character
//   - startIndex/endIndex define the character range for children
//   - Separate parameter/wildcard pointers avoid mixing with static routes
//
// Example tree structure for routes /users, /users/:id, /users/:id/posts:
//   
//   root (prefix: "")
//    └── "users" (data: handler1)
//         └── parameter ":id" (data: handler2)
//              └── "/posts" (data: handler3)
type treeNode[T any] struct {
	prefix     string          // The common prefix for this node
	data       T               // Handler data (zero value if no handler)
	children   []*treeNode[T]  // Static path children
	parameter  *treeNode[T]    // Parameter child (e.g., :id)
	wildcard   *treeNode[T]    // Wildcard child (e.g., *path)
	indices    []uint8         // Maps character offset to children index
	startIndex uint8           // First character in children range
	endIndex   uint8           // Last character + 1 in children range
	kind       byte            // Node type: ':', '*', or 0 for static
}

// split splits the node at the given index and inserts
// a new child node with the given path and data.
// If path is empty, it will not create another child node
// and instead assign the data directly to the node.
//
// Split operation example:
//   Original: "blogs" -> (handler1)
//   New path: "blog" -> (handler2)
//   Result: "blog" -> (handler2)
//             └── "s" -> (handler1)
//
// Algorithm:
// 1. Clone current node with suffix as prefix
// 2. Reset current node to common prefix
// 3. Add cloned node as child
// 4. Add new branch if path is not empty
func (node *treeNode[T]) split(index int, path string, data T) {
	// Create split node with the remaining string
	splitNode := node.clone(node.prefix[index:])

	// The existing data must be removed
	node.reset(node.prefix[:index])

	// If the path is empty, it means we don't create a 2nd child node.
	// Just assign the data for the existing node and store a single child node.
	if path == "" {
		node.data = data
		node.addChild(splitNode)
		return
	}

	node.addChild(splitNode)

	// Create new nodes with the remaining path
	node.append(path, data)
}

// clone clones the node with a new prefix.
// This is used during split operations to preserve the existing node's data
// and children while changing its position in the tree.
//
// Note: This creates a shallow copy - children arrays and nodes are shared,
// not duplicated. This is safe because tree modifications only add nodes,
// never modify existing ones in-place.
func (node *treeNode[T]) clone(prefix string) *treeNode[T] {
	return &treeNode[T]{
		prefix:     prefix,
		data:       node.data,
		indices:    node.indices,
		startIndex: node.startIndex,
		endIndex:   node.endIndex,
		children:   node.children,
		parameter:  node.parameter,
		wildcard:   node.wildcard,
		kind:       node.kind,
	}
}

// reset resets the existing node data.
// This is used during split operations to convert a leaf node into an internal node.
// The node keeps its identity (same memory address) but loses its handler data
// and special children, becoming a pure routing node.
//
// Only the prefix is preserved from the original node state.
func (node *treeNode[T]) reset(prefix string) {
	var empty T
	node.prefix = prefix
	node.data = empty        // Clear handler
	node.parameter = nil     // Clear parameter child
	node.wildcard = nil      // Clear wildcard child
	node.kind = 0            // Reset to static node
	node.startIndex = 0      // Reset index range
	node.endIndex = 0
	node.indices = nil       // Clear index mapping
	node.children = nil      // Clear children array
}

// addChild adds a child tree.
// This method maintains an efficient index structure for O(1) child lookups.
//
// Index structure explanation:
//   - indices is a sparse array mapping characters to children array positions
//   - startIndex/endIndex define the valid character range
//   - Character 'c' maps to children[indices[c - startIndex]]
//
// Example:
//   startIndex = 'a' (97), endIndex = 'd' (100)
//   indices = [0, 5, 0, 3]  // Positions for 'a', 'b', 'c', 'd'
//   'b' -> children[5], 'd' -> children[3]
//
// The method dynamically expands the index range as needed.
func (node *treeNode[T]) addChild(child *treeNode[T]) {
	// First child needs special handling - index 0 is reserved for "no child"
	if len(node.children) == 0 {
		node.children = append(node.children, nil)
	}

	firstChar := child.prefix[0]

	switch {
	// First time setting up indices
	case node.startIndex == 0:
		node.startIndex = firstChar
		node.indices = []uint8{0}
		node.endIndex = node.startIndex + uint8(len(node.indices))

	// New child's character is before current range - expand backwards
	case firstChar < node.startIndex:
		diff := node.startIndex - firstChar
		newIndices := make([]uint8, diff+uint8(len(node.indices)))
		copy(newIndices[diff:], node.indices)
		node.startIndex = firstChar
		node.indices = newIndices
		node.endIndex = node.startIndex + uint8(len(node.indices))

	// New child's character is after current range - expand forwards
	case firstChar >= node.endIndex:
		diff := firstChar - node.endIndex + 1
		newIndices := make([]uint8, diff+uint8(len(node.indices)))
		copy(newIndices, node.indices)
		node.indices = newIndices
		node.endIndex = node.startIndex + uint8(len(node.indices))
	}

	// Map character to children array position
	index := node.indices[firstChar-node.startIndex]

	if index == 0 {
		// No child at this position yet - add it
		node.indices[firstChar-node.startIndex] = uint8(len(node.children))
		node.children = append(node.children, child)
		return
	}

	// Replace existing child (happens during route updates)
	node.children[index] = child
}

// addTrailingSlash adds a trailing slash with the same data.
// This enables routes to work with and without trailing slashes.
//
// Example: /users and /users/ return the same handler
//
// Skip conditions:
//   - Node already ends with slash
//   - Node is a wildcard (captures everything)
//   - Node already has a "/" child
//
// This improves UX by making trailing slashes optional without
// requiring explicit registration of both variants.
func (node *treeNode[T]) addTrailingSlash(data T) {
	if strings.HasSuffix(node.prefix, "/") || node.kind == consts.RuneAsterisk ||
		(consts.RuneFwdSlash >= node.startIndex && consts.RuneFwdSlash < node.endIndex &&
			node.indices[consts.RuneFwdSlash-node.startIndex] != 0) {
		return
	}

	node.addChild(&treeNode[T]{
		prefix: "/",
		data:   data,
	})
}

// append appends the given path to the tree.
// This method handles the complex logic of parsing paths with parameters and wildcards,
// creating the appropriate node structure.
//
// Path parsing rules:
//   - Static segments: Added as regular nodes
//   - :param segments: Added as parameter nodes (match one segment)
//   - *param segments: Added as wildcard nodes (match everything)
//
// The method processes the path iteratively, creating nodes as needed.
func (node *treeNode[T]) append(path string, data T) {
	// Process the path iteratively until fully consumed
	for {
		if path == "" {
			node.data = data
			return
		}

		// Find the next parameter or wildcard marker
		paramStart := strings.IndexByte(path, consts.RuneColon)

		if paramStart == -1 {
			paramStart = strings.IndexByte(path, consts.RuneAsterisk)
		}

		// Case 1: No parameters remaining - add as static node
		if paramStart == -1 {
			// Optimization: Reuse current node if it has no prefix yet
			if node.prefix == "" {
				node.prefix = path
				node.data = data
				node.addTrailingSlash(data)
				return
			}

			// Create static child node
			child := &treeNode[T]{
				prefix: path,
				data:   data,
			}

			node.addChild(child)
			child.addTrailingSlash(data)
			return
		}

		// Case 2: Parameter/wildcard at current position
		if paramStart == 0 {
			// Find parameter name end (either next / or end of path)
			paramEnd := strings.IndexByte(path, consts.RuneFwdSlash)

			if paramEnd == -1 {
				paramEnd = len(path)
			}

			// Create parameter/wildcard node
			// Note: prefix stores the parameter name without : or *
			child := &treeNode[T]{
				prefix: path[1:paramEnd],  // Skip : or *
				kind:   path[paramStart],   // Store : or *
			}

			switch child.kind {
			case consts.RuneColon:
				// Parameter node - can have children
				child.addTrailingSlash(data)
				node.parameter = child
				node = child
				path = path[paramEnd:]
				continue

			case consts.RuneAsterisk:
				// Wildcard node - captures everything, no children
				child.data = data
				node.wildcard = child
				return
			}
		}

		// Case 3: Parameter/wildcard later in the path
		// Add static part first, then continue with parameter

		// Optimization: Reuse current node if it has no prefix yet
		if node.prefix == "" {
			node.prefix = path[:paramStart]
			path = path[paramStart:]
			continue
		}

		// Create static node for the part before parameter
		child := &treeNode[T]{
			prefix: path[:paramStart],
		}

		// Special handling: "/" nodes inherit parent data
		// This enables /users and /users/ to work identically
		if child.prefix == "/" {
			child.data = node.data
		}

		node.addChild(child)
		node = child
		path = path[paramStart:]
	}
}

// end is called when the node was fully parsed
// and needs to decide the next control flow.
// end is only called from `tree.Add`.
//
// This method determines what to do after matching a node's prefix:
//   1. Continue to a child node (if one matches)
//   2. Add remaining path as new nodes
//   3. Handle parameter node transitions
//
// Returns: (next node, new offset, control flow directive)
func (node *treeNode[T]) end(path string, data T, i int, offset int) (*treeNode[T], int, flow) {
	char := path[i]

	// Try to find a matching child for the next character
	if char >= node.startIndex && char < node.endIndex {
		index := node.indices[char-node.startIndex]

		if index != 0 {
			// Found matching child - continue traversal there
			node = node.children[index]
			offset = i
			return node, offset, flowNext
		}
	}

	// No matching static child found
	
	// Special case: Empty prefix means this is the root node
	if node.prefix == "" {
		node.append(path[i:], data)
		return node, offset, flowStop
	}

	// Check if we should transition to a parameter node
	// Example:
	//   node: /user/|:id (has parameter child)
	//   path: /user/|:id/profile
	if node.parameter != nil && path[i] == consts.RuneColon {
		node = node.parameter
		offset = i
		return node, offset, flowBegin
	}

	// No suitable child - append remaining path as new nodes
	node.append(path[i:], data)
	return node, offset, flowStop
}

// each traverses the tree and calls the given function on every node.
// This performs a depth-first traversal of the entire tree structure.
//
// Traversal order:
//   1. Current node
//   2. All static children
//   3. Parameter child (if any)
//   4. Wildcard child (if any)
//
// Used by Tree.Map to transform all handlers in the tree.
// The callback is guaranteed to be called exactly once per node.
func (node *treeNode[T]) each(callback func(*treeNode[T])) {
	callback(node)

	// Traverse static children
	for _, child := range node.children {
		if child == nil {
			continue
		}

		child.each(callback)
	}

	// Traverse parameter child
	if node.parameter != nil {
		node.parameter.each(callback)
	}

	// Traverse wildcard child
	if node.wildcard != nil {
		node.wildcard.each(callback)
	}
}
