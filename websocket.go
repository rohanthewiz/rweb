package rweb

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// WebSocket opcodes as defined in RFC 6455
const (
	wsContinuation = 0x0
	wsText         = 0x1
	wsBinary       = 0x2
	wsClose        = 0x8
	wsPing         = 0x9
	wsPong         = 0xA
)

// WebSocket close codes
const (
	wsCloseNormalClosure           = 1000
	wsCloseGoingAway               = 1001
	wsCloseProtocolError           = 1002
	wsCloseUnsupportedData         = 1003
	wsCloseNoStatusReceived        = 1005
	wsCloseAbnormalClosure         = 1006
	wsCloseInvalidFramePayloadData = 1007
	wsClosePolicyViolation         = 1008
	wsCloseMessageTooBig           = 1009
	wsCloseMandatoryExtension      = 1010
	wsCloseInternalServerErr       = 1011
	wsCloseTLSHandshake            = 1015
)

// WebSocket errors
var (
	ErrWebSocketNotUpgraded     = errors.New("connection not upgraded to websocket")
	ErrWebSocketAlreadyClosed   = errors.New("websocket connection already closed")
	ErrWebSocketInvalidOpcode   = errors.New("invalid websocket opcode")
	ErrWebSocketPayloadTooLarge = errors.New("websocket payload too large")
	ErrWebSocketBadMask         = errors.New("websocket frame not masked")
)

// WebSocket GUID as per RFC 6455
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Default WebSocket configuration values
const (
	defaultMaxMessageSize = 1024 * 1024 * 10 // 10MB
	defaultPingInterval   = 30 * time.Second
	defaultPongTimeout    = 10 * time.Second
	defaultWriteTimeout   = 10 * time.Second
)

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type MessageType
	Data []byte
}

// MessageType represents the type of WebSocket message
type MessageType int

const (
	// TextMessage denotes a text data message
	TextMessage MessageType = wsText
	// BinaryMessage denotes a binary data message
	BinaryMessage MessageType = wsBinary
	// CloseMessage denotes a close control message
	CloseMessage MessageType = wsClose
	// PingMessage denotes a ping control message
	PingMessage MessageType = wsPing
	// PongMessage denotes a pong control message
	PongMessage MessageType = wsPong
)

// WSConn represents a WebSocket connection
type WSConn struct {
	conn           net.Conn
	isServer       bool
	closed         bool
	closeMutex     sync.Mutex
	writeMutex     sync.Mutex
	maxMessageSize int64
	closeHandlers  []func(code int, text string)
	pingHandler    func([]byte) error
	pongHandler    func([]byte) error
	readDeadline   time.Time
	writeDeadline  time.Time

	// for managing fragmented messages
	fragmentedMessage []byte
	fragmentedType    MessageType
}

// NewWSConn creates a new WebSocket connection from an existing net.Conn
// The isServer parameter indicates if this is a server-side connection
func NewWSConn(conn net.Conn, isServer bool) *WSConn {
	ws := &WSConn{
		conn:           conn,
		isServer:       isServer,
		maxMessageSize: defaultMaxMessageSize,
		closeHandlers:  make([]func(int, string), 0),
	}

	// Set default ping handler that responds with pong
	ws.pingHandler = func(data []byte) error {
		return ws.writePong(data)
	}

	// Set default pong handler (no-op)
	ws.pongHandler = func([]byte) error {
		return nil
	}

	return ws
}

// performHandshake performs the WebSocket handshake on the server side
// This validates the client's request and sends the appropriate response
func performHandshake(ctx *context) error {
	// Check for required headers
	if ctx.request.Header("Upgrade") != "websocket" {
		return errors.New("missing or invalid Upgrade header")
	}

	if !strings.Contains(strings.ToLower(ctx.request.Header("Connection")), "upgrade") {
		return errors.New("missing or invalid Connection header")
	}

	key := ctx.request.Header("Sec-WebSocket-Key")
	if key == "" {
		return errors.New("missing Sec-WebSocket-Key header")
	}

	version := ctx.request.Header("Sec-WebSocket-Version")
	if version != "13" {
		return errors.New("unsupported WebSocket version")
	}

	// Calculate the accept key
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Set response headers for successful upgrade
	ctx.response.SetStatus(101) // Switching Protocols
	ctx.response.SetHeader("Upgrade", "websocket")
	ctx.response.SetHeader("Connection", "Upgrade")
	ctx.response.SetHeader("Sec-WebSocket-Accept", acceptKey)

	// Handle optional protocol header
	if protocol := ctx.request.Header("Sec-WebSocket-Protocol"); protocol != "" {
		// For now, accept the first protocol offered
		// In a real implementation, you'd validate against supported protocols
		protocols := strings.Split(protocol, ",")
		if len(protocols) > 0 {
			ctx.response.SetHeader("Sec-WebSocket-Protocol", strings.TrimSpace(protocols[0]))
		}
	}

	return nil
}

// ReadMessage reads a complete message from the WebSocket connection
// It handles fragmentation and returns the complete message
func (ws *WSConn) ReadMessage() (*WSMessage, error) {
	for {
		frameType, data, err := ws.readFrame()
		if err != nil {
			return nil, err
		}

		switch frameType {
		case wsText, wsBinary:
			// Handle data frames
			return &WSMessage{
				Type: MessageType(frameType),
				Data: data,
			}, nil

		case wsContinuation:
			// Handle fragmented messages
			if ws.fragmentedMessage == nil {
				return nil, errors.New("unexpected continuation frame")
			}
			ws.fragmentedMessage = append(ws.fragmentedMessage, data...)
			// TODO: Check for FIN flag to see if message is complete

		case wsClose:
			// Handle close frame
			code := wsCloseNoStatusReceived
			text := ""
			if len(data) >= 2 {
				code = int(binary.BigEndian.Uint16(data[:2]))
				if len(data) > 2 {
					text = string(data[2:])
				}
			}
			ws.handleClose(code, text)
			return &WSMessage{Type: CloseMessage, Data: data}, nil

		case wsPing:
			// Handle ping frame
			if ws.pingHandler != nil {
				if err := ws.pingHandler(data); err != nil {
					return nil, err
				}
			}
			// Continue reading for the next message

		case wsPong:
			// Handle pong frame
			if ws.pongHandler != nil {
				if err := ws.pongHandler(data); err != nil {
					return nil, err
				}
			}
			// Continue reading for the next message

		default:
			return nil, fmt.Errorf("%w: %d", ErrWebSocketInvalidOpcode, frameType)
		}
	}
}

// WriteMessage writes a message to the WebSocket connection
func (ws *WSConn) WriteMessage(messageType MessageType, data []byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed {
		return ErrWebSocketAlreadyClosed
	}

	return ws.writeFrame(int(messageType), data)
}

// readFrame reads a single WebSocket frame
func (ws *WSConn) readFrame() (opcode int, payload []byte, err error) {
	// Read first 2 bytes
	header := make([]byte, 2)
	if _, err := io.ReadFull(ws.conn, header); err != nil {
		return 0, nil, err
	}

	// Parse first byte
	fin := (header[0] & 0x80) != 0
	_ = fin // TODO: Handle fragmentation properly
	opcode = int(header[0] & 0x0F)

	// Parse second byte
	masked := (header[1] & 0x80) != 0
	payloadLen := int64(header[1] & 0x7F)

	// Client frames must be masked, server frames must not be masked
	if ws.isServer && !masked {
		return 0, nil, ErrWebSocketBadMask
	}
	if !ws.isServer && masked {
		return 0, nil, ErrWebSocketBadMask
	}

	// Read extended payload length if needed
	if payloadLen == 126 {
		extLen := make([]byte, 2)
		if _, err := io.ReadFull(ws.conn, extLen); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(extLen))
	} else if payloadLen == 127 {
		extLen := make([]byte, 8)
		if _, err := io.ReadFull(ws.conn, extLen); err != nil {
			return 0, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(extLen))
	}

	// Check payload size
	if payloadLen > ws.maxMessageSize {
		return 0, nil, ErrWebSocketPayloadTooLarge
	}

	// Read mask key if present
	var maskKey []byte
	if masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(ws.conn, maskKey); err != nil {
			return 0, nil, err
		}
	}

	// Read payload
	payload = make([]byte, payloadLen)
	if _, err := io.ReadFull(ws.conn, payload); err != nil {
		return 0, nil, err
	}

	// Unmask payload if needed
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return opcode, payload, nil
}

// writeFrame writes a WebSocket frame
func (ws *WSConn) writeFrame(opcode int, data []byte) error {
	if ws.writeDeadline.After(time.Now()) {
		ws.conn.SetWriteDeadline(ws.writeDeadline)
	}

	// Create frame header
	header := make([]byte, 2)
	header[0] = 0x80 | byte(opcode) // FIN = 1, opcode

	dataLen := len(data)
	if !ws.isServer {
		header[1] = 0x80 // Set mask bit for client frames
	}

	// Determine payload length encoding
	var extLen []byte
	if dataLen < 126 {
		header[1] |= byte(dataLen)
	} else if dataLen <= 65535 {
		header[1] |= 126
		extLen = make([]byte, 2)
		binary.BigEndian.PutUint16(extLen, uint16(dataLen))
	} else {
		header[1] |= 127
		extLen = make([]byte, 8)
		binary.BigEndian.PutUint64(extLen, uint64(dataLen))
	}

	// Write header
	if _, err := ws.conn.Write(header); err != nil {
		return err
	}

	// Write extended length if needed
	if extLen != nil {
		if _, err := ws.conn.Write(extLen); err != nil {
			return err
		}
	}

	// Write mask and masked data for client frames
	if !ws.isServer {
		mask := make([]byte, 4)
		if _, err := rand.Read(mask); err != nil {
			return err
		}

		if _, err := ws.conn.Write(mask); err != nil {
			return err
		}

		// Write masked payload
		masked := make([]byte, len(data))
		for i := range data {
			masked[i] = data[i] ^ mask[i%4]
		}

		if _, err := ws.conn.Write(masked); err != nil {
			return err
		}
	} else {
		// Server frames are not masked
		if _, err := ws.conn.Write(data); err != nil {
			return err
		}
	}

	return nil
}

// Close closes the WebSocket connection with the given code and reason
func (ws *WSConn) Close(code int, reason string) error {
	ws.closeMutex.Lock()
	defer ws.closeMutex.Unlock()

	if ws.closed {
		return nil
	}

	// Send close frame
	data := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(data[:2], uint16(code))
	copy(data[2:], reason)

	if err := ws.writeFrame(wsClose, data); err != nil {
		// Even if writing the close frame fails, mark as closed
		ws.closed = true
		return ws.conn.Close()
	}

	ws.closed = true

	// Give the other side a chance to send their close frame
	time.Sleep(time.Second)

	return ws.conn.Close()
}

// handleClose handles an incoming close frame
func (ws *WSConn) handleClose(code int, text string) {
	ws.closeMutex.Lock()
	defer ws.closeMutex.Unlock()

	if ws.closed {
		return
	}

	// Call close handlers
	for _, handler := range ws.closeHandlers {
		handler(code, text)
	}

	// Send close response
	ws.closed = true
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, uint16(code))
	ws.writeFrame(wsClose, data)
	ws.conn.Close()
}

// writePong writes a pong frame
func (ws *WSConn) writePong(data []byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed {
		return ErrWebSocketAlreadyClosed
	}

	return ws.writeFrame(wsPong, data)
}

// WritePing writes a ping frame
func (ws *WSConn) WritePing(data []byte) error {
	ws.writeMutex.Lock()
	defer ws.writeMutex.Unlock()

	if ws.closed {
		return ErrWebSocketAlreadyClosed
	}

	return ws.writeFrame(wsPing, data)
}

// SetPingHandler sets the handler for ping messages
func (ws *WSConn) SetPingHandler(handler func([]byte) error) {
	ws.pingHandler = handler
}

// SetPongHandler sets the handler for pong messages
func (ws *WSConn) SetPongHandler(handler func([]byte) error) {
	ws.pongHandler = handler
}

// OnClose adds a close handler
func (ws *WSConn) OnClose(handler func(code int, text string)) {
	ws.closeHandlers = append(ws.closeHandlers, handler)
}

// SetMaxMessageSize sets the maximum message size
func (ws *WSConn) SetMaxMessageSize(size int64) {
	ws.maxMessageSize = size
}

// SetReadDeadline sets the read deadline
func (ws *WSConn) SetReadDeadline(t time.Time) error {
	ws.readDeadline = t
	return ws.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline
func (ws *WSConn) SetWriteDeadline(t time.Time) error {
	ws.writeDeadline = t
	return nil // Applied on next write
}

// LocalAddr returns the local network address
func (ws *WSConn) LocalAddr() net.Addr {
	return ws.conn.LocalAddr()
}

// RemoteAddr returns the remote network address
func (ws *WSConn) RemoteAddr() net.Addr {
	return ws.conn.RemoteAddr()
}
