# Session: decline N-005 (GetPostValue deprecation)

Session ID: `c9d1da3e-9450-4651-99d5-501edd4993ea`
Date: 2026-10-10

Continuation of the same session as
`2026-1010-2307-head-fallback-and-501-unknown-method` (N-021, N-022, N-023,
raised N-025). This doc covers only the part after that save.

## N-005: `GetPostValue` vs `FormValue`

The item proposed deprecating `GetPostValue` to a thin alias of `FormValue`,
noting it had lapsed and was a candidate for Non-goals.

Re-checked against the code:
- `GetPostValue` (`Request.go:299`) reads only the
  `application/x-www-form-urlencoded` body via `PostArgs()`; it never
  triggers a multipart parse. The `Request` interface documents it that way
  ("does NOT read multipart form fields — use FormValue for those").
- `FormValue` (`Request.go:451`) dispatches on Content-Type: multipart →
  lazy `ParseMultipartForm` and `multipartForm.Value`; otherwise it falls
  through to `GetPostValue`. So `GetPostValue` is the narrower method that
  `FormValue` builds on, not a duplicate.
- Callers: `examples/cookies/main.go`, tests (`Server_test.go`,
  `proxy_test.go`, `Request_leak_test.go`), and one outside project under
  `~/projs/go` (`camctl/handlers/handlers.go`).

Options put to the user: decline, add a `// Deprecated:` comment (staticcheck
SA1019 would flag every caller), or make it an alias (behavior change: it
would start reading multipart fields). User chose **decline**.

Moved N-005 to Non-goals with that reason. No code change.

## Files

`ai_docs/todo/next-list.md`, this doc.

## Next

Closed: None. Declined: N-005. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
