package rweb

import (
	"encoding/json"
	"testing"

	"github.com/rohanthewiz/assert"
)

// TestSSEHubRegisterUnregister verifies basic client lifecycle and ClientCount accuracy.
func TestSSEHubRegisterUnregister(t *testing.T) {
	hub := NewSSEHub()
	assert.Equal(t, 0, hub.ClientCount())

	ch1 := make(chan any, 8)
	ch2 := make(chan any, 8)

	hub.Register(ch1)
	assert.Equal(t, 1, hub.ClientCount())

	hub.Register(ch2)
	assert.Equal(t, 2, hub.ClientCount())

	hub.Unregister(ch1)
	assert.Equal(t, 1, hub.ClientCount())

	hub.Unregister(ch2)
	assert.Equal(t, 0, hub.ClientCount())
}

// TestSSEHubUnregisterIdempotent ensures calling Unregister twice on the same channel
// does not panic from a double-close.
func TestSSEHubUnregisterIdempotent(t *testing.T) {
	hub := NewSSEHub()
	ch := make(chan any, 8)

	hub.Register(ch)
	hub.Unregister(ch)
	// Second call should be a no-op, not a panic
	hub.Unregister(ch)
	assert.Equal(t, 0, hub.ClientCount())
}

// TestSSEHubBroadcast verifies that Broadcast JSON-wraps the event and delivers
// it as a "message" SSEvent to all registered clients.
func TestSSEHubBroadcast(t *testing.T) {
	hub := NewSSEHub()
	ch1 := make(chan any, 8)
	ch2 := make(chan any, 8)

	hub.Register(ch1)
	hub.Register(ch2)

	hub.Broadcast(SSEvent{Type: "update", Data: "hello"})

	// Both clients should receive the wrapped event
	for _, ch := range []chan any{ch1, ch2} {
		select {
		case evt := <-ch:
			ssEvt, ok := evt.(SSEvent)
			assert.Equal(t, true, ok)
			assert.Equal(t, "message", ssEvt.Type)

			// Parse the JSON data to verify wrapping
			var payload map[string]any
			err := json.Unmarshal([]byte(ssEvt.Data.(string)), &payload)
			assert.Nil(t, err)
			assert.Equal(t, "update", payload["type"])
			assert.Equal(t, "hello", payload["data"])
		default:
			t.Fatal("expected event on client channel but got none")
		}
	}

	hub.Unregister(ch1)
	hub.Unregister(ch2)
}

// TestSSEHubBroadcastRaw verifies that BroadcastRaw sends the SSEvent as-is
// without JSON wrapping.
func TestSSEHubBroadcastRaw(t *testing.T) {
	hub := NewSSEHub()
	ch := make(chan any, 8)
	hub.Register(ch)

	hub.BroadcastRaw(SSEvent{Type: "custom-event", Data: "raw-data"})

	select {
	case evt := <-ch:
		ssEvt, ok := evt.(SSEvent)
		assert.Equal(t, true, ok)
		// Should preserve original type and data — no wrapping
		assert.Equal(t, "custom-event", ssEvt.Type)
		assert.Equal(t, "raw-data", ssEvt.Data)
	default:
		t.Fatal("expected event on client channel but got none")
	}

	hub.Unregister(ch)
}

// TestSSEHubBroadcastSkipsFullChannels ensures Broadcast does not block when a
// client's channel buffer is full.
func TestSSEHubBroadcastSkipsFullChannels(t *testing.T) {
	hub := NewSSEHub()

	// Create a channel with capacity 1 so it fills quickly
	ch := make(chan any, 1)
	hub.Register(ch)

	// Fill the channel
	hub.Broadcast(SSEvent{Type: "a", Data: "1"})

	// This should not block even though the channel is full
	hub.Broadcast(SSEvent{Type: "b", Data: "2"})

	// Only the first event should be in the channel
	assert.Equal(t, 1, len(ch))

	hub.Unregister(ch)
}

// TestSSEHubBroadcastAny verifies the convenience wrapper constructs an SSEvent
// and delegates to Broadcast.
func TestSSEHubBroadcastAny(t *testing.T) {
	hub := NewSSEHub()
	ch := make(chan any, 8)
	hub.Register(ch)

	hub.BroadcastAny("notification", "you have mail")

	select {
	case evt := <-ch:
		ssEvt, ok := evt.(SSEvent)
		assert.Equal(t, true, ok)
		assert.Equal(t, "message", ssEvt.Type)

		var payload map[string]any
		err := json.Unmarshal([]byte(ssEvt.Data.(string)), &payload)
		assert.Nil(t, err)
		assert.Equal(t, "notification", payload["type"])
		assert.Equal(t, "you have mail", payload["data"])
	default:
		t.Fatal("expected event on client channel but got none")
	}

	hub.Unregister(ch)
}

// TestSSEHubHandler verifies that the Handler method creates a proper rweb handler
// that registers a client, sets sseCleanup, and configures SSE on the context.
func TestSSEHubHandler(t *testing.T) {
	hub := NewSSEHub()
	s := NewServer()

	handler := hub.Handler(s)

	// Create a context via the server's internal constructor
	ctx := s.newContext()

	// Call the handler — it should register a client and set up SSE
	err := handler(ctx)
	assert.Nil(t, err)

	// Hub should have one registered client
	assert.Equal(t, 1, hub.ClientCount())

	// Context should have an SSE channel and cleanup set
	assert.Equal(t, true, ctx.sseEventsChan != nil)
	assert.Equal(t, true, ctx.sseCleanup != nil)
	assert.Equal(t, "message", ctx.sseEventName)

	// Simulate sendSSE exit by calling cleanup — should auto-unregister
	ctx.sseCleanup()
	assert.Equal(t, 0, hub.ClientCount())
}
