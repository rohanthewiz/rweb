# Session: decline N-018 (Host validation on synthetic requests)

Session ID: `42a2434a-d003-4fe8-9d86-23de74c5fee8`
Date: 2026-10-11

## N-018: synthetic `s.Request()` bypasses Host validation

The item noted that Host validation runs only on the wire path, so synthetic
`s.Request()` calls skip it, and flagged it as a candidate for Non-goals.

Re-checked against the code:
- The check lives in the connection header loop (`Server.go:1331`): a second
  Host line or a value failing `isValidHostHeader` gets a raw 400, per RFC
  9112 §3.2. Its stated purpose is the wire: a proxy in front may route or
  authorize on a different Host line than the one `Header()` returns, and a
  malformed value could be echoed into redirects, links or logs.
- `s.Request()` (`Server.go:1022`) takes its headers straight from the
  caller and only seeds the `ContentType` shortcut from them. It is
  documented as a test helper ("very useful inside tests where you don't
  want to spin up a real web server").

The premise holds, but the risk the check guards against needs an untrusted
client and a second hop, and a synthetic request has neither. User chose
**decline**.

Moved N-018 to Non-goals with that reason. Also refreshed N-015's stale
line reference (`Server.go:1103` → `:1338`). No code change.

## Files

`ai_docs/todo/next-list.md`, this doc.

## Next

Closed: None. Declined: N-018. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-015. Full list: `ai_docs/todo/next-list.md`.
