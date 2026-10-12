# Session: N-027 — stream response bodies (Stream, SendFile, SendFileAs)

Session ID: `e46e39ad-a0b2-4581-8134-a2075902dc40`
Date: 2026-10-11

## The ask

From the cats-todo backlog: a downstream app's database-dump download was not
streamed. rweb held every response in memory, so the app kept dumps over
512 MB on the server and logged their path. Real streaming needed a change in
rweb.

## Design

Static files were already streamed through a pending `response.bodyFile`
(`*os.File` + size). That became `response.bodyStream` (`io.Reader`) plus
`bodyStreamSize`, so one mechanism serves files and generated bodies:

- **Known size (>= 0):** `Content-Length: len(body)+size`, then the
  in-memory body, then `io.CopyN` of the stream. An `*os.File` still gets
  sendfile(2) on plain TCP, since the dynamic type survives the `io.Reader`
  field. A short copy closes the conn (as before).
- **Unknown size (< 0, normalized to -1):** `Transfer-Encoding: chunked`.
  `writeChunkedBody` (`Server.go`) lays out each chunk's size line, payload
  and CRLF in one buffer, so one Write goes out per chunk. It sends whatever
  each Read returns at once, so a slow producer shows steady progress. The
  in-memory body goes first as chunks. The last chunk is sent only at EOF. On
  a read or write error the conn is closed, so a failed dump is a truncated
  download, never a short "complete" file.
- **Pending, not hidden (unchanged contract):** the body accessors and
  `ctx.Bytes`/`WriteString` materialize the stream first. For unknown size
  that means growing via `bytes.Buffer.ReadFrom`. Synthetic `s.Request()`
  materializes. HEAD never reads the stream and just closes it.
- **Ownership:** the response closes the stream if it is an `io.Closer`
  (`closeStream`). Closing a `*io.PipeReader` is how a producer learns the
  client left: its next write fails with `io.ErrClosedPipe`.
- **Handler error drops a pending stream** (`handleRequest`, before
  `errorHandler`). Otherwise the default error page's `WriteHTML` would
  read the whole stream into memory and glue the page onto it.
- The request context stays live through `writeResponse` (it is cancelled in
  `handleRequest`'s defer). A producer tied to `ctx.Request().Context()`
  therefore keeps running while the body streams. The disconnect watch is
  already stopped by then, so a client leaving is signalled by the closed
  pipe, not by the context.

Public API (`send.go`):
- `Stream(ctx, r io.Reader, size int64) error`: package-level like `File`,
  not a `Context` method, so external `Context` implementations don't
  break. A foreign `Response` falls back to copying into memory.
  - The doc shows the `io.Pipe` + goroutine pattern. The copy runs after the
    handler returns, so a producer must not be torn down by a handler defer.
- `SendFile(ctx, path)` / `SendFileAs(ctx, path, name)`: open, then Stat the
  open file. Then a 304 via `notModifiedSince`, `setFileHeaders` by `name`,
  and `Stream`.
  - A missing path returns the `os.Open` error. A directory returns an error
    wrapping `os.ErrNotExist`, so one `errors.Is` check maps both to a 404.
- `notModifiedSince` was extracted from `staticFiles`, which now also ends
  in `Stream(ctx, f, info.Size())`.

Known limit, documented: chunked encoding is HTTP/1.1. The server ignores the
request-line version, so an HTTP/1.0 client can't read an unknown-size body.

## Tests

`stream_body_test.go`, tcp and tls over raw keep-alive conns:
- Chunked over keep-alive, with a written prefix, an empty stream and a
  following request.
- A sized non-file reader with extra bytes past the size, and the stream is
  closed after sending.
- A producer error truncates the body.
- A client leaving stops the producer (`io.ErrClosedPipe`).
- HEAD never reads the stream, sized or chunked.
- Synthetic requests get the full body.
- Middleware rewriting the body gets Content-Length, not chunked.
- A handler error drops the stream unread.
- `SendFile`/`SendFileAs`: headers, name, Last-Modified, 304, and 404 for a
  missing file or a directory.

`static_stream_test.go` was updated for the renamed field. The full suite
passes under `-race`. A manual curl check of a 200k-row piped chunked dump,
twice on one conn, received every row.

Docs: README (example routes), `ai_docs/SKILL.md`, CLAUDE.md (Static File
Serving bullet and a new Streamed bodies bullet).

## Next

Closed: N-027. Declined: None. Raised: N-027.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
