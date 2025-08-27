// Package main demonstrates WebSocket support in the rweb framework.
// This example shows how to create WebSocket endpoints for real-time bidirectional communication.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rohanthewiz/rweb"
)

// Message represents a WebSocket message structure for our chat application
type Message struct {
	Type      string    `json:"type"`      // Message type: "chat", "system", "ping"
	Content   string    `json:"content"`   // Message content
	Sender    string    `json:"sender"`    // Sender identifier
	Timestamp time.Time `json:"timestamp"` // Message timestamp
}

// Hub manages WebSocket connections and message broadcasting
type Hub struct {
	clients    map[*rweb.WSConn]bool // Connected clients
	broadcast  chan Message          // Channel for messages to broadcast
	register   chan *rweb.WSConn     // Channel to register new clients
	unregister chan *rweb.WSConn     // Channel to unregister clients
	mu         sync.RWMutex          // Mutex for thread-safe client map access
}

// NewHub creates a new Hub instance
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
			// Register new client
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

			// Notify other clients
			h.broadcast <- Message{
				Type:      "system",
				Content:   fmt.Sprintf("A new user joined. Total users: %d", len(h.clients)),
				Sender:    "Server",
				Timestamp: time.Now(),
			}

		case client := <-h.unregister:
			// Unregister client
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close(1000, "Client disconnected")
				h.mu.Unlock()

				// Notify other clients
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
			// Broadcast message to all connected clients
			h.mu.RLock()
			data, _ := json.Marshal(message)
			for client := range h.clients {
				err := client.WriteMessage(rweb.TextMessage, data)
				if err != nil {
					// Remove client if write fails
					go func(c *rweb.WSConn) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func main() {
	// Create server with verbose logging
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
	})

	// Create and start the hub for managing WebSocket connections
	hub := NewHub()
	go hub.Run()

	// Serve the HTML client page
	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteHTML(htmlClient)
	})

	// Simple echo WebSocket endpoint
	// This demonstrates basic WebSocket functionality
	s.WebSocket("/ws/echo", func(ws *rweb.WSConn) error {
		defer ws.Close(1000, "Server closing connection")

		fmt.Printf("Echo WebSocket connected from %s\n", ws.RemoteAddr())

		// Send initial message
		err := ws.WriteMessage(rweb.TextMessage, []byte("Connected to echo server"))
		if err != nil {
			return err
		}

		// Echo loop - read messages and send them back
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				fmt.Printf("Echo read error: %v\n", err)
				break
			}

			// Handle different message types
			switch msg.Type {
			case rweb.TextMessage:
				// Echo text messages back with prefix
				response := fmt.Sprintf("Echo: %s", string(msg.Data))
				if err := ws.WriteMessage(rweb.TextMessage, []byte(response)); err != nil {
					fmt.Printf("Echo write error: %v\n", err)
					break
				}

			case rweb.BinaryMessage:
				// Echo binary messages as-is
				if err := ws.WriteMessage(rweb.BinaryMessage, msg.Data); err != nil {
					fmt.Printf("Echo write error: %v\n", err)
					break
				}

			case rweb.CloseMessage:
				fmt.Println("Received close message")
				return nil
			}
		}

		return nil
	})

	// Chat WebSocket endpoint with broadcasting
	// This demonstrates a more complex use case with multiple connected clients
	s.WebSocket("/ws/chat", func(ws *rweb.WSConn) error {
		// Register client with hub
		hub.register <- ws
		defer func() {
			hub.unregister <- ws
		}()

		fmt.Printf("Chat WebSocket connected from %s\n", ws.RemoteAddr())

		// Set up ping/pong to keep connection alive
		ws.SetPongHandler(func(data []byte) error {
			fmt.Printf("Received pong from %s\n", ws.RemoteAddr())
			return nil
		})

		// Start periodic ping
		go func() {
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if err := ws.WritePing([]byte("ping")); err != nil {
						return
					}
				}
			}
		}()

		// Read messages from client
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				fmt.Printf("Chat read error from %s: %v\n", ws.RemoteAddr(), err)
				break
			}

			if msg.Type == rweb.TextMessage {
				// Parse incoming message
				var incomingMsg Message
				if err := json.Unmarshal(msg.Data, &incomingMsg); err != nil {
					// If not JSON, treat as plain text
					incomingMsg = Message{
						Type:      "chat",
						Content:   string(msg.Data),
						Sender:    ws.RemoteAddr().String(),
						Timestamp: time.Now(),
					}
				}

				// Broadcast to all clients
				hub.broadcast <- incomingMsg
			}
		}

		return nil
	})

	// Status endpoint to check WebSocket server status
	s.Get("/status", func(ctx rweb.Context) error {
		hub.mu.RLock()
		clientCount := len(hub.clients)
		hub.mu.RUnlock()

		status := map[string]interface{}{
			"status":            "running",
			"connected_clients": clientCount,
			"endpoints": []string{
				"/ws/echo - Simple echo server",
				"/ws/chat - Multi-client chat",
			},
		}

		return ctx.WriteJSON(status)
	})

	fmt.Println("WebSocket server starting on :8080")
	fmt.Println("Open http://localhost:8080 in your browser to test")
	log.Fatal(s.Run())
}

// HTML client for testing WebSocket functionality
const htmlClient = `<!DOCTYPE html>
<html>
<head>
    <title>RWeb WebSocket Demo</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            max-width: 1200px;
            margin: 0 auto;
            padding: 20px;
            background: #f5f5f5;
        }
        .container {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 20px;
        }
        .panel {
            background: white;
            border-radius: 8px;
            padding: 20px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 {
            color: #333;
            text-align: center;
        }
        h2 {
            color: #666;
            border-bottom: 2px solid #007bff;
            padding-bottom: 10px;
        }
        .controls {
            margin-bottom: 15px;
        }
        input[type="text"] {
            width: 70%;
            padding: 8px;
            border: 1px solid #ddd;
            border-radius: 4px;
            font-size: 14px;
        }
        button {
            padding: 8px 16px;
            background: #007bff;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 14px;
            margin-left: 5px;
        }
        button:hover {
            background: #0056b3;
        }
        button:disabled {
            background: #ccc;
            cursor: not-allowed;
        }
        .messages {
            height: 300px;
            overflow-y: auto;
            border: 1px solid #ddd;
            padding: 10px;
            background: #fafafa;
            border-radius: 4px;
            font-family: 'Courier New', monospace;
            font-size: 13px;
        }
        .message {
            margin-bottom: 8px;
            padding: 5px;
            border-radius: 3px;
        }
        .message.sent {
            background: #e3f2fd;
            text-align: right;
        }
        .message.received {
            background: #f3e5f5;
        }
        .message.system {
            background: #fff3e0;
            font-style: italic;
            color: #666;
        }
        .status {
            margin-top: 10px;
            padding: 5px;
            border-radius: 4px;
            font-size: 12px;
        }
        .status.connected {
            background: #c8e6c9;
            color: #2e7d32;
        }
        .status.disconnected {
            background: #ffcdd2;
            color: #c62828;
        }
        .timestamp {
            font-size: 11px;
            color: #999;
        }
    </style>
</head>
<body>
    <h1>RWeb WebSocket Demo</h1>
    
    <div class="container">
        <!-- Echo Server Panel -->
        <div class="panel">
            <h2>Echo Server (/ws/echo)</h2>
            <div class="controls">
                <input type="text" id="echoInput" placeholder="Type a message..." />
                <button id="echoSend">Send</button>
                <button id="echoConnect">Connect</button>
            </div>
            <div id="echoMessages" class="messages"></div>
            <div id="echoStatus" class="status disconnected">Disconnected</div>
        </div>
        
        <!-- Chat Server Panel -->
        <div class="panel">
            <h2>Chat Server (/ws/chat)</h2>
            <div class="controls">
                <input type="text" id="chatInput" placeholder="Type a message..." />
                <button id="chatSend">Send</button>
                <button id="chatConnect">Connect</button>
            </div>
            <div id="chatMessages" class="messages"></div>
            <div id="chatStatus" class="status disconnected">Disconnected</div>
        </div>
    </div>
    
    <script>
        // Echo WebSocket Client
        let echoWs = null;
        const echoMessages = document.getElementById('echoMessages');
        const echoStatus = document.getElementById('echoStatus');
        const echoInput = document.getElementById('echoInput');
        const echoSendBtn = document.getElementById('echoSend');
        const echoConnectBtn = document.getElementById('echoConnect');
        
        function connectEcho() {
            if (echoWs) {
                echoWs.close();
                return;
            }
            
            const wsUrl = 'ws://' + window.location.host + '/ws/echo';
            echoWs = new WebSocket(wsUrl);
            
            echoWs.onopen = function() {
                addEchoMessage('Connected to echo server', 'system');
                echoStatus.textContent = 'Connected';
                echoStatus.className = 'status connected';
                echoConnectBtn.textContent = 'Disconnect';
                echoSendBtn.disabled = false;
            };
            
            echoWs.onmessage = function(event) {
                addEchoMessage(event.data, 'received');
            };
            
            echoWs.onclose = function() {
                addEchoMessage('Disconnected from echo server', 'system');
                echoStatus.textContent = 'Disconnected';
                echoStatus.className = 'status disconnected';
                echoConnectBtn.textContent = 'Connect';
                echoSendBtn.disabled = true;
                echoWs = null;
            };
            
            echoWs.onerror = function(error) {
                addEchoMessage('Error: ' + error, 'system');
            };
        }
        
        function sendEchoMessage() {
            if (echoWs && echoWs.readyState === WebSocket.OPEN) {
                const message = echoInput.value.trim();
                if (message) {
                    echoWs.send(message);
                    addEchoMessage('You: ' + message, 'sent');
                    echoInput.value = '';
                }
            }
        }
        
        function addEchoMessage(message, type) {
            const msgDiv = document.createElement('div');
            msgDiv.className = 'message ' + type;
            const timestamp = new Date().toLocaleTimeString();
            msgDiv.innerHTML = message + ' <span class="timestamp">' + timestamp + '</span>';
            echoMessages.appendChild(msgDiv);
            echoMessages.scrollTop = echoMessages.scrollHeight;
        }
        
        echoConnectBtn.onclick = connectEcho;
        echoSendBtn.onclick = sendEchoMessage;
        echoSendBtn.disabled = true;
        echoInput.onkeypress = function(e) {
            if (e.key === 'Enter') sendEchoMessage();
        };
        
        // Chat WebSocket Client
        let chatWs = null;
        const chatMessages = document.getElementById('chatMessages');
        const chatStatus = document.getElementById('chatStatus');
        const chatInput = document.getElementById('chatInput');
        const chatSendBtn = document.getElementById('chatSend');
        const chatConnectBtn = document.getElementById('chatConnect');
        
        function connectChat() {
            if (chatWs) {
                chatWs.close();
                return;
            }
            
            const wsUrl = 'ws://' + window.location.host + '/ws/chat';
            chatWs = new WebSocket(wsUrl);
            
            chatWs.onopen = function() {
                addChatMessage('Connected to chat server', 'system');
                chatStatus.textContent = 'Connected';
                chatStatus.className = 'status connected';
                chatConnectBtn.textContent = 'Disconnect';
                chatSendBtn.disabled = false;
            };
            
            chatWs.onmessage = function(event) {
                try {
                    const msg = JSON.parse(event.data);
                    const content = msg.sender + ': ' + msg.content;
                    addChatMessage(content, msg.type === 'system' ? 'system' : 'received');
                } catch (e) {
                    addChatMessage(event.data, 'received');
                }
            };
            
            chatWs.onclose = function() {
                addChatMessage('Disconnected from chat server', 'system');
                chatStatus.textContent = 'Disconnected';
                chatStatus.className = 'status disconnected';
                chatConnectBtn.textContent = 'Connect';
                chatSendBtn.disabled = true;
                chatWs = null;
            };
            
            chatWs.onerror = function(error) {
                addChatMessage('Error: ' + error, 'system');
            };
        }
        
        function sendChatMessage() {
            if (chatWs && chatWs.readyState === WebSocket.OPEN) {
                const message = chatInput.value.trim();
                if (message) {
                    const msg = {
                        type: 'chat',
                        content: message,
                        sender: 'You',
                        timestamp: new Date().toISOString()
                    };
                    chatWs.send(JSON.stringify(msg));
                    addChatMessage('You: ' + message, 'sent');
                    chatInput.value = '';
                }
            }
        }
        
        function addChatMessage(message, type) {
            const msgDiv = document.createElement('div');
            msgDiv.className = 'message ' + type;
            const timestamp = new Date().toLocaleTimeString();
            msgDiv.innerHTML = message + ' <span class="timestamp">' + timestamp + '</span>';
            chatMessages.appendChild(msgDiv);
            chatMessages.scrollTop = chatMessages.scrollHeight;
        }
        
        chatConnectBtn.onclick = connectChat;
        chatSendBtn.onclick = sendChatMessage;
        chatSendBtn.disabled = true;
        chatInput.onkeypress = function(e) {
            if (e.key === 'Enter') sendChatMessage();
        };
    </script>
</body>
</html>`
