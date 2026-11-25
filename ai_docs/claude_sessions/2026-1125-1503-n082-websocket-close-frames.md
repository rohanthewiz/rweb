# N-082: WebSocket close frames: empty close answered empty, malformed close frames fail (1002/1007)

Session id: `f88d2fc6-80dc-45a7-8134-33eb44979a3f`
Date: 2026-11-25 · Branch: `master` · Start `44356a9` → end `0bba2c1` (+ this doc)
Run: `/clear`, `/sl` (loaded the N-081 session doc and the next-list), "Start on N-082", then `/sw`.

## Done

### The item

N-082 (raised in the N-081 session) had two halves, both confirmed by
reading `ReadMessage`/`handleClose` (the first had already been confirmed by
N-081's throwaway probe):

- (a) a close frame with **no body** was answered with status **1005** on the
  wire: `ReadMessage` set `code = wsCloseNoStatusReceived` for an empty body
  and `handleClose` echoed `code`. RFC 6455 §7.4.1 forbids sending 1005, and
  the empty close is the common case (a browser's `ws.close()` with no
  arguments).
- (b) incoming close frames were not validated: a 1-byte body, codes that
  may not be sent, and a non-UTF-8 reason were accepted, echoed (the code)
  and passed to `OnClose` handlers.

### Fix (`websocket.go`, commit `0bba2c1`)

- **`parseCloseBody(data)`** (new) → `(code, reason, failCode, err)`, with a
  table in its comment: empty → 1005, `""`; 1 byte → fail 1002; code not
  `validCloseCode` → fail 1002; reason not `utf8.Valid` → fail 1007. Errors
  wrap new exported **`ErrWebSocketBadClose`** with what was wrong.
- **`validCloseCode(code)`** (new), table in its comment: 1000–1003,
  1007–1014, 3000–4999 accepted; 0–999, 1004, 1005, 1006, 1015, 1016–2999,
  ≥ 5000 refused. Decision: **1012–1014 accepted** (not in RFC 6455 but
  IANA-registered: service restart, try again later, bad gateway), since
  refusing them would fail exactly the close a client should act on by
  reconnecting; unassigned 1016–2999 refused, as Autobahn expects.
- **`closeFramePayload(code, reason)`** (new): code + reason, or an empty
  body for 1005 (reason dropped: a reason cannot go without a code). Used
  by `handleClose`'s echo (so an empty close gets an empty close back) and
  by `Close` (so an `OnClose` handler answering with `Close(code, text)`,
  i.e. `Close(1005, "")`, sends no body either; SKILL.md documents that
  echo pattern).
- **`ReadMessage`'s close case**: `closeRcvd` is still stored first, now
  also for a malformed frame (the peer has sent its close either way), so a
  `Close` waiting for the answer returns at once and a later `ReadMessage`
  returns `ErrWebSocketAlreadyClosed`. On a parse error: `failConnection`
  with the code, return the error; no echo, no `OnClose`.
- Doc comments: `OnClose` (code 1005 = no body; text is valid UTF-8; bad
  frames fail instead), `Close` (1005 → no body), `handleClose` (why).
- `ErrWebSocketBadClose` is a protocol error, so `isWebSocketSessionEnd`
  does not swallow it (as for N-081's errors).
- Server and client (`DialWebSocket`) roles share the code.

### Tests (`websocket_close_frame_test.go`, new)

Helpers: `closeReply`, `closeFrameFrom` (like `closeCodeFrom` but also
reports a close frame with no body), `recvClose`, `closeBody`.

- `TestWebSocketEmptyCloseAnsweredEmpty`: automatic answer, and an
  `OnClose` handler answering via `Close(code, text)`; CloseMessage with no
  data, `OnClose(1005, "")`, answer with no body.
- `TestWebSocketCloseNoStatusSendsEmptyFrame`: `Close(1005, "")` and
  `Close(1005, "dropped")`.
- `TestWebSocketCloseValidCodesAccepted`: 16 codes × 4 reasons (empty,
  ASCII, multi-byte, the 123-byte maximum); handler and message see them,
  answer echoes the code.
- `TestWebSocketBadCloseFrameFails`: 17 cases (1-byte body; codes 0, 999,
  1004, 1005, 1006, 1015, 1016, 1100, 2000, 2999, 5000, 65535; 1006 with a
  reason; bad byte, surrogate, truncated character in the reason): error,
  peer's close code, `Done()`, no `OnClose`, next read
  `ErrWebSocketAlreadyClosed`.
- `TestWebSocketBadCloseAnswerEndsClose`: a peer answering `Close` with 1005
  does not hold `Close` for the 2 s handshake timeout.
- `TestWebSocketCloseFramesOverTheWire`: tcp and tls through an rweb server
  (`echoServer`): empty close answered empty; 1005 answered with 1002.
- `TestDialWebSocketCloseFrames`: client role over `rawDialServer` (empty,
  1-byte, 1006, bad reason).

**Deliberate breaks** (each applied to a copy of the fixed file by a small
script, tests run, file restored): 1005 echoed again; `Close` not mapping
1005; any code accepted; no UTF-8 check on the reason; 1-byte body read as
empty; 1012–1014 refused; `closeRcvd` stored only on success. All seven
caught (the last by the "already closed" assertion and by
`TestWebSocketBadCloseAnswerEndsClose`).

### Found, not fixed (raised)

- **N-084 (medium):** outgoing control frames are not checked. Confirmed with
  a throwaway probe test (deleted): `Close(1011, <200-byte reason>)` and
  `WritePing(<200 bytes>)` go out as control frames over 125 bytes, which the
  peer refuses (`ErrWebSocketBadControlFrame`), losing code and reason;
  `Close(1006, …)` puts an unsendable code on the wire (the peer now answers
  1002). Likely trigger: `ws.Close(1011, err.Error())`. Needs an API decision
  (repair in `Close` vs return an error); recommendation recorded in the item:
  repair in `Close` (truncate reason at a UTF-8 boundary, map the code),
  refuse in `WritePing`. `closeFramePayload` is where the `Close` half goes.

### Docs

- README (WebSocket section): empty close answered empty, 1005 never sent;
  bad close frames → 1002/1007, `ErrWebSocketBadClose`.
- `ai_docs/SKILL.md`: new bullet after N-081's (v0.2.1+), incl. the
  `Close(1005, "")` echo case and the accepted code ranges.
- `CLAUDE.md` WebSockets bullet: `parseCloseBody`, `closeFramePayload`,
  `validCloseCode` ranges, `closeRcvd` set before the check.

## Checks (final tree)

- `go test -race -count=1 ./...`: ok (root package 25 s).
- `go vet ./...` and `GOOS=windows go vet ./...`: clean; `gofmt -l .`: clean.
- `examples/websocket`, `examples/websocket_client` (own modules): vet ok.
- staticcheck not run (still N-079).

## Next

Closed: N-082. Declined: None. Raised: N-084. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
