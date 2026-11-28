# N-085: WriteMessage / WriteMessages send text, binary, ping and pong only

Session id: `a2fd1f3b-2a70-4946-9159-8d84d426deb1`
Date: 2026-11-27 · Branch: `master` · Start `edb7fa0` → end `a477086` (+ this doc)
Run: after `/clear`, N-085's text pasted as the task; mid-turn: "When completely
done, do a /sess-wrap".

## Done

### The item

N-085 (raised in the N-084 session): `WriteMessage`/`WriteMessages` passed any
`MessageType` to `appendFrame` as the opcode.

- `WriteMessage(CloseMessage, data)` wrote a close frame around `Close`:
  `closed` not set (data frames could follow, against RFC 6455 §5.5.1), code
  and reason unchecked (`closeFramePayload` skipped), no wait for the answer.
- A value that is no message type (0, 3, 11, …) put a continuation or
  reserved-opcode frame on the wire, which a peer fails with 1002.

Premise confirmed by reading the code. The item offered two fixes; I took the
first: refuse, rather than route `CloseMessage` to `Close`. `Close` takes a
code and reason, not a raw payload, and blocks up to 2 s for the answer;
neither fits a call that writes one frame.

### Code (`websocket.go`)

- **New `checkWriteType`**, called by both `WriteMessage` and `WriteMessages`:
  - `TextMessage`, `BinaryMessage`, `PingMessage`, `PongMessage` pass.
  - `CloseMessage` → `ErrWebSocketBadMessageType` wrapped with
    "CloseMessage; use Close(code, reason)".
  - Anything else → the same sentinel wrapped with the number.
  - The doc comment has a table of what each refused value used to put on the
    wire, and why refusing beats routing to `Close`.
- **New sentinel `ErrWebSocketBadMessageType`** rather than reusing
  `ErrWebSocketInvalidOpcode`: that one is what `ReadMessage` returns for a
  peer's reserved opcode after failing the conn with 1002; this one leaves the
  conn open and not marked closed. Nothing is written, and a `WriteMessages`
  batch is refused whole.
- **Decision made on the way: the closed check stays first.** An echo loop that
  hands back `ReadMessage`'s result (`WriteMessage(msg.Type, msg.Data)`, as in
  README's example and seven test handlers) gets the peer's `CloseMessage`;
  `ReadMessage` has answered it and closed the conn by then (`handleClose`), so
  the write still returns `ErrWebSocketAlreadyClosed`, exactly as before. With
  the type check first, those loops would have seen a new error instead.
- Doc comments updated: `WriteMessage`, `WriteMessages`, the
  `ErrWebSocketBadControlFrame` sentinel and `appendFrame` (which now say
  ping/pong rather than "a control type").

### Tests

- New `websocket_write_type_test.go`:
  - `TestWriteMessageRefusesType`: 10 bad types (`CloseMessage`, 0, 3, 7, 11,
    15, 16, 0x81, −1) × both calls × both roles. The error comes back at once
    with no reader on the pipe (so nothing was written), the `CloseMessage`
    error names `Close(code, reason)`, `closed` is not set, `Done()` stays
    open, and the peer's next frame is the text message sent after.
  - `TestWriteMessageSendsAllowedTypes`: the four allowed types arrive with
    their own opcode, single and batched, both roles.
  - `TestWriteMessageCloseAfterPeerCloseIsAlreadyClosed`: echoing a peer's
    `CloseMessage` through either call gets `ErrWebSocketAlreadyClosed`.
- `TestControlFrameOver125Refused` (`websocket_control_out_test.go`) lost its
  `WriteMessage(CloseMessage, big)` case, which is now a type refusal; a
  comment points to the new test.
- Six deliberate breaks, each caught:
  - no check in `WriteMessage` / in `WriteMessages` (RefusesType)
  - type check before the closed check (CloseAfterPeerClose)
  - `CloseMessage` allowed (RefusesType)
  - pong refused (SendsAllowedTypes, Over125Refused)
  - reserved opcodes 1–15 allowed (RefusesType)
- `go test -race ./...` green; `go vet ./...` and `GOOS=windows go vet ./...`
  clean; gofmt clean.
- `BenchmarkWSEcho`, old vs new binaries, 5 interleaved runs, medians:
  binary64K −0.3 %, text16 −5.0 %, text16x64 −0.3 %, text1K +1.3 %: noise (a
  switch on a constant). As last session, `git stash` left the new untracked
  test file in place and the old build failed; moving it aside fixed that.

### Docs

- README: after the N-084 sentence, what `WriteMessage` sends, the new error,
  and why the echo loop above still ends cleanly.
- `ai_docs/SKILL.md`: a new bullet after the outgoing-control-frame rules;
  `CloseMessage` marked read-only in Message Types; the API reference notes
  the allowed types on `WriteMessage`.
- CLAUDE.md: the WebSockets entry's "Outgoing control frames" part covers
  `checkWriteType` and the check order.

### Behaviour change

An app that closed via `WriteMessage(CloseMessage, …)` now gets
`ErrWebSocketBadMessageType` and must call `Close`. Nothing in the repo did.

### Commits

- `a477086` WebSocket: WriteMessage sends text, binary, ping and pong only (N-085)
- this doc + next-list

## Next

Closed: N-085. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`. Open is now empty.
