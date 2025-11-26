package rtr_test

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
)

// TestParameterNameConsistency verifies that routes sharing the same parameter
// position use consistent parameter names. This is a requirement because
// routes at the same position share the same parameter node in the radix tree.
func TestParameterNameConsistency(t *testing.T) {
	r := rtr.New[string]()

	// Valid: Both routes use :year at the first parameter position
	r.Add(consts.MethodGet, "/users/:year/:title", "Route 1")
	r.Add(consts.MethodGet, "/users/:year/posts/:postId", "Route 2")

	// Verify both routes work correctly
	data, params := r.Lookup(consts.MethodGet, "/users/2024/easter-message")
	assert.Equal(t, len(params), 2)
	assert.Equal(t, params[0].Key, "year")
	assert.Equal(t, params[0].Value, "2024")
	assert.Equal(t, params[1].Key, "title")
	assert.Equal(t, params[1].Value, "easter-message")
	assert.Equal(t, data, "Route 1")

	data, params = r.Lookup(consts.MethodGet, "/users/2024/posts/123")
	assert.Equal(t, len(params), 2)
	assert.Equal(t, params[0].Key, "year")
	assert.Equal(t, params[0].Value, "2024")
	assert.Equal(t, params[1].Key, "postId")
	assert.Equal(t, params[1].Value, "123")
	assert.Equal(t, data, "Route 2")
}

// TestParameterNameConflictDetection verifies that the router panics when
// routes with conflicting parameter names at the same position are registered.
func TestParameterNameConflictDetection(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("Expected panic due to conflicting parameter names, but no panic occurred")
		}

		// Verify the panic message contains useful information
		msg, ok := recovered.(string)
		if !ok {
			t.Fatalf("Expected string panic message, got %T: %v", recovered, recovered)
		}

		// Check that the error message is descriptive
		assert.True(t, strings.Contains(msg, "conflicting parameter names"))
		assert.True(t, strings.Contains(msg, "id"))
		assert.True(t, strings.Contains(msg, "userId"))
		assert.True(t, strings.Contains(msg, "same position"))
	}()

	r := rtr.New[string]()

	// First route establishes :id as the parameter at this position
	r.Add(consts.MethodGet, "/users/:id", "Route 1")

	// This should panic because :userId conflicts with :id at the same position
	// Both routes share /users/ prefix and then immediately have a parameter,
	// so they must use the same parameter name
	r.Add(consts.MethodGet, "/users/:userId/profile", "Route 2")
}

// TestParameterNameConflictAtDifferentDepths verifies that parameter names
// at different depths can be different (they don't share nodes).
func TestParameterNameConflictAtDifferentDepths(t *testing.T) {
	r := rtr.New[string]()

	// These are valid because the parameters are at different depths
	r.Add(consts.MethodGet, "/api/v1/:id", "API v1")
	r.Add(consts.MethodGet, "/api/v2/:userId", "API v2")
	r.Add(consts.MethodGet, "/api/v3/:resourceId", "API v3")

	// Verify all routes work correctly
	data, params := r.Lookup(consts.MethodGet, "/api/v1/123")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, params[0].Value, "123")
	assert.Equal(t, data, "API v1")

	data, params = r.Lookup(consts.MethodGet, "/api/v2/456")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "userId")
	assert.Equal(t, params[0].Value, "456")
	assert.Equal(t, data, "API v2")

	data, params = r.Lookup(consts.MethodGet, "/api/v3/789")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "resourceId")
	assert.Equal(t, params[0].Value, "789")
	assert.Equal(t, data, "API v3")
}

// TestSecondParameterConflict verifies conflict detection for the second
// parameter position in routes.
func TestSecondParameterConflict(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("Expected panic due to conflicting parameter names, but no panic occurred")
		}

		msg, ok := recovered.(string)
		if !ok {
			t.Fatalf("Expected string panic message, got %T: %v", recovered, recovered)
		}

		// Check that the error mentions both conflicting parameter names
		assert.True(t, strings.Contains(msg, "conflicting parameter names"))
		assert.True(t, strings.Contains(msg, "title"))
		assert.True(t, strings.Contains(msg, "slug"))
	}()

	r := rtr.New[string]()

	// First route establishes :year and :title
	r.Add(consts.MethodGet, "/posts/:year/:title", "Route 1")

	// This should panic because :slug conflicts with :title at the second position
	// Note: :year is fine (first position matches), but :slug vs :title causes conflict
	r.Add(consts.MethodGet, "/posts/:year/:slug", "Route 2")
}

// TestThirdParameterConflict verifies conflict detection works for deeply nested
// parameter positions (third level and beyond).
func TestThirdParameterConflict(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("Expected panic due to conflicting parameter names, but no panic occurred")
		}

		msg, ok := recovered.(string)
		if !ok {
			t.Fatalf("Expected string panic message, got %T: %v", recovered, recovered)
		}

		assert.True(t, strings.Contains(msg, "conflicting parameter names"))
		assert.True(t, strings.Contains(msg, "commentId"))
		assert.True(t, strings.Contains(msg, "replyId"))
	}()

	r := rtr.New[string]()

	// First route establishes three consecutive parameters
	r.Add(consts.MethodGet, "/posts/:year/:title/:commentId", "Route 1")

	// This should panic at the third parameter position
	r.Add(consts.MethodGet, "/posts/:year/:title/:replyId", "Route 2")
}

// TestMultipleRoutesConsistentParams verifies that many routes can share
// the same parameter position successfully when they use the same name.
func TestMultipleRoutesConsistentParams(t *testing.T) {
	r := rtr.New[string]()

	// All these routes use :id at the first parameter position
	r.Add(consts.MethodGet, "/users/:id", "Get user")
	r.Add(consts.MethodGet, "/users/:id/profile", "Get profile")
	r.Add(consts.MethodGet, "/users/:id/posts", "Get posts")
	r.Add(consts.MethodGet, "/users/:id/settings", "Get settings")
	r.Add(consts.MethodGet, "/users/:id/friends", "Get friends")

	// Verify all routes work
	data, params := r.Lookup(consts.MethodGet, "/users/123")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, data, "Get user")

	data, params = r.Lookup(consts.MethodGet, "/users/456/profile")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, data, "Get profile")

	data, params = r.Lookup(consts.MethodGet, "/users/789/friends")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, data, "Get friends")
}

// TestParameterAfterStaticSegment verifies that parameters after static
// segments are independent (don't share nodes even at the same depth).
func TestParameterAfterStaticSegment(t *testing.T) {
	r := rtr.New[string]()

	// These are valid because the static segments (admin vs user) create
	// different branches in the tree, so the parameters don't share nodes
	r.Add(consts.MethodGet, "/admin/:userId", "Admin route")
	r.Add(consts.MethodGet, "/user/:profileId", "User route")

	// Verify both routes work with different parameter names
	data, params := r.Lookup(consts.MethodGet, "/admin/123")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "userId")
	assert.Equal(t, data, "Admin route")

	data, params = r.Lookup(consts.MethodGet, "/user/456")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "profileId")
	assert.Equal(t, data, "User route")
}

// TestSameRouteReregistration verifies that re-registering the exact same
// route with the same parameter names works (route update scenario).
func TestSameRouteReregistration(t *testing.T) {
	r := rtr.New[string]()

	// Register route
	r.Add(consts.MethodGet, "/users/:id/posts/:postId", "Handler v1")

	// Re-register same route with same parameter names (should work)
	r.Add(consts.MethodGet, "/users/:id/posts/:postId", "Handler v2")

	// Verify the handler was updated
	data, params := r.Lookup(consts.MethodGet, "/users/123/posts/456")
	assert.Equal(t, len(params), 2)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, params[1].Key, "postId")
	assert.Equal(t, data, "Handler v2") // Should be the updated handler
}

// TestMixedStaticAndParameterRoutes verifies complex route structures
// with both static and parameter segments interleaved.
func TestMixedStaticAndParameterRoutes(t *testing.T) {
	r := rtr.New[string]()

	// Complex route structure
	r.Add(consts.MethodGet, "/api/:version/users/:userId/posts", "List posts")
	r.Add(consts.MethodGet, "/api/:version/users/:userId/posts/:postId", "Get post")
	r.Add(consts.MethodGet, "/api/:version/users/:userId/comments", "List comments")

	// Verify all routes work
	data, params := r.Lookup(consts.MethodGet, "/api/v1/users/123/posts")
	assert.Equal(t, len(params), 2)
	assert.Equal(t, params[0].Key, "version")
	assert.Equal(t, params[0].Value, "v1")
	assert.Equal(t, params[1].Key, "userId")
	assert.Equal(t, params[1].Value, "123")
	assert.Equal(t, data, "List posts")

	data, params = r.Lookup(consts.MethodGet, "/api/v2/users/456/posts/789")
	assert.Equal(t, len(params), 3)
	assert.Equal(t, params[0].Key, "version")
	assert.Equal(t, params[1].Key, "userId")
	assert.Equal(t, params[2].Key, "postId")
	assert.Equal(t, data, "Get post")
}
