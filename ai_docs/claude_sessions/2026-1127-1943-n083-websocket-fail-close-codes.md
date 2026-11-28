# N-083: WebSocket close frames on every protocol error (1002/1007/1009)

Session id: `4dfc6b05-dd6e-4c62-9f27-53da68996a5f`
Date: 2026-11-27 · Branch: `master` · Start `2dc70e5` → end `2fcdfb5` (+ this doc)
Run: "Next list item N-083" (item text pasted), then `/sess-wrap`.

## Done

### The item

N-083 (raised in the N-081 session): `ReadMessage` sent a close frame via
`failConnection` only for bad control frames (N-062-era) and N-081's
sequence and UTF-8 errors. Every other protocol error just returned, so the
peer saw a bare TCP close (the HTTP loop's on the server once the handler
returned; the app's on the client). RFC 6455 §7.1.7 asks the endpoint to
*fail the WebSocket connection*, which means sending a close frame with a
§7.4.1 code. Premise confirmed by reading `ReadMessage`, `readFrame` and
`wsDeflate.decompress`.

### The change (`websocket.go`)

| Error path | Where | Code |
|---|---|---|
| `ErrWebSocketBadControlFrame` (fragmented or > 125 bytes) | `readFrame` → `frameFailCode` | 1002 (unchanged) |
| `ErrWebSocketBadRSV` (RSV2/RSV3) | `readFrame` → `frameFailCode` | 1002 |
| `ErrWebSocketBadMask` (wrong for the peer's role) | `readFrame` → `frameFailCode` | 1002 |
| `ErrWebSocketPayloadTooLarge` (frame, or a 64-bit length with its top bit set) | `readFrame` → `frameFailCode` | 1009 |
| I/O errors (EOF, reset, deadline, closed) | `readFrame` → `frameFailCode` false | none: returned as is, conn left open |
| RSV1 without deflate, or on a control/continuation frame | `ReadMessage` | 1002 |
| reserved opcode (0x3–0x7, 0xB–0xF) | `ReadMessage` default case | 1002 |
| reassembled message over the limit | `ReadMessage` continuation case | 1009 |
| compressed message inflating past the limit | new `ws.inflate` | 1009 |
| compressed data that does not inflate | new `ws.inflate` | 1007 (error stays `compress/flate`'s) |

- `frameFailCode(err) (int, bool)` keeps the mapping in one table-commented
  place, and makes the I/O case explicit. A read deadline in particular must not
  fail the connection: keepalive's dead-peer timeout and app deadlines
  both surface there, and a write to a peer that isn't there would only wait
  out `closeHandshakeTimeout` (2 s).
- The top-bit-set 64-bit length is strictly a framing error (§5.2, so 1002
  would fit). It keeps `ErrWebSocketPayloadTooLarge` (from N-080's fix), so it
  gets 1009. Either code ends the connection; noted on `frameFailCode`.
- `ws.inflate` wraps both decompress call sites (unfragmented and
  reassembled), so the two can't drift apart.
- The returned errors are unchanged, so the `errors.Is` checks in callers
  still hold.
- I first wrote a comment claiming that "browsers and common Go libraries send
  1007" for a corrupt deflate stream. It was unverified, so I replaced it with
  the RFC reasoning before committing.

### Tests

- New `websocket_fail_codes_test.go`, `TestReadMessageFailCodes`: 15 cases on a
  net.Pipe pair. Each checks the error, the close code the peer reads
  (`closeCodeFrom`) and that `Done()` closes (`expectFailed`, both from
  websocket_sequence_test.go). The cases: RSV2, RSV3, RSV1 without the
  extension, RSV1 on a ping, RSV1 on a continuation, unmasked client frame,
  reserved data opcode, reserved control opcode, frame over the limit,
  fragmented message over the limit, top-bit length, deflate bomb (whole and
  fragmented), data that does not inflate (whole and fragmented; `0xff…` uses
  reserved block type 11). Helper `rawFrameRSV` sets any RSV bits and masking.
- `TestReadMessageIOErrorSendsNoCloseFrame`: a 20 ms read deadline returns a
  timeout, `Done()` stays open, and a message sent afterwards is read.
- To find the tests that the change made block, I timed every
  `WebSocket|WS|Deflate|Dial` test against master (`git stash`) and joined
  the two runs. Only three newly took 2 s or more: `TestDeflateRSVRules`
  (4 s, two cases), `TestDeflateBombIsRefused` and
  `TestWebSocketPayloadLengthTopBitSet`. Each now reads the peer side with
  `closeCodeFrom` and checks the code. `TestWebSocketSetMaxMessageSizeDuringReads`
  already drained its peer, so it wasn't affected.
- `TestDialWebSocketRefusesBadServerFrames` (client role) now reads the
  client's close frame on the raw server and checks it: masked → 1002,
  negative length → 1009, plus a new reserved-opcode case → 1002.
- Seven deliberate breaks, each caught: no header fail (10 failing), I/O
  errors failing too (1), no RSV1 fail (5), no opcode fail (5), no
  reassembled-size fail (2), inflate always 1007 (4), too large sent as 1002
  (6).
- `go test -race ./...` green; `GOOS=windows go vet ./...` clean; gofmt clean.
- No benchmark: only error paths changed.

### Docs

README (WebSocket section, after the close-frame paragraph), `ai_docs/SKILL.md`
(new bullet after the close-frame rules) and CLAUDE.md (WebSockets: the
`failConnection` sentence now lists every path and code, and notes that a
net.Pipe test needs `closeCodeFrom` or waits 2 s).

### Commits

- `2fcdfb5` WebSocket: send a close frame on every protocol error (N-083)
- this doc + next-list

### Noticed, not raised

The editor's diagnostics flagged older lint in files this session didn't touch
(`Server.go:928` tagged switch, `request_framing.go` unused `errChunkTooLarge`,
unused tables in `bytesconv_table.go`, `fmt.Appendf`/`WriteString` hints in
tests). That is what N-079's staticcheck run covers, so no new item.

## Next

Closed: N-083. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
