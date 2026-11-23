# N-057: lazy request body, `BodyReader`, `MaxBody`, streamed `Proxy` uploads

Session id: `74bc1dff-27ff-44ca-a271-b3679534e9b8`
Date: 2026-11-23 · Branch: `roh/once-over-fab5.2` · Start `78b587b` → end `a5c0d81` (+ this doc)
Run: the user named Roadmap item N-057 (which had absorbed N-046), then asked
for `/sess-wrap` when it was complete. After the first wrap (`9d96d5e`,
`a28da1f`) the user ruled out the panic used for unreadable bodies; that was
reworked in `a5c0d81` and the session wrapped again. This doc describes the
final state. Open and Validate stay empty; Roadmap is down to N-056.

## Done

### N-057: the request body is read on demand (`9d96d5e`, `a5c0d81`)

Before: `serveConn` read every body into memory, up to the 100 MB cap, before
routing. Now it parses and checks the head, records the framing in
`request.lazy` (`lazyBody`), and reads nothing.

**New file `request_body.go`** (diagram and rationale at the top):

- `Body()` buffers what is left of the body on first call and keeps it.
- `Request().BodyReader()` (new on `ItfRequest`) streams from the connection.
  It yields what `Body()` buffered first, then the rest. Without an earlier
  `Body()` it is one-shot. A reader kept past its handler returns
  `ErrBodyReadAfterHandler` (`bodyHandle` carries `request.bodyGen`).
- urlencoded forms parse on first use (`parsePostArgs`), as multipart already
  did. Multipart forms parse straight off the connection, so an upload is
  never held whole.
- `lazyBody.err` is the state: nil = more to read, `io.EOF` = connection is at
  the next request, `errBodyAbandoned` = server gave up (close, no refusal),
  anything else = failed.
- `settleBody` runs after the handler chain (table on the function):
  - read to its end, or no body: connection kept.
  - failed: the server's 400/408/413/431 replaces the handler's response
    (`refuseBody`), then a lingering close. Nothing for a client that left.
  - small unread remainder (≤ 256 KB arriving within 1 s): drained, kept.
  - larger or slower, or 100 Continue never sent: the handler's response
    with `Connection: close`, then `lingerClose`.
  - an event stream to follow: the body is read to its end first, as before.
- `rweb.MaxBody(n)` middleware lowers the cap (`lazyBody.max`).
- New exported errors: `ErrBodyTooLarge`, `ErrBodyReadAfterHandler`. New
  `Request().BodyErr()`. New `consts.StatusRequestEntityTooLarge`.

**Decisions made without asking** (each explained in a comment at its site):

1. **A body that fails mid-handler (decided by the user: no panics).**
   `Body()`, `GetPostValue`, `PostArgs` and `FormValue` have no error result.
   - As first committed (`9d96d5e`) they ended the handler with
     `panic(ErrAbortHandler)`, so no handler could act on an upload that
     never arrived.
   - The user's call: panics are bad in production; handle it gracefully and
     log. Reworked in `a5c0d81`:
     - `Body()` returns nil, never the part that did arrive. The form
       accessors return `""`.
     - New `Request().BodyErr()` returns the error, for a handler to check
       before it acts on the body. (My addition: without it a `Body()` or
       `FormValue` user cannot tell a failed upload from an empty one.)
     - `BodyReader` and the multipart accessors return the error, as before.
     - `refuseBody` logs a warning (method, path, status, error) when a
       handler was given an empty result and never called `BodyErr`
       (`lazyBody.unseen`). Every other case is a Debug line. The Logger
       policy table in `logging.go` lists both.
     - `ErrAbortHandler` and its recover branch are removed.
   - Left as is: a handler that skips the check runs on with the nil body.
     The client still gets the server's refusal; the warning is the trace.
   - The warning can be triggered by any client that sends a bad body to
     such a route, one line per request.
2. **The server's refusal always replaces the handler's response** for a
   failed body, so a handler that ignores a read error cannot answer 200 for
   a broken upload. The error handler is skipped for such a request, so a
   handler can simply return `BodyErr()`.
3. **`MaxBody` only lowers the cap**, and refuses a known-oversize
   `Content-Length` on the spot. "Last one in the chain wins" was tried in a
   test and dropped: an outer lower cap refuses a sized body before the inner
   one runs, so it could not hold for both framings.
4. **Drain bound: 256 KB (net/http's value) and 1 s.** The response waits on
   the drain, hence the time bound.
5. **100 Continue is sent by the first read of the body** (`startBody`), so a
   body no handler reads is never sent.
6. **`ReadTimeout` runs from the first read of the body**, not from the end
   of the head. Handler time before the read is not the client's.
7. **The disconnect watch starts only when the body has been read to its
   end** (`startWatchIfIdle`), as in net/http. A handler that never reads its
   request's body gets no disconnect notice.
8. **HEAD/TRACE bodies are still discarded in `serveConn`**, unchanged.

**`Proxy` uploads** (`proxy.go`, `proxyRequestBody`, diagram on the type):

- Bodies up to 256 KB (`proxyBufferBodyMax`) stay buffered: the Transport can
  replay them on a stale upstream connection, and they go out with a
  `Content-Length` even if the client sent them chunked.
- Larger ones are streamed from the client's connection. A sized one keeps
  its `Content-Length`; a chunked one sends its first 256 KB from memory and
  the rest chunked.
- The Transport reads the body on its own goroutine and may go on after
  `client.Do` returns. While `request.lazy.lease` is set the body's reading
  state belongs to that goroutine. An unfinished lease after the chain means
  `Connection: close`, relay the response, then `detach` (interrupts a parked
  read with a past read deadline, as `stopWatch` does).
- `Expect: 100-continue` is passed upstream for a streamed body nobody has
  started reading, so an upstream that refuses on the headers stops the
  upload at the client.
- A streamed request is not retried when the upstream closes a kept-alive
  connection under it (a 502). Documented on `Proxy`.

**Other code changes:**

- `request_framing.go`: `readChunkedBody` became `nextChunk`, the same
  decoder cut at the chunk boundary. Every check is unchanged.
  `errChunkTooLarge` is now `ErrBodyTooLarge`.
- `Server.go`: `serveConn` sets up `lazyBody`; `handleRequest` calls
  `settleBody`, ends the lease and lingers after the response. `Proxy`
  buffers a WebSocket handshake's body explicitly, so a failed one is not
  sent upstream as "no body". `lineReader` moved into `request.lr` so a handler-side chunked
  read can use it.
- `Context.go`: `upgradeWebSocket` and `GetConn` buffer an unread body before
  taking the connection over. `Clean` resets `lazy`.
- `conn_reader.go`: `netConn()` accessor.
- The Debug "request body" line is written when a body is buffered, not for
  every request. `logging_test.go`'s handler now reads its body for that
  reason; no other existing test changed.

**Docs:** README (new "Request Bodies" section), `ai_docs/SKILL.md`,
CLAUDE.md, the `conn_limits.go` deadline diagram, `ServerOptions` comments,
and an "As done" note under N-046 in `PLAN_ONCE_OVER_2026-1109.md`.

## Checks

- `go test -race ./...`: all packages pass.
- `GOOS=windows go vet ./...`: clean.
- New tests, repeated under `-race` (8 times at `9d96d5e`; the ones changed
  for `a5c0d81` 6 times more) without a failure:
  - `request_body_test.go` (19): handler runs before the body arrives;
    3 MB streamed with nothing buffered (both framings, TCP and TLS);
    `Body`/`BodyReader` together; small unread body drained with a pipelined
    request behind it; 50 MB unread body answered at once and closed; no
    100 Continue for an unread body; four failure kinds, each with a handler
    that checks `BodyErr` and one that does not (nil body, server's refusal,
    warning or none); `FormValue` of a failed body; `BodyReader` error
    overruled by the server's 413; `MaxBody` cases; stale reader; multipart
    streamed; urlencoded on use; watch starts after the body; synthetic
    requests; SSE after an unread body; WebSocket handshake with a body.
  - `proxy_upload_test.go` (6): large upload streamed (both framings) with
    keep-alive after; small chunked upload buffered and sent sized; upstream
    answers mid-upload (TCP and TLS); `Expect` passed on, refused and
    accepted; client leaves mid-upload; malformed streamed chunk is 400.
- Keep-alive benchmarks against `78b587b` (3 runs each): no change in time
  or allocations (hello 3 allocs, JSON 5, browser 11).

Not tested: a streamed upload to an HTTP/2 upstream. The lease logic does not
depend on the protocol, but no test has an h2 upstream.

## Next

Closed: N-057. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: N-056.
Full list: `ai_docs/todo/next-list.md`.
