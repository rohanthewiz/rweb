package rweb

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// SSEHubOptions configures optional behaviors for an SSEHub.
// All fields have sensible defaults so a zero-value SSEHubOptions works out of the box.
type SSEHubOptions struct {
	// ChannelSize sets the buffered channel capacity per client.
	// Larger values absorb more burst traffic; smaller values detect slow clients faster.
	// Default: 8
	ChannelSize int

	// MaxDropped is the number of consecutive dropped (channel-full) messages before
	// the hub automatically evicts a client. This prevents stale clients from
	// accumulating when cleanup hasn't fired yet.
	// Default: 3. Set to 0 to disable auto-eviction.
	MaxDropped int

	// HeartbeatInterval, when > 0, starts a background goroutine that periodically
	// sends an SSE comment (`:keepalive\n\n`) to all clients. This prevents proxies,
	// load balancers, and firewalls from killing idle connections (typically 30-60s).
	// Call Close() to stop the heartbeat goroutine.
	// Default: 0 (disabled)
	HeartbeatInterval time.Duration

	// OnDisconnect is called whenever a client is removed from the hub — either by
	// normal unregister or by auto-eviction. Optional.
	OnDisconnect func()
}

// hubClient tracks per-client state within the hub.
// Currently used for drop-counting; extensible for future per-client metadata.
type hubClient struct {
	dropped int // consecutive broadcast sends that fell into the default (channel full) branch
}

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
	clients map[chan any]*hubClient
	opts    SSEHubOptions
	done    chan struct{} // signals heartbeat goroutine to stop
}

// NewSSEHub creates a new SSEHub ready to accept client registrations.
// An optional SSEHubOptions configures channel size, auto-eviction, and heartbeat.
// If omitted, sensible defaults are used (channelSize=8, maxDropped=3, no heartbeat).
func NewSSEHub(options ...SSEHubOptions) *SSEHub {
	var opts SSEHubOptions
	if len(options) > 0 {
		opts = options[0]
	}

	// Apply defaults for zero-valued fields
	if opts.ChannelSize <= 0 {
		opts.ChannelSize = 8
	}
	if opts.MaxDropped <= 0 && len(options) == 0 {
		// Only default to 3 when no options were passed at all.
		// Explicit SSEHubOptions{MaxDropped: 0} means "disable eviction".
		opts.MaxDropped = 3
	}

	hub := &SSEHub{
		clients: make(map[chan any]*hubClient),
		opts:    opts,
		done:    make(chan struct{}),
	}

	// Start heartbeat goroutine if configured
	if opts.HeartbeatInterval > 0 {
		go hub.runHeartbeat()
	}

	return hub
}

// runHeartbeat periodically sends an SSE comment keepalive to all clients.
// SSE comments (lines starting with `:`) are ignored by EventSource's onmessage
// but keep the TCP connection alive through intermediary proxies.
func (h *SSEHub) runHeartbeat() {
	ticker := time.NewTicker(h.opts.HeartbeatInterval)
	defer ticker.Stop()

	// sseKeepalive is a sentinel value that sendSSE recognizes and writes as `:keepalive\n\n`
	keepalive := sseKeepalive{}

	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client <- keepalive:
				default:
					// Channel full — heartbeat is best-effort, skip this client
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Close stops the heartbeat goroutine (if running) and releases resources.
// Safe to call multiple times; subsequent calls are no-ops.
func (h *SSEHub) Close() {
	select {
	case <-h.done:
		// Already closed
	default:
		close(h.done)
	}
}

// Register adds a client channel to the hub so it receives broadcast events.
func (h *SSEHub) Register(client chan any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = &hubClient{}
}

// Unregister removes a client channel from the hub and closes it.
// Safe to call multiple times — the channel is only closed if still registered,
// preventing double-close panics. Fires OnDisconnect if configured.
func (h *SSEHub) Unregister(client chan any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unregisterLocked(client)
}

// unregisterLocked removes and closes a client while the write lock is held.
// Fires OnDisconnect callback if configured.
func (h *SSEHub) unregisterLocked(client chan any) {
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client)

		if h.opts.OnDisconnect != nil {
			h.opts.OnDisconnect()
		}
	}
}

// broadcastToClients sends a value to all clients, tracking dropped messages
// and auto-evicting stale clients that exceed MaxDropped consecutive drops.
// This is the shared send loop used by both Broadcast and BroadcastRaw.
func (h *SSEHub) broadcastToClients(value any) {
	h.mu.RLock()

	// Collect stale clients during the read pass so we don't upgrade the lock
	// on every broadcast — only when eviction is needed.
	var stale []chan any

	for client, hc := range h.clients {
		select {
		case client <- value:
			// Successful send — reset the drop counter
			hc.dropped = 0
		default:
			hc.dropped++
			// Check if this client has exceeded the eviction threshold
			if h.opts.MaxDropped > 0 && hc.dropped >= h.opts.MaxDropped {
				stale = append(stale, client)
			}
		}
	}

	h.mu.RUnlock()

	// Evict stale clients under a write lock
	if len(stale) > 0 {
		h.mu.Lock()
		for _, client := range stale {
			h.unregisterLocked(client)
		}
		h.mu.Unlock()
	}
}

// Broadcast JSON-wraps the SSEvent's Type and Data into {"type":..., "data":...}
// and sends it as a standard "message" SSE event to all clients.
// This design lets JS EventSource clients use a single `onmessage` handler
// and JSON.parse() the data to extract the event type and payload.
// Non-blocking: slow or full client channels are skipped (and may be evicted).
func (h *SSEHub) Broadcast(event SSEvent) {
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

	h.broadcastToClients(wrappedEvent)
}

// BroadcastRaw sends the SSEvent directly to all clients without JSON wrapping.
// Use this when you want full control over the SSE event type and data format,
// e.g., for clients using addEventListener(eventType, ...) on specific event names.
// Non-blocking: slow or full client channels are skipped (and may be evicted).
func (h *SSEHub) BroadcastRaw(event SSEvent) {
	h.broadcastToClients(event)
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
//   - Creates a buffered channel (using ChannelSize from options) for the connecting client
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
		// Create a per-client buffered channel; size from options (default 8)
		clientChan := make(chan any, h.opts.ChannelSize)
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
