// Package main demonstrates WebSocket support in the rweb framework.
// This example shows how to create WebSocket endpoints for real-time bidirectional communication.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rohanthewiz/element"
	"github.com/rohanthewiz/rweb"
)

//go:embed static/style.css
var cssContent string

//go:embed static/app.js
var jsContent string

// Message represents a WebSocket message structure for our chat application
type Message struct {
	Type      string    `json:"type"`      // Message type: "chat", "system", "ping"
	Content   string    `json:"content"`   // Message content
	Sender    string    `json:"sender"`    // Sender identifier
	Timestamp time.Time `json:"timestamp"` // Message timestamp
}

// Hub manages WebSocket connections and message broadcasting.
// It uses a channel-based event loop to serialize access to the client map,
// with a mutex for the broadcast path where concurrent reads are safe.
type Hub struct {
	clients    map[*rweb.WSConn]bool
	broadcast  chan Message
	register   chan *rweb.WSConn
	unregister chan *rweb.WSConn
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*rweb.WSConn]bool),
		broadcast:  make(chan Message, 100),
		register:   make(chan *rweb.WSConn),
		unregister: make(chan *rweb.WSConn),
	}
}

// Run starts the hub's event loop for managing clients and broadcasting messages
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

			// Send welcome message to the new client
			welcome := Message{
				Type:      "system",
				Content:   "Welcome to the WebSocket chat!",
				Sender:    "Server",
				Timestamp: time.Now(),
			}
			data, _ := json.Marshal(welcome)
			client.WriteMessage(rweb.TextMessage, data)

			// Notify other clients about the new connection
			h.broadcast <- Message{
				Type:      "system",
				Content:   fmt.Sprintf("A new user joined. Total users: %d", len(h.clients)),
				Sender:    "Server",
				Timestamp: time.Now(),
			}

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close(1000, "Client disconnected")
				h.mu.Unlock()

				h.broadcast <- Message{
					Type:      "system",
					Content:   fmt.Sprintf("A user left. Total users: %d", len(h.clients)),
					Sender:    "Server",
					Timestamp: time.Now(),
				}
			} else {
				h.mu.Unlock()
			}

		case message := <-h.broadcast:
			// Fan out the message to all connected clients
			h.mu.RLock()
			data, _ := json.Marshal(message)
			for client := range h.clients {
				err := client.WriteMessage(rweb.TextMessage, data)
				if err != nil {
					// Schedule removal of failed client outside the read lock
					go func(c *rweb.WSConn) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// ClientCount returns the number of connected clients (thread-safe)
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func main() {
	s := rweb.NewServer(
		rweb.WithAddress(":8080"),
		rweb.WithVerbose(),
	)

	hub := NewHub()
	go hub.Run()

	// Serve the HTML client page built with the element builder
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML(buildPage())
	})

	// Simple echo WebSocket endpoint — demonstrates basic bidirectional messaging
	s.WebSocket("/ws/echo", func(ws *rweb.WSConn) error {
		defer ws.Close(1000, "Server closing connection")

		fmt.Printf("Echo WebSocket connected from %s\n", ws.RemoteAddr())

		err := ws.WriteMessage(rweb.TextMessage, []byte("Connected to echo server"))
		if err != nil {
			return err
		}

		// Read loop — echo back every message with a prefix
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				fmt.Printf("Echo read error: %v\n", err)
				break
			}

			switch msg.Type {
			case rweb.TextMessage:
				response := fmt.Sprintf("Echo: %s", string(msg.Data))
				if err := ws.WriteMessage(rweb.TextMessage, []byte(response)); err != nil {
					fmt.Printf("Echo write error: %v\n", err)
					return nil
				}
			case rweb.BinaryMessage:
				if err := ws.WriteMessage(rweb.BinaryMessage, msg.Data); err != nil {
					fmt.Printf("Echo write error: %v\n", err)
					return nil
				}
			case rweb.CloseMessage:
				fmt.Println("Received close message")
				return nil
			}
		}

		return nil
	})

	// Chat WebSocket endpoint — multi-client broadcasting via Hub
	s.WebSocket("/ws/chat", func(ws *rweb.WSConn) error {
		hub.register <- ws
		defer func() {
			hub.unregister <- ws
		}()

		fmt.Printf("Chat WebSocket connected from %s\n", ws.RemoteAddr())

		// Ping/pong keepalive so idle connections aren't dropped by proxies.
		// Done() ensures the ticker goroutine exits cleanly when the connection closes.
		ws.SetPongHandler(func(data []byte) error {
			fmt.Printf("Received pong from %s\n", ws.RemoteAddr())
			return nil
		})

		go func() {
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ws.Done():
					return
				case <-ticker.C:
					ws.WritePing([]byte("ping"))
				}
			}
		}()

		// Read loop — broadcast incoming messages to all clients
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				fmt.Printf("Chat read error from %s: %v\n", ws.RemoteAddr(), err)
				break
			}

			if msg.Type == rweb.TextMessage {
				var incomingMsg Message
				if err := json.Unmarshal(msg.Data, &incomingMsg); err != nil {
					// If not valid JSON, wrap as plain text chat message
					incomingMsg = Message{
						Type:      "chat",
						Content:   string(msg.Data),
						Sender:    ws.RemoteAddr().String(),
						Timestamp: time.Now(),
					}
				}
				hub.broadcast <- incomingMsg
			}
		}

		return nil
	})

	// Status endpoint for monitoring
	s.Get("/status", func(ctx rweb.Context) error {
		return ctx.WriteJSON(map[string]any{
			"status":            "running",
			"connected_clients": hub.ClientCount(),
			"endpoints": []string{
				"/ws/echo - Simple echo server",
				"/ws/chat - Multi-client chat",
			},
		})
	})

	fmt.Println("WebSocket server starting on :8080")
	fmt.Println("Open http://localhost:8080 in your browser to test")
	log.Fatal(s.Run())
}

// buildPage generates the demo UI using the element builder.
// Two side-by-side panels: echo server (left) and chat server (right).
func buildPage() string {
	b := element.B()

	b.Html().R(
		b.Head().R(
			b.Title().T("RWeb WebSocket Demo"),
			b.Style().T(cssContent),
		),
		b.Body().R(
			b.H1().T("RWeb WebSocket Demo"),

			b.DivClass("container").R(
				// Echo server panel
				b.DivClass("panel").R(
					b.H2().T("Echo Server (/ws/echo)"),
					b.DivClass("controls").R(
						b.Input("type", "text", "id", "echoInput",
							"placeholder", "Type a message..."),
						b.Button("id", "echoSend").T("Send"),
						b.Button("id", "echoConnect").T("Connect"),
					),
					b.Div("id", "echoMessages", "class", "messages").R(),
					b.Div("id", "echoStatus", "class", "status disconnected").T("Disconnected"),
				),

				// Chat server panel
				b.DivClass("panel").R(
					b.H2().T("Chat Server (/ws/chat)"),
					b.DivClass("controls").R(
						b.Input("type", "text", "id", "chatInput",
							"placeholder", "Type a message..."),
						b.Button("id", "chatSend").T("Send"),
						b.Button("id", "chatConnect").T("Connect"),
					),
					b.Div("id", "chatMessages", "class", "messages").R(),
					b.Div("id", "chatStatus", "class", "status disconnected").T("Disconnected"),
				),
			),

			b.Script().T(jsContent),
		),
	)

	return b.String()
}
