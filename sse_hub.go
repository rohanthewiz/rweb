package rweb

import (
	"encoding/json"
	"fmt"
	"sync"
)

// SSEHub manages multiple SSE client connections with fan-out broadcast capability.
// Each client gets its own buffered channel; Broadcast sends to all connected clients.
// SSEHub is standalone (not tied to a Server) so it can be shared across routes.
//
// Typical usage:
//
//	hub := rweb.NewSSEHub()
//	s.Get("/events", hub.Handler(s))
//	hub.Broadcast(rweb.SSEvent{Type: "update", Data: "new data"})
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan any]bool
}

// NewSSEHub creates a new SSEHub ready to accept client registrations.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan any]bool),
	}
}

// Register adds a client channel to the hub so it receives broadcast events.
func (h *SSEHub) Register(client chan any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = true
}

// Unregister removes a client channel from the hub and closes it.
// Safe to call multiple times — the channel is only closed if still registered,
// preventing double-close panics.
func (h *SSEHub) Unregister(client chan any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Guard against double-close: only close if we still track this client
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client)
	}
}

// Broadcast JSON-wraps the SSEvent's Type and Data into {"type":..., "data":...}
// and sends it as a standard "message" SSE event to all clients.
// This design lets JS EventSource clients use a single `onmessage` handler
// and JSON.parse() the data to extract the event type and payload.
// Non-blocking: slow or full client channels are skipped.
func (h *SSEHub) Broadcast(event SSEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Build a JSON payload wrapping type + data for easy client-side parsing
	payload := map[string]any{
		"type": event.Type,
		"data": event.Data,
	}

	bytPayload, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("SSEHub: failed to marshal broadcast event: %v\n", err)
		return
	}

	// Wrap in a standard "message" SSE event so JS clients can use onmessage
	wrappedEvent := SSEvent{
		Type: "message",
		Data: string(bytPayload),
	}

	for client := range h.clients {
		select {
		case client <- wrappedEvent:
		default:
			// Client channel is full — skip to avoid blocking the broadcaster
		}
	}
}

// BroadcastRaw sends the SSEvent directly to all clients without JSON wrapping.
// Use this when you want full control over the SSE event type and data format,
// e.g., for clients using addEventListener(eventType, ...) on specific event names.
// Non-blocking: slow or full client channels are skipped.
func (h *SSEHub) BroadcastRaw(event SSEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		select {
		case client <- event:
		default:
			// Client channel is full — skip to avoid blocking the broadcaster
		}
	}
}

// BroadcastAny is a convenience wrapper that constructs an SSEvent from the
// given event type and data, then delegates to Broadcast for JSON-wrapped delivery.
func (h *SSEHub) BroadcastAny(eventType string, data any) {
	h.Broadcast(SSEvent{
		Type: eventType,
		Data: data,
	})
}

// Handler returns an rweb.Handler that manages per-client SSE lifecycle:
//   - Creates a buffered channel (cap 8) for the connecting client
//   - Registers the channel with the hub
//   - Sets up automatic unregister via ctx.sseCleanup (called when sendSSE exits)
//   - Configures SSE streaming on the context
//
// The optional eventName parameter sets the default SSE event type (defaults to "message").
// Since SSEHub.Broadcast sends SSEvent structs with their own Type field, the eventName
// here is mainly used as a fallback for non-SSEvent data on the channel.
func (h *SSEHub) Handler(server *Server, eventName ...string) Handler {
	evtName := "message"
	if len(eventName) > 0 && eventName[0] != "" {
		evtName = eventName[0]
	}

	return func(c Context) error {
		// Create a per-client buffered channel; capacity 8 gives headroom for bursts
		clientChan := make(chan any, 8)
		h.Register(clientChan)

		// Access the private context struct to set the cleanup callback.
		// Since SSEHub lives in the rweb package, this type assertion is valid.
		ctx := c.(*context)
		ctx.sseCleanup = func() {
			h.Unregister(clientChan)
		}

		return server.SetupSSE(c, clientChan, evtName)
	}
}

// ClientCount returns the number of currently registered clients.
// Useful for monitoring and debugging SSE connection state.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
