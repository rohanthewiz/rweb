# Session: Fix Request.Host() ignoring the Host header

**Session ID:** `2503fd52-2d86-475c-98a2-5d010e9d3776`
**Date:** 2026-09-19
**Branch:** master
**Origin:** the session ran in `~/rocfg/rosync` (a separate repo); this fix was a
side trip into rweb at the user's request.

## Ask

While building `rosync serve` — a loopback-only rweb app that can rewrite files
under `$HOME` — a DNS-rebinding guard compared `ctx.Request().Host()` against
`127.0.0.1:<port>` and rejected every request, valid ones included. The user
asked: "Go ahead and make the fix to rweb for me and do a sess-wrap there."

## Diagnosis

The first diagnosis, made from the rosync side, was **wrong**: a grep for
`.host =` found nothing, so `Host()` was reported as "never populated, always
returns an empty string". The grep missed the assignment because `request` is
embedded in `context` and the field is set as `ctx.host`, in
`Server.handleRequest`:

```go
ctx.scheme, ctx.host, ctx.path, ctx.query = parseURL(url, s.options.URLOptions)
```

The actual behaviour, confirmed by a failing test before any change:

| Request | `Host()` returned |
| --- | --- |
| `GET http://example.com/x` (absolute-form target) | `example.com` — correct |
| `GET /x` with `Host: example.com` (origin-form; what browsers send) | `localhost` |
| `GET /x` with `Host: 127.0.0.1:7420` | `localhost` |

`parseURL` takes the host only from the request target, and defaulted an empty
one to `consts.Localhost`. The `Host` header was never consulted. Since nearly
every real request is origin-form, `Host()` said `"localhost"` regardless of
what the client asked for.

This is worse than returning nothing. A loopback server defends against DNS
rebinding by noticing a Host that is *not* localhost; a `Host()` that always
answers "localhost" makes that check pass for an attacker's hostname. It also
made `Host()` useless for virtual hosting.

The existing `TestRequest` did not catch it because it only exercises the
absolute-form case (`s.Request(GET, "http://example.com/request?x=1", ...)`).

## Fix

Precedence follows RFC 9112 §3.2.2:

```
absolute-form target  "GET http://example.com/x"  -> host from the target; Host header ignored
origin-form target    "GET /x"                     -> the Host header
neither                                            -> "localhost"
```

- **`http.go` — `parseURL`**: no longer defaults an empty host to `localhost`.
  It returns `""` when the URL names no host, so the caller can tell "the
  request line said localhost" from "it said nothing". `parseURL` has a single
  caller, so this changes no other behaviour.
- **`Server.go` — `handleRequest`**: after `parseURL`, an empty `ctx.host` is
  filled from `ctx.request.Header(consts.HeaderHost)`, and only then falls back
  to `consts.Localhost`. Request headers are already parsed by this point. The
  value is kept exactly as sent, port included.
- **`Request.go`**: `Host()` doc comment now states the precedence.

`Header()` matches names with `strings.EqualFold`, so `host:` and `Host:` both
work.

## Verification

- New `TestRequestHost` (`Request_test.go`), table-driven, five cases:
  origin-form uses the header; port kept as sent; header name case-insensitive;
  absolute-form wins over a conflicting header; no host anywhere → `localhost`.
- Confirmed the test guards the right thing: with the three non-test files
  stashed it fails on the three origin-form cases, each reporting
  `Host() = "localhost"`; with the fix restored it passes.
- `go test ./...` passes (root package, `core/rtr`, `middleware/critical`,
  `middleware/stylus`). `go vet ./...` clean.

## Files Changed

| File | Change |
| --- | --- |
| `Server.go` | resolve effective host in `handleRequest`, with the precedence diagram as a comment |
| `http.go` | `parseURL` leaves host empty instead of defaulting it |
| `Request.go` | `Host()` doc comment |
| `Request_test.go` | `TestRequestHost` |

## Behavioural Change to Note

Code that relied on `Host()` being `"localhost"` for ordinary requests will now
see the real Host header (`"localhost:8080"`, `"example.com"`, …). That reliance
would have been accidental, but it is a visible change and worth a line in the
release notes.

## Downstream

`rosync` (`internal/web/server.go`, `guard`) works around the old behaviour by
reading `req.Header("Host")` directly. Its comment there repeats the wrong
"never populated" diagnosis and should be corrected; once rweb is tagged and
rosync bumps to it, the guard can use `req.Host()`.

## Next

- Tag a release (v0.1.29) so downstream modules can pick this up; `go.mod`
  consumers are pinned to v0.1.28, which has the bug.
- `Request.Scheme()` has the same shape of problem: `parseURL` only finds a
  scheme in an absolute-form target, so for origin-form requests it is `""`.
  It could be derived from whether the connection is TLS. Not touched here.
- The Host header is taken as sent and not validated: no check for multiple
  `Host` headers (RFC 9112 says respond 400) or for a malformed value. Callers
  comparing against an allow-list are unaffected; anything echoing it is not.
- `gofmt -l .` lists 11 pre-existing unformatted files (`Context_test.go`,
  `Cookie.go`, `Group.go`, `context_data_test.go`, `core/rtr/*.go`,
  `group_test.go`, `websocket.go`). None were touched this session.
- Carried from 2026-0701-2109, deliberate non-goals for now: streaming static
  files / proxy responses, header count/size limits, `Connection: close`
  handling. (That doc's other item — its changes being uncommitted — was
  resolved by `dfc4d7a`.)
