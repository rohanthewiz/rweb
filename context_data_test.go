package rweb

import (
	"testing"

	"github.com/rohanthewiz/assert"
)

func TestContextData(t *testing.T) {
	s := NewServer()
	ctx := s.newContext()

	// Test Set and Get
	ctx.Set("key1", "value1")
	ctx.Set("key2", 123)
	ctx.Set("key3", true)

	assert.Equal(t, "value1", ctx.Get("key1"))
	assert.Equal(t, 123, ctx.Get("key2"))
	assert.Equal(t, true, ctx.Get("key3"))

	// Test Has
	assert.True(t, ctx.Has("key1"))
	assert.True(t, ctx.Has("key2"))
	assert.False(t, ctx.Has("nonexistent"))

	// Test Get non-existent key
	assert.Nil(t, ctx.Get("nonexistent"))

	// Test Delete
	ctx.Delete("key1")
	assert.False(t, ctx.Has("key1"))
	assert.Nil(t, ctx.Get("key1"))

	// Test overwrite
	ctx.Set("key2", "new value")
	assert.Equal(t, "new value", ctx.Get("key2"))

	// Test complex data types
	ctx.Set("user", map[string]any{
		"id":    1,
		"name":  "John",
		"admin": true,
	})
	
	user := ctx.Get("user").(map[string]any)
	assert.Equal(t, 1, user["id"])
	assert.Equal(t, "John", user["name"])
	assert.Equal(t, true, user["admin"])

	// Test Clean clears data
	ctx.Clean()
	assert.False(t, ctx.Has("key2"))
	assert.False(t, ctx.Has("key3"))
	assert.False(t, ctx.Has("user"))
}

func TestContextDataNilMap(t *testing.T) {
	// Test behavior when data map is nil
	ctx := &context{}

	// Get should return nil
	assert.Nil(t, ctx.Get("any"))

	// Has should return false
	assert.False(t, ctx.Has("any"))

	// Delete should not panic
	ctx.Delete("any")

	// Set should initialize the map
	ctx.Set("key", "value")
	assert.Equal(t, "value", ctx.Get("key"))
	assert.True(t, ctx.Has("key"))
}