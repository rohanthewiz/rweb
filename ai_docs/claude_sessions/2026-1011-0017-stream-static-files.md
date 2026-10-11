# Session: stream static files (N-010), drop unused context guard (N-006)

Session ID: `7bb27145-31f9-419c-a5a9-4f43581ce652`
Date: 2026-10-10 – 2026-10-11

Two backlog items from `ai_docs/todo/next-list.md`, both handed in as pasted
cats-todo text.

## N-006: unused `context` import guard

`Request_leak_test.go` ended with `var _ = context.Background`, described as
keeping the import alive for `httptrace.WithClientTrace` via `req.Context()`.
That premise was wrong: `req.Context()` is a method and needs no import, so the
guard was the import's only use. Removed the guard, its comment and the
`"context"` import. `go vet` and `go test ./...` passed. Committed as
`c902a20`.

## N-010: stream static files instead of `os.ReadFile`

### Before

The `StaticFiles` / `StaticFilesAbs` handler did `os.Stat` then
`os.ReadFile`, and handed the bytes to `FileWithModTime`, so every request
held the whole file in memory (a HEAD too: the file was read, then the body
was skipped).

### Design

The response gets a **pending body file**: `response.bodyFile *os.File` plus
`bodyFileSize int64`. The body is `response.body` followed by the first
`bodyFileSize` bytes of the file.

```
handler ── setBodyFile(f, size) ──► response{ body: [], bodyFile: f }
                                         │
          middleware touches body? ──yes─┤ materializeBody(): read f into body, close f
                                         │
writeResponse ── Content-Length = len(body) + bodyFileSize
              ── write headers, write body
              ── writeBodyFile: io.CopyN(conn, f, size)  (sendfile on *net.TCPConn)
              ── short copy → close conn (Content-Length can't be honoured)
```

Main decisions:

- **Pending, not hidden.** Every body accessor calls `materializeBody()`:
  `Body()`, `Write`, `WriteString` and `SetBody` (which closes the file
  instead of reading it), plus `ctx.Bytes` and `ctx.WriteString`, which
  append to `ctx.response.body` directly. Middleware that rewrites bodies
  after `Next()` (e.g. `middleware/critical` on static `.html`) sees the same
  bytes as before, and only that request pays for the read. An alternative,
  streaming always and letting `Body()` come back empty, would have silently
  stopped critical-CSS inlining on static pages.
- **Open, then Stat the open file**, replacing Stat-then-ReadFile, so size,
  mod time and the directory check describe the bytes actually sent. Open
  failure is a 404, where the old code gave a 500 if a ReadFile failed after
  a successful stat.
- **`io.CopyN`, not `io.Copy`**: `*net.TCPConn.ReadFrom` recognises the
  `*io.LimitedReader` over an `*os.File` and runs sendfile. The limit also
  stops a file that grew from running past the Content-Length already sent.
  TLS conns get an ordinary buffered copy.
- **Short copy closes the connection.** If the file shrank after it was
  opened, the client is owed bytes it will never get; on keep-alive the next
  response would be parsed as the rest of the body. Closing the conn is the
  only remaining way to delimit the body.
- **Synthetic `Server.Request`** sets a new `context.synthetic` flag;
  `writeResponse` materializes the file for it, because the writer there is
  `io.Discard` and the caller reads `Body()` afterwards. This also closes the
  fd straight away instead of leaving it to the finalizer.
- **HEAD and 304 never read the file**; HEAD reports `Content-Length` =
  file size, then `closeBodyFile()`.
- **`Clean()` calls `closeBodyFile()`** as the backstop for error paths and
  WebSocket/SSE early returns.
- The static handler type-asserts `ctx.Response().(*response)`; if it fails
  (never, today) it falls back to `io.ReadAll` + `ctx.Bytes`.
- The TOCTOU comment now says a swapped-in link is followed by `Open` (was
  `ReadFile`); `os.Root` is still ruled out by the go 1.23 directive.

### Files

- `Response.go`: `bodyFile`/`bodyFileSize` fields; `setBodyFile`,
  `materializeBody`, `closeBodyFile`; accessors materialize first.
- `Context.go`: `synthetic` field; `Bytes`/`WriteString` materialize;
  `Clean` closes a pending file.
- `Server.go`: static handler opens + streams; `writeResponse` computes
  Content-Length with the file and calls the new `writeBodyFile`;
  `Request()` sets `synthetic`.
- `static_stream_test.go` (package `rweb`, reusing `serveTest`/`dialTest`/
  `transports` from `request_context_test.go`), each over TCP and TLS:
  - two 3 MB+17 B random files then a small one on one keep-alive conn
  - the handler leaves the body empty with the file pending, and the file is
    closed once the next response arrives
  - HEAD sends only the length, and the next GET parses cleanly
  - middleware read+`SetBody` and append-after-`Next` both work, with
    matching Content-Length
  - synthetic `Request` returns the full file
  - truncating the file between handler and send gives
    `io.ErrUnexpectedEOF` after the remaining 1000 bytes, then a closed conn
- `ai_docs/SKILL.md`: static-files bullet on streaming and middleware
  visibility.
- `CLAUDE.md`: Static File Serving line notes the pending body file.

`go test -race -count=1 ./...` passes; gofmt and vet are clean.

### Not done (not raised)

- `rweb.File` / `FileWithModTime` still take `[]byte`. There is no public
  "send this file streamed" helper; the streaming path is internal to
  `StaticFiles`.
- No Range / If-Range support (none before either).

## Next

Closed: N-006, N-010. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
