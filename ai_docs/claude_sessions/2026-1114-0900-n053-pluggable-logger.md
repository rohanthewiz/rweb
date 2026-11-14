# Phase 4: N-053, a pluggable Logger defaulting to rohanthewiz/logger

Session id: `7a244f89-d4cb-4e3d-a3cb-fd039205c24d`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `0fc7bea` → end `77055e5` (+ this doc)
Plan: `ai_docs/todo/PLAN_ONCE_OVER_2026-1109.md`, Phase 4, N-053.

## What was asked

1. "Do next-list item N-53. Use rohanthewiz/logger". Library code printed to
   stdout through about sixty `fmt.Print*` / `log.Printf` calls, several
   unconditional, including the dropped-header-name line N-031 added.
2. "yes" to committing, closing the item and `/sess-wrap`.

Checks run before committing: `go test -race -count=1 ./...`, `go vet ./...`,
`GOOS=windows go vet ./...`, `gofmt -l`. All clean. staticcheck was not run.

## N-053 (`9a6cad0`)

### Design

- **`Logger` interface** (`logging.go`): `Debug`/`Info`/`Warn`/`Error(msg
  string, keyVals ...any)`. The plan suggested a `Printf`-shaped one; the
  leveled key-value shape was chosen instead because it is what
  rohanthewiz/logger's package functions take and exactly the method set of
  `*slog.Logger`, which therefore plugs in with no adapter (compile-time
  check in the tests). `*log.Logger` does not fit; `slog` covers that need.
- **Default:** `rohanLogger{}`, an empty struct giving rohanthewiz/logger's
  package functions (a global logrus logger) a method set. rweb never calls
  `InitLog` or changes the level: that state is process-wide and the app's.
- **serr:** the adapter's `Error` looks for an error value carrying a
  `*serr.SErr` (`errors.As`) and sends the line through `logger.LogErr`, so a
  handler's `serr.Wrap(err, "userId", id)` reaches the log as fields. LogErr
  makes the error's text the message, so rweb's message goes under `event`;
  a wrapping error's full text is kept under its key. LogErr also appends its
  own caller (the adapter) as the first `location`/`function` entry; the
  Wrap locations follow. Plain `Error` would have kept only the text.
- **Wiring:** `ServerOptions.Logger`, `WithLogger`, copied by `WithOptions`.
  `NewServer` resolves nil once into `Server.log`; `newContext` copies it to
  `response.log` (the response's methods have no way back to the server, and
  that is where dropped header names are logged). `ctxLogger(ctx)` serves
  package-level code holding only a Context (default error handler,
  `RequestInfo`). `SSEHubOptions.Logger` for the hub, which has no server.
  Each falls back to the default when nil (contexts and responses built
  outside `NewServer` in tests).

### Level policy

Table on the `Logger` type. Errors and warnings are always produced; info
lines only under `Verbose`, debug lines only under `Debug`. The gate stays at
each call site (`if s.options.Debug { s.log.Debug(...) }`) so the variadic
args are not built when off. Gating in rweb rather than leaving it to the
logger's level matters for this user: `logger.InitLog` defaults to debug, so
a level-only gate would make every server chatty.

The flip side, documented on `ServerOptions.Debug` and in the README: with
`Debug: true` and no `InitLog`, logrus sits at info and drops the debug lines.
Mutating the global level from `NewServer` was considered and rejected.

### What changed in output

| Line | Before | Now |
|---|---|---|
| SSE stream ended (channel closed / close event) | unconditional | info, Verbose |
| `Error writing headers`, SSE write/flush errors | unconditional | debug, Debug (client gone) |
| SSE event that cannot be encoded | unconditional | warn, always (producer bug) |
| WebSocket upgrade failed | unconditional | debug, Debug (error handler logs it too) |
| Request body read error | Verbose | debug, Debug |
| Accept error, retrying | Debug | warn, always (net/http does the same; backoff caps it at 1/s) |
| ReadyChan without buffer | Verbose | warn, always |
| StaticFiles refused at registration | unconditional print | error |
| Dropped header name (N-031) | `log.Printf` | warn via the server's logger, name `strconv.Quote`d |
| Default error handler | `log.Printf [ERR: code]` | error with `code`, `method`, `path`, `error` |
| `RequestInfo` | `fmt.Printf` with its own timestamp | info via the server's logger |
| 64-dash separator per request | Verbose | removed (noise in a structured log) |

`ListRoutes` still prints its table to stdout: printing it is what the caller
asked for (comment added). `go.mod`: rohanthewiz/logger v1.3.0 direct
(logrus v1.9.4 and x/sys indirect); CLAUDE.md's "minimal dependencies" line
now lists the real set.

### Tests (`logging_test.go`)

A `captureLogger` records level, message and key-values.

- `TestLoggerOptionResolution`: nil → the adapter; set via ServerOptions,
  `WithLogger`, `WithOptions`; reaches pooled responses; fallbacks for a
  context/response with no server.
- `TestDefaultLoggerWritesThroughRohanLogger`: `logger.InitLog` with the
  LogChan hook; checks levels, plain error, a serr error (`userId=42`,
  `event=...`) and one wrapped by `fmt.Errorf`. Cleanup puts logrus back to
  info (the hook cannot be removed; it sends without blocking).
- `TestSlogLoggerPlugsIn`, `TestDroppedHeaderNameWarnsThroughServerLogger`,
  `TestDefaultErrorHandlerLogsCode` (logged code equals the page's; the code
  format is `XXXX-XXXX`, which the first regex missed), 
  `TestRequestInfoLogsThroughServerLogger`, `TestSSEHubLogger`.
- `TestServerLogLevelsFollowOptions`: default / Verbose / Debug servers over
  TCP, with a 404, a POST body, an SSE stream ended by `SSEClose` with an
  unencodable event in it, and one ended by closing the channel; asserts the
  warn is always there, info only with Verbose, debug only with Debug, no
  errors.
- `TestReadyChanWithoutBufferWarns`, `TestAcceptErrorWarns` (reuses
  `scriptedListener` from graceful_shutdown_test.go).
- `TestWebSocketHandlerErrorAfterUpgrade` now captures through a `Logger`;
  its `captureLog`/`syncBuffer` helpers (std `log` redirection) are gone.

**Docs:** README "Logging" section; SKILL.md option comments and the
`RequestInfo` note; CLAUDE.md `logging.go` bullet and dependency line.

## Notes

- Commits: `9a6cad0` (code), `77055e5` (next-list close), then this doc
  with the session stem. `/sw` pushes.
- Examples still print with `fmt`/`log`; they are apps, so left as they are.
- Phase 4 still has N-054 (needs D8) and N-055 (needs D9). N-073 (medium)
  and N-074 (low) are open too.

## Next

Closed: N-053. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
