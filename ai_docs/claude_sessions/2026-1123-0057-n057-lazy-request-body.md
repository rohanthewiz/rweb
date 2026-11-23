# N-057: lazy request body, `BodyReader`, `MaxBody`, streamed `Proxy` uploads

Session id: `74bc1dff-27ff-44ca-a271-b3679534e9b8`
Date: 2026-11-23 · Branch: `roh/once-over-fab5.2` · Start `78b587b` → end `9d96d5e` (+ this doc)
Run: the user named Roadmap item N-057 (which had absorbed N-046), then asked
for `/sess-wrap` when it was complete. Open and Validate stay empty; Roadmap
is down to N-056.

## Done

### N-057: the request body is read on demand (`9d96d5e`)

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
- New exported errors: `ErrAbortHandler`, `ErrBodyTooLarge`,
  `ErrBodyReadAfterHandler`. New `consts.StatusRequestEntityTooLarge`.

**Decisions made without asking** (each explained in a comment at its site):

1. **A body that fails mid-handler ends the handler.** `Body()`,
   `GetPostValue`, `PostArgs` and `FormValue` have no error result. Before
   this change a request with a bad body never reached a handler. Returning
   nil would let a handler act on an upload that never arrived (store an
   empty document over a good one). So these panic with `ErrAbortHandler`,
   recovered in `handleRequest`, as net/http does with its own
   `ErrAbortHandler`. `BodyReader` and the multipart accessors return the
   error instead. An app may panic with it to drop a request.
   The alternative is one line in `Body()`: return nil instead of panicking.
2. **The server's refusal always replaces the handler's response** for a
   failed body, so a handler that ignores a read error cannot answer 200 for
   a broken upload. The error handler is skipped for such a request.
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
- `Server.go`: `serveConn` sets up `lazyBody`; `handleRequest` recovers
  `ErrAbortHandler`, calls `settleBody`, ends the lease and lingers after the
  response. `lineReader` moved into `request.lr` so a handler-side chunked
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
- New tests, all run 8 times under `-race` without a failure:
  - `request_body_test.go` (19): handler runs before the body arrives;
    3 MB streamed with nothing buffered (both framings, TCP and TLS);
    `Body`/`BodyReader` together; small unread body drained with a pipelined
    request behind it; 50 MB unread body answered at once and closed; no
    100 Continue for an unread body; four failure kinds end the handler at
    `Body()`; `BodyReader` error overruled by the server's 413; `MaxBody`
    cases; stale reader; multipart streamed; urlencoded on use; watch starts
    after the body; app-raised `ErrAbortHandler`; synthetic requests; SSE
    after an unread body; WebSocket handshake with a body.
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
