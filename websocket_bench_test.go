package rweb

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
)

// BenchmarkWSEcho measures one WebSocket message round trip on a loopback
// connection that went through the server's own upgrade path
// (s.WebSocket, default options, so server pings and the liveness read
// deadline are on): the client sends one masked frame, the handler's
// ReadMessage parses and unmasks it, WriteMessage echoes it, and the client
// reads the echo.
//
//	client ── masked frame ──► readFrame ─► ReadMessage ─► WriteMessage ──┐
//	   ▲                                                                   │
//	   └──────────────────────── unmasked echo frame ◄─────────────────────┘
//
// Three sizes cover the three length encodings and the two costs that
// matter: per-frame overhead (header reads, small allocations, deadline
// updates) for the small text messages, per-byte cost (unmasking, copying)
// for the 64 KB binary one.
//
// The burst case sends 64 small messages in one write before reading the
// 64 echoes, as a chatty client does when the server falls behind: frames
// then queue in the socket buffer, and what a frame costs to read is mostly
// how many read syscalls it takes. One round trip per message (the other
// cases) hides that behind the loopback latency of the round trip.
//
// One op is one message in every case. As in BenchmarkKeepAliveHello, the
// client writes frames built once and reads the echoes into a buffer
// allocated once, so the allocations reported are the server's
// (b.ReportAllocs counts every goroutine's).
func BenchmarkWSEcho(b *testing.B) {
	for _, bc := range []struct {
		name   string
		opcode int
		size   int
		burst  int // messages per client write
	}{
		{"text16", wsText, 16, 1},
		{"text1K", wsText, 1 << 10, 1},
		{"binary64K", wsBinary, 64 << 10, 1},
		{"text16x64", wsText, 16, 64},
	} {
		b.Run(bc.name, func(b *testing.B) {
			conn := benchWSConn(b)

			payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz012345"), bc.size/32+1)[:bc.size]
			var frame bytes.Buffer
			if err := writeRawFrame(&frame, bc.opcode, true, true, payload); err != nil {
				b.Fatal(err)
			}
			req := bytes.Repeat(frame.Bytes(), bc.burst)

			// The echo is the same frame unmasked: 4 bytes of mask key fewer.
			echoLen := frame.Len() - 4
			echo := make([]byte, echoLen*bc.burst)

			roundTrip := func() {
				if _, err := conn.Write(req); err != nil {
					b.Fatal(err)
				}
				if _, err := io.ReadFull(conn, echo); err != nil {
					b.Fatal(err)
				}
			}
			// One untimed round, so the handler is in its read loop and any
			// lazily built state exists before the timer starts.
			roundTrip()
			if !bytes.Equal(echo[echoLen-len(payload):echoLen], payload) {
				b.Fatal("echo does not carry the payload sent")
			}

			b.SetBytes(int64(bc.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i += bc.burst {
				roundTrip()
			}
		})
	}
}

// benchWSConn serves an echo WebSocket route on a loopback listener the
// benchmark owns and returns a client conn already upgraded on it, with
// nothing buffered past the 101 (the server sends nothing until the first
// frame arrives). The listener and conn close when the benchmark ends.
func benchWSConn(b *testing.B) net.Conn {
	b.Helper()
	s := NewServer()
	s.WebSocket("/ws", func(ws *WSConn) error {
		for {
			msg, err := ws.ReadMessage()
			if err != nil {
				return nil
			}
			if err := ws.WriteMessage(msg.Type, msg.Data); err != nil {
				return nil
			}
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConnection(c)
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	if _, err := io.WriteString(conn, wsUpgradeReq); err != nil {
		b.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		b.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		b.Fatalf("upgrade: status %d, want 101", resp.StatusCode)
	}
	if br.Buffered() != 0 {
		b.Fatalf("%d bytes buffered past the 101", br.Buffered())
	}
	return conn
}
