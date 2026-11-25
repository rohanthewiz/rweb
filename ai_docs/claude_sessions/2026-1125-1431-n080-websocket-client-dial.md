# N-080: review of the WebSocket client draft, branch deleted, `rweb.DialWebSocket` built

Session id: `eebd9a56-e064-45b0-9826-696bafcdde19`
Date: 2026-11-25 · Branch: `master` · Start `154dd78` → end `0296cdf` (+ this doc)
Run: `/sl`, then the user asked for a deep look at the last feature branch,
`origin/roh/add-websocket-client`; on the findings, "Delete the old branch;
build the client"; then `/sw`.

## Done

### Review of `roh/add-websocket-client` (`13ba05e`)

Three commits of 2025-03-26, 216 behind master. N-080's premise was off: the
merge base (`074c63d`, 2025-03-07) had no `websocket.go` at all, so the
branch was a self-contained **client**, not one "against the old server
code". Contents: a 658-line `websocket.go` (`WebSocketConn` framing,
`WebSocketClient` with `Connect`/`SendText`/`SendBinary`/`Close`, read and
ping goroutines, a callback interface `WebSocketHandler` with
`BaseWebSocketHandler`) and `examples/hello_ws`, which sent a prompt to an AI
agent server at `ws://localhost:8000/ws/agent`.

Not mergeable: `git merge-tree` reported an add/add conflict on
`websocket.go`, and its `WebSocketHandler` interface clashes with master's
server handler type (`Server.go:1084`). The example did not build either:
`13ba05e` removed the `replace` line (it pointed at another machine's path),
leaving an unresolvable pseudo-version.

**Probe.** The branch's file, copied into a scratch module (it imports
nothing from rweb), was run in 16 scenarios against master's server or a raw
TCP server writing exact bytes. Echo up to 200 KB, the accept-key check,
answering server pings and its own pings worked. Confirmed defects:

| # | Defect | Seen | Cause (branch `websocket.go`) |
|---|---|---|---|
| 1 | One frame crashes the whole app | 64-bit length with the top bit set → `panic: makeslice: len out of range`, exit 2 | negative length passes the size check (`:160`) |
| 2 | Fragmented message hangs the reader for good | never delivered; goroutine stuck in `Mutex.Lock` after `Close` | `ReadMessage` recurses while holding `readMutex` (`:126`, `:190`) |
| 3 | Frames sent with the 101 lost | raw server: always; rweb greeting: 22 of 30 | 101 read via `bufio.NewReader(conn)`, frames then read from raw conn |
| 4 | `Close` sends no close frame | server sees EOF | `isClosed` set before `WriteMessage`, which then refuses |
| 5 | Sending from `OnConnect` deadlocks `Connect` | blocked > 1.5 s | `Connect` holds `c.mu` across `OnConnect` |
| 6 | No reconnect | 2nd `Connect` closes itself at once | `done` never remade |
| 7 | `Connect` can hang forever | silent server: > 2 s | no timeout, no context |
| 8 | Data race | 2 `-race` reports on `isClosed` | plain bool across locks |
| 9 | IPv6 literals fail | "too many colons" | `Hostname()+":"+port` |

Also accepted masked server frames; 3–4 socket writes per message.

Master's `WSConn` was already symmetric (`NewWSConn(conn, false)`: masks
writes, refuses masked frames, reassembles fragments, close handshake), and
seven test files used it as a client after a hand-rolled handshake. Missing
for an app: the handshake itself, a way to hand over bytes read with the 101
(tests set the unexported `ws.r`), and keepalive (`applyOptions` /
`startKeepalive` ran only on the server's upgrade path).

### Branch deleted

`git push origin --delete roh/add-websocket-client` (tip `13ba05e`). Restore
while the objects last: `git push origin 13ba05e:refs/heads/roh/add-websocket-client`.
Origin now has only `master`.

### `rweb.DialWebSocket` (`0296cdf`)

New `websocket_client.go`, diagram at the top:

```go
func DialWebSocket(ctx context.Context, url string, opts ...WSDialOptions) (*WSConn, *http.Response, error)
```

Design: only the opening handshake is new. The result is
`NewWSConn(conn, false)`, then the server's own `applyOptions` +
`startKeepalive`, so framing, fragments, close handshake, keepalive and
deflate are one implementation for both roles.

- **URL**: ws, wss, http, https; `net.JoinHostPort` (IPv6); userinfo → Basic
  `Authorization` unless the caller gives one; a space or control byte in the
  target is refused (`url.Parse` keeps a literal space in a query).
- **`WSDialOptions`**: `Header` (validated before dialing with
  `isValidHeaderKey`/`isValidHeaderValue`; `Upgrade`, `Connection`,
  `Sec-WebSocket-*`, `Content-Length`, `Transfer-Encoding` refused; `Host`
  may override; sorted for a deterministic request), `Subprotocols`,
  `Compression` + `CompressionLevel` (range-checked), `TLSConfig` (cloned,
  `ServerName` from the URL, `h2` removed from `NextProtos`),
  `NetDialContext`, `HandshakeTimeout` (30 s; negative = ctx only),
  `WriteTimeout`/`PingInterval`/`PongTimeout` (server defaults: 10 s, 30 s,
  10 s).
- **Bounding the handshake**: ctx narrowed by `HandshakeTimeout` drives the
  dial and `tls.HandshakeContext`; the upgrade I/O gets ctx's deadline on the
  conn, and `context.AfterFunc` moves it into the past on cancel. If
  `stop()` reports the func ran, the outcome is discarded. Errors go through
  `handshakeCtxErr`: the socket's timer can fire before the context's, so a
  socket timeout is reported as `context.DeadlineExceeded`.
- **Response**: read through a `bufio.Reader` over an `io.LimitedReader`
  (1 MB head cap; the limit does not carry over, since WSConn reads the conn
  directly). Not 101 → `ErrWebSocketBadHandshake` (new) with the response,
  its body kept up to 64 KB. A 101 is checked (`checkUpgradeResponse`):
  `Upgrade`/`Connection` tokens (`hasToken`), `Sec-WebSocket-Accept`, at most
  one subprotocol and only an offered one. Extensions via `clientDeflate`
  (RFC 7692): only `permessage-deflate` when offered;
  `server_no_context_takeover` → `readNoContext`, `client_no_context_takeover`
  → `writeNoContext`, `server_max_window_bits` 8–15 accepted (the kept 32 KB
  dictionary covers a smaller window), `client_max_window_bits` refused (not
  offered: compress/flate cannot use a smaller window), unknown or repeated
  parameters and a second extension refused. The offer is the bare
  `permessage-deflate`.
- **Lead bytes**: whatever the handshake's buffer holds past the 101 becomes
  `ws.r = io.MultiReader(lead, conn)`, as the server does for bytes behind an
  upgrade request.
- **Shared/changed in `websocket.go`**: `wsAcceptKey` (used by
  `performHandshake` too); `ErrWebSocketBadMask`'s text was "websocket frame
  not masked", wrong for a client refusing a masked frame, now names both
  directions (text change only; nothing compared it).
- **Lifecycle**: the app owns the conn; nothing calls `finish`, so `Close`
  stops the ping goroutine. Documented: call `Close` also after a read error.

Not done: `HTTP_PROXY` (use `NetDialContext`), a callback wrapper like the
draft's `BaseWebSocketHandler`, switching the seven test files' hand-rolled
client handshakes to `DialWebSocket` (they write raw bytes on purpose).

The client exercises one path the server never did: `wsDeflate.decompress`
with `readNoContext == false` (dictionary carried between messages), since
rweb's server always imposes `client_no_context_takeover`. Covered by the
compression test.

### Tests (`websocket_client_test.go`, 19)

Helpers: `rawDialServer` (one-shot raw server; cleanup registered up front,
not from the accept goroutine), `rawSession` (answers the client's close
frame, so `Close` does not wait out 2 s on a silent script), `upgradeHead`,
`serverFrame`, `echoServer`, `mustDial`, `readText`, `readCountConn` (the name
`countingConn` was taken in `websocket_deflate_test.go`).

Echo + Close with code/reason (tcp, tls); http:// scheme; frame in the same
write as the 101; greeting from an rweb handler on 25 connections (tcp, tls);
three fragments with a ping between, pong payload checked server-side;
Shutdown → client reads 1001, answers, Shutdown returns at once (tcp, tls);
refusals (cross-origin 403 via `Origin` header, 404) with status and body; 15
response-check cases (wrong accept, missing Upgrade/Connection, subprotocol
not offered / unasked / two, extension unasked, `client_max_window_bits`,
window 7, unknown and repeated parameter, two extensions, other extension;
accepted: token lists and case, deflate with window and no-context) each
checking the conn is closed on failure; request capture (target with escaped
query, Host override, headers, subprotocol list, deflate offer, Basic from
userinfo with `%40`, own Authorization wins, 16-byte key); bad input never
dialed (11 cases) and the error redacts the password; `HandshakeTimeout`
200 ms and cancel at 150 ms; head cap; `TestHandshakeCtxErr`; subprotocol
pick against an rweb route; compression over tcp and tls (40 similar JSON
messages: 9,990 bytes arrived as 562 on tcp, 1,442 on tls; 380 KB message;
declined offer runs uncompressed); liveness (silent server fails the read
after ~200 ms; a live rweb server stays up over six windows on pongs alone);
masked frame → `ErrWebSocketBadMask`, top-bit length →
`ErrWebSocketPayloadTooLarge` (no panic); IPv6; four writers + reader +
`Close` during a blocked read.

**Mutation check** (each break applied to a copy, then restored): no lead
bytes → `KeepsFrameAfter101` and `GreetingFromHandler/tcp` fail (tls does not:
TLS hands bufio one record at a time, so the 101's record arrives alone); no
accept check → `ChecksResponse/wrong_accept`; no timeout mapping →
`TestHandshakeCtxErr` (the end-to-end timeout test cannot trigger the race on
demand, hence the unit test); always `readNoContext` → `Compression` tcp/tls;
no value check → `RefusesBadInput/CRLF_in_value`; `Hostname()+":"+port` →
`IPv6`. The first header mutation also showed a test flaw (the `dialed` flag
was not reset per subtest), fixed.

### Example `examples/websocket_client`

Own module (`replace ../..`), relays terminal lines to a server and prints
what comes back; `-url` (default the `examples/websocket` echo route),
`-token`; Ctrl-C/SIGTERM or end of input → `Close(1000, …)`; a refusal prints
the response body. Binary name added to `.gitignore`; the websocket example's
README points to it.

Run live against `examples/websocket` on :8080 (built binaries in the
scratchpad): through a fifo, greeting, `Echo: hello`, `Echo: über – ünïcode`,
end of input → "connection closed", exit 0; SIGINT → clean close, exit 0;
`/nope` → "server answered 404 Not Found", exit 1. Piped input
(`printf 'a\nb\n' |`) closes before the echoes print: `Close` drops data
until the server's close frame (existing `WSConn.Close` behaviour, now noted
in the example and SKILL.md). Server stopped with SIGTERM by PID; nothing
left running, :8080 free.

### Docs

README (feature list; "WebSocket client" subsection), `ai_docs/SKILL.md`
("WebSocket Client (v0.2.1+)" with usage and behaviour notes; skill
description now says "WebSockets (server and client)"), CLAUDE.md
(`websocket_client.go` component entry; WebSockets bullet).

## Checks (final tree)

- gofmt clean; `go vet ./...` on darwin, windows, linux.
- `go test -count=1 ./...` and `go test -race -count=1 ./...`: ok (root ≈ 22 s).
- Client tests `-race -count=5` and `-count=3 -shuffle=on`: ok.
- Five example modules (`hello`, `proxy_server`, `sse_hub`, `websocket`,
  `websocket_client`): vet (darwin and windows) and build ok.

## Next

Closed: N-080. Declined: None. Raised: N-081. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
