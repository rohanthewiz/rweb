package rweb_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// The router's parameter-name rule as an app meets it: routes of one method
// that share a parameter position must use one name (the shared node holds
// one name, and lookup reports values under it), so registering a clash
// panics at startup instead of handing the second route's handler an empty
// Param. Router-level cases are in core/rtr/parameter_validation_test.go.

// registerPanic runs register and returns its panic message, or "" if it
// did not panic.
func registerPanic(t *testing.T, register func()) (msg string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			s, ok := r.(string)
			if !ok {
				t.Fatalf("want a string panic, got %T: %v", r, r)
			}
			msg = s
		}
	}()
	register()
	return ""
}

func TestRouteParamNameClashPanics(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/users/:id", func(ctx rweb.Context) error { return nil })

	msg := registerPanic(t, func() {
		s.Get("/users/:userId/profile", func(ctx rweb.Context) error { return nil })
	})
	assert.True(t, strings.Contains(msg, "conflicting parameter names"))
	assert.True(t, strings.Contains(msg, `"/users/:userId/profile"`))
}

// A group registers its prefix joined to the route, so the panic names the
// full path, which is what the app has to look for.
func TestRouteParamNameClashInGroupNamesFullPath(t *testing.T) {
	s := rweb.NewServer()
	users := s.Group("/users")
	users.Get("/:id", func(ctx rweb.Context) error { return nil })

	msg := registerPanic(t, func() {
		users.Get("/:year/:filename", func(ctx rweb.Context) error { return nil })
	})
	assert.True(t, strings.Contains(msg, `"/users/:year/:filename"`))
	assert.True(t, strings.Contains(msg, ":year"))
	assert.True(t, strings.Contains(msg, ":id"))
}

// Shared positions with one name, and different names under different
// methods, register and route with the right values.
func TestRouteParamNamesConsistentAndPerMethod(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/users/:id", func(ctx rweb.Context) error {
		return ctx.WriteString("user " + ctx.Request().Param("id"))
	})
	s.Get("/users/:id/profile", func(ctx rweb.Context) error {
		return ctx.WriteString("profile " + ctx.Request().Param("id"))
	})
	s.Delete("/users/:userId", func(ctx rweb.Context) error {
		return ctx.WriteString("deleted " + ctx.Request().Param("userId"))
	})

	for _, c := range []struct{ method, path, want string }{
		{"GET", "/users/5", "user 5"},
		{"GET", "/users/5/profile", "profile 5"},
		{"DELETE", "/users/5", "deleted 5"},
	} {
		resp := s.Request(c.method, c.path, nil, nil)
		assert.Equal(t, http.StatusOK, resp.Status())
		assert.Equal(t, c.want, string(resp.Body()))
	}
}
