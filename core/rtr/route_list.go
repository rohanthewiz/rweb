package rtr

// RouteList represents a registered route for debugging and inspection purposes.
// This struct is used by router implementations to expose their route tables
// in a human-readable format.
//
// Fields:
//   - Method: HTTP method (GET, POST, etc.) from consts package
//   - Path: The URL path pattern (e.g., "/users/:id")
//   - HandlerRef: String representation of the handler (for debugging)
//
// This is primarily used for:
//   - Route table visualization
//   - Debugging route conflicts
//   - Generating API documentation
//   - Testing route registration
type RouteList struct {
	Method     string
	Path       string
	HandlerRef string
}
