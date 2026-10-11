# Session: N-007 — staticcheck dead code and S1017

Session ID: `0ace868a-857d-404f-b7b9-378fc2abbf97`
Date: 2026-10-11

## N-007: dead code flagged by `staticcheck ./...`

The installed `~/bin/staticcheck` was built with go1.23.2 and refuses the
module (`requires at least go1.24.0`), so the item was re-checked with
`go run honnef.co/go/tools/cmd/staticcheck@latest ./...` (v0.8.1, which
switches the toolchain to go1.26). All 14 findings named by the item still
reproduced; only the S1017 had drifted, from `Server.go:512` to `:686`.

Done:
- `args.go`: removed `visitArgsKey`, `delAllArgsBytes`, `appendArgBytes`,
  `decodeArgAppendNoPlus`, `peekAllArgBytesToDst`, `peekArgsKeys` (all
  fasthttp leftovers with no caller in the package or its tests).
- `bytesconv.go`: removed `readHexInt`, `writeHexInt`, `lowercaseBytes`,
  `hexIntBufPool`, `errEmptyHexNum`, `errTooLargeHexNum`, and the `bufio`,
  `io`, `sync` imports only they used. Kept exported
  `consts.MaxHexIntChars` (public API of `consts`). `lowerhex` and the
  generated `toLowerTable` are still referenced elsewhere / not flagged, so
  they stay.
- `consts/text_bytes.go`: removed unexported `defaultContentType`.
- `Server.go` (`Proxy`): `if strings.HasPrefix(p, "/") { p = p[1:] }` →
  `p = strings.TrimPrefix(p, "/")` (S1017; same behavior).

Everything removed was unexported, so no external caller can depend on it.

Verified: `go build ./...`, `go vet ./...`, `gofmt -l .` (clean),
`go test ./...` (all pass), and staticcheck v0.8.1 exits 0 with no findings.

Note: gopls briefly showed "undefined" errors in `Server.go`
(`rejectWebSocketRequest`, `baseCtx`, …) mid-edit; these were a stale index —
the full build compiled cleanly.

## Files

`args.go`, `bytesconv.go`, `consts/text_bytes.go`, `Server.go`,
`ai_docs/todo/next-list.md`, this doc.

## Next

Closed: N-007. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
