# Session: HEAD falls back to GET, 501 for unknown methods, skill re-sync

Session ID: `c9d1da3e-9450-4651-99d5-501edd4993ea`
Date: 2026-10-10

Three next-list items from the cats-todo backlog: N-021 (installed skill copy
lagging `ai_docs/SKILL.md`), N-022 (unknown method panics in dispatch) and
N-023 (HEAD not implied by GET).

## N-021: installed skill re-sync

- `diff` showed `ai_docs/SKILL.md` was a strict superset of
  `~/.claude/skills/rweb-light-go-webserver/SKILL.md`: the only line differing
  on the installed side was the `WriteMessage` comment, which the repo had
  since updated. Missing from the installed copy: 404 vs 405, Encrypted
  Cookies, `StaticContainSymlinks`, WebSocket compression and
  `WriteMessages`, the v0.1.31 `Scheme()`/`Host` notes, Request Context.
- Replaced the installed file with a symlink to
  `~/projs/go/rweb/ai_docs/SKILL.md` (link, not copy, so it cannot drift
  again). `cmp` identical; Claude Code reloaded the skill through the link.
  Machine-local: another machine/checkout needs the same `ln -s`.
- Committed as `c419b4b` (next-list only; the link lives outside the repo).

## N-022: unknown methods

Premise re-checked with a throwaway probe test before changing anything:

| Path | Before |
|---|---|
| synthetic `s.Request("FOO", "/p/1")` | nil-tree panic, recovered → 500 + `[ERR]` stack |
| synthetic `s.Request("GOT", "/x")` | **200**, served by the GET route |
| wire `FOO /x HTTP/1.1` | `BAD-METHOD / HTTP/1.1\r\n` — a request line, not a response |

So the panic was synthetic-only. The wire path already rejected the method via
`isValidRequestMethod` but wrote `consts.HTTPBadMethod`, and
`TestBadRequestMethod` sent that same string and expected it echoed back, so it
always passed. Also new: both routers' GET fast path was `method[0] == 'G'`.

Fixes:
- `consts.HTTPNotImplemented` (`501`, `Content-Length: 0`,
  `Connection: close`); the wire path writes it. `HTTPBadMethod` kept, now
  documented as a test request line.
- Default handler: `!isValidRequestMethod` → 501, checked only on the hash-miss
  path so exact-route hits pay nothing (catches synthetic requests).
- `RadixRouter.Lookup`/`LookupNoAlloc`: nil tree → zero value. `Add` still
  dereferences (registering an unservable method is a startup-time
  programming error; comment says so).
- GET fast path in `RadixRouter` and `HashRouter` is `method ==
  consts.MethodGet`. `BenchmarkGitHub` before/after within noise (~7.0 vs 7.3
  ns Param0, same for Param1/2).

## N-023: HEAD → GET fallback

- Default handler: if no handler and method is HEAD,
  `s.lookupGetForHead(ctx)` — hash then radix for GET. It first truncates
  `ctx.request.params`: a failed HEAD radix lookup can already have recorded a
  `:param` (the tree adds a param when its segment is consumed, before knowing
  the rest matches), which would leak into the GET handler.
- `allowedMethods` adds HEAD when GET is routed (GET is probed first in
  `routableMethods`). 405 `Allow` values change, e.g. `GET, HEAD, POST`.
- `writeResponse`: HEAD + `sseEventsChan` → headers only, run `sseCleanup`,
  return (no stream on a HEAD; conn loop closes after any SSE response).
- `IsWebSocketUpgrade` false for HEAD (RFC 6455 handshake is GET), so a
  WebSocket route never upgrades on HEAD.
- Body drop for HEAD and `Content-Length` were already in `writeResponse`.

## Tests

- New `head_and_unknown_method_test.go`: synthetic 501 (incl. `GOT`), wire 501
  via `net/http` (`PROPFIND`), HEAD fallback (method, params, explicit HEAD
  wins, POST-only still 405, 404), failed-HEAD-param drop, HEAD-then-GET on
  one keep-alive conn (Content-Length, no body leak), HEAD to SSE (headers,
  close, event unconsumed), HEAD to WebSocket (no 101).
- `core/rtr/RadRouter_test.go`: `TestUnknownMethod`,
  `TestHashRouterUnknownMethod` (`FOO`, `GOT`, `G`, `""`).
- Updated: `method_not_allowed_test.go` Allow values and HEAD case;
  `TestBadRequestMethod` expects `HTTPNotImplemented`.
- Unknown-method test confirmed failing on master (3 nil-tree panics).
- `go test ./...` all ok; new tests pass under `-race`.

## Docs

- `ai_docs/SKILL.md`: "404 vs 405" → "404, 405 and 501"; new "HEAD (v0.2.1+)"
  section. v0.2.1 is not tagged yet (latest `v0.2.0`), so this ships with the
  405/cookie work.

## Files

`Server.go`, `Context.go`, `consts/http.go`, `core/rtr/RadRouter.go`,
`core/rtr/HashRouter.go`, `Server_test.go`, `method_not_allowed_test.go`,
`core/rtr/RadRouter_test.go`, `head_and_unknown_method_test.go` (new),
`ai_docs/SKILL.md`, `ai_docs/todo/next-list.md`.

## Next

Closed: N-021, N-022, N-023. Declined: None. Raised: N-025.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
