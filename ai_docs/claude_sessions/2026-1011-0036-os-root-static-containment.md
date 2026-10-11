# Session: close the StaticContainSymlinks check-to-open race with os.Root (N-017)

Session ID: `ffcda65c-0451-4eaa-93d4-baa7d7e8fb38`
Date: 2026-10-11

One backlog item from `ai_docs/todo/next-list.md`, handed in as pasted
cats-todo text.

## N-017: race between `EvalSymlinks` and the open

### Before

With `StaticContainSymlinks` on, the static handler resolved the root and the
candidate with `filepath.EvalSymlinks`, required the resolved candidate to be
inside the resolved root, and then called `os.Open(candAbs)` separately.
Someone who can write in the served tree could swap a link between the
check and the open, and the open would follow it out of the root. The
comment said closing this needed `os.Root` (Go 1.24), which the module's
`go 1.23.0` directive ruled out.

### Go directive

- `go.mod`: `go 1.23.0` → `go 1.24.0`. `go get go@1.24.0 toolchain@none`
  dropped `toolchain go1.23.4`. 1.24 is the minimum for `os.Root`, so it
  keeps the widest consumer range. The local toolchain is 1.25.4.
- The four example modules with their own `go.mod` (`hello`,
  `proxy_server`, `sse_hub`, `websocket`) all `replace` rweb with `../..`,
  so they had to rise to 1.24.0 as well. `go mod tidy` there also moved their
  stale `element`/`serr` pseudo-versions to `v0.5.6`/`v1.3.0`, the versions
  the root already requires.

### Design: `openContained` (`static_contain.go`)

```
root.Open(rel) ── ok ─────────────────────────────────────► serve
      │
      └─ not found ──────────────────────────────────────► 404
      └─ other (escape, absolute link) ─┐
                                        ▼
   EvalSymlinks(root), EvalSymlinks(root/rel) → realRel
      │
      └─ fails, or realRel leaves the root ──────────────► 404
      └─ realRel inside ─► root.Open(realRel) ─ ok ──────► serve
```

- **The check is the open.** `os.OpenRoot(rootAbs)` and then
  `root.Open(rel)` walk the name from the root descriptor, resolving every
  link on the way and refusing any that leaves. A link swapped in has
  nothing to slip past.
- **Absolute links that point inside the root.** `os.Root` refuses every
  absolute symlink, even one that points back inside the root. The
  existing `TestStaticFilesContainSymlinks` fixture's `inside.txt` is one of
  these, and `ln -s "$PWD/…"` produces them routinely. The option's
  documented contract is that links resolving inside the root serve, so a
  refused open falls back to `EvalSymlinks` and reopens the link-free
  `realRel` **through the same Root**. A swap after the resolve is still
  contained: the worst it can do is turn a 200 into a 404. The root is
  resolved too, because `OpenRoot` followed links in the root's own path
  (macOS `/var` → `/private/var`, a `current` release link).
- **`ErrNotExist` skips the fallback**, so plain 404s don't pay for two
  `EvalSymlinks` walks.
- **The Root is opened per request, not per route.** A root that is a
  `current -> releases/N` link then moves with the link, as `os.Open` did.
  Opening once per route would pin the release that was current at
  registration. The cost is an extra open and close per request, only with
  the option on.
- **With the option off nothing changes:** still a plain `os.Open(candAbs)`.
- The `*os.File` returned outlives the Root's `Close`, so the streaming path
  from N-010 (`setBodyFile`) is unaffected.

### Files

- `static_contain.go` (new): `openContained`, `errStaticEscape`.
- `Server.go`: the static handler's pre-open `EvalSymlinks` block is
  replaced by a choice between `openContained(rootAbs, rel)` and `os.Open`.
  The lexical `filepath.Rel` check stays in front of both. The
  `StaticContainSymlinks` option doc notes the `os.Root` open.
- `StaticFilesAbs_test.go`:
  - `TestStaticFilesContainRelativeSymlinks`: relative links go through
    `os.Root`'s own path: `rel-inside.txt -> sub/deep.txt` gives 200,
    `rel-escape.txt -> ../secret.txt` gives 404, and `updir -> ..` gives 404
    for `updir/secret.txt` and for `updir` itself.
  - `TestStaticFilesContainSymlinksSwapRace`: a goroutine rename-swaps
    `root/d` among `real` (relative, inside), `../out` (relative, outside)
    and `<abs>/out` (absolute, outside, so the fallback runs) while 5000
    requests fetch `/files/d/x.txt`. The outside body must never appear.
    Checked against the old code by temporarily restoring HEAD's
    `Server.go`: it served the outside file at requests 246, 129 and 71 in
    three runs. The new code passes repeatedly (`-count=20`).
- `CLAUDE.md`: "Go 1.22+ required" → "Go 1.24+ required (`os.Root`, used by
  `StaticContainSymlinks`)".
- `ai_docs/SKILL.md`: the symlink bullet notes the `os.Root` open.
- `go.mod`, `examples/{hello,proxy_server,sse_hub,websocket}/go.{mod,sum}`.

### A corrected expectation

`/files/updir/root/allowed.txt` (where `updir -> ..`) was first expected to
404, but it serves `from-abs`. The path leaves the root and then resolves
back inside it. The option is defined on the *resolved* location, and the
old `EvalSymlinks` check served it too, so the test now expects 200.

### Found on the way

`examples/websocket` does not compile: `main.go:122` passes
`rweb.WithAddress(":8080")` and `rweb.WithVerbose()` to `rweb.NewServer`,
which takes a `ServerOptions` struct. At `e76054b` it failed even earlier,
asking for `go mod tidy`. Not caused by this change; raised as N-026.

`go test -race -count=1 ./...` passes; gofmt and vet are clean. The other
examples build.

## Next

Closed: N-017. Declined: None. Raised: N-026.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
