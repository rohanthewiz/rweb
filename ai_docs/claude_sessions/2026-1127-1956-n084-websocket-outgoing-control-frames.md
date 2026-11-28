# N-084: outgoing WebSocket control frames kept within the rules

Session id: `4dfc6b05-dd6e-4c62-9f27-53da68996a5f` (same session as N-083)
Date: 2026-11-27 · Branch: `master` · Start `eb7abad` → end `bcdf843` (+ this doc)
Run: after N-083's wrap, "Do N-084", then "yes" to `/sess-wrap`.

## Done

### The item

N-084 (raised in the N-082 session): control frames this side sends were not
checked, and `appendFrame` checked nothing for control opcodes. Three problems:

- `Close(code, reason)` with a reason over 123 bytes put a close frame over
  125 bytes on the wire. A peer refuses that, so the code and reason were lost.
- `Close` with a code that may not be sent was answered by the peer with 1002
  (since N-082).
- A reason that is not UTF-8 went out as is.

Separately, `WritePing` sent any size. The item asked for a decision and
recommended repairing in `Close` and refusing in `WritePing`. I took that
recommendation.

### Code (`websocket.go`)

- **`closeFramePayload`** is used by `Close` and by `handleClose`'s echo; the
  echo's code has already passed `parseCloseBody`, so that path is unchanged.
  - A code that may not be sent (`!validCloseCode`, which now also covers 1005)
    gives an empty body, and the reason is dropped with it, since a reason
    cannot go without a code (§5.5.1).
  - **Decision (flagged to the user):** the item suggested mapping such a code
    to 1000 or 1011. I sent no code instead, because 1000 or 1011 would tell
    the peer "normal" or "server error", a meaning the app never gave. 1005
    already worked this way, so it is now one rule.
  - The table of inputs and what is sent is in the doc comment.
- **New `closeReason`** makes a reason sendable:
  - The fast path returns a reason of ≤ 123 bytes that is already valid UTF-8 untouched.
  - Otherwise `strings.ToValidUTF8(reason, "�")` runs first, then a cut at
    `maxCloseReason` (123) that steps back to a `utf8.RuneStart`.
  - The order matters: replacing first means a U+FFFD that straddles the limit
    is dropped whole rather than leaving half a character.
- **The 125-byte refusal is in `appendFrame`, not `WritePing`.**
  - `WriteMessage` and `WriteMessages` pass `int(messageType)` straight
    through, so `WriteMessage(PingMessage, big)` would have bypassed a check
    placed only in `WritePing`.
  - `appendFrame` builds every frame sent, so one check covers all paths.
  - It returns `ErrWebSocketBadControlFrame`, wrapped with the size, and
    appends nothing. A `WriteMessages` batch is refused whole, before its one
    write.
  - The connection stays open.
  - The sentinel's doc comment now also covers the write side.
- `Close` still returns nil after a repair: its job is to close.
- The `WritePing` and `Close` doc comments are updated.
- I checked the in-repo callers: the examples, README and SKILL use only
  `Close(1000, …)`, and `goingAway` writes 1001 directly.

### Tests (new `websocket_control_out_test.go`)

- `TestCloseReason`: 11 cases.
  - The limits: 123 kept, 124 and 200 cut.
  - A 2-byte é straddling the cut and ending exactly at it, and a 4-byte 🙂
    straddling it.
  - A bad byte, a surrogate, and a U+FFFD that lands across the limit.
- `TestCloseFramePayloadCodes`: 12 unsendable codes give nil, including -1 and
  70000, which used to wrap through `uint16`. 8 sendable codes are kept with
  their reason. A body stays ≤ 125 bytes.
- Over net.Pipe, with `closeOverPipe` running a peer `ReadMessage` plus `OnClose`:
  - A 280-byte reason with 1011 arrives as its first 123 bytes, in both roles
    (so client masking is covered).
  - A bad reason arrives as `bye�`.
  - Codes 0, 1006, 1015, 2000 and 5000 arrive as 1005 with no reason, and the
    peer reports no error.
- `TestControlFrameOver125Refused`: five write paths × two roles. The paths
  are `WritePing`, `WriteMessage` with ping, pong and close, and a
  `WriteMessages` batch. For each:
  - The error comes back at once with no reader on the pipe, which proves
    nothing was written, since a write to net.Pipe would block.
  - `Done()` stays open.
  - The peer's first frame afterwards is the text message sent next.
- `TestPing125Arrives`: a ping at exactly the limit arrives whole.
- Seven deliberate breaks, each caught:
  - no `appendFrame` check (1 failing)
  - `>=` 125 (2)
  - only 1005 dropped (2)
  - no reason repair (3)
  - byte cut without the rune boundary (4)
  - no UTF-8 repair (5)
  - the fast path skipping the UTF-8 check (4)
- `go test -race ./...` green; `GOOS=windows go vet ./...` clean; gofmt clean.
- `BenchmarkWSEcho`: I built master's binary in a temporary `git worktree`,
  because `git stash` had left the new untracked test file in place and the
  build failed. Over 8 interleaved runs (medians), new vs old: binary64K
  −1.2 %, text16 −0.9 %, text16x64 −1.9 %, text1K −1.2 %. That is noise, and
  B/op is unchanged.

### Docs

- README: a sentence after N-083's paragraph.
- `ai_docs/SKILL.md`: a new bullet after the protocol-error rules, plus notes
  in the API reference on `Close` and `WritePing`.
- CLAUDE.md: the WebSockets entry now covers `appendFrame`'s refusal and
  `closeFramePayload`/`closeReason`.

### Raised

- **N-085**: `WriteMessage`/`WriteMessages` accept any `MessageType`.
  - `WriteMessage(CloseMessage, …)` sends a close frame without `Close`'s state
    (`closed` is not set, so data frames can follow it), its body unchecked.
  - A non-message type such as 0 or 3 sends a continuation or reserved-opcode
    frame.
  - Since N-084 only a small payload gets through.
  - Value low; nothing in the repo calls it that way.

### Commits

- `bcdf843` WebSocket: keep outgoing control frames within the rules (N-084)
- this doc + next-list

## Next

Closed: N-084. Declined: None. Raised: N-085.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
