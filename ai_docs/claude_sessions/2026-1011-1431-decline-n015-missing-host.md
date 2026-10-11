# Session: decline N-015 (400 for a missing Host header)

Session ID: `ec809b63-061c-41dd-814a-a642b12dc38e`
Date: 2026-10-11

## N-015: should a request with no Host header get 400?

RFC 9112 §3.2 says an HTTP/1.1 request without Host MUST get 400. rweb
allows it on purpose: `Host()` falls back to `"localhost"`.

Re-checked against the code:
- The duplicate/malformed-Host 400 (`Server.go:1327`) exists because hops can
  disagree about which Host line counts. With no Host line there is nothing
  to disagree about, so nothing can be smuggled.
- The `"localhost"` fallback (`Server.go:1521`) gives a client nothing it
  couldn't get by sending `Host: localhost`. That includes the DNS-rebinding
  guard the fallback comment mentions: the attack needs a browser, and
  browsers always send Host.
- The server never reads the version from the request line (`Server.go:1282`
  keeps only method and target). HTTP/1.0 may omit Host, so a correct check
  needs new version parsing, and the fallback would stay for 1.0 anyway.
- Only a handful of raw-socket tests omit Host (`Server_test.go:57,66,279`,
  the "no host at all is tolerated" case in `TestHostHeaderValidation`).

Decision: **decline**. Rejecting a missing Host would close no hole and break
hand-rolled clients. Moved N-015 to Non-goals. Expanded the comment at the
Host check with the reasons (comment only, no behavior change). Build, vet
and Host tests pass.

## Files

`Server.go` (comment), `ai_docs/todo/next-list.md`, this doc.

## Next

Closed: None. Declined: N-015. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
