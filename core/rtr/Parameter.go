package rtr

// Parameter represents a URL parameter extracted from dynamic route segments.
// This is used by the radix router to return captured values from routes like /user/:id.
//
// Example:
//   Route: /user/:id/posts/:postId
//   URL:   /user/123/posts/456
//   Result: []Parameter{{Key: "id", Value: "123"}, {Key: "postId", Value: "456"}}
//
// Design notes:
// - Simple struct avoids allocation overhead compared to map[string]string
// - Ordered slice preserves parameter sequence from the route definition
type Parameter struct {
	Key   string
	Value string
}
