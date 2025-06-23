package rweb_test

import (
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestContextDataIntegration(t *testing.T) {
	s := rweb.NewServer()

	// First middleware - sets auth data
	s.Use(func(ctx rweb.Context) error {
		auth := ctx.Request().Header("Authorization")
		if auth == "Bearer valid-token" {
			ctx.Set("isAuthenticated", true)
			ctx.Set("userId", "123")
		}
		return ctx.Next()
	})

	// Second middleware - adds session data if authenticated
	s.Use(func(ctx rweb.Context) error {
		if ctx.Has("isAuthenticated") && ctx.Get("isAuthenticated").(bool) {
			ctx.Set("session", map[string]string{
				"theme": "dark",
				"lang":  "en",
			})
		}
		return ctx.Next()
	})

	// Handler that uses the context data
	s.Get("/protected", func(ctx rweb.Context) error {
		if !ctx.Has("isAuthenticated") || !ctx.Get("isAuthenticated").(bool) {
			return ctx.Status(401).WriteString("Unauthorized")
		}

		userId := ctx.Get("userId").(string)
		session := ctx.Get("session").(map[string]string)
		
		return ctx.WriteJSON(map[string]any{
			"userId":  userId,
			"theme":   session["theme"],
			"lang":    session["lang"],
		})
	})

	// Test without auth header
	response := s.Request(consts.MethodGet, "/protected", nil, nil)
	assert.Equal(t, response.Status(), 401)
	assert.Equal(t, string(response.Body()), "Unauthorized")

	// Test with auth header
	headers := []rweb.Header{{Key: "Authorization", Value: "Bearer valid-token"}}
	response = s.Request(consts.MethodGet, "/protected", headers, nil)
	assert.Equal(t, response.Status(), 200)
	assert.Contains(t, string(response.Body()), `"userId":"123"`)
	assert.Contains(t, string(response.Body()), `"theme":"dark"`)
	assert.Contains(t, string(response.Body()), `"lang":"en"`)
}

func TestContextDataCleanup(t *testing.T) {
	s := rweb.NewServer()
	
	requestCount := 0
	
	s.Get("/", func(ctx rweb.Context) error {
		requestCount++
		
		// Verify context is clean on each request
		assert.False(t, ctx.Has("previousData"))
		
		// Set some data for this request
		ctx.Set("previousData", requestCount)
		
		return ctx.WriteString("OK")
	})

	// Make multiple requests
	for i := 0; i < 3; i++ {
		response := s.Request(consts.MethodGet, "/", nil, nil)
		assert.Equal(t, response.Status(), 200)
	}
	
	assert.Equal(t, requestCount, 3)
}