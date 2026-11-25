package rtr_test

import (
	"fmt"
	"math/rand"
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

// addRecover registers a route and returns what its Add panicked with, if
// anything.
func addRecover(r *rtr.RadixRouter[string], method, path, data string) (recovered any) {
	defer func() { recovered = recover() }()
	r.Add(method, path, data)
	return nil
}

// The panic names the route being registered, so the clash can be found in
// an app with many routes, and both parameter names in the ':name' form a
// route is written in.
func TestParamConflictMessageNamesRoute(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/users/:id", "Route 1")

	recovered := addRecover(r, consts.MethodGet, "/users/:userId/profile", "Route 2")
	msg, ok := recovered.(string)
	if !ok {
		t.Fatalf("want a string panic, got %T: %v", recovered, recovered)
	}
	assert.True(t, strings.Contains(msg, `"/users/:userId/profile"`))
	assert.True(t, strings.Contains(msg, ":userId"))
	assert.True(t, strings.Contains(msg, ":id"))
}

// A refused route changes nothing: the check runs before the Add has
// touched the tree, so the routes already registered keep their handlers and
// names, and the refused route is not routed (it does not resolve under the
// earlier route's name). Matters to an app or test that recovers the panic.
func TestParamConflictLeavesTreeIntact(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/users/:id", "user")
	r.Add(consts.MethodGet, "/users/:id/posts", "posts")

	for _, clash := range []string{"/users/:userId", "/users/:userId/profile", "/users/:uid/posts"} {
		if addRecover(r, consts.MethodGet, clash, "clash") == nil {
			t.Fatalf("Add(%s) did not panic", clash)
		}
	}

	data, params := r.Lookup(consts.MethodGet, "/users/5")
	assert.Equal(t, data, "user")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")

	data, params = r.Lookup(consts.MethodGet, "/users/5/posts")
	assert.Equal(t, data, "posts")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")

	data, _ = r.Lookup(consts.MethodGet, "/users/5/profile")
	assert.Equal(t, data, "")
}

// Each method has its own tree, so the same position may be named
// differently under different methods.
func TestParamNamesPerMethod(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/users/:id", "get")
	r.Add(consts.MethodDelete, "/users/:userId", "delete")
	r.Add(consts.MethodPost, "/users/:uid/posts", "post")

	data, params := r.Lookup(consts.MethodGet, "/users/5")
	assert.Equal(t, data, "get")
	assert.Equal(t, params[0].Key, "id")

	data, params = r.Lookup(consts.MethodDelete, "/users/5")
	assert.Equal(t, data, "delete")
	assert.Equal(t, params[0].Key, "userId")

	data, params = r.Lookup(consts.MethodPost, "/users/5/posts")
	assert.Equal(t, data, "post")
	assert.Equal(t, params[0].Key, "uid")
}

// Randomised check of the rule over many route shapes and registration
// orders, which the hand-written cases above cannot cover:
//
//   - consistent names: every route resolves to its own handler with its own
//     names and values (the check refuses nothing it should accept);
//   - random names: Add panics exactly when a parameter's position is
//     already held under another name, and afterwards every route that was
//     accepted still resolves correctly (a refused Add leaves no trace).
//
// A parameter's position is identified by the route's pattern before it with
// parameter names dropped ("/users/:/posts/" for :postId in
// /users/:id/posts/:postId): two routes share a parameter node exactly when
// that key is equal. Request values start with 'z', which begins no static
// segment, so lookup never prefers a static sibling over the parameter.
func TestParamNamesRandomized(t *testing.T) {
	statics := []string{"a", "b", "users", "u", "posts", "p", "c"}
	names := []string{"id", "uid", "x", "y", "slug"}

	type route struct {
		path  string
		names []string // parameter names, in order
		keys  []string // position key of each parameter
	}

	// gen builds a route of 1 to 4 segments, each static or a parameter. With
	// fixed non-nil, the name at a position comes from fixed (and is chosen
	// once), so no two routes clash.
	gen := func(rng *rand.Rand, fixed map[string]string) route {
		var path, key strings.Builder
		var rt route
		for d := 1 + rng.Intn(4); d > 0; d-- {
			path.WriteByte('/')
			key.WriteByte('/')
			if rng.Intn(2) == 0 {
				s := statics[rng.Intn(len(statics))]
				path.WriteString(s)
				key.WriteString(s)
				continue
			}
			k := key.String()
			name := names[rng.Intn(len(names))]
			if fixed != nil {
				if n, ok := fixed[k]; ok {
					name = n
				} else {
					fixed[k] = name
				}
			}
			path.WriteByte(':')
			path.WriteString(name)
			key.WriteByte(':')
			rt.names = append(rt.names, name)
			rt.keys = append(rt.keys, k)
		}
		rt.path = path.String()
		return rt
	}

	// resolves reports whether rt's request finds rt with its own names and
	// values, in order.
	resolves := func(r *rtr.RadixRouter[string], rt route) error {
		segs := strings.Split(rt.path, "/")
		var vals []string
		for i, s := range segs {
			if strings.HasPrefix(s, ":") {
				segs[i] = fmt.Sprintf("z%d", len(vals))
				vals = append(vals, segs[i])
			}
		}
		req := strings.Join(segs, "/")
		data, params := r.Lookup(consts.MethodGet, req)
		if data != rt.path || len(params) != len(rt.names) {
			return fmt.Errorf("GET %s => %q %v, want %q with %v", req, data, params, rt.path, rt.names)
		}
		for i, p := range params {
			if p.Key != rt.names[i] || p.Value != vals[i] {
				return fmt.Errorf("GET %s => param %d %s=%s, want %s=%s", req, i, p.Key, p.Value, rt.names[i], vals[i])
			}
		}
		return nil
	}

	for _, consistent := range []bool{true, false} {
		accepted, refused := 0, 0
		for seed := int64(1); seed <= 2000; seed++ {
			rng := rand.New(rand.NewSource(seed))
			r := rtr.New[string]()
			var fixed map[string]string
			if consistent {
				fixed = map[string]string{}
			}
			held := map[string]string{} // position key -> name in the tree
			live := map[string]route{}  // accepted routes by path
			var order []string

			for n := 2 + rng.Intn(10); n > 0; n-- {
				rt := gen(rng, fixed)
				clash := false
				for j, k := range rt.keys {
					if h, ok := held[k]; ok && h != rt.names[j] {
						clash = true
					}
				}

				got := addRecover(r, consts.MethodGet, rt.path, rt.path)
				if (got != nil) != clash {
					t.Fatalf("consistent=%v seed %d: Add(%s) panicked=%v, want %v; accepted %v",
						consistent, seed, rt.path, got != nil, clash, order)
				}
				if got != nil {
					refused++
					continue
				}
				accepted++
				for j, k := range rt.keys {
					held[k] = rt.names[j]
				}
				if _, dup := live[rt.path]; !dup {
					order = append(order, rt.path)
				}
				live[rt.path] = rt
			}

			for _, p := range order {
				if err := resolves(r, live[p]); err != nil {
					t.Fatalf("consistent=%v seed %d: %v; accepted %v", consistent, seed, err, order)
				}
			}
		}
		if consistent && refused != 0 {
			t.Fatalf("consistent names: %d routes refused", refused)
		}
		if !consistent && refused == 0 {
			t.Fatal("random names: no route was refused, so the conflict path went untested")
		}
		t.Logf("consistent=%v: %d accepted, %d refused", consistent, accepted, refused)
	}
}
