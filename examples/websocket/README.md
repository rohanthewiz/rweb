# RWeb WebSocket Example

This example demonstrates WebSocket support in the RWeb framework.

## Features

- **Echo Server** (`/ws/echo`): Simple echo server that returns messages sent to it
- **Chat Server** (`/ws/chat`): Multi-client chat room with broadcasting
- **Web Client**: HTML/JavaScript client for testing both endpoints
- **Status Endpoint** (`/status`): JSON API to check server status

## Running the Example

1. Navigate to this directory:
   ```bash
   cd examples/websocket
   ```

2. Run the server:
   ```bash
   go run main.go
   ```

3. Open your browser and navigate to:
   ```
   http://localhost:8080
   ```

4. Use the web interface to:
   - Connect to either WebSocket endpoint
   - Send messages and see responses
   - Open multiple browser tabs to test the chat functionality

## API Endpoints

### HTTP Endpoints

- `GET /` - Serves the HTML client interface
- `GET /status` - Returns JSON with server status and connected clients count

### WebSocket Endpoints

- `WS /ws/echo` - Echo server that returns any message sent to it
- `WS /ws/chat` - Chat server that broadcasts messages to all connected clients

## Message Format

The chat server uses JSON messages with the following structure:

```json
{
  "type": "chat",
  "content": "Hello, World!",
  "sender": "User123",
  "timestamp": "2024-01-01T12:00:00Z"
}
```

## WebSocket Features Demonstrated

1. **Connection Upgrade**: HTTP to WebSocket protocol upgrade
2. **Bidirectional Communication**: Real-time message exchange
3. **Broadcasting**: Sending messages to multiple connected clients
4. **Ping/Pong**: Keep-alive mechanism for connection health
5. **Error Handling**: Graceful handling of disconnections
6. **JSON Messaging**: Structured data exchange
7. **Binary Support**: The echo server can handle binary messages

## Testing with wscat

You can also test the WebSocket endpoints using `wscat`:

```bash
# Install wscat
npm install -g wscat

# Connect to echo server
wscat -c ws://localhost:8080/ws/echo

# Connect to chat server
wscat -c ws://localhost:8080/ws/chat
```

## Code Structure

- **Hub**: Manages multiple WebSocket connections and message broadcasting
- **Message**: Structured message type for the chat application
- **WebSocket Handlers**: Demonstrate different patterns for handling WebSocket connections
- **HTML Client**: Complete web interface for testing both endpoints