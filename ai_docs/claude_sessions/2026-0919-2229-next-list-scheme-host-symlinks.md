# Session: Work the Next list — Scheme(), Host validation, symlink containment, gofmt

**Session ID:** `13988b8d-5d08-4862-a6c3-31b28e717f02`
**Date:** 2026-09-19
**Branch:** master

## Ask

"Do what's in the Next list except what is already done or the static file
streaming." There is no living list (`ai_docs/todo/next-list.md`), so the Next
section of `2026-0919-2153-static-files-abs` was the source.

## Triage of the list

| Item | Outcome |
|---|---|
| Tag release v0.1.30 | Already done — tag exists locally and on origin at `a70d3c2`. Skipped. |
| Symlinks not resolved before the containment check | Done, as an opt-in (below). |
| Static files read fully into memory | Excluded by the user. |
| `Request.Scheme()` is `""` for origin-form requests | Done. |
| Host header not validated | Done. |
| `gofmt -l .` lists 11 files | Done. |
| Non-goals (streaming, header limits, `Connection: close`) | Left as non-goals. |

## Finding: `Clean()` dropped `ctx.conn` between keep-alive requests

Not on the list; found while working out how `Scheme()` could see the
connection. `handleConnection` sets `ctx.conn = conn` once, before its request
loop. `Context.Clean()` ends with `ctx.conn = nil` — correct when the context
returns to the pool, but the loop also calls `Clean()` after every request. So
from the second request on a keep-alive connection, `ctx.conn` was nil:
`GetConn()` returned nil and `ClientIP()` lost its `RemoteAddr` fallback. The
WebSocket upgrade path and the SSE disconnect detection read the same field, so
they were presumably affected as well; that was not tested.

Fix: the loop restores `ctx.conn = conn` right after `Clean()`. `Clean()` itself
is unchanged.

## Changes

### `Request.Scheme()` — derived from the transport

In `handleRequest`, after the host is resolved: if `parseURL` found no scheme
(every origin-form request), it is `https` when `ctx.conn` is a `*tls.Conn`,
else `http`. A synthetic `s.Request()` has no connection and reports `http`,
matching its `localhost` host.

`X-Forwarded-Proto` / `Forwarded` are deliberately not consulted: they are
client-controlled unless a trusted proxy overwrites them, and the server cannot
know whether one does. Behind a TLS-terminating proxy `Scheme()` is `http`.

### Host header validation

In the header-reading loop of `handleConnection`: a second `Host` header
(case-insensitive, even an identical one) or a value that fails
`isValidHostHeader` gets `400` and the connection is closed, per RFC 9112 §3.2.
The point beyond conformance: `Header()` returns the first match while a proxy
in front may act on the last, so two Host lines show each hop a different host.

`isValidHostHeader` (`http.go`) is an allocation-free character-class check of
`uri-host [":" port]`: reg-name alphabet, digit-only port (empty allowed),
bracketed IP-literals with hex / `:` / `.` only (no zone IDs). Empty is legal.

**A missing Host is still tolerated**, although the RFC wants 400 for that too —
hand-rolled clients and several existing raw-socket tests omit it, and an absent
header cannot smuggle anything. This is a judgment call left for the user to
confirm.

Validation happens on the wire path only; synthetic `s.Request()` bypasses it.

### Symlink containment — opt-in

Decision: keep following symlinks by default (nginx and `net/http.FileServer` do
the same; operators commonly link assets in, and changing the default would
silently break them). New `ServerOptions.StaticContainSymlinks` /
`WithStaticContainSymlinks()` (also copied in `WithOptions`): when set,
`staticFiles` runs `filepath.EvalSymlinks` on both the root and the candidate
and repeats the `filepath.Rel` containment test on the resolved paths. Resolving
the root too means a root that is itself a symlink still serves. Unresolvable →
404.

Known limit, stated in the code comment: a TOCTOU window remains between
`EvalSymlinks` and `os.ReadFile`. Closing it needs `os.Root` (Go 1.24+); `go.mod`
says `go 1.23.0`.

### gofmt

`gofmt -w` on the 11 listed files. `git diff -w` confirmed the only
non-whitespace changes are gofmt's own (a one-line struct expanded in
`Context_test.go`, `//` lines in doc comments in `core/rtr`, a blank line in
`Cookie.go`). `gofmt -l .` is now empty.

### Docs

`ai_docs/SKILL.md`: `req.Scheme()` in the request cheat-sheet and a paragraph on
its semantics; Host validation; the symlink option under StaticFilesAbs. All
labelled v0.1.31+, which is not yet tagged.

## Verification

- `go test -count=1 ./...` passes; `go vet .` clean; `gofmt -l .` empty.
- New tests:
  - `http_host_test.go` (internal package) — table test of `isValidHostHeader`.
  - `Request_scheme_host_test.go` — synthetic scheme; plain-TCP scheme plus two
    requests on one connection asserting `GetConn() != nil` both times; scheme
    over a TLS listener; Host rejection cases over raw connections.
  - `StaticFilesAbs_test.go` — default still follows an escaping link (pinned);
    with the option, the escaping link 404s while an inside link and a symlinked
    root serve.
- The keep-alive test was run with the `ctx.conn = conn` restore commented out:
  it failed (`conn=false` on the second request), then passed with it restored.

## Files Changed

- `Server.go` — conn restore after `Clean()`; scheme derivation; Host check in
  the header loop; `StaticContainSymlinks` option, `WithStaticContainSymlinks`,
  `WithOptions` copy; symlink containment in `staticFiles`
- `http.go` — `isValidHostHeader`
- `Request.go` — `Scheme()` doc comment
- `http_host_test.go`, `Request_scheme_host_test.go` — new
- `StaticFilesAbs_test.go` — symlink fixture and two tests
- `ai_docs/SKILL.md`
- gofmt only: `Context_test.go`, `Cookie.go`, `Group.go`, `context_data_test.go`,
  `core/rtr/{HashRouter,Parameter,Tree,flow,treeNode}.go`, `group_test.go`,
  `websocket.go`

## Next

- Tag a release (v0.1.31) — `ai_docs/SKILL.md` already labels `Scheme()`, Host
  validation and `StaticContainSymlinks` as v0.1.31+.
- Decide whether a request with **no** Host header should get 400 (RFC 9112
  says so for HTTP/1.1). Currently tolerated; rejecting it would break
  hand-rolled clients and some existing raw-socket tests.
- The keep-alive `ctx.conn` fix was verified through `GetConn()` only. A
  WebSocket upgrade and an SSE stream on a *reused* connection are untested.
- `StaticContainSymlinks` leaves a check-to-read race; closing it needs
  `os.Root`, i.e. raising the `go` directive to 1.24.
- Host validation does not cover synthetic `s.Request()` calls; only the wire
  path is checked.
- Static files are read fully into memory per request (`os.ReadFile`) — carried;
  the user excluded it from this session.
- Closed since 2026-0919-2153: `Request.Scheme()` for origin-form requests; Host
  header validation (multiple / malformed); symlink containment decision; gofmt
  of the 11 files. "Tag v0.1.30" was already done.
- Deliberate non-goals, carried: streaming proxy responses, header count/size
  limits, `Connection: close` handling.
