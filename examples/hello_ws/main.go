package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
)

func main() {
	// Create a handler
	handler := &rweb.BaseWebSocketHandler{
		OnConnectFunc: func(conn *rweb.WebSocketConn) {
			fmt.Println("Connected to WebSocket server")
		},
		OnMessageFunc: func(messageType byte, data []byte) {
			if messageType == rweb.WSOpText {
				fmt.Printf("Received text message: %s\n", string(data))
			} else {
				fmt.Printf("Received binary message of %d bytes\n", len(data))
			}
		},
		OnCloseFunc: func(code int, text string) {
			fmt.Printf("Connection closed: %d %s\n", code, text)
		},
		OnErrorFunc: func(err error) {
			fmt.Printf("Error: %v\n", err)
		},
	}

	// Create a client
	client := rweb.NewWebSocketClient("ws://localhost:8000/ws/agent", handler)

	// Connect
	if err := client.Connect(); err != nil {
		panic(err)
	}

	// Send a message
	msg := PromptMsg{Prompt: "What is a snowman?"}
	bytMsg, err := json.Marshal(msg)
	if err != nil {
		logger.LogErr(err, "Error on marshal")
		os.Exit(1)
	}
	if err := client.SendText(string(bytMsg)); err != nil {
		logger.LogErr(err, "Error on client SendText")
		os.Exit(1)
	}

	// Keep the connection open
	select {}
}

type PromptMsg struct {
	Prompt     string   `json:"prompt"`
	Parameters struct{} `json:"parameters"`
}
