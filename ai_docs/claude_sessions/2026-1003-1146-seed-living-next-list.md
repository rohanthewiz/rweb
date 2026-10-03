# Session: Seed the living Next list

**Session ID:** `25658227-2685-4386-a2e5-bc964eaeecbf`
**Date:** 2026-10-03
**Branch:** master

## Ask

1. `/next-list 30` — rebuild the Next list from session-doc history.
2. "yes, seed it" — write the result as the living list
   `ai_docs/todo/next-list.md`.

No code changed this session.

## Rebuild

There was no living list, so `/next-list` ran in History mode. Only 8 session
docs exist (`2026-0509-2225` … `2026-0919-2229`), so `n = 30` covered the whole
history and no age was floored by the window.

The newest doc's Next list carried six items, plus three non-goals. Scanning
every earlier doc's tail (the older ones used "Items rejected from scope",
"Notes for future work", "Notes for next time", "Next steps", "Not Yet Done")
turned up items that had never been carried forward:

| Lapsed item | Raised in | Still open in code? |
|---|---|---|
| `CookieConfig.EncryptionKey` declared but unused | 0509-2225 | Yes — and its comment promises encryption that never happens |
| 405 Method Not Allowed | 0509-2225 | Yes |
| Lazy multipart parsing (drop the eager pre-parse) | 0509-2315, again 0509-2336 | Yes |
| Deprecate `GetPostValue` to an alias of `FormValue` | 0509-2315 | Yes |
| `var _ = context.Background` guard in `Request_leak_test.go` | 0509-2336 | Yes |
| Unused code flagged by lint | 0509-2336 | Yes (13 U1000 + 1 S1017 from `staticcheck`) |

## Findings from re-checking premises

- **Eager multipart pre-parse is worse than written.** It runs in
  `handleRequest` (`Server.go:1274`) *before* the handler chain, so a multipart
  POST to any path — routed or not — is parsed and may spill to temp files up
  to the 100 MB body cap. The accessors already lazy-parse idempotently
  (`Request.go:280,314,349`), so removing the pre-parse looks safe.
- **`EncryptionKey` is a silent security footgun**, not just a gap: the doc
  comment at `Cookie.go:121` says it "enables automatic cookie value
  encryption", but nothing reads the field.
- **Wrong premise:** 0509-2336 said the `context.Background` guard could go
  because `httptrace.WithClientTrace` keeps `context` in use. It doesn't —
  `req.Context()` is a method and needs no import; the guard is the import's
  only use, so both go together.
- **Already done, never marked closed:**
  - "Re-enable `ParseMultipartForm` in `GetFormFile`" (declined in 0509-2225)
    — both `GetFormFile` and `GetFormFiles` now call it.
  - "Push `feat/stylus-middleware`, open a PR" (0701-1727) — `ded4423` is on
    master.
  - "Tag v0.1.31" (0919-2229) — `v0.1.31` and `v0.1.32` both exist.
- The "audit every cached `request` field for a `Clean()` reset" lesson from
  0509-2336 now holds: every field is reset or reassigned per request.
- Commits `cb8e038` (WebSocket permessage-deflate) and `3169f85` (SSEHub atomic
  drop counter) postdate the newest session doc and have no doc of their own.

## Seeding

`ai_docs/todo/next-list.md` was written with a preamble, Conventions, a
**Next ID** line, and Open / Roadmap / Non-goals / Closed sections. IDs were
assigned chronologically by `raised` across all 18 items (closed ones
included), so Open has gaps:

- **Open (11):** N-001, N-002, N-004, N-005, N-006, N-007, N-010, N-015,
  N-016, N-017, N-018. Medium: N-001, N-004. The rest are low.
- **Roadmap:** empty — no doc marked an item deferred-but-wanted.
- **Non-goals:** N-011 streaming proxy responses, N-012 header count/size
  limits, N-013 `Connection: close` handling.
- **Closed:** N-003, N-008, N-009, N-014.
- **Next ID:** N-019.

N-010 (stream static files) has `raised` = `2026-0701-2109`, where it was first
written down as a non-goal; its text notes it was reopened in 0919-2153.
N-005 and N-018 are flagged as candidates for Non-goals pending the user's
call.

## Downstream (not in the list)

These belong to rosync and can't be verified from this repo:

- rosync `guard` can use `req.Host()` now that it's on ≥ v0.1.29; its comment
  repeating the "never populated" diagnosis should be corrected (0919-1443).
- rosync `monacoAsset` can switch to `StaticFilesAbs` (0919-2153).

## Files Changed

- `ai_docs/todo/next-list.md` — new
- `ai_docs/claude_sessions/2026-1003-1146-seed-living-next-list.md` — this doc

## Next

Seeded the living list this session; from here on, sessions edit it in place.
Closed: N-003, N-008, N-009, N-014 (found done while seeding). Declined:
N-011, N-012, N-013 (carried non-goals). Raised: N-001–N-018 (seed).
Deferred: None. Promoted: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
