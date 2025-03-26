package rweb

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	// WebSocket constants
	WebSocketVersion           = "13"
	WebSocketProtocol          = "websocket"
	HeaderUpgrade              = "Upgrade"
	HeaderConnection           = "Connection"
	HeaderSecWebSocketKey      = "Sec-WebSocket-Key"
	HeaderSecWebSocketVersion  = "Sec-WebSocket-Version"
	HeaderSecWebSocketAccept   = "Sec-WebSocket-Accept"
	HeaderSecWebSocketProtocol = "Sec-WebSocket-Protocol"

	// WebSocket OpCodes
	WSOpContinuation = 0x0
	WSOpText         = 0x1
	WSOpBinary       = 0x2
	WSOpClose        = 0x8
	WSOpPing         = 0x9
	WSOpPong         = 0xA

	// WebSocket Close Codes
	WSCloseNormalClosure           = 1000
	WSCloseGoingAway               = 1001
	WSCloseProtocolError           = 1002
	WSCloseUnsupportedData         = 1003
	WSCloseNoStatusReceived        = 1005
	WSCloseAbnormalClosure         = 1006
	WSCloseInvalidFramePayloadData = 1007
	WSClosePolicyViolation         = 1008
	WSCloseMessageTooBig           = 1009
	WSCloseMandatoryExtension      = 1010
	WSCloseInternalServerErr       = 1011
	WSCloseTLSHandshake            = 1015
)

// Define WebSocket Structures

// WebSocketMessage represents a WebSocket message
type WebSocketMessage struct {
	OpCode  byte
	Payload []byte
}

// WebSocketConfig holds configuration for WebSocket connections
type WebSocketConfig struct {
	// Maximum message size allowed
	MaxMessageSize int64

	// Time allowed to read the next pong message
	PongWait time.Duration

	// Send pings with this period
	PingPeriod time.Duration

	// Buffer size for read and write channels
	BufferSize int
}

// DefaultWebSocketConfig returns default WebSocket configuration
func DefaultWebSocketConfig() WebSocketConfig {
	return WebSocketConfig{
		MaxMessageSize: 512 * 1024, // 512KB
		PongWait:       60 * time.Second,
		PingPeriod:     (60 * time.Second * 9) / 10, // slightly less than pong wait
		BufferSize:     256,
	}
}

// WebSocketHandler defines the interface for handling WebSocket connections
type WebSocketHandler interface {
	// OnConnect is called when a new WebSocket connection is established
	OnConnect(conn *WebSocketConn)

	// OnMessage is called when a message is received
	OnMessage(messageType byte, data []byte)

	// OnClose is called when the connection is closed
	OnClose(code int, text string)

	// OnError is called when an error occurs
	OnError(err error)
}

// WebSocketConn represents a WebSocket connection
type WebSocketConn struct {
	conn       net.Conn
	config     WebSocketConfig
	readMutex  sync.Mutex
	writeMutex sync.Mutex
	closeOnce  sync.Once
	isClosed   bool
}

// Implement WebSocket Connection Functions

// newWebSocketConn creates a new WebSocket connection
func newWebSocketConn(conn net.Conn, config WebSocketConfig) *WebSocketConn {
	return &WebSocketConn{
		conn:   conn,
		config: config,
	}
}

// ReadMessage reads a message from the WebSocket connection
func (c *WebSocketConn) ReadMessage() (byte, []byte, error) {
	c.readMutex.Lock()
	defer c.readMutex.Unlock()

	if c.isClosed {
		return 0, nil, errors.New("connection closed")
	}

	// Read first two bytes for header
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return 0, nil, err
	}

	fin := (header[0] & 0x80) != 0
	opCode := header[0] & 0x0F
	masked := (header[1] & 0x80) != 0
	payloadLen := int64(header[1] & 0x7F)

	// Handle extended payload length
	if payloadLen == 126 {
		extLen := make([]byte, 2)
		if _, err := io.ReadFull(c.conn, extLen); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(extLen))
	} else if payloadLen == 127 {
		extLen := make([]byte, 8)
		if _, err := io.ReadFull(c.conn, extLen); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(extLen))
	}

	// Check message size
	if payloadLen > c.config.MaxMessageSize {
		return 0, nil, fmt.Errorf("message too large: %d bytes", payloadLen)
	}

	// Read masking key if message is masked
	var maskKey []byte
	if masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(c.conn, maskKey); err != nil {
			return 0, nil, err
		}
	}

	// Read payload
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return 0, nil, err
	}

	// Unmask payload if necessary
	if masked {
		for i := int64(0); i < payloadLen; i++ {
			payload[i] ^= maskKey[i%4]
		}
	}

	// Handle continuation frames (we should accumulate them)
	// This is a simplified implementation
	if !fin {
		// In a real implementation, you would accumulate fragments until fin is true
		nextOpCode, nextPayload, err := c.ReadMessage()
		if err != nil {
			return 0, nil, err
		}

		if nextOpCode == WSOpContinuation {
			payload = append(payload, nextPayload...)
		} else {
			return 0, nil, fmt.Errorf("expected continuation frame, got %d", nextOpCode)
		}
	}

	return opCode, payload, nil
}

// WriteMessage writes a message to the WebSocket connection
func (c *WebSocketConn) WriteMessage(opCode byte, payload []byte) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if c.isClosed {
		return errors.New("connection closed")
	}

	// Determine length
	var payloadLen byte
	var extPayloadLen []byte

	if len(payload) < 126 {
		payloadLen = byte(len(payload))
	} else if len(payload) <= 65535 {
		payloadLen = 126
		extBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(extBuf, uint16(len(payload)))
		extPayloadLen = extBuf
	} else {
		payloadLen = 127
		extBuf := make([]byte, 8)
		binary.BigEndian.PutUint64(extBuf, uint64(len(payload)))
		extPayloadLen = extBuf
	}

	// Create header
	// For client, we must mask the payload
	header := make([]byte, 2)
	header[0] = 0x80 | opCode     // FIN bit set
	header[1] = 0x80 | payloadLen // MASK bit set for client

	// Generate random mask
	mask := make([]byte, 4)
	_, err := rand.Read(mask)
	if err != nil {
		return err
	}

	// Write header
	if _, err := c.conn.Write(header); err != nil {
		return err
	}

	// Write extended length if needed
	if len(extPayloadLen) > 0 {
		if _, err := c.conn.Write(extPayloadLen); err != nil {
			return err
		}
	}

	// Write mask
	if _, err := c.conn.Write(mask); err != nil {
		return err
	}

	// Apply mask to payload
	maskedPayload := make([]byte, len(payload))
	for i := 0; i < len(payload); i++ {
		maskedPayload[i] = payload[i] ^ mask[i%4]
	}

	// Write masked payload
	if _, err := c.conn.Write(maskedPayload); err != nil {
		return err
	}

	return nil
}

// Close closes the WebSocket connection
func (c *WebSocketConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.isClosed = true

		// Send close frame with normal closure code
		closeMsg := make([]byte, 2)
		binary.BigEndian.PutUint16(closeMsg, WSCloseNormalClosure)
		err = c.WriteMessage(WSOpClose, closeMsg)

		// Give time for the close frame to be sent before closing the TCP connection
		time.Sleep(100 * time.Millisecond)

		err = c.conn.Close()
	})
	return err
}

// SendText sends a text message
func (c *WebSocketConn) SendText(message string) error {
	return c.WriteMessage(WSOpText, []byte(message))
}

// SendBinary sends a binary message
func (c *WebSocketConn) SendBinary(data []byte) error {
	return c.WriteMessage(WSOpBinary, data)
}

// SendPing sends a ping message
func (c *WebSocketConn) SendPing(data []byte) error {
	return c.WriteMessage(WSOpPing, data)
}

// SendPong sends a pong message
func (c *WebSocketConn) SendPong(data []byte) error {
	return c.WriteMessage(WSOpPong, data)
}

// WebSocketClient provides a client for WebSocket connections
type WebSocketClient struct {
	url          string
	conn         *WebSocketConn
	handler      WebSocketHandler
	config       WebSocketConfig
	connected    bool
	mu           sync.Mutex
	done         chan struct{}
	pingTicker   *time.Ticker
	pongDeadline time.Time
}

// NewWebSocketClient creates a new WebSocket client
func NewWebSocketClient(url string, handler WebSocketHandler, config ...WebSocketConfig) *WebSocketClient {
	wsConfig := DefaultWebSocketConfig()
	if len(config) > 0 {
		wsConfig = config[0]
	}

	// Ensure URL starts with ws:// or wss://
	if !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		url = "ws://" + url
	}

	return &WebSocketClient{
		url:     url,
		handler: handler,
		config:  wsConfig,
		done:    make(chan struct{}),
	}
}

// generateWebSocketKey generates a random key for the WebSocket handshake
func generateWebSocketKey() string {
	key := make([]byte, 16)
	rand.Read(key)
	return base64.StdEncoding.EncodeToString(key)
}

// computeAcceptKey computes the expected Sec-WebSocket-Accept value
func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(websocketGUID)) // WebSocket GUID
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Connect establishes a WebSocket connection
func (c *WebSocketClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	// Parse URL
	wsURL, err := url.Parse(c.url)
	if err != nil {
		return err
	}

	// Determine scheme and port
	scheme := "ws"
	port := "80"
	if wsURL.Scheme == "wss" {
		scheme = "wss"
		port = "443"
	}
	if wsURL.Port() != "" {
		port = wsURL.Port()
	}

	// Create host string
	host := wsURL.Hostname() + ":" + port

	// Create a TCP connection
	var conn net.Conn
	if scheme == "wss" {
		conn, err = tls.Dial("tcp", host, &tls.Config{})
	} else {
		conn, err = net.Dial("tcp", host)
	}
	if err != nil {
		return err
	}

	// Generate WebSocket key
	wsKey := generateWebSocketKey()

	// Create HTTP request for upgrade
	path := wsURL.Path
	if path == "" {
		path = "/"
	}
	if wsURL.RawQuery != "" {
		path += "?" + wsURL.RawQuery
	}

	request := fmt.Sprintf(
		"GET %s HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n"+
			"\r\n",
		path, wsURL.Host, wsKey)

	// Send upgrade request
	if _, err = conn.Write([]byte(request)); err != nil {
		conn.Close()
		return err
	}

	// Read HTTP response
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return err
	}
	defer resp.Body.Close()

	// Check if upgrade was successful
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("websocket upgrade failed: %d %s, body: %s",
			resp.StatusCode, resp.Status, string(body))
	}

	// Verify headers
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		conn.Close()
		return errors.New("invalid upgrade header")
	}

	if !strings.EqualFold(resp.Header.Get("Connection"), "upgrade") {
		conn.Close()
		return errors.New("invalid connection header")
	}

	// Verify the accept key
	expectedAccept := computeAcceptKey(wsKey)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		conn.Close()
		return errors.New("invalid websocket accept key")
	}

	// Create WebSocket connection
	c.conn = newWebSocketConn(conn, c.config)
	c.connected = true

	// Start ping/pong handler
	c.pingTicker = time.NewTicker(c.config.PingPeriod)
	c.pongDeadline = time.Now().Add(c.config.PongWait)

	// Start read pump
	go c.readPump()

	// Start ping pump
	go c.pingPump()

	// Notify handler of connection
	c.handler.OnConnect(c.conn)

	return nil
}

// readPump reads messages from the WebSocket connection
func (c *WebSocketClient) readPump() {
	defer func() {
		c.Close()
	}()

	for {
		select {
		case <-c.done:
			return
		default:
			opCode, payload, err := c.conn.ReadMessage()
			if err != nil {
				c.handler.OnError(err)
				return
			}

			switch opCode {
			case WSOpText, WSOpBinary:
				c.handler.OnMessage(opCode, payload)

			case WSOpPong:
				// Update pong deadline
				c.mu.Lock()
				c.pongDeadline = time.Now().Add(c.config.PongWait)
				c.mu.Unlock()

			case WSOpPing:
				// Respond with Pong
				if err := c.conn.SendPong(payload); err != nil {
					c.handler.OnError(err)
					return
				}

			case WSOpClose:
				// Parse close code and reason
				closeCode := WSCloseNormalClosure
				closeReason := ""

				if len(payload) >= 2 {
					closeCode = int(binary.BigEndian.Uint16(payload[:2]))
					if len(payload) > 2 {
						closeReason = string(payload[2:])
					}
				}

				c.handler.OnClose(closeCode, closeReason)
				return
			}
		}
	}
}

// pingPump sends periodic pings
func (c *WebSocketClient) pingPump() {
	for {
		select {
		case <-c.pingTicker.C:
			c.mu.Lock()
			// Check if we've received a pong recently
			if time.Now().After(c.pongDeadline) {
				// No pong received in time
				c.mu.Unlock()
				c.handler.OnError(errors.New("ping timeout"))
				c.Close()
				return
			}

			// Send ping
			if err := c.conn.SendPing([]byte{}); err != nil {
				c.mu.Unlock()
				c.handler.OnError(err)
				c.Close()
				return
			}
			c.mu.Unlock()

		case <-c.done:
			return
		}
	}
}

// SendText sends a text message
func (c *WebSocketClient) SendText(message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return errors.New("not connected")
	}

	return c.conn.SendText(message)
}

// SendBinary sends a binary message
func (c *WebSocketClient) SendBinary(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return errors.New("not connected")
	}

	return c.conn.SendBinary(data)
}

// Close closes the WebSocket connection
func (c *WebSocketClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return nil
	}

	select {
	case <-c.done:
		// Already closed
	default:
		close(c.done)
	}

	if c.pingTicker != nil {
		c.pingTicker.Stop()
	}

	c.connected = false
	return c.conn.Close()
}

// IsConnected returns true if the client is connected
func (c *WebSocketClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// BaseWebSocketHandler provides a default implementation of WebSocketHandler
type BaseWebSocketHandler struct {
	OnConnectFunc func(conn *WebSocketConn)
	OnMessageFunc func(messageType byte, data []byte)
	OnCloseFunc   func(code int, text string)
	OnErrorFunc   func(err error)
}

// OnConnect handles new connections
func (h *BaseWebSocketHandler) OnConnect(conn *WebSocketConn) {
	if h.OnConnectFunc != nil {
		h.OnConnectFunc(conn)
	}
}

// OnMessage handles incoming messages
func (h *BaseWebSocketHandler) OnMessage(messageType byte, data []byte) {
	if h.OnMessageFunc != nil {
		h.OnMessageFunc(messageType, data)
	}
}

// OnClose handles connection closures
func (h *BaseWebSocketHandler) OnClose(code int, text string) {
	if h.OnCloseFunc != nil {
		h.OnCloseFunc(code, text)
	}
}

// OnError handles errors
func (h *BaseWebSocketHandler) OnError(err error) {
	if h.OnErrorFunc != nil {
		h.OnErrorFunc(err)
	}
}
