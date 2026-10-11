# Session: next-list review, cookie encryption (N-001), 405 Method Not Allowed (N-002)

**Session ID:** `84cf0cbb-23e3-4bf1-9e76-04961f3b5d6b`
**Date:** 2026-10-10
**Branch:** master
**Base commit:** `dd0e42f`

## Ask

1. `/next-list`: review the living list against the code.
2. "Do N-001 and N-002."
3. "When completely done here do a /sess-wrap, then do N-004 next."

## /next-list review

Living-list mode over all 10 session docs (`2026-0509-2225` … `2026-1006-1954`).
No lapses: every ID in the file's git history is still present, IDs unique,
Next ID correct, both summary-form `## Next` sections match the file. Every
Open premise re-checked against `dd0e42f`:

- **N-021 premise partly wrong.** The installed skill copy
  (`~/.claude/skills/rweb-light-go-webserver/SKILL.md`) already had the TLS
  `TLSAddr`/autocert notes; what it lacks is "Compression (permessage-deflate)"
  and "Request Context (v0.2.0+)". Text corrected.
- **N-016 moved Open → Validate** (new section). Its remaining work is a test.
  `request_context_test.go` (from `0700738`) adds WS and SSE tests, but each
  upgrades/streams on the conn's *first* request; `TestRequestContextKeepAlive`
  reuses a conn only for plain requests. Still owed.
- Conventions section updated to define Validate.
- All other premises held (staticcheck output for N-007 unchanged, line refs
  current).

## N-001: CookieConfig.EncryptionKey now encrypts

The field was declared with a doc comment promising encryption but was never
read, so callers who set it got plaintext. Implemented the full fix rather
than just correcting the comment.

**Design**

- `newCookieAEAD(key)` (`Cookie.go`) builds an AES-GCM `cipher.AEAD` once in
  `NewServer`, stored on `Server.cookieAEAD`. Empty key → nil (encryption off).
  A `cipher.AEAD` is safe for concurrent use, and key expansion is the costly
  part, so per-cookie setup would be waste.
- Wire format: `base64.RawURLEncoding(nonce[12] || ciphertext || tag[16])` —
  cookie-safe characters, ~38 chars overhead. Random nonce per seal (96-bit
  random nonce bound is irrelevant at cookie volumes).
- The **cookie name is GCM additional data**, binding a value to its name, so
  a client can't move the sealed value of `role` into `plan`.
- `SetCookieWithOptions` seals only the wire copy (`stdCookie.Value`); the
  caller's `*Cookie` keeps plaintext, so a reused struct isn't sealed twice.
  Deletion cookies (`MaxAge < 0`) are not sealed.
- `GetCookie` opens per call; `parsedCookies` keeps the sealed form (no
  plaintext cache to invalidate). `GetCookieAndClear` inherits this.
- Every open failure (bad base64, too short, auth failure) →
  exported `ErrCookieDecrypt`; a missing cookie is still "cookie not found".
- **Bad key fails closed**: `NewServer` has no error return, so it logs
  `[ERR] rweb: invalid CookieConfig.EncryptionKey (N bytes; need 16, 24 or 32)`
  and stores the error in `Server.cookieAEADErr`; every cookie set/get returns
  it instead of writing or reading plaintext.
- `HasCookie` reports presence only (documented).

**Consequence for users**: with a key set, every cookie the server *reads*
must have been sealed by it. Cookies from client-side JS or written before the
key was configured fail with `ErrCookieDecrypt`; key rotation logs everyone
out. Documented in the field comment, `ai_docs/SKILL.md` and README.

## N-002: 405 Method Not Allowed

The routers keep one table per method and nothing per path, so a wrong-method
request got 404. Instead of maintaining a path→methods index (which would have
to mirror both routers' matching rules, `:params` and `*wildcards`), the
default handler, on a route miss, calls `Server.allowedMethods(path)`: it asks
each of the nine method tables (hash router, then radix router) whether the
path is routed. Any hit → `405` with `Allow: GET, POST` (fixed order in
`routableMethods`); none → `404` as before. Runs only on the miss path, so
matched requests pay nothing.

- Radix probes pass a package-level no-op `discardParam` callback so probing
  can't leave `:params` on the request (tested).
- Behaviour change: a POST to a GET-only route, a HEAD to a GET-only route,
  and any non-GET under a `GET /*path` catch-all now get 405 instead of 404.
  Correct per RFC 9110, since the server would serve a GET there.

## Found along the way (raised)

- **N-022 (medium)**: a request with an unknown method (`FOO /x`) panics in
  dispatch: `RadixRouter.selectTree` returns nil and `LookupNoAlloc`
  dereferences it. Recovered → 500 plus an `[ERR]` with stack per request.
  Confirmed it predates this session (the panic is in the dispatch lookup
  before the 405 probe). Should be 501.
- **N-023 (low)**: HEAD isn't implied by GET; HEAD on a GET-only route now gets
  405 `Allow: GET` (was 404).

## Files

- `Cookie.go`: `EncryptionKey` doc, `ErrCookieDecrypt`, `newCookieAEAD`,
  `sealCookieValue`, `openCookieValue`.
- `Context.go`: seal in `SetCookieWithOptions`, open in `GetCookie`.
- `Server.go`: `cookieAEAD`/`cookieAEADErr` fields and setup in `NewServer`;
  405 branch in the default handler; `routableMethods`, `discardParam`,
  `allowedMethods`.
- `Cookie_encrypt_test.go` (new): round trip, fresh nonce, six rejection
  cases (tampered, truncated, name swapped, wrong key, plaintext, empty), bad
  key fails closed, reused struct, delete/clear.
- `method_not_allowed_test.go` (new): exact/param/wildcard/group routes and
  404s, probe side effects, over-the-wire 405 with `Allow` and empty body.
- `ai_docs/SKILL.md`: "404 vs 405 (v0.2.1+)" and "Encrypted Cookies
  (v0.2.1+)" sections. The version label is a guess at the next tag.
- `README.md`: encryption paragraph under Cookie Security.
- `ai_docs/todo/next-list.md`: review edits plus this session's changes.

## Verification

`go test -race ./...` passes; `gofmt -l .` empty; `go vet .` clean;
`staticcheck .` shows only the pre-existing N-007 hits.

## Next

Closed: N-001, N-002. Declined: None. Raised: N-022, N-023.
Deferred: None. Promoted: None. Moved: N-016 → Validate.
Updated: N-021. Full list: `ai_docs/todo/next-list.md`.

Up next (user's call): N-004, the lazy multipart parse.
