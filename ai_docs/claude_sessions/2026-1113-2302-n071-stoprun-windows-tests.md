# N-071: stop Run with Shutdown, not SIGTERM; tests build on Windows

Session id: `b9e1ccc8-21e2-4f2b-b939-dc5fc597b9ed` (the same session as
`2026-1111-1814-n063-n069-n067-n071-hardening`, continued)
Date: 2026-11-13 · Branch: `roh/once-over-fab5.2` · Start `9b44ab6` → end `9bd8452` (+ this doc)

## What was asked

1. "Go with option a for N-071": replace the test suite's SIGTERM stops with a direct `Shutdown`.
2. `/sw`.

## 1. What changed (`9bd8452`)

**The helper** is `stopRun(servers ...*rweb.Server)`, in the new `run_stop_test.go` (package `rweb_test`). It calls `Shutdown` on each server under one 10 s context and ignores the error, as these tests ignored `Run`'s. The comment there has a two-goroutine diagram and explains the points below.

**Why `Shutdown` ends `Run`** (`runUntilSignal`, `Server.go`):
- `Shutdown` closes the listener, and `serve` returns `ErrServerClosed`.
- `Run` takes that to mean a `Shutdown` called directly is in charge of draining, so it returns at once.

**One change in timing:**
- With SIGTERM, `Run` drained the server before it returned.
- Now `Run` may return while `stopRun` is still draining.
- A test that needs the server fully drained must wait for `stopRun`, not for `Run`.
- The errors go nowhere near `t`, because the test function may have returned by then.

**A `Shutdown` right after `ReadyChan` cannot be undone.** `beginServing` runs synchronously before the `serve` goroutine, which fires `ReadyChan` only after tracking the listener, and `beginServing` skips its re-arm once a `Shutdown` has begun. I checked this in `shutdown.go`.

**The rewrite:**
- I made the replacement with a script and asserted that no `syscall.Kill` was left in the 13 files. It counted 42 sites; the earlier "43" was a miscount.
- The call took four forms, all now `stopRun(s)`:
  - `defer syscall.Kill(...)`
  - a one-line deferred closure
  - a three-line deferred closure
  - a bare statement, as in sse_test.go's defer blocks and at the end of `TestSSEClientDisconnect`
- Each file's `"syscall"` import was dropped; none of the 13 used it for anything else.

**Special cases:**
- `proxy_test.go` runs three servers with `Run`: two upstreams and the proxy. One SIGTERM used to stop all three, so it now calls `stopRun(pxy, usTgt, euTgt)`. The same goes for the commented-out `TestProxySkewed`, so it stays correct if it is ever restored.
- In `sse_test.go`, the four tests that run `Run` in a goroutine already waited on `serverDone` after the stop. The comment about Run's SIGTERM handler now says that `Shutdown` makes `Run` return.

**CLAUDE.md:** the shutdown.go bullet now says that a test using `Run` stops it with `stopRun`, never a signal. It also says that only the unix-tagged `graceful_shutdown_signal_test.go` sends SIGTERM, and how to check the Windows build.

## 2. Verification

- `gofmt -l`: clean.
- `go vet ./...`: passes on darwin, `GOOS=linux` and `GOOS=windows`.
- `GOOS=windows go test -c`: builds.
- staticcheck v0.7.0: clean.
- `go test -race -count=1 ./...`: passing in all four packages.
- `go test -race -count=5 .` (root package, about 72 s): passing, so the new shutdown timing shows no flakes.
- **Not run:** the tests on an actual Windows machine. Only the build is checked. Some tests may still assume Unix behaviour, such as the `:8080` port in `TestUnavailablePort` or loopback and socket details.

## Next

Closed: N-071. Declined: None. Raised: None. Deferred: None. Promoted: None.
Moved: None. Updated: None. Full list: `ai_docs/todo/next-list.md`.

Next up: Phase 4, starting with N-048. It adds an error-handler setter, not-found and method-not-allowed hooks, and status text on the default error page, which today reads "502 Internal Server Error" for the proxy. Then N-049, percent-decoding path params (decision D7).
