# Session: StaticFilesAbs — serve a directory by absolute path

**Session ID:** `2503fd52-2d86-475c-98a2-5d010e9d3776`
**Date:** 2026-09-19
**Branch:** master
**Origin:** the session ran in `~/rocfg/rosync`; this is the second side trip into
rweb from it (the first was `2026-0919-1443-fix-request-host-header`).

## Ask

`rosync serve` needed to serve the Monaco editor's files from a per-user state
directory (`~/.config/rocfg/state/monaco/<version>/`). `StaticFiles` could not:
it resolves its target relative to the working directory. rosync worked around
it with a hand-written handler. The user then said: "Go ahead with the Rweb
StaticFile upstream fix as long as it does not break the relative path
functionality."

## Finding: the cwd-relative behaviour is the contract, not a bug

`StaticFiles` computes its root as

```go
rootAbs, _ := filepath.Abs("." + filepath.Join("/", targetDir))
```

so a leading `/` in `targetDir` does **not** mean "absolute" — it is stripped of
that meaning on purpose. The README and `examples/hello` document it:

```go
s.StaticFiles("static/images/", "/assets/images", 2) // serves ./assets/images
s.StaticFiles("/.well-known/", "/", 0)               // serves the working directory
```

That rules out the obvious fix (treat an absolute `targetDir` as absolute).
`filepath.IsAbs("/assets/images")` is true, so such a change would silently
re-point every route written in the documented style — and
`StaticFiles("/.well-known/", "/", 0)` would begin serving the filesystem root.
A heuristic ("absolute if it exists on disk") would be worse: behaviour would
depend on what happens to exist on the host.

## Change

A separate, explicit method; `StaticFiles` behaves exactly as before.

- **`Server.StaticFilesAbs(reqDir, targetDir, nbrOfTokensToStrip)`** — same
  signature and semantics, except `targetDir` is an absolute filesystem path. A
  non-absolute `targetDir` is refused at registration (message printed, no route
  added), rather than guessed at.
- **`Group.StaticFilesAbs`** — the group-prefixed wrapper, mirroring
  `Group.StaticFiles`.
- **`Server.staticFiles(..., absTarget bool)`** — the former body of
  `StaticFiles`, now shared. The two modes differ in one place only, where the
  root and candidate paths are anchored:

  ```go
  if absTarget {
      rootAbs = filepath.Clean(targetDir)
      candAbs = filepath.Join(rootAbs, strings.Join(rhTokens, "/"), decoded)
  } else {
      rootAbs, rErr = filepath.Abs("." + filepath.Join("/", targetDir))   // unchanged
      candAbs, cErr = filepath.Abs("." + fileSpec)                        // unchanged
  }
  ```

  Everything else — route construction, token stripping, the `..`/NUL/percent-
  decoding rejection, the `filepath.Rel` containment check, directory refusal,
  `If-Modified-Since` — is common code, so the two entry points cannot drift
  apart on safety. In absolute mode the candidate is joined onto the root
  directly instead of reusing `fileSpec`, whose forced leading `/` would mangle
  a Windows root such as `C:\data`.
- Doc comments: `StaticFiles` now states that its target is cwd-relative even
  with a leading `/`, and points to `StaticFilesAbs`; `StaticFilesAbs` explains
  why it is a separate method.
- README: a `StaticFilesAbs` example after the three `StaticFiles` ones, with
  the relative-vs-absolute note.

## Verification

New file `StaticFilesAbs_test.go`. Its fixture makes one temp dir the working
directory and puts the absolute root in another, with a same-named file in each
holding a different body, so a response shows which tree it came from.

| Test | What it establishes |
| --- | --- |
| `TestStaticFilesAbsServesFromTheAbsoluteDir` | serves from the absolute root (body is `from-abs`, not the cwd's `from-cwd`), nested files, 404 for missing, no directory listing |
| `TestStaticFilesAbsTokenStripping` | strip count behaves as in `StaticFiles` |
| `TestStaticFilesAbsPathTraversal` | the same eight attacks `StaticFiles` is tested with; a sibling `secret.txt` stays unreachable |
| `TestStaticFilesAbsRefusesARelativeDir` | a relative `targetDir` registers nothing |
| `TestGroupStaticFilesAbs` | group prefix + stripping |
| `TestStaticFilesStaysRelativeToCWD` | pins the existing contract: `"safe"`, `"/safe"`, `"./safe"` and `"/"` all resolve under the cwd; and an absolute path handed to `StaticFiles` is *not* followed |

The user's condition — relative behaviour must not break — was checked directly:
`TestStaticFilesStaysRelativeToCWD` was run against the **original** `Server.go`
and `Group.go` (stashed the changes, kept only that test plus the fixture so it
compiled against the old API). It passed there, along with the existing
`TestStaticFilesPathTraversal` and `TestGroupStaticFiles`. It passes on the new
code too. So the test describes behaviour that existed before and still does.

`go test ./...` and `go vet ./...` pass.

## Files Changed

| File | Change |
| --- | --- |
| `Server.go` | `StaticFilesAbs`; body moved to shared `staticFiles`; doc comments |
| `Group.go` | `Group.StaticFilesAbs` |
| `StaticFilesAbs_test.go` | new |
| `README.md` | example and note |

## Downstream

rosync (`internal/web/server.go`, `monacoAsset`) still uses its own handler. It
can switch to `StaticFilesAbs` once this is tagged, with one caveat: its root
is derived from `os.UserHomeDir()` and a version constant, known at startup, so
it fits; but it also returns 404 until Monaco has been installed at runtime,
which `StaticFilesAbs` handles the same way (the stat fails → 404).

## Next

- Tag a release (v0.1.30) so downstream modules can use `StaticFilesAbs`.
- Neither static mode resolves symlinks before the containment check: a symlink
  *inside* the served root that points outside it is followed. That is
  pre-existing behaviour in `StaticFiles`, inherited here, and arguably what an
  operator who placed the link intends — but it is worth a deliberate decision
  (`filepath.EvalSymlinks` on the candidate, then re-check containment).
- Static files are read fully into memory per request (`os.ReadFile`); carried
  in spirit from the earlier "streaming static files" non-goal below.
- Carried from 2026-0919-1443, still open:
  - `Request.Scheme()` is `""` for origin-form requests; could be derived from
    whether the connection is TLS.
  - The Host header is not validated: multiple `Host` headers are not rejected
    (RFC 9112 says 400), nor a malformed value.
  - `gofmt -l .` lists 11 pre-existing unformatted files, `Group.go` among
    them. This session added lines to `Group.go` without reformatting the rest,
    to keep the diff to the change.
- Closed since 2026-0919-1443: "tag a release (v0.1.29)" — done, and rosync
  bumped to it.
- Deliberate non-goals, carried: streaming static files / proxy responses,
  header count/size limits, `Connection: close` handling.
