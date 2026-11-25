// Command websocket_client dials a WebSocket server with rweb.DialWebSocket
// and relays lines between the terminal and the server.
//
// Start the server example first, then this one in another terminal:
//
//	cd examples/websocket && go run .
//	cd examples/websocket_client && go run . -url ws://localhost:8080/ws/echo
//
// Each line typed is sent as a text message; every message the server sends
// is printed. Ctrl-D (end of input) or Ctrl-C closes the connection with a
// close frame, as a well-behaved client should.
//
// The shape is the one any client takes: one goroutine owns ReadMessage
// (pings from the server are answered inside it, and a close frame ends it),
// and writes come from anywhere else. It is the same *rweb.WSConn a server
// handler gets, so the same methods apply.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rohanthewiz/rweb"
)

func main() {
	url := flag.String("url", "ws://localhost:8080/ws/echo", "WebSocket URL (ws:// or wss://)")
	token := flag.String("token", "", "bearer token to send as Authorization (optional)")
	flag.Parse()

	// Ctrl-C or SIGTERM cancels ctx; the main loop then closes the
	// connection properly instead of the process just dying.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := rweb.WSDialOptions{
		// Offered; used only if the server accepts (rweb routes with
		// WSOptions{Compression: true} do).
		Compression: true,
	}
	if *token != "" {
		opts.Header = http.Header{"Authorization": {"Bearer " + *token}}
	}

	// The context bounds the dial and handshake only; the connection
	// itself lives until Close.
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ws, resp, err := rweb.DialWebSocket(dialCtx, *url, opts)
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			// The server answered, but refused: its page says why.
			body, _ := io.ReadAll(resp.Body)
			log.Fatalf("dial: %v\n%s", err, body)
		}
		log.Fatalf("dial: %v", err)
	}
	compression := "off"
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		compression = ext
	}
	fmt.Printf("connected to %s (compression: %s)\n", *url, compression)

	// The reader. It ends when the server's close frame arrives (also the
	// answer to our own Close) or the connection fails.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				if !errors.Is(err, rweb.ErrWebSocketAlreadyClosed) {
					fmt.Println("read:", err)
				}
				return
			}
			switch msg.Type {
			case rweb.TextMessage:
				fmt.Println("<", string(msg.Data))
			case rweb.BinaryMessage:
				fmt.Printf("< %d bytes of binary\n", len(msg.Data))
			case rweb.CloseMessage:
				fmt.Println("connection closed")
				return
			}
		}
	}()

	// Lines from the terminal, on their own goroutine since Scan blocks.
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			lines <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			fmt.Println("stdin:", err)
		}
		close(lines)
	}()

loop:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				break loop // end of input
			}
			if err := ws.WriteMessage(rweb.TextMessage, []byte(line)); err != nil {
				fmt.Println("write:", err)
				break loop
			}
		case <-ctx.Done():
			break loop
		case <-readDone:
			// The server ended the session; the reader has already
			// answered its close frame.
			_ = ws.Close(1000, "")
			return
		}
	}

	// Close sends a close frame (1000: normal closure) and waits (at most
	// 2 s) for the server's answer, then closes the socket. Close means
	// "done now": replies still in flight are not printed (once its close
	// frame is out, this side drops data until the server's close frame).
	// So piping a file in (printf 'a\nb\n' | go run .) closes before the
	// echoes are shown; typed lines have long been answered by then.
	if err := ws.Close(1000, "bye"); err != nil {
		fmt.Println("close:", err)
	}
	<-readDone
}
