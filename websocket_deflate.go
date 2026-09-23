package rweb

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"strings"
)

// permessage-deflate (RFC 7692): compressed WebSocket messages.
//
// Off unless a route asks for it (WSOptions.Compression), so every existing
// WebSocket route behaves exactly as before. When a route does ask, and the
// client offers the extension, each text/binary message is deflated and sent
// with RSV1 set; control frames never are.
//
// What is negotiated, and why:
//
//	server → client   CONTEXT TAKEOVER (unless the client asks otherwise).
//	                  One flate.Writer per connection keeps its 32 KB window
//	                  from message to message, so a message that repeats the
//	                  shape of the last one (JSON with the same keys, a UI
//	                  update like the one before it) compresses to little
//	                  more than what is new in it. The writer is sync-flushed
//	                  after each message and the 00 00 ff ff flush marker is
//	                  stripped, as §7.2.1 prescribes.
//	client → server   client_no_context_takeover, always (the server may
//	                  impose it, §7.1.1.2). Each client message is then a
//	                  self-contained deflate stream, so the server keeps no
//	                  window per connection for the direction that, in the
//	                  typical app, carries small, infrequent messages.
//
// An offer is declined — and the connection simply runs uncompressed — when
// it asks for something Go's compress/flate cannot do: a server window
// smaller than 32 KB (server_max_window_bits < 15), or a parameter this
// implementation does not know.

// WSOptions configures a WebSocket route (Server.WebSocketWithOptions).
type WSOptions struct {
	// Compression negotiates permessage-deflate with a client that offers it.
	Compression bool
	// CompressionLevel is the compress/flate level for outgoing messages; 0
	// selects defaultCompressionLevel (2).
	CompressionLevel int
}

// deflateTail completes a message for the inflater: the 00 00 ff ff the
// sender stripped (§7.2.2), then an empty final stored block so the reader
// sees a clean end of stream instead of an unexpected EOF.
var deflateTail = []byte{0x00, 0x00, 0xff, 0xff, 0x01, 0x00, 0x00, 0xff, 0xff}

// flushMarker is the empty stored block a sync flush ends with.
var flushMarker = []byte{0x00, 0x00, 0xff, 0xff}

// defaultCompressionLevel is 2, not flate.BestSpeed (1), and not higher.
//
// Level 1 is a different algorithm in compress/flate: it Huffman-codes any
// flush under 128 bytes without looking for matches and resets its history
// for it, so a stream of small messages — most of what a WebSocket carries —
// gets nothing from context takeover. Level 2 is the first level that keeps
// the window across flushes, at the same speed. Measured on terminal JSON
// (small diffs / 120 KB full frames): level 1 3.4× / 7.2×, level 2 6.8× /
// 8.3× at the same µs per message, level 6 7.7× / 11.2× at five times the
// cost on the large ones.
const defaultCompressionLevel = 2

// maxWindow is the LZ77 window both directions use: 2^15, the most deflate
// allows and all compress/flate knows.
const maxWindow = 1 << 15

// wsDeflate is one connection's negotiated compression state.
type wsDeflate struct {
	level int

	// Outgoing. w persists across messages unless writeNoContext, which is
	// what context takeover means for the sender.
	w              *flate.Writer
	wbuf           bytes.Buffer
	writeNoContext bool

	// Incoming. readNoContext means each message is its own stream; without
	// it, dict carries the last 32 KB of decompressed output into the next
	// message, which is exactly the window the peer's compressor kept.
	r             io.ReadCloser
	dict          []byte
	readNoContext bool
}

// deflateOffer is the part of a client's offer the server has to honour.
type deflateOffer struct {
	serverNoContext bool
}

// parseDeflateOffer picks the first acceptable permessage-deflate offer from
// a Sec-WebSocket-Extensions header value. ok is false when there is none.
//
// The header is a comma-separated list of offers in preference order, each an
// extension name followed by ;-separated parameters (RFC 6455 §9.1).
func parseDeflateOffer(header string) (offer deflateOffer, ok bool) {
	for _, ext := range strings.Split(header, ",") {
		parts := strings.Split(ext, ";")
		if strings.TrimSpace(parts[0]) != "permessage-deflate" {
			continue
		}
		o, acceptable := deflateOffer{}, true
		for _, p := range parts[1:] {
			name, value, _ := strings.Cut(strings.TrimSpace(p), "=")
			name = strings.TrimSpace(name)
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch name {
			case "server_no_context_takeover":
				o.serverNoContext = true
			case "client_no_context_takeover":
				// Imposed on every client anyway (see the file comment).
			case "client_max_window_bits":
				// A hint that the client could use a smaller window; the server
				// accepts any client window, so there is nothing to answer.
			case "server_max_window_bits":
				// Only the full window is possible with compress/flate.
				if value != "15" {
					acceptable = false
				}
			case "":
				// An empty parameter (a stray ";") carries nothing.
			default:
				acceptable = false
			}
		}
		if acceptable {
			return o, true
		}
	}
	return deflateOffer{}, false
}

// response is the Sec-WebSocket-Extensions value that accepts the offer.
func (o deflateOffer) response() string {
	r := "permessage-deflate; client_no_context_takeover"
	if o.serverNoContext {
		r += "; server_no_context_takeover"
	}
	return r
}

func newServerDeflate(o deflateOffer, level int) *wsDeflate {
	if level == 0 {
		level = defaultCompressionLevel
	}
	return &wsDeflate{level: level, writeNoContext: o.serverNoContext, readNoContext: true}
}

// compress deflates one message. The returned slice is valid until the next
// call.
func (d *wsDeflate) compress(p []byte) ([]byte, error) {
	d.wbuf.Reset()
	if d.w == nil {
		w, err := flate.NewWriter(&d.wbuf, d.level)
		if err != nil {
			return nil, err
		}
		d.w = w
	} else if d.writeNoContext {
		d.w.Reset(&d.wbuf)
	}
	if _, err := d.w.Write(p); err != nil {
		return nil, err
	}
	if err := d.w.Flush(); err != nil {
		return nil, err
	}
	out := d.wbuf.Bytes()
	if !bytes.HasSuffix(out, flushMarker) {
		return nil, errors.New("websocket: deflate flush did not end with a sync marker")
	}
	return out[:len(out)-len(flushMarker)], nil
}

// decompress inflates one message, refusing to produce more than limit
// bytes: a few kilobytes of deflate can expand to gigabytes, and the frame
// size check has only seen the compressed size.
func (d *wsDeflate) decompress(p []byte, limit int64) ([]byte, error) {
	src := io.MultiReader(bytes.NewReader(p), bytes.NewReader(deflateTail))
	var dict []byte
	if !d.readNoContext {
		dict = d.dict
	}
	if d.r == nil {
		d.r = flate.NewReaderDict(src, dict)
	} else if err := d.r.(flate.Resetter).Reset(src, dict); err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(d.r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, ErrWebSocketPayloadTooLarge
	}
	if !d.readNoContext {
		// The peer's window is its last 32 KB of input across messages.
		d.dict = append(d.dict, out...)
		if len(d.dict) > maxWindow {
			d.dict = append(d.dict[:0], d.dict[len(d.dict)-maxWindow:]...)
		}
	}
	return out, nil
}
