# N-068, N-070, N-074: hub closes outside the lock, response head conformance, fewer allocations

Session id: `3a7f4f72-7532-415b-a72a-5eb9a2277e67`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `c9698d7` → end `e70cfd8` (+ this doc)
Run: `/work-next-list`, third batch. Earlier batches:
`2026-1114-1718-n062-n059-n060-n072-ws-browser-linger-strict-head`,
`2026-1114-1729-n064-n065-n066-proxy-read-timeout-location-websocket`.

## Done

### N-068: hub clients closed outside the lock (`14ac26a`)

- `examples/websocket/main.go` and the SKILL.md chat example: the
  unregister case now deletes the client under the lock, then calls
  `go client.Close(...)` after releasing it. Before, `Close` (which waits up
  to 2 s for the peer's close reply) held the hub lock, and with it
  `ClientCount` and the Run loop, which does every broadcast.
- Also found and fixed in the example: `Run` sent its join and leave
  notices to its own `broadcast` channel, and it is that channel's only
  reader. With the buffer (100) full, `Run` would have blocked on itself
  forever. Those notices now go through an inline `fanOut`, which the
  broadcast case uses too.
- Checked: vet and build of the example module, then a run with two wscat
  clients (welcome, join notices, a chat message reaching both, the leave
  notice reaching the client that stayed).

### N-074 + N-070: response head (`ae34627`)

- `appendStatusLine` (`http.go`), used by `writeResponse` and the WebSocket
  101, produces `HTTP/1.1 SP code SP reason CRLF`; the space is always
  written. The reason is taken from `consts.StatusTextFromCode` (which keeps
  416's RFC 9110 name), then `http.StatusText`, then left empty.
- `appendDateLine` (`http.go`) formats the whole `Date: …\r\n` line once per
  second; the line sits behind an `atomic.Pointer` to an immutable value,
  with no timer goroutine. `writeResponse` adds it unless the handler set
  `Date`. Cost: 33 ns per response, including `time.Now`. The canned early
  refusals still carry no Date, as net/http's do not.
- Numbers in the head go in with `strconv.AppendInt` into
  `respBuf.AvailableBuffer()`, both the status code and Content-Length.
  `FormatInt` used to allocate for any length of 100 or more.
- `writeChunkedBody` now takes its 32 KB buffer from `chunkBufPool`
  (`*[]byte`).
- Added `BenchmarkKeepAliveChunked(TLS)`; `readFirstResponse` can now read
  a chunked response.
- Measurement, comparing two test binaries in turns (old built from HEAD's
  Server.go/http.go with the new bench file):

  | Benchmark | cpu-ns/op (median) | B/op | allocs/op |
  |---|---|---|---|
  | Chunked | 16496 → 12894 (−22 %) | 41048 → 90 | 8 → 6 |
  | Hello | 10578 → 10559 (10 rounds) | 37 → 32 | 4 → 3 |
  | JSON | within noise | 344 → 336 | 7 → 5 |
  | Browser | within noise | 646 → 642 | 12 → 11 |

  A first 4-round run showed hello/JSON +4–6 %, which looked like the Date
  line. A third binary without the Date line and a 10-round rerun showed it
  was noise: old 10578, new 10559, no-Date 10925.
- **Decision:** keep the `AppendInt` half even though its time gain is
  under the item's 5 % bar. It is part of the status-line helper N-074
  needed anyway. The pool clears the bar.
- Tests `response_head_test.go`:
  - status lines for 200, 413, 416, 422, 429 and an unnamed 299;
  - exactly one parseable, current Date on a 200 and on a 404;
  - a handler's Date kept and sent alone;
  - the cached line is reused within a second without allocating, renewed
    the next second, and comes out in GMT for any input zone.
- Side note: the first version of the status test registered routes with
  spaces in the path (from `http.StatusText`), which the N-072 strict
  request line rightly refused with 400. The routes became `/s/<code>`.

## Checks run for each item

`go test ./...`, `go test -race .`, `go vet ./...` (darwin, linux,
windows), `gofmt -l .`; the example module is vetted and built on its own.

## Remaining in the run

- N-076 and N-077 (both raised in the first batch) come next.
- N-065 (relative `Location` / cookie `Path`) and N-075 (forwarded
  Proto/Host for `Scheme()` and the WebSocket origin check) wait on the
  user's decision.

## Next

Closed: N-068, N-070, N-074. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
