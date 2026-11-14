# N-076, N-077: ReadMessage after the close handshake, bare-LF empty lines

Session id: `3a7f4f72-7532-415b-a72a-5eb9a2277e67`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `124255e` → end `e89b0c4` (+ this doc)
Run: `/work-next-list`, final batch. These two items were raised earlier in
this same run (by N-062 and N-072). Earlier batches:
`2026-1114-1718-n062-n059-n060-n072-ws-browser-linger-strict-head`,
`2026-1114-1729-n064-n065-n066-proxy-read-timeout-location-websocket`,
`2026-1114-1737-n068-n070-n074-hub-close-response-head-date`.

## Done

### N-076: `ReadMessage` fails at once after the peer's close frame (`db59591`)

- `ReadMessage` returns `ErrWebSocketAlreadyClosed` as soon as it holds
  `readSem` if `closeRcvd` is set. That is safe because whichever reader
  saw the close frame stores `closeRcvd` before it releases `readSem`.
  RFC 6455 §5.5.1 allows no data frame after a close frame, so nothing is
  left to wait for. The error already counts as a session end
  (`isWebSocketSessionEnd`), so a handler that returns it logs nothing.
- Why a raw client in the test: rweb's own client `WSConn` closes TCP
  right after replying to a close frame. That ended the server's next read
  with EOF and hid the 2 s wait. Chrome answers the close frame and then
  waits for the server to close TCP (RFC 6455 §7.1.1), which is what the
  N-062 run showed. The test client writes a masked close reply by hand
  and keeps TCP open. Without the change the read fails with an i/o
  timeout and Shutdown takes 2.00 s.
- Also: a read after a peer-initiated close now returns the named error
  rather than "use of closed network connection".
- Tests: `websocket_read_after_close_test.go`. Noted in SKILL.md
  (WSConn API).

### N-077: bare-LF empty line accepted (`b9ce1ac`)

- **Decision:** accept. The item offered two ways: accept a bare-LF empty
  line, or refuse bare LF throughout. Accepting is consistent with the
  bare-LF request, header and chunk-size lines rweb already takes, and with
  net/http. Nothing becomes ambiguous: a parser that refuses bare LF
  refuses the whole head.
- `isBlankLine` (`http.go`, `"\r\n"` or `"\n"`) now ends both the head
  (`serveConn`) and a chunked body's trailers (`readChunkedBody`).
- **Bug found on the way:** the trailer loop also ended only on CRLF. A
  bare-LF end of trailers left them open, so the next pipelined request
  was read as trailer lines and never answered, while a proxy that takes
  LF would see two requests. Now fixed. CLAUDE.md asks for the two checks
  to stay in step.
- Chunk data must still end in exactly CRLF; that is unchanged.
- Tests `bare_lf_test.go`:
  - two pipelined LF-only requests over tcp and tls (400 before);
  - a chunked POST with bare-LF trailers followed by a pipelined GET (the
    GET timed out before).
- Documented in SKILL.md (Strict Request Framing) and CLAUDE.md.

## Checks run for each item

`go test ./...`, `go test -race .`, `go vet ./...` (darwin, linux,
windows), `gofmt -l .`.

## Whole run (all four batches)

Closed: N-062, N-059, N-060, N-072, N-064, N-066, N-068, N-070, N-074,
N-076, N-077. Raised and closed in the run: N-076, N-077.

Still open, both waiting on the user's decision:

- **N-065**: should rweb rewrite a relative `Location` (and cookie `Path`)
  under the proxy prefix? (a) Leave it as nginx does and move the item to
  Non-goals, or (b) add an opt-in per-route option. Absolute URLs on the
  target are already mapped (`0cd99bd`).
- **N-075**: should `Request().Scheme()` and the WebSocket origin check use
  a trusted peer's `X-Forwarded-Proto` / `-Host`, as an opt-in, or on with
  the default trusted list?

## Next

Closed: N-076, N-077. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
