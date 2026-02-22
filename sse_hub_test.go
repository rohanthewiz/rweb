package rweb

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
)

// TestSSEHubRegisterUnregister verifies basic client lifecycle and ClientCount accuracy.
func TestSSEHubRegisterUnregister(t *testing.T) {
	hub := NewSSEHub()
	defer hub.Close()
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
	defer hub.Close()
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
	defer hub.Close()
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
	defer hub.Close()
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
	defer hub.Close()

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
	defer hub.Close()
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
	defer hub.Close()
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

// TestSSEHubStaleClientEviction verifies that clients whose channels stay full
// for MaxDropped consecutive broadcasts are automatically evicted from the hub.
func TestSSEHubStaleClientEviction(t *testing.T) {
	var disconnectCount atomic.Int32

	hub := NewSSEHub(SSEHubOptions{
		ChannelSize: 8,
		MaxDropped:  2,
		OnDisconnect: func() {
			disconnectCount.Add(1)
		},
	})
	defer hub.Close()

	// Healthy client that drains its channel
	healthy := make(chan any, 8)
	hub.Register(healthy)

	// Stale client with capacity 1 — will fill immediately
	stale := make(chan any, 1)
	hub.Register(stale)

	assert.Equal(t, 2, hub.ClientCount())

	// First broadcast: fills the stale client's channel (dropped=0 for this one since it fits)
	hub.BroadcastRaw(SSEvent{Type: "a", Data: "1"})

	// Drain healthy so it stays alive
	<-healthy

	// Second broadcast: stale channel is full, dropped increments to 1
	hub.BroadcastRaw(SSEvent{Type: "b", Data: "2"})
	<-healthy

	// Stale client still registered (dropped=1 < MaxDropped=2)
	assert.Equal(t, 2, hub.ClientCount())

	// Third broadcast: stale channel still full, dropped increments to 2 → eviction
	hub.BroadcastRaw(SSEvent{Type: "c", Data: "3"})
	<-healthy

	// Stale client should be evicted
	assert.Equal(t, 1, hub.ClientCount())
	assert.Equal(t, int32(1), disconnectCount.Load())

	hub.Unregister(healthy)
	assert.Equal(t, 0, hub.ClientCount())
}

// TestSSEHubEvictionDisabled verifies that setting MaxDropped=0 explicitly
// disables auto-eviction, preserving the original skip-and-move-on behavior.
func TestSSEHubEvictionDisabled(t *testing.T) {
	hub := NewSSEHub(SSEHubOptions{
		MaxDropped: 0, // explicitly disable eviction
	})
	defer hub.Close()

	// Tiny channel that fills immediately
	ch := make(chan any, 1)
	hub.Register(ch)

	// Broadcast many times — none should evict the client
	for i := range 20 {
		hub.BroadcastRaw(SSEvent{Type: "x", Data: i})
	}

	assert.Equal(t, 1, hub.ClientCount())

	hub.Unregister(ch)
}

// TestSSEHubHeartbeatDelivery verifies that when HeartbeatInterval is set,
// the hub periodically sends sseKeepalive values to all clients.
func TestSSEHubHeartbeatDelivery(t *testing.T) {
	hub := NewSSEHub(SSEHubOptions{
		HeartbeatInterval: 50 * time.Millisecond,
	})
	defer hub.Close()

	ch := make(chan any, 8)
	hub.Register(ch)

	// Wait long enough for at least one heartbeat tick
	time.Sleep(120 * time.Millisecond)

	// Should have received at least one keepalive
	received := false
	for len(ch) > 0 {
		evt := <-ch
		if _, ok := evt.(sseKeepalive); ok {
			received = true
			break
		}
	}
	assert.Equal(t, true, received)

	hub.Unregister(ch)
}

// TestSSEHubCloseStopsHeartbeat verifies that Close() terminates the heartbeat
// goroutine so no more keepalives are sent.
func TestSSEHubCloseStopsHeartbeat(t *testing.T) {
	hub := NewSSEHub(SSEHubOptions{
		HeartbeatInterval: 30 * time.Millisecond,
	})

	ch := make(chan any, 32)
	hub.Register(ch)

	// Let a few heartbeats fire
	time.Sleep(100 * time.Millisecond)

	// Stop the heartbeat
	hub.Close()

	// Drain whatever is already buffered
	for len(ch) > 0 {
		<-ch
	}

	// Wait and verify no more keepalives arrive
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 0, len(ch))

	hub.Unregister(ch)
}

// TestSSEHubCloseIdempotent ensures calling Close() multiple times does not panic.
func TestSSEHubCloseIdempotent(t *testing.T) {
	hub := NewSSEHub(SSEHubOptions{
		HeartbeatInterval: 50 * time.Millisecond,
	})

	hub.Close()
	hub.Close() // should not panic
}

// TestSSEHubBackwardCompatibility verifies that NewSSEHub() with no arguments
// preserves the original behavior: channelSize=8, maxDropped=3.
func TestSSEHubBackwardCompatibility(t *testing.T) {
	hub := NewSSEHub()
	defer hub.Close()

	assert.Equal(t, 8, hub.opts.ChannelSize)
	assert.Equal(t, 3, hub.opts.MaxDropped)
	assert.Equal(t, time.Duration(0), hub.opts.HeartbeatInterval)
}

// TestSSEHubCustomChannelSize verifies that ChannelSize from options is used
// when creating client channels via Handler.
func TestSSEHubCustomChannelSize(t *testing.T) {
	hub := NewSSEHub(SSEHubOptions{
		ChannelSize: 16,
	})
	defer hub.Close()
	s := NewServer()

	handler := hub.Handler(s)
	ctx := s.newContext()

	err := handler(ctx)
	assert.Nil(t, err)

	// The channel capacity should match the configured size
	assert.Equal(t, 16, cap(ctx.sseEventsChan))

	ctx.sseCleanup()
}

// TestSSEHubOnDisconnectCalledOnUnregister verifies the OnDisconnect callback
// fires during normal unregistration (not just eviction).
func TestSSEHubOnDisconnectCalledOnUnregister(t *testing.T) {
	var called atomic.Int32

	hub := NewSSEHub(SSEHubOptions{
		OnDisconnect: func() {
			called.Add(1)
		},
	})
	defer hub.Close()

	ch := make(chan any, 8)
	hub.Register(ch)
	hub.Unregister(ch)

	assert.Equal(t, int32(1), called.Load())

	// Second unregister should be a no-op — callback should not fire again
	hub.Unregister(ch)
	assert.Equal(t, int32(1), called.Load())
}
