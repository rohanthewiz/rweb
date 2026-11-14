# Phase 4: N-052, static files and SendFile get Range, ETag and options

Session id: `d6d43d31-e71c-45ee-9036-b7b6886ebe3a`
Date: 2026-11-14 · Branch: `roh/once-over-fab5.2` · Start `6e93fc9` → end `0e68982` (+ this doc)
Plan: `ai_docs/todo/PLAN_ONCE_OVER_2026-1109.md`, Phase 4, N-052.
Same session as `2026-1114-0017-n051-text-charset-utf8` (N-051), continued.

## What was asked

1. "Next list item N-052": static files and `SendFile` had no Range, ETag,
   Cache-Control or index options, forced unknown types to download, and
   encoded download names badly.
2. `/sw`.

Checks run before committing: `go test -race -count=1 ./...`, `go vet ./...`,
`GOOS=windows go vet ./...`, `gofmt -l`, and staticcheck v0.7.0, installed
this session into the scratchpad (`GOBIN=<scratchpad>/bin go install
honnef.co/go/tools/cmd/staticcheck@v0.7.0`). All clean. `~/bin/staticcheck`
is still the old build.

## N-052 (`ed5a124`)

### Shared serving core: `file_serve.go`

`serveFile(ctx, f, info, name, cacheControl)` is now the one path for every
static route and for `SendFile`/`SendFileAs`, so the two cannot drift. The
file opens with a diagram of the order it follows (RFC 9110 §13.2.2):

1. **Validators.** `fileETag` is `"<mtime ns hex>-<size hex>"`, computed from
   the Stat already made. `Last-Modified` and `Accept-Ranges: bytes` go on
   every file response, 200 included.
2. **`checkPreconditions`:**
   - `If-Match` (strong comparison) or, only when it is absent,
     `If-Unmodified-Since`: a failed condition gives 412.
   - `If-None-Match` (weak comparison) or, only when it is absent,
     `If-Modified-Since`: an unchanged file gives 304 for GET and HEAD. For
     other methods a matching `If-None-Match` gives 412.
   - A 304 carries Date, ETag and Cache-Control (RFC 9110 §15.4.5). It does
     not carry Last-Modified, since the ETag is there.
3. **`etagListMatches`** scans the list tag by tag rather than splitting on
   commas, because an opaque tag may contain a comma. A malformed list
   counts as no match.
4. **Range,** for GET only (a HEAD gets the 200 headers):
   - `ifRangeHolds` comes first. An entity tag must match strongly, and a
     date must equal Last-Modified exactly. Otherwise the whole file is sent.
   - `parseByteRange` accepts one range: `bytes=a-b`, `a-` or `-n`. A last
     position past the end is clamped. A first position at the end, or
     `-0`, is unsatisfiable and gets 416 with `Content-Range: bytes */size`
     and no body.
   - Ignored (whole file, 200): a list of ranges, another unit, bad syntax,
     a last position before the first, and any range on an empty file (as
     net/http does, for clients that add `bytes=0-` to every request).
   - `parseRangePos` takes ASCII digits only and saturates on overflow, so
     `bytes=99999999999999999999-` is a 416.
   - A 206 seeks the file to the start and hands `Stream` the span length.
     `writeBodyStream` copies with `io.CopyN` from the file's current
     offset, so plain TCP still uses sendfile.

### Headers (`send.go`)

- `SendFileAs` now calls `serveFile`. `notModifiedSince` is kept and used by
  `checkPreconditions`.
- **Unknown extensions:** `setFileHeaders`' default case asks
  `mime.TypeByExtension` (Go's table plus the system `mime.types`) before
  falling back to an octet-stream download. A known type is served inline;
  an octet-stream answer still downloads. The answer can differ between
  machines, which the code comment and SKILL.md note.
- **Download names:** `contentDisposition` uses `mime.FormatMediaType`, which
  gives `attachment; filename="Q3 report.zip"`. A non-ASCII name is sent
  twice: an ASCII `filename` with "_" per non-ASCII rune, followed by
  `filename*=utf-8''…` (RFC 6266 §4.3). Go's `mime.ParseMediaType` prefers
  `filename*` in either order (probed). ASCII token names stay unquoted, so
  `TestSendFile`'s expectations still hold.
- **`x-filename`:** `headerFileName` is `url.QueryEscape` with `+` turned
  into `%20` (the `encodeURIComponent` form), so both `decodeURIComponent`
  and `url.QueryUnescape` give the name back.

### Options (`Server.go`, `Group.go`)

- `StaticFilesWithOptions` and `StaticFilesAbsWithOptions` on Server and
  Group take `StaticOptions{CacheControl, Index, SPAFallback}`. The
  internal `staticFiles` and `staticFilesAbs` take `opts`; the existing
  methods pass the zero value.
- **`CacheControl`** is sent on 200, 206 and 304. It replaces the
  `must-revalidate` that download types otherwise get.
- **`Index`** must be a plain name (no separator, not `.` or `..`). A
  directory serves its index, through `openContained` under
  `StaticContainSymlinks`.
- **`SPAFallback`** is checked with `filepath.IsLocal` and is relative to
  `baseAbs`. Every miss gets it, and a directory without an index counts as
  a miss. Safety refusals stay 404: `..`, a bad escape, and
  `errStaticEscape`.
- **The open path became `openFile`,** a closure that opens and Stats.
  `servedName` is the requested name (not a symlink target), the index
  name, or the fallback's base name.

### Router facts found by probing

These shaped the Index and SPA design:

- `/pub/*path` never matches `/pub/`, because a wildcard needs at least one
  character.
- The request parser drops a trailing slash (`http.go`, unless
  `KeepTrailingSlashes`), so `Path()` is `/pub/docs` for `/pub/docs/`.
- Registering `/pub` also serves `/pub/` (`addTrailingSlash`).
- `/` does not match `/*path`.
- A later registration of the same exact path replaces the earlier one.

Consequences:

- With Index or SPAFallback set, the prefix (`path.Join("/", reqDir)`) is
  registered too. Its param is empty, which names the top directory.
- There is no net/http-style redirect that adds the trailing slash: the two
  forms are indistinguishable by default, so it would loop. Index pages
  should use root-relative links (StaticOptions doc, SKILL.md).
- **Root mount:** the guard was `len(reqDir) < 2`, which refused `/`; it is
  now `reqDir == ""`. A root mount is a GET catch-all, so its 404s bypass
  the not-found hook, and a POST to an unknown path gets 405. Documented.

### Statuses (`consts/http_status.go`)

`StatusPreconditionFailed` (412) and `StatusRangeNotSatisfiable` (416) were
added, with reason phrases. Without them the status line would have none.

### `middleware/critical`

It rewrote any 2xx HTML response, so a 206 span would have had CSS spliced
into a fragment. It now:

- skips 206;
- on a rewritten page, turns a strong ETag into `W/"…"`, which never
  satisfies If-Range but still revalidates;
- sets `Accept-Ranges: none`.

Body-rewriting middleware in general should do the same (SKILL.md).

### Judgement calls against the plan

- **Strong ETag rather than weak.** A weak tag never satisfies `If-Range`,
  so resumed downloads would restart from zero, and caches could not
  combine ranges. nginx and Apache derive the same strong tag. The cost:
  identical size and mtime with different content shares a tag, which
  happens with builds that fix timestamps (documented on `fileETag`).
- **`If-Match` and `If-Unmodified-Since` also implemented (412).** The plan
  named only If-None-Match ahead of If-Modified-Since. These are cheap, and
  net/http has parity.
- **Root mount allowed, `critical` adjusted, 412/416 constants added.** All
  three were needed for the item to work as intended.

### Tests

`static_range_test.go` (package `rweb`, reusing `streamFixture`,
`roundTrip`, `serveTest` and `dialTest`):

- **Unit tests:** `TestParseByteRange` (23 cases, overflow included) and
  `TestETagListMatches`.
- **`TestStaticRangeRequests`,** on both transports over keep-alive. It
  sends five spans of a 3 MB file, including 1 MB from inside it, each
  followed by a whole response that must parse. It also covers 416, ignored
  ranges, and HEAD with a Range.
- **`TestStaticRangeSynthetic`** and **`TestStaticConditionalRequests`.**
  The latter has 17 precondition cases, an If-Range table, and a change to
  the file that invalidates the old tag.
- **Options:** `TestStaticCacheControlOption`, `TestStaticIndexOption` (with
  and without containment), `TestStaticOptionsRefusedAtRegistration`,
  `TestStaticSPAFallback` (with and without containment, plus a symlink
  escape that stays 404), `TestStaticRootMount` and
  `TestGroupStaticFilesWithOptions`.
- **Headers:** `TestSendFileRangeAndNames` (dispositions checked with
  `mime.ParseMediaType`, and x-filename) and `TestFileTypeFromMimePackage`
  (uses `mime.AddExtensionType`).
- **`middleware/critical`:** `TestStaticFileRangeAndValidators`.
- **Mutation check:** turning off the 206 branch and the If-None-Match
  match failed five of the tests, which pass once the code is restored.

**Docs:**
- SKILL.md: Static Files bullets, a new "Static Options" section, and a
  note on SendFile.
- README: the SendFile comment, and a `StaticFilesWithOptions` example.
- CLAUDE.md: the Static File Serving bullet, a new "File serving core"
  bullet, and Streamed bodies.

## Found along the way: N-074

On the wire, `HTTP/1.1 429\r\n` lacks the space RFC 9112 §4 requires
before an empty reason phrase. `consts.StatusTextFromCode` lacks most
codes, and ordinary responses carry no `Date` header. Both were logged
rather than fixed under N-052.

## Notes

- Commits: `ed5a124` (code), `0e68982` (next-list close and raise), then
  this doc with the session stems. `/sw` pushes.
- Phase 4 still has N-053 (pluggable logger, medium), N-054 (needs D8) and
  N-055 (needs D9). N-073 (medium) and N-074 (low) are open too.

## Next

Closed: N-052. Declined: None. Raised: N-074.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
