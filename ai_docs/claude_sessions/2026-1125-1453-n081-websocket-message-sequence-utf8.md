# N-081: WebSocket message sequence (1002) and UTF-8 text (1007) in `ReadMessage`

Session id: `d935d6ec-d584-49bb-aab1-9b49d0e73ec2`
Date: 2026-11-25 · Branch: `master` · Start `f7f05ef` → end `fcb294e` (+ this doc)
Run: `/clear`, then the user pasted next-list item N-081 to work on; then `/sw`.

## Done

### The item

N-081 (raised in the N-080 session while reading `ReadMessage` for the
client): (1) a text/binary frame arriving while a fragmented message is in
progress was not refused (RFC 6455 §5.4); with FIN=0 it silently replaced
the partial message, with FIN=1 it was returned and the stale
`fragmentedMessage` was later appended to by the next continuation; (2) text
messages were not checked for UTF-8 (§8.1, 1007). Premise confirmed by
reading the code.

A third defect in the same state machine turned up: the state was
`fragmentedMessage != nil`, and `append(nil[:0], empty...)` stays nil, so a
fragmented message whose **first fragment was empty** had its continuation
refused as "unexpected continuation frame" (the shape of Autobahn 6.1.2-style
empty-fragment cases).

### Fix (`websocket.go`, commit `fcb294e`)

- **State:** new `WSConn.fragmenting bool` (set on a text/binary FIN=0 frame,
  cleared on the final continuation and on the too-large path). State
  diagram added above the opcode switch in `ReadMessage`.
- **Sequence:** text/binary while `fragmenting`, or a continuation while not,
  calls `failConnection(wsCloseProtocolError)` and returns new exported
  `ErrWebSocketBadFragment`, wrapped with which case. It replaces the bare
  `errors.New("unexpected continuation frame")`, after which the conn used to
  be left open (no close frame).
- **UTF-8:** both data-message return paths now go through new
  `WSConn.dataMessage(typ, data)`: a `TextMessage` that fails `utf8.Valid`
  calls `failConnection(wsCloseInvalidFramePayloadData)` (1007) and returns
  new exported `ErrWebSocketInvalidUTF8`. Checked once on the whole message,
  after reassembly and after inflate (permessage-deflate), never per fragment:
  a sender may split a character across fragments. Binary is not checked.
  Trade-off noted in the comment: no fail-fast, so a peer can make this side
  buffer invalid text up to the message limit, which any valid message of
  that size can do too.
- Both roles (server, and `DialWebSocket`'s client role) share the code.
- Both new errors are protocol errors, so `isWebSocketSessionEnd` does not
  swallow them: a handler returning one gets the error log line, as for
  `ErrWebSocketBadControlFrame`.

### Cost (`BenchmarkWSEcho`)

The pre-change test binary was built before editing
(`go test -c`), then old and new binaries were run alternately, 8 rounds
each, `-benchmem`. `benchstat` could not be fetched (`go run …@latest`
was refused by the permission classifier), so medians were computed with a
small Python script over the two result files:

| case | before (median, range) | after (median, range) | Δ |
|---|---|---|---|
| text16 | 14068 ns [13837–14769] | 14103 ns [13601–14352] | +0.2 % |
| text1K | 14662 ns [14248–15050] | 14783 ns [14464–18435] | +0.8 % |
| binary64K | 35582 ns [34855–36514] | 35088 ns [34215–36535] | −1.4 % |
| text16x64 | 1618 ns [1576–1727] | 1628 ns [1583–1688] | +0.7 % |

All inside the runs' own spread; allocations unchanged (2/op, same B/op).
Numbers recorded in `dataMessage`'s comment.

### Tests

- New `websocket_sequence_test.go`, with helpers `seqFrame`, `sendFrames`
  (raw frames, masked or not, RSV1 optional), `closeCodeFrom` (reads the
  peer side until a close frame; needed on `net.Pipe`, where an unread close
  frame blocks `failConnection`'s write for up to 2 s) and `expectFailed`
  (error, close code, `Done()` closed):
  - `TestWebSocketBadFragmentSequence`: 6 cases: text FIN=1 and binary FIN=0
    inside a fragmented message, a new message after a ping mid-fragment,
    orphan continuation, continuation after a completed fragmented message
    (state reset) and after an unfragmented one.
  - `TestWebSocketFragmentsInSequence`: all-empty fragments, empty first
    fragment, two fragmented messages back to back with pings between.
  - `TestWebSocketTextMustBeUTF8`: 9 cases: bad byte, surrogate, overlong,
    fragmented ending mid-character, bad middle fragment, compressed text
    that inflates to bad UTF-8, a character split across fragments (valid),
    binary unchecked, multi-byte text.
  - `TestDialWebSocketRefusesBadSequenceAndUTF8`: client role over
    `rawDialServer` (interleaved, orphan continuation, invalid UTF-8); the raw
    server reads the client's masked close frame and its code.
- `TestWebSocketUnexpectedContinuation` (websocket_test.go) matched the old
  error text; now checks `ErrWebSocketBadFragment` and the 1002 close.
- `TestDeflateFragmentedMessage` used `got.Data` in its `Fatalf` with `got`
  nil, which panicked the test binary and hid every later test (seen during
  the deliberate breaks); err is now checked on its own first.
- **Deliberate breaks**, each applied to a copy of the fixed file and run:
  no interleave check → 3 sequence cases + client "interleaved" fail; state
  back to `fragmentedMessage == nil` → both empty-fragment cases fail; state
  not reset at the final fragment → "continuation after a completed
  fragmented message" and "back to back" fail; no UTF-8 check → 6 UTF-8
  cases + client case fail; per-fragment UTF-8 check → "character split
  across fragments" and `TestDeflateFragmentedMessage` fail.

### Found, not fixed (raised)

- **N-082 (medium):** a close frame with no body is answered with status
  **1005** on the wire (`ReadMessage` sets `code = wsCloseNoStatusReceived`,
  `handleClose` echoes it); RFC 6455 §7.4.1 forbids sending 1005. Confirmed
  with a throwaway probe test (deleted). A browser's `ws.close()` with no
  arguments sends exactly that empty close. Also: incoming close frames are
  not validated (1-byte body, unsendable codes, non-UTF-8 reason).
- **N-083 (low):** the other protocol errors (`ErrWebSocketBadRSV`,
  `BadMask`, `InvalidOpcode`, `PayloadTooLarge`, deflate errors) return from
  `ReadMessage` without `failConnection`, so the peer gets a TCP close with
  no code. Fixing it needs the `net.Pipe` tests that trigger them to read the
  peer side (`closeCodeFrom`).

### Docs

- README (WebSocket section): out-of-sequence frames → 1002 /
  `ErrWebSocketBadFragment`; non-UTF-8 text → 1007 /
  `ErrWebSocketInvalidUTF8`; whole-message check; client too.
- `ai_docs/SKILL.md`: new bullet after the control-frame one, with an
  `errors.Is` snippet (marked v0.2.1+).
- `CLAUDE.md` WebSockets bullet: the sequence rule, the `fragmenting` flag
  and why not `fragmentedMessage != nil`, `dataMessage` as the single exit
  for data messages, protocol failures via `failConnection`.

## Checks (final tree)

- `go test -race -count=1 ./...`: ok (root package 25 s).
- `go vet ./...` and `GOOS=windows go vet ./...`: clean; `gofmt -l .`: clean.
- `examples/websocket`, `examples/websocket_client` (own modules): vet and
  build ok.

## Next

Closed: N-081. Declined: None. Raised: N-082, N-083. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
