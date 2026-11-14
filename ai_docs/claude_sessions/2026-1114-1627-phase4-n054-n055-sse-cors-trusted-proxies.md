# Phase 4 end: N-054 SSE CORS opt-in + id/retry, N-055 trusted proxies for ClientIP

Session id: `11b9823e-2fc0-452b-b05e-e3086e599dd2`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `39b4e37` → end `c49d6be` (+ this doc)
Plan: `ai_docs/todo/PLAN_ONCE_OVER_2026-1109.md`, Phase 4, N-054 and N-055.

## What was asked

1. `/sl`, then "Continue with phase 4". Both remaining items needed a decision,
   asked together:
   - **D8 (N-054)**: SSE `Access-Control-Allow-Origin: *` → **opt-in** (the
     plan's recommendation).
   - **D9 (N-055)**: which peers `ClientIP` trusts by default → **loopback +
     private ranges** (not the plan's "none by default"; chosen so nginx on
     the host and Docker/VPC setups keep working unconfigured).
2. "yes commit, close and raise as needed. Then do /sess-wrap".

Checks before committing: `go test -race -count=1 ./...`, `go vet ./...`,
`GOOS=windows go vet ./...`, `gofmt -l`, and staticcheck 2026.1 (v0.7.0,
installed into the session scratchpad; `~/bin` still has 0.6.1). All clean.

Commits were made only after the user's yes. N-054's state was saved as a
tree object (`git add -A; git write-tree; git reset`) before N-055 started,
then committed with `git commit-tree` on top of `39b4e37`, so each item has
its own commit although both touched Context.go, Server.go and the docs.

## N-054 (`8c0fced`)

- `SetSSEHeaders` (Response.go) no longer sets ACAO. New
  `context.setSSECORS` (Context.go), called from `SetSSE`, so `SSEHandler`,
  `SetupSSE` and `SSEHub` routes all get it:

  | `SSECfg.AllowedOrigins` | Response |
  |---|---|
  | empty (default) | nothing; a header the app set (middleware) is kept |
  | contains `"*"` | `Access-Control-Allow-Origin: *` |
  | Origin matches an entry | Origin echoed + `Vary: Origin` |
  | no match / no Origin | `Vary: Origin` only |

- Matching factored out of `originAllowed` (websocket.go) into
  `originListed(origin, originHost, list) (listed, wildcard)` and
  `originHostOf`, so SSE and WebSocket lists mean the same thing (full
  origin case-insensitive, bare `host[:port]` any scheme, `"null"`, `"*"`;
  trailing slash and spaces trimmed).
- `WithSSEAllowedOrigins(...)` (clones the slice). `WithOptions` already
  copies `SSECfg` whole.
- Credentialed `EventSource` (`withCredentials`) is left to the app:
  `Access-Control-Allow-Credentials` exposes cookie-authenticated data, and
  browsers refuse it with `*`. Documented on the field and in SKILL.md.
- `SSEvent.ID` / `SSEvent.Retry time.Duration`. `writeSSEEvent` now takes an
  `SSEvent`; `id:` and `retry:` go after `event:`, only when set, so streams
  without them are unchanged byte for byte. ID loses CR, LF, NUL (new
  `sseFieldValue`, also used for the event name); retry is whole ms rounded
  up by divide-then-increment (no overflow near the max).
- `SSEHub.Broadcast` copies ID and Retry onto the JSON-wrapped event (they
  are event fields, not payload).
- `SSEHandler` doc: all clients share the channel, so each event reaches one
  client; use `SSEHub` for fan-out. Pinned by a test.

Tests (`sse_options_test.go`, rweb_test): default off, app header kept, 14
list cases, option forms (functional option, WithOptions, hub route), id and
retry bytes on the wire, shared-channel delivery; `TestSSEHubBroadcastKeepsIDAndRetry`
in sse_hub_test.go. With the old `*` line restored, both default-off tests
fail.

## N-055 (`fce3ebb`)

New `client_ip.go` (with the walk's diagram at the top). `ClientIP` moved
there from Context.go; the interface doc was rewritten.

- `ServerOptions.TrustedProxies []netip.Prefix`:
  nil → `DefaultTrustedProxies()` (127/8, ::1, 10/8, 172.16/12, 192.168/16,
  fc00::/7, as Rails' RemoteIp); `NoTrustedProxies()` = `[]netip.Prefix{}`;
  `TrustAllProxies()` = 0.0.0.0/0 + ::/0. `WithTrustedProxies(...)` always
  stores a non-nil slice, so no arguments means none, not the default.
  `NewServer` resolves it once (`resolveTrustedProxies` → `Server.trustedProxies`):
  masked copy, invalid (zero) prefixes logged as errors and dropped.
  `WithOptions` copies the field.
- `context.forwardedTrusted()`: peer in the list; or no conn (synthetic
  `s.Request`, whose headers are the caller's own, as reasoned in N-018)
  and the list non-empty.
- `ClientIP`: untrusted peer → peer address (`peerIP`, as before, so a
  non-IP peer such as a pipe is reported as is). Trusted → `clientFromXFF`
  walks all X-Forwarded-For lines right to left without allocating: empty
  elements skipped, a non-IP entry stops the walk (answer = last valid
  entry seen), the first untrusted entry is the answer, all trusted → the
  leftmost. Then `X-Real-IP` if it parses, then the peer. Header addresses
  are canonical (`parseForwardedAddr`: port, brackets, IPv4-mapping and zone
  removed). `addrTrusted` unmaps and drops the zone, since `Prefix.Contains`
  never matches a zoned address.
- Proxy (`setForwardedHeaders`, proxy.go): a trusted peer's
  `X-Forwarded-Proto`/`-Host` pass through; untrusted, or none sent → this
  hop's values. This was the N-047 note carried on N-055.
- `consts.HeaderXRealIP` added.

Tests: `client_ip_test.go` (internal package) drives `ClientIP` through a
`peerConn` (a `net.Conn` with only `RemoteAddr`) for 35 cases; with the old
leftmost-entry `ClientIP` swapped in, 20 fail. `TestTrustedProxiesResolution`
and a wire test `TestClientIPOverTheWire`. `TestProxySetsForwardedHeaders`
now runs with `NoTrustedProxies()` (its client is on loopback, trusted by
default); `TestProxyPassesTrustedForwardedProtoHost` added. The existing
`TestClientIP` (synthetic requests) passes unchanged.

## Examples (`b72d739`)

None of the four example modules with their own go.mod (`hello`,
`proxy_server`, `sse_hub`, `websocket`) had built since N-053 (`9a6cad0`):
their go.sum lacked rohanthewiz/logger, logrus and x/sys. `go mod tidy` in
each added only indirect requires at the root's versions. The N-053 session
had checked `./examples/...` in the root module only.

## Docs

README: new "Client IP Behind Proxies" section; SSE and Proxy comments in the
usage example. SKILL.md: SSE "one channel, one receiver", "Event IDs and
reconnect delay" (with a `Last-Event-ID` resume example), "Cross-origin
streams (CORS)"; Reverse Proxy `X-Forwarded-*` bullet; new "Client IP and
Trusted Proxies (v0.2.1+)". CLAUDE.md: SSE bullet, new `client_ip.go`
bullet, Reverse Proxy bullet.

## Notes

- Checked for unkeyed `SSEvent{...}` literals (they would stop compiling
  with the new fields): none here or in other projects under `~/projs/go`.
- `sendSSE` (Server.go) has a duplicated comment paragraph about the reader
  goroutine; left alone.
- Phase 4 is done except N-056, which the plan parks on the roadmap. Open:
  N-073 (medium) and the low items, including N-075 raised here.

## Next

Closed: N-054, N-055. Declined: None. Raised: N-075.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
