// Package main demonstrates SSEHub support in the rweb framework.
// This example shows how to broadcast Server-Sent Events to multiple connected clients
// using the built-in SSEHub fan-out pattern.
//
// Architecture:
//   - SSEHub manages client connections automatically via hub.Handler()
//   - Broadcast sends JSON-wrapped events to all clients as "message" events
//   - BroadcastRaw sends events with custom SSE event types (for addEventListener)
//   - HeartbeatInterval sends periodic keepalives to prevent proxy/LB idle timeouts
//   - MaxDropped auto-evicts stale clients whose channels stay full
//   - A background ticker simulates a real-time data source (e.g., stock prices, metrics)
//   - The /send endpoint lets you push custom messages from the browser
package main

import (
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/rohanthewiz/element"
	"github.com/rohanthewiz/rweb"
)

//go:embed static/style.css
var cssContent string

//go:embed static/app.js
var jsContent string

func main() {
	s := rweb.NewServer(
		rweb.WithAddress(":8080"),
		rweb.WithVerbose(),
	)

	// Create the SSE hub with hardened options for production-like resilience:
	//   - HeartbeatInterval keeps connections alive through proxies and load balancers
	//   - MaxDropped auto-evicts clients whose channels stay full (likely disconnected)
	//   - OnDisconnect logs when a client leaves (normal disconnect or eviction)
	hub := rweb.NewSSEHub(rweb.SSEHubOptions{
		ChannelSize:       8,
		MaxDropped:        3,
		HeartbeatInterval: 30 * time.Second,
		OnDisconnect: func() {
			fmt.Println("Client disconnected")
		},
	})
	// Close stops the heartbeat goroutine on shutdown
	defer hub.Close()

	// Serve the HTML demo page built with the element builder
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML(buildPage())
	})

	// SSE endpoint — hub.Handler() manages per-client lifecycle automatically:
	// creates a buffered channel (sized by ChannelSize), registers it,
	// and auto-unregisters on disconnect
	s.Get("/events", hub.Handler(s))

	// Endpoint to push a custom message via the browser form.
	// Demonstrates BroadcastAny — a convenience wrapper around Broadcast.
	s.Post("/send", func(ctx rweb.Context) error {
		msg := ctx.Request().FormValue("message")
		if msg == "" {
			msg = "(empty)"
		}
		hub.BroadcastAny("chat", msg)
		return ctx.WriteText("OK")
	})

	// Status endpoint showing connected client count
	s.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]any{
			"clients": hub.ClientCount(),
		})
	})

	// Background ticker — simulates a real-time data source pushing updates.
	// Broadcast wraps {type, data} in JSON under SSE event "message",
	// so JS clients can use a single onmessage handler + JSON.parse().
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		counter := 0
		for range ticker.C {
			counter++
			hub.Broadcast(rweb.SSEvent{
				Type: "tick",
				Data: fmt.Sprintf("Tick #%d — %s — %d client(s)",
					counter, time.Now().Format("15:04:05"), hub.ClientCount()),
			})
		}
	}()

	fmt.Println("SSE Hub demo starting on :8080")
	fmt.Println("Open http://localhost:8080 in multiple browser tabs to test broadcasting")
	log.Fatal(s.Run())
}

// buildPage generates the demo UI using the element builder.
// The page connects via EventSource, displays broadcast events,
// and provides a form to push custom messages.
func buildPage() string {
	b := element.B()

	b.Html().R(
		b.Head().R(
			b.Title().T("RWeb SSE Hub Demo"),
			b.Style().T(cssContent),
		),
		b.Body().R(
			b.H1().T("RWeb SSE Hub Demo"),
			b.PClass("hint").T("Open this page in multiple tabs to see broadcast in action"),

			// Send message panel
			b.DivClass("panel").R(
				b.H2().T("Send a Message"),
				b.DivClass("controls").R(
					b.Input("type", "text", "id", "msgInput",
						"placeholder", "Type a message to broadcast..."),
					b.Button("id", "sendBtn").T("Send"),
				),
			),

			// Events display panel
			b.DivClass("panel").R(
				b.H2().T("Events"),
				b.Div("id", "messages", "class", "messages").R(),
				b.Div("id", "status", "class", "status disconnected").T("Disconnected"),
			),

			b.Script().T(jsContent),
		),
	)

	return b.String()
}
