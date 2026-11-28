package rweb

import (
	"bytes"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

// Close codes for the protocol errors of ReadMessage that are not about the
// message sequence or UTF-8 (those are in websocket_sequence_test.go): a
// frame header that breaks the rules, a reserved opcode, a message over the
// limit, and compressed data that does not inflate (N-083). Each must fail
// the connection with a close frame carrying the RFC 6455 §7.4.1 code, not
// just drop TCP.

// rawFrameRSV is a frame as a client sends it (masked unless unmasked is
// set), with extra RSV bits (0x40 RSV1, 0x20 RSV2, 0x10 RSV3) OR-ed into the
// first byte, which writeRawFrame cannot set.
func rawFrameRSV(t *testing.T, opcode int, fin bool, rsv byte, unmasked bool, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeRawFrame(&b, opcode, fin, !unmasked, data); err != nil {
		t.Fatal(err)
	}
	frame := b.Bytes()
	frame[0] |= rsv
	return frame
}

func TestReadMessageFailCodes(t *testing.T) {
	// A deflate stream that cannot be parsed: the first byte's block type
	// bits are 11, which RFC 1951 §3.2.3 reserves (flate.CorruptInputError).
	corrupt := []byte{0xff, 0xff, 0xff}

	for _, tc := range []struct {
		name     string
		deflate  bool  // negotiate permessage-deflate on the server side
		limit    int64 // SetMaxMessageSize, 0 to leave the default
		frames   func(t *testing.T) [][]byte
		wantErr  error
		wantCode int
	}{
		{
			name: "RSV2 set",
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsText, true, 0x20, false, []byte("x"))}
			},
			wantErr: ErrWebSocketBadRSV, wantCode: wsCloseProtocolError,
		},
		{
			name: "RSV3 set",
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsBinary, true, 0x10, false, []byte("x"))}
			},
			wantErr: ErrWebSocketBadRSV, wantCode: wsCloseProtocolError,
		},
		{
			name: "RSV1 without the extension",
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsText, true, 0x40, false, []byte("x"))}
			},
			wantErr: ErrWebSocketBadRSV, wantCode: wsCloseProtocolError,
		},
		{
			name:    "RSV1 on a ping",
			deflate: true,
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsPing, true, 0x40, false, nil)}
			},
			wantErr: ErrWebSocketBadRSV, wantCode: wsCloseProtocolError,
		},
		{
			name:    "RSV1 on a continuation",
			deflate: true,
			frames: func(t *testing.T) [][]byte {
				return [][]byte{
					rawFrameRSV(t, wsText, false, 0, false, []byte("a")),
					rawFrameRSV(t, wsContinuation, true, 0x40, false, []byte("b")),
				}
			},
			wantErr: ErrWebSocketBadRSV, wantCode: wsCloseProtocolError,
		},
		{
			name: "unmasked client frame",
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsText, true, 0, true, []byte("bare"))}
			},
			wantErr: ErrWebSocketBadMask, wantCode: wsCloseProtocolError,
		},
		{
			name: "reserved data opcode",
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, 0x3, true, 0, false, []byte("x"))}
			},
			wantErr: ErrWebSocketInvalidOpcode, wantCode: wsCloseProtocolError,
		},
		{
			name: "reserved control opcode",
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, 0xB, true, 0, false, nil)}
			},
			wantErr: ErrWebSocketInvalidOpcode, wantCode: wsCloseProtocolError,
		},
		{
			name:  "frame over the limit",
			limit: 4,
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsText, true, 0, false, []byte("hello"))}
			},
			wantErr: ErrWebSocketPayloadTooLarge, wantCode: wsCloseMessageTooBig,
		},
		{
			name:  "fragmented message over the limit",
			limit: 8,
			frames: func(t *testing.T) [][]byte {
				return [][]byte{
					rawFrameRSV(t, wsText, false, 0, false, []byte("hello")),
					rawFrameRSV(t, wsContinuation, true, 0, false, []byte("world")),
				}
			},
			wantErr: ErrWebSocketPayloadTooLarge, wantCode: wsCloseMessageTooBig,
		},
		{
			name: "64-bit length with its top bit set",
			frames: func(*testing.T) [][]byte {
				return [][]byte{{0x82, 0x80 | 127, 0x80, 0, 0, 0, 0, 0, 0, 5, 1, 2, 3, 4}}
			},
			wantErr: ErrWebSocketPayloadTooLarge, wantCode: wsCloseMessageTooBig,
		},
		{
			name:    "deflate bomb",
			deflate: true,
			limit:   1 << 16,
			frames: func(t *testing.T) [][]byte {
				bomb := deflateRaw(t, make([]byte, 1<<20))
				return [][]byte{rawFrameRSV(t, wsBinary, true, 0x40, false, bomb)}
			},
			wantErr: ErrWebSocketPayloadTooLarge, wantCode: wsCloseMessageTooBig,
		},
		{
			name:    "fragmented deflate bomb",
			deflate: true,
			limit:   1 << 16,
			frames: func(t *testing.T) [][]byte {
				bomb := deflateRaw(t, make([]byte, 1<<20))
				half := len(bomb) / 2
				return [][]byte{
					rawFrameRSV(t, wsBinary, false, 0x40, false, bomb[:half]),
					rawFrameRSV(t, wsContinuation, true, 0, false, bomb[half:]),
				}
			},
			wantErr: ErrWebSocketPayloadTooLarge, wantCode: wsCloseMessageTooBig,
		},
		{
			name:    "data that does not inflate",
			deflate: true,
			frames: func(t *testing.T) [][]byte {
				return [][]byte{rawFrameRSV(t, wsText, true, 0x40, false, corrupt)}
			},
			wantErr: nil, wantCode: wsCloseInvalidFramePayloadData,
		},
		{
			name:    "fragmented data that does not inflate",
			deflate: true,
			frames: func(t *testing.T) [][]byte {
				return [][]byte{
					rawFrameRSV(t, wsText, false, 0x40, false, corrupt[:1]),
					rawFrameRSV(t, wsContinuation, true, 0, false, corrupt[1:]),
				}
			},
			wantErr: nil, wantCode: wsCloseInvalidFramePayloadData,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, client := newTestPair()
			defer client.conn.Close()
			if tc.deflate {
				server.deflate = newServerDeflate(deflateOffer{}, 0)
			}
			if tc.limit > 0 {
				server.SetMaxMessageSize(tc.limit)
			}
			// The frames are written before the reader starts: net.Pipe is
			// synchronous, and the writes complete as ReadMessage consumes
			// them; the reader then takes the close frame off the pipe.
			frames := tc.frames(t)
			go func() {
				for _, f := range frames {
					if _, err := client.conn.Write(f); err != nil {
						return
					}
				}
			}()
			code := closeCodeFrom(client)
			_, err := server.ReadMessage()
			if tc.wantErr == nil {
				// A deflate error is the flate package's own; only its
				// presence is checked, and that it is no I/O error.
				if err == nil {
					t.Fatal("ReadMessage succeeded on data that does not inflate")
				}
				tc.wantErr = err
			}
			expectFailed(t, server, err, tc.wantErr, code, tc.wantCode)
		})
	}
}

// An I/O error is not the peer breaking the protocol: a read that hits its
// deadline returns the timeout and leaves the connection open, with no close
// frame sent (frameFailCode reports false), so the app may extend the
// deadline and read again.
func TestReadMessageIOErrorSendsNoCloseFrame(t *testing.T) {
	server, client := newTestPair()
	defer client.conn.Close()
	defer server.conn.Close()

	_ = server.conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, err := server.ReadMessage()
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadMessage = %v, want a deadline timeout", err)
	}
	select {
	case <-server.Done():
		t.Fatal("a read timeout failed the connection")
	default:
	}

	// Still usable: a message sent after the timeout arrives.
	_ = server.conn.SetReadDeadline(time.Time{})
	go func() { _ = client.WriteMessage(TextMessage, []byte("after")) }()
	msg, err := server.ReadMessage()
	if err != nil || string(msg.Data) != "after" {
		t.Fatalf("ReadMessage after the timeout = %v, %v", msg, err)
	}
}
