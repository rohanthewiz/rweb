# N-062, N-059, N-060, N-072: WebSockets in a real browser, lingering close, strict request heads

Session id: `3a7f4f72-7532-415b-a72a-5eb9a2277e67`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `2c72644` → end `7301fca` (+ this doc)
Run: `/work-next-list` (whole Open + Validate sections), first batch of three.

## What was asked

`/work-next-list`: do every Next-list item that does not need the user's
input, make minor decisions on judgement, commit and push after each item,
and `/sess-wrap` every 2-3 items. This doc covers the first batch.

## Triage (printed at the start of the run)

Doable, in run order: N-062 (Validate, medium), N-059, N-060 + N-072
(together, as N-072 asks), N-064, N-065, N-066 (proxy group), N-068,
N-070 + N-074 (both touch `writeResponse`'s status line).

Skipped, needs the user: **N-075**. The item asks for a decision with no
default: should a trusted peer's `X-Forwarded-Proto`/`-Host` feed
`Request().Scheme()` and the WebSocket origin check as an opt-in, or be on
with the default trusted list?

## Done

### N-062: WebSockets checked with a real browser, wscat, and a Host-rewriting proxy (`b9d839f`, list only)

Scratch harness (in the session scratchpad, not committed): rweb on :18081
(page, `/ws` with default options, `/ws-listed` with
`AllowedOrigins: ["http://127.0.0.1:18083"]`, `/log`, `/shutdown`), a second
rweb origin on :18082, and a Go `httputil.ReverseProxy` on :18083 whose
Director rewrites `Host` to `backend.internal:18081`. Four headless Chrome
instances (`--headless=new`, separate profiles, killed by PID), plus
`wscat` 6.0.1 from npm, run under `script` for a pty, since wscat prints
nothing on a non-TTY.

| Check | Result |
|---|---|
| Same-origin page | 101, echo works |
| Page on another origin | 403; browser: error event, close 1006 |
| Through the Host-rewriting proxy, `/ws` | 403 (Origin host ≠ rewritten Host), as N-040 predicted |
| Through the proxy, route with `AllowedOrigins` | 101, echo works |
| 75 s idle | 30 s pings; Chrome's pongs reached the pong handler twice per socket (direct and proxied) |
| `Shutdown` | 1001 sent; Chrome logged `close code=1001 clean=true`; handlers got `CloseMessage` and returned; `Shutdown` returned nil 0.2 s after the call |
| wscat, no Origin | connects (direct and through the proxy) |
| wscat `-o http://evil.example` | 403 |
| wscat with the proxy's origin | 403 on `/ws`, 101 on the listed route |

Not checkable from a browser or wscat: the write timeout (needs a peer
that stops reading); Go-client tests cover it.

Finding → **N-076**: the first harness handler ignored `CloseMessage` and
read again. After `goingAway`, that `ReadMessage` waits out the 2 s
`closeHandshakeTimeout` deadline (the conn stays open for the handler's
loop to close), and `Shutdown` waits on it meanwhile. Not a defect in
documented usage (the example and SKILL.md return on `CloseMessage`), but
`ReadMessage` could fail at once after a completed handshake.

### N-059: lingering close after an early refusal (`6774799`)

- `serveConn` refused requests before any handler (400, 408, 413, 417, 431,
  501) by writing the response and returning, so the deferred `Close` hit a
  socket the client was still writing to. That causes a reset, which on
  Linux can wipe the response from the client's buffer before it is read.
- New `refuse(resp)` closure in `serveConn`; all 16 such sites go through
  it (two inside `headFailed`). It writes the canned response and calls
  `lingerClose` (`conn_limits.go`): `CloseWrite` (FIN; close_notify on
  TLS), then `io.Copy(io.Discard, conn)` under a read deadline of
  `lingerTimeout` = 1 s.
- **Decision: 1 s, time-bounded only.** net/http sleeps 500 ms without
  reading after a 431 (sends stall on full buffers); nginx lingers up to
  30 s. A client that stops sending reads the response and EOF at once and
  ends the drain by closing. No byte cap: time is the bound, and the
  client pays for every byte.
- A conn without `CloseWrite` (test fakes), or TLS before its handshake,
  closes at once as before. The conn stays active in the registry, so a
  `Shutdown` may wait up to 1 s longer for it.
- Tests `linger_close_test.go` (tcp and tls): 4 MB written after a 413
  gets through and the client reads the 413 and EOF (**broken pipe**
  without the change); EOF arrives right after a 400, before the drain
  ends (shows the half-close comes first); an endless sender is cut off
  after about 1 s (**89 µs** without the change); `lingerClose` on a conn
  without `CloseWrite` neither blocks nor reads.
- Docs: deadline diagram in `conn_limits.go` ("then lingerClose"),
  CLAUDE.md (conn_limits.go), SKILL.md (Connection limits).

### N-060 + N-072: strict request line, no control bytes in header values (`eca2f2f`)

- `isValidRequestLine` (`http.go`): one or two single spaces, a non-empty
  last field, no control byte (tab, VT, FF, bare CR, NUL, DEL). Anything
  else gets 400 and a close via `refuse`. Checked **before the method**,
  because `GET\t/x HTTP/1.1` first came back 501 (the method parsed as
  `GET\t/x`); a malformed line is a 400. No-version lines (`GET /x`) stay
  accepted (existing tests rely on them); non-ASCII targets still route.
- `isValidHeaderValue` (`http.go`): no CTL but HTAB, checked after removing
  the line ending (`\n`, then an optional `\r`).
- **Decision (N-060 left it open): refuse, not strip.** net/http refuses
  the same (`httpguts.ValidHeaderFieldValue`), and it matches the framing
  rules: a proxy that strips and a handler that sees the bytes would read
  two different heads. obs-text (UTF-8) is kept.
- The value is now trimmed of exactly OWS (`strings.Trim(raw, " \t")`)
  instead of `TrimSpace` over the rest of the line, which dropped VT, FF
  and stray CRs at either end silently (they are refused now).
- Tests: `TestRequestLineRefusesExtraWhitespaceAndControls` (11 lines × tcp
  and tls), `TestRequestLineKeepsNonASCIITarget`, `header_value_test.go`
  (8 control-byte values × tcp and tls; tab inside, OWS, UTF-8, empty, no
  space after the colon, and a bare-LF ending are still accepted). Every
  refusal case was served or 404'd by the old code; that run was slow
  because each accepted keep-alive request waited out the 10 s test
  deadline.
- Benchmark `BenchmarkKeepAliveBrowser`, two interleaved rounds:
  allocations unchanged (12/op); time within this machine's noise (the
  second round ran about 2× slower for both builds).
- Finding → **N-077**: a head that ends in a bare-LF blank line is a 400,
  although bare-LF request and header lines are accepted.
- Docs: SKILL.md (Strict Request Framing), CLAUDE.md (request_framing.go
  entry).

## Checks run for each code item

`go test ./...`, `go test -race .`, `go vet ./...` for darwin, linux and
windows. `staticcheck` on this machine is built with Go 1.23 and cannot
load the module (go 1.24 required), so vet was the lint.

## In progress at the time of this doc

Work on N-064 and N-065 had started in `proxy.go`: `proxyBodyReader`
(per-Read idle timer that cancels the upstream request context; maps to
`errProxyReadTimeout`) and `proxyRedirect` (rewrites an absolute
`Location`/`Content-Location` on the target's scheme and host under the
target base back to the client prefix). It is held in `git stash` ("proxy
WIP N-064/N-065") during this wrap and continues in the next batch.
Decisions taken so far for that work:
- N-064: `ProxyReadTimeout` **off by default**. The repo keeps body read
  timeouts off by default, and a 60 s default like nginx's would cut
  proxied SSE or long-poll streams that go quiet.
- N-065: only absolute URLs pointing at the target get rewritten (nginx's
  `proxy_redirect default`). A relative `Location: /new` is ambiguous: an
  upstream configured with a sub-path root already sends `/api/new`, and a
  rewrite would make it `/api/api/new`. Cookie `Path` is left alone (an
  upstream's `Path=/` often means the whole site). The relative case goes
  back into the item as a question for the user.
- N-066: use the proxy's own `http.Client`. Go's Transport returns a 101
  whose `Body` is an `io.ReadWriteCloser` and forces HTTP/1 for upgrade
  requests (`requiresHTTP1`), so dialing, TLS, `HTTP_PROXY` and the header
  timeout come for free.

## Next

Closed: N-062, N-059, N-060, N-072. Declined: None. Raised: N-076, N-077.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
