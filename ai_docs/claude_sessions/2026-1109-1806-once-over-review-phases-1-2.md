# Once-over review, plan, and Phases 1–2 (reliability)

Session id: `8b6a65bb-d29b-4236-9f58-9764271d9448`
Date: 2026-11-09 · Branch: `roh/once-over-fab5.2` · Start `9781e96` → end `f2c7c47` (+ this doc)

## What was asked

1. "Take a deep look at rweb and see how reliability, performance, and
   convenience can be improved."
2. "Create a detailed plan under ai_docs/todo/ to address these."
3. "Go with your recommendations, start phase 1 (spawn opus 5.5 agents to do
   the work as necessary)."
4. "Fix N-058 then start phase 2."

## 1. The review

Read the core end to end (`Server.go`, `Context.go`, `Request.go`,
`Response.go`, `conn_reader.go`, `Group.go`, `websocket.go`, `sse_hub.go`,
`send.go`, `http.go`, the router) and confirmed the main findings with
throwaway tests in a scratch module outside the repo (a `replace` pointing at
the checkout).

Reproduced at `9781e96`:

| Finding | Evidence |
|---|---|
| `://` in a query string misroutes | `GET /redirect?url=http://evil.com/admin` served by `/admin`, `Host()` = `evil.com` |
| Group middleware returning nil without `Next()` still runs the handler | `401 "denied;SECRET"` |
| Sibling sub-groups overwrite each other's middleware | both ran `B` |
| Response headers accept CR/LF | `Redirect` with a query value injected `Set-Cookie` |
| `SSEHubOptions.OnDisconnect` deadlocks if it calls the hub | hung until the test's timeout |
| SSE events malformed | `%!s(int=42)`; text after the first newline dropped |
| No header size limit, no timeouts | a 64 MB header line was answered normally |
| Lenient framing | Content-Length won over chunked; `-5` accepted; last of two lengths won |
| `Run` never releases signals | a process with `Run` in a goroutine survived SIGTERM |
| `Expect: 100-continue` unanswered | no interim response |
| 204 with `Content-Length: 0` | on the wire |
| Path params not percent-decoded | `John%20Doe` |

Read only: no `Shutdown`, accept loop with no backoff, WebSocket without an
Origin check or pings, `ws.closed` raced, pooled buffers keeping their largest
size, proxy on `http.DefaultClient`, and the convenience gaps.

Performance baseline: keep-alive hello on loopback 14.7 µs/op, 144 B/op,
8 allocs/op (six request headers). A prototype writing headers and a small
body in one write measured 3–4 % faster on plain TCP.

A benchmark-harness bug of my own cost some time: the client waited for 80
bytes of a 78-byte response (`WriteText` sends bare `text/plain`, not the
charset its doc promises, which became finding N-051).

## 2. The plan

`ai_docs/todo/PLAN_ONCE_OVER_2026-1109.md`: four phases, each item with
evidence (ran / read), fix design and tests, plus a table of nine behaviour
decisions (D1–D9) with a recommendation each. Items were added to the living
next list as N-028 … N-057. The user accepted all recommendations.

## 3. How the work was done

Opus 5.5 agents, each in its own git worktree, one commit per item, failing
test first. Each report was reviewed diff by diff, cherry-picked onto the
branch, and the full `go test -race ./...` run on the combined result. Items
were grouped so that agents running in parallel did not edit the same
functions; the read-loop items ran in sequence in one agent, and N-039 waited
for them. Docs-only cherry-pick conflicts (`SKILL.md`, `CLAUDE.md`) were
resolved by keeping both sides. N-058 was done directly.

Agent worktrees were created from `9781e96`, not the current HEAD, so later
briefs told each agent to fast-forward to the intended commit first.

After Phase 2 the original reproductions were re-run against the branch; every
one shows the fixed behaviour.

## 4. What changed

### Phase 1 — correctness

| Item | Commit | Change |
|---|---|---|
| N-030 | `eeb9004` | Sub-groups get their own middleware slice (`slices.Concat`); `Server.Group` clones the variadic slice |
| N-029 | `af2afcf` | Group middleware continues only through `Next()` (D1) |
| N-028 | `0812f95` | Scheme/host only from an absolute-form target, before `?`; a schemeless `evil.com/admin` is no longer split; `http://example.com` parses |
| N-031 | `2a35db0` | CR/LF/NUL in header values → space; invalid header names dropped (logged) |
| N-032 | `4b7d319` | `OnDisconnect` outside the hub lock; `Close` via `sync.Once` |
| N-033 | `db5bce9` | `writeSSEEvent` / `sseDataText`; one `data:` line per payload line; `rweb.SSEClose` |
| N-034 | `ee2b87c` | 1xx/204/304 sent as the header block alone |
| N-058 | `c6a1ecd` | Group middleware now runs for `g.StaticFiles`, `g.StaticFilesAbs`, `g.Proxy` (found while auditing N-029) |

### Phase 2 — connection hardening

| Item | Commit(s) | Change |
|---|---|---|
| N-041 | `863d3b4` | `Clean` releases buffers over 256 KB |
| N-040 | `009122c`…`15d0cd0` | WebSocket: same-origin check (403), 30 s pings + liveness read deadline, 10 s write timeout, atomic `closed`, control-frame validation, `Subprotocols`, no `[ERR]` on normal disconnect, `Close` releases its lock before waiting (D5) |
| N-035 | `28f214c` | `MaxHeaderBytes` 1 MB (431), `ReadHeaderTimeout` 30 s (408), `IdleTimeout` 120 s; `ReadTimeout`/`WriteTimeout` off by default; SSE per-flush timeout 30 s; per-write deadline in 1 MB slices (D2). `conn_limits.go` |
| N-036 | `dcef51d` | Ambiguous framing refused with 400 / 501; chunk extensions and trailers handled within limits (D3). `request_framing.go` |
| N-037 | `6b6513b` | `100 Continue` just before the body is read; 417 for other expectations |
| N-038 | `2e0407d` | `Connection: close` and HTTP/1.0 keep-alive rules (D4) |
| N-039 | `9b57685` | `Serve`, `Shutdown`, `ErrServerClosed`, `ShutdownTimeout`; `Run` calls `signal.Stop`; connection registry; accept backoff; `RunWithHttpsRedirect` shuts both servers down. `shutdown.go` |

Also fixed on the way: `sendSSE` kept writing to a dead connection after a
write error on an event over 4 KB; the stale CLAUDE.md note "SSE tests need
fixing" was removed (all pass under `-race`, none skipped).

### Behaviour changes an app may notice

- A header line with no colon, a folded line, or a bare-LF end of head is 400.
- Limits are on by default (1 MB head, 30 s head read, 120 s idle).
- WebSocket: cross-origin handshakes get 403, including a manual
  `ctx.UpgradeWebSocket()`. The check compares against `Host()`, so behind a
  proxy that rewrites `Host` the public origin must be in `AllowedOrigins`.
  A peer silent for 40 s times out. Offered subprotocols are no longer echoed
  unless listed in `WSOptions.Subprotocols`. `ws.Done()` also closes when the
  handler returns.
- SSE: bare structs and maps go out as JSON, not `%+v`.
- `Run` returns an error wrapping `context.DeadlineExceeded` when the shutdown
  timeout runs out (it used to return nil always).
- A SIGTERM with no `Run` listening now kills the process; tests should use
  `Serve` / `Shutdown`.
- Group middleware that relied on auto-continue must call `Next()`.

## 5. Verification

- `go build ./...`, `go vet ./...`, `gofmt -l`: clean. Example modules build.
- `go test -race -count=1 ./...`: passing, three consecutive full runs at the
  end (root package about 12.1 s; it was about 8.3 s at the start).
- Agent benchmark (`keepalive_bench_test.go`, one header): 4 allocs/op before
  and after Phase 2.
- **Not run: staticcheck.** The installed binary predates Go 1.24 and the
  cached v0.8.1 needs Go 1.26 (machine has 1.25.4). Raised as N-061.
- Not checked with a real browser or `wscat` (N-062). Accept backoff tested
  with a fake listener only.

## 6. Files of note

- New: `shutdown.go`, `conn_limits.go`, `request_framing.go`, and tests
  `graceful_shutdown_test.go`, `conn_limits_test.go`, `request_framing_test.go`,
  `expect_continue_test.go`, `connection_close_test.go`,
  `websocket_hardening_test.go`, `buffer_retention_test.go`,
  `bodyless_status_test.go`, `header_injection_test.go`,
  `response_header_test.go`, `keepalive_bench_test.go`.
- Docs updated alongside: `ai_docs/SKILL.md`, `README.md`, `CLAUDE.md`,
  `architecture/sse/sse.md`.

## Next

Closed: N-028, N-029, N-030, N-031, N-032, N-033, N-034, N-035, N-036, N-037,
N-038, N-039, N-040, N-041, N-058. Declined: None.
Raised: N-028 … N-057 (the review), N-058, N-059, N-060, N-061 (Validate),
N-062 (Validate). Deferred: N-056, N-057 (raised straight into Roadmap).
Promoted: None. Moved: None. Updated: N-043, N-053.
Full list: `ai_docs/todo/next-list.md`.

Reopened in substance by new items, with the old Non-goals lines left in
place: N-012 (header limits → N-035, closed), N-013 (`Connection: close` →
N-038, closed), N-011 (streaming proxy → N-047, open).

Next up: Phase 3 (N-042 … N-047, performance), starting with N-042, which adds
the benchmark file the phase is judged by; then Phase 4 (N-048 … N-055).
