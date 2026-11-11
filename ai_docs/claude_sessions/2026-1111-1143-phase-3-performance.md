# Once-over review, Phase 3 (performance)

Session id: `8941df35-3075-4b33-99c9-96b3a5c14f9a`
Date: 2026-11-11 · Branch: `roh/once-over-fab5.2` · Start `371ce8c` → end `35f6c59` (+ this doc)

## What was asked

1. `/sl`: load the last session doc and the next-list.
2. "Continue with phase 3" (N-042 … N-047 in
   `ai_docs/todo/PLAN_ONCE_OVER_2026-1109.md`).
3. `/sw`.

## 1. How the work was done

The method was the same as the previous session's:
- Opus 5.5 agents worked in their own git worktrees, one commit per item, with a failing test or a benchmark baseline first.
- I read each diff, then cherry-picked it onto the branch.
- `gofmt`, `go vet`, staticcheck and `go test -race -count=1 ./...` were run on the combined branch after each batch.

The agents were split so that no two edited the same functions:

| Agent | Items | Area |
|---|---|---|
| A | N-042, then N-044 | `writeResponse`, the head-reading loop in `serveConn` |
| B | N-043 | `websocket.go` |
| C | N-045, then N-047 | `staticFiles`, `proxy` (and new `proxy.go`) |

Each brief told the agent to `git reset --hard 371ce8c` first. That was the
lesson of the last session: worktrees start from an older commit. The
cherry-picks applied without conflicts, docs included.

Before spawning, I got staticcheck working again (N-061):
- The binary on PATH (`~/bin/staticcheck`, 2025.1.1) was built with go1.23.2 and refuses this go1.24 module.
- The cached v0.8.x needs Go 1.26.
- v0.7.0 (2026.1) needs Go 1.25 and builds with the machine's 1.25.4.
- I installed it into the session scratchpad with `GOBIN=<scratchpad>/bin go install honnef.co/go/tools/cmd/staticcheck@v0.7.0`.
- It is clean at `371ce8c`, which covers the 26 commits of the previous session, and after every Phase 3 commit.

All three agents benchmarked at the same time on one machine. They
interleaved old and new binaries and compared medians. Agent A also added a
`cpu-ns/op` metric (getrusage user+sys per request), which a busy machine
disturbs far less than round-trip time.

## 2. Decision: N-046 folded into N-057

The plan's step 1 for N-046 was to look the route up before reading the body
and skip the body on a miss. Reading the dispatch code turned up a catch the
plan had missed:
- Global middleware (`s.Use`) runs *before* the router, because the router is the last handler in `s.handlers`.
- So middleware sees unrouted requests and can answer them: logging, custom 404 pages, a middleware-served path.
- Skipping the body on a miss would hand such middleware an empty `Body()`.
- Every routed POST already accepts bodies up to the 100 MB server-wide cap, so the miss path alone protects only against scanners.

I offered four options:
- fold into N-057
- skip the body on a miss
- read the body lazily on a miss only
- skip only when there is no `s.Use` middleware

The user chose **fold into N-057**, the lazy request body with per-route caps.

## 3. What changed

| Item | Commit(s) | Change |
|---|---|---|
| N-045 | `ff9e98b` | `staticFiles` resolves the kept route tokens, the absolute root and the base once, at registration. Only paths are cached; `os.Root` is still opened per request. A relative root no longer follows a later `os.Chdir` (documented) |
| N-047 | `f5581c2` | New `proxy.go`, details below |
| N-043 | `5fb94b0` | WebSocket concurrency, details below |
| N-043 | `0f71fda` | WebSocket reads, details below |
| N-042 | `7ba428b` | `writeResponse` appends an in-memory body of up to `coalesceBodyMax` (8 KB) to the header block and writes once, details below |
| — | `35f6c59` | next-list and plan updates |

**N-047 (proxy):**
- **Client:** one per server, used by every `Proxy` route:
  - Transport cloned from `DefaultTransport`, with 32 idle conns per host and compression off.
  - `CheckRedirect` returns `ErrUseLastResponse`, so a 3xx goes back to the client.
  - The wait for the upstream's headers is bounded by the new `ProxyResponseHeaderTimeout` / `WithProxyResponseHeaderTimeout` (default 60 s).
- **Headers:**
  - Hop-by-hop headers are dropped both ways, and `Expect` is dropped upstream.
  - Request headers are added rather than set, so repeated lines survive.
  - `X-Forwarded-For` is appended to; `X-Forwarded-Proto` and `-Host` are set.
- **Body:** streamed via `Stream(ctx, resp.Body, resp.ContentLength)`.
- **Errors:** 504 on timeout and 502 on any other upstream failure (was 500).
- **Shutdown:** closes the client's idle conns.

**N-043, concurrency (`5fb94b0`):**
- Frame reading is single-owner through `readSem`.
- `Close` from another goroutine during `ReadMessage` lets that call read the peer's close frame (`closeRcvd`), so there is never a second reader.
- `handlersMu` guards `SetPingHandler`, `SetPongHandler` and `OnClose`.
- An `OnClose` handler that calls `Close` no longer deadlocks.
- A 64-bit frame length with the top bit set no longer panics.

**N-043, reads (`0f71fda`):**
- A lazily made 4 KB `bufio.Reader` sits above the liveness reader.
- The header is parsed from a fixed `[14]byte`.
- Unmasking is word-wise (`maskBytes`, no `unsafe`).

**N-042 (`7ba428b`):**
- HEAD, 1xx/204/304, SSE, chunked bodies, HTTP/1.0 unframed bodies and bodies over the cap are written as before.
- A sized stream after the in-memory part is unchanged and still goes by sendfile.
- Adds `bench_test.go`, the phase's benchmark file.

### Results (agents' interleaved runs, medians, Apple M3)

| Benchmark | Change |
|---|---|
| KeepAlive hello / JSON / browser-like / 8 KB body (plain TCP) | −6.1 / −8.3 / −8.4 / −4.9 % ns/op |
| The same on TLS | −6.6 / −4.3 / −4.1 / −0.5 % |
| WSEcho 1 KB / 64 KB / burst of 64 small messages | −21 / −40 / −48 %; 4–5 → 2 allocs per message |
| Static file, relative root (synthetic / keep-alive) | −27 / −15 %; allocs 44 → 22, 41 → 19 |
| Static file, absolute root | −4 / −2 % |

My own end-to-end check on a quiet machine: `BenchmarkKeepAliveHello`,
`371ce8c` against the branch head, 8 alternating runs.
- 14.5 → 13.8 µs/op (−4.8 %)
- 4 allocs/op and 37 B/op both before and after

### Deviations from the plan

- **N-042 cap 8 KB, not 64 KB.** On plain TCP, coalescing lost 6–25 % from 16 KB bodies up. TLS was flat there.
- **N-044 dropped.** Agent A built it and it passed every test:
  - Design: the head is gathered in a pooled buffer and becomes one string, with keys and values as substrings.
  - Allocations fell 12 → 2 per browser-like request.
  - Time on one connection moved −1.4 % to +3.9 % across two runs, under the plan's 5 % bar.
  - I checked whether GC pressure would show under concurrency, using a throwaway `RunParallel` benchmark with 64 connections. Result: 4625 → 4630 ns/op, CPU per request unchanged (~27.3 µs), so no.
  - Recorded under Non-goals.
- Agent C chose the proxy's 60 s header timeout (nginx's `proxy_read_timeout`) and the 502/504 split; both are commented at the code.

### Behaviour changes an app may notice

- **Proxy:**
  - 3xx is passed to the client, not followed.
  - Upstream failure is 502 (was 500); a header timeout is 504.
  - Hop-by-hop headers are dropped; `X-Forwarded-*` is set, and a client-sent `X-Forwarded-Proto` or `-Host` is replaced.
  - The response body is streamed.
  - An upstream that stalls before headers fails after 60 s by default.
- **Static files:** a relative root is resolved at registration. If `os.Getwd` fails then, the route is not registered.
- **WebSocket:**
  - Buffered messages are returned even after a read deadline passes.
  - 4 KB read buffer per connection.
  - `Close` from an `OnClose` handler sets the reply code.

## 4. Verification

- `gofmt -l`, `go vet`, staticcheck v0.7.0: clean.
- `go test -race -count=1 ./...`: passing after each batch and at the end.
- WebSocket and proxy tests: `-race -count=3` passing.
- Agent runs:
  - Agent B ran the WebSocket tests 5×.
  - Agent C ran the proxy tests 10×.
  - Agent A ran the coalescing tests 20×.
  - No flakes.
- The example modules `proxy_server` and `hello` build (agent C).
- **Not run:** a real browser or `wscat` against the WebSocket changes (N-062, still open). TLS was benchmarked only on loopback.

## 5. Files of note

- New:
  - `proxy.go`
  - `bench_test.go`, `bench_cpu_unix_test.go`, `bench_cpu_other_test.go`
  - `write_coalesce_test.go`
  - `static_bench_test.go`
  - `proxy_client_test.go`
  - `websocket_bench_test.go`, `websocket_concurrency_test.go`, `websocket_read_test.go`
- `keepalive_bench_test.go` is now a thin wrapper on `benchKeepAlive`.
- `testCert` takes a `testing.TB`.
- Docs updated alongside: `ai_docs/SKILL.md`, `README.md`, `CLAUDE.md` (`proxy.go` entry, Reverse Proxy, Graceful shutdown, WebSockets bullets).
- The plan has "As done / Decided" notes under N-042, N-044 and N-046.
- Housekeeping, not in the repo:
  - The three agent worktrees are still under `.claude/worktrees/`. All their commits are on the branch.
  - `~/bin/staticcheck` still needs reinstalling as v0.7.0 to make N-061's fix permanent.

## Next

Closed: N-042, N-043, N-045, N-046 (merged into N-057), N-047, N-061.
Declined: N-044. Raised: N-063, N-064, N-065, N-066, N-067, N-068, N-069,
N-070, N-071. Deferred: None. Promoted: None. Moved: None.
Updated: N-048, N-055, N-057. Full list: `ai_docs/todo/next-list.md`.

Next up: N-063 is the item to do first. A bare-LF request line with no version
mis-routes (`GET /xy\n` → `/x`) or panics (`GET \n`), and any client can
trigger it. After that, N-069 (a pooled header slice can keep about 8 MB).
Then Phase 4 (N-048 … N-055, convenience), whose decisions D7, D8 and D9 were
accepted with the plan.
