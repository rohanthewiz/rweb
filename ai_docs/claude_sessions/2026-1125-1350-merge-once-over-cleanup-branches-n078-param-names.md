# Merge the once-over branch, test sweep, branch cleanup, N-078 (parameter names)

Session id: `efa06a90-d452-41c6-8682-69d360d34fa2`
Date: 2026-11-25 · Branch: `master` · Start `9781e96` (master) / `1ec549c` (`roh/once-over-fab5.2`) → end `fbe5489` (+ this doc)
Run: the user asked to merge the feature branch into master and "test all the
things", then step by step: commit and push, fix `log.Fatal(s.Run())`, delete
the feature branch, clean up the agent worktrees, check and delete merged
remote branches (then `preprod`), review the last unmerged feature branch, and
port its fix. Every step was asked for; nothing was pushed or deleted
unprompted.

## Done

### Merge `roh/once-over-fab5.2` → master (fast-forward)

Master (`9781e96`) was an ancestor of the branch (94 commits ahead, 0 behind)
and recent master history is linear, so `git merge --ff-only`: master →
`1ec549c`, no merge commit. 123 files, +22,590/−1,013 (the whole once-over:
N-042 … N-077, N-057).

### Test sweep ("test all the things")

All green after one fix (below):

| Check | Result |
|---|---|
| `go build ./...`, `go vet ./...` on darwin, `GOOS=windows`, `GOOS=linux` | ok |
| `gofmt -l` over tracked `.go` files | clean |
| `go test -count=1 ./...` (4 packages; root ≈ 21 s) | ok |
| `go test -race -count=1 ./...` | ok, no races |
| `go test -count=3 -shuffle=on ./...` | ok (no flaky or order-dependent tests) |
| Test binaries cross-compiled for windows and linux | ok |
| Every benchmark once (`-bench . -benchtime 1x`): 17 funcs, 25 with subs | ok |
| 4 example modules (`hello`, `proxy_server`, `sse_hub`, `websocket`): build, vet, test | ok (no tests) |
| `hello` live (scratch copy on :18080, curl) | 8 documented routes, HEAD, 404, 405 ok |

Not run: **staticcheck**. `~/bin/staticcheck` was built with Go 1.23.2 and
refuses the `go 1.24.0` module; `go run …/staticcheck@2025.1.1` was blocked by
the permission classifier. Raised as N-079 (Validate).

Sequential, not parallel, for the suite and `-race`: the suite has
timing-sensitive tests (shutdown, linger, timeouts) that CPU contention can
fail falsely.

### `TestUnavailablePort` no longer needs :8080 free (`09b3bac`)

The only failure. Not a regression: the test (unchanged since January)
listened on a fixed `:8080` to make the server's bind fail, and another local
app was on 8080, so the test failed in its own setup. Now it listens on `:0`
(wildcard, so the server's wildcard bind is a true conflict; a
specific-address holder would not block a wildcard bind on BSD/macOS, where Go
sets `SO_REUSEADDR`) and asserts `Run` returns the bind error instead of
discarding `Run`'s result.

### `log.Fatal(s.Run())` → `if err := s.Run(); err != nil { log.Fatal(err) }` (`b2d59c3`)

Found live: since graceful shutdown, `Run` returns nil after SIGINT/SIGTERM,
so `log.Fatal(s.Run())` printed `<nil>` and exited 1 on every clean stop.
Fixed in 7 examples (`context_data`, `critical_css`, `hello`, `proxy_server`,
`sse_hub`, `stylus_css`, `websocket`; `cookies` and `groups` already did it
right), the README sample and 3 snippets in `ai_docs/SKILL.md` (the
`rweb-light-go-webserver` skill is a symlink to it). The graceful-shutdown
sections of README and SKILL.md now say why. Verified live: SIGTERM exits 0
with no `<nil>`; a second instance on a taken port prints `bind: address
already in use` and exits 1. (A first attempt at the port-taken case used
`nc -l`, which on macOS binds IPv4 only, so the example's dual-stack listen
succeeded; the second-instance approach is the real conflict.)

Pushed master: `9781e96..b2d59c3`.

### Branch and worktree cleanup

- **`roh/once-over-fab5.2`** deleted locally (`-d`) and on origin (tip
  `1ec549c`, in master).
- **Agent worktrees** under `.claude/worktrees/` (3) removed with
  `git worktree remove` (no `--force`; all clean, nothing ignored, no stashes).
  Their branches were not ancestors of master but `git cherry` matched every
  commit to an identical patch in master, so deleted with `-D`:
  `agent/n042-n044` (`54fc312`), `n043-ws-frame-reads` (`5315dd3`),
  `n045-n047` (`8987ddc`). Three leftover `worktree-agent-*` branches, all at
  `371ce8c` (in master), deleted with `-d`. None were ever on origin.
- **Remote branches**: 18 checked against `origin/master` (ancestry, then
  `git cherry` for patch-equivalence, then what the unmatched commits were).
  None had an open PR. Deleted 16:
  - 13 fully merged: `claude/add-write-bytes-methods-B0LfW` (`041eaa8`), `dev`
    (`be9a028`), `feat/stylus-middleware` (`024c374`),
    `roh/add-query-param-method` (`a323ad3`), `roh/add-sse-support`
    (`b7b02d9`), `roh/add-user-agent` (`4433fac`), `roh/add-websocket-support`
    (`06cd6d8`), `roh/fix-server-args-inter-requests` (`5e6556d`),
    `roh/improve-sse-handling` (`62f6541`), `roh/serve-assert` (`b7c46a4`),
    `roh/sse-tweaks` (`af34171`), `roh/static-file-serve-add-mime-type-by-ext`
    (`74bf80c`), `roh/use-fork-of-assert` (`b7c46a4`).
  - 2 superseded drafts (code not in master, features rebuilt differently):
    `roh/add-cookies-and-multiform` (`2698eff`; master has `Cookie.go` and lazy
    multipart) and `roh/add-proxy-feature` (`1bee03e`; master has `proxy.go`).
  - `preprod` (`13ba05e`): same commit as `roh/add-websocket-client`, nothing
    of its own, not referenced anywhere in the repo.
  - Restore any of them while the objects last:
    `git push origin <sha>:refs/heads/<name>`.
- Kept: `roh/add-websocket-client` (draft WebSocket client, not in master;
  raised as N-080) and `roh/radix-tree-parameter-validation` (ported below,
  then deleted, tip `031590d`).

Origin now has `master` and `roh/add-websocket-client`; locally only `master`.

### N-078: one name per parameter position (`3381bad`)

**Bug (on master until now)**: routes of one method that are the same up to a
`:param` share its radix node, which holds one name (`prefix`), and lookup
reports values under that name. `Tree.Add` stepped into the existing node in
`treeNode.end` without comparing names:

| Registered | Request | Handler got |
|---|---|---|
| `/users/:id`, then `/users/:userId/profile` | `/users/5/profile` | `[id=5]`, `Param("userId") == ""` |
| the same, other order | `/users/5` | `[userId=5]`, `Param("id") == ""` |
| `/posts/:year/:title`, then `/posts/:year/:slug` | `/posts/2024/t` | 2nd replaced 1st, key still `title` |

**Review of the branch** (`031590d`, 2025-11-26, 175 commits behind; done in
two scratch worktrees, one plain master, one master + cherry-pick, both
removed afterwards):

- The check in `end()` is correct. Cherry-picked onto master the full suite
  passed; on plain master only the branch's 3 expect-a-panic tests failed
  (consecutive params like `/:year/:title` already worked).
- A randomized probe (3,000 consistent-name and 3,000 random-name route sets
  in random order) showed: no false refusals; a panic exactly on a clash; a
  refused Add leaves earlier routes resolving correctly.
- The second copy of the check in `append()` was reached 0 times (vs 16,698
  for `end()`) over the router suite and the probe: `append` only builds new
  nodes once nothing in the tree matches. Its "reuse the node" step would add
  a duplicate static child if it were ever reached.
- Not mergeable as is: `arch/` held three `package main` debug programs (two
  with unused `otherMain` funcs); the `group_test.go` rewrite dropped the plain
  `/:id` test and carried commented-out code from another app (`authctlr`,
  `sermon_controller`); 3 files conflicted with master.

**Ported** (user's choice: port, not merge):

- `checkParamName(path, i, existing)` in `core/rtr/treeNode.go`, called from
  `treeNode.end` where an Add steps into an existing parameter node. Panics
  with a string naming the route (quoted) and both names (`:userId` / `:id`).
  The comment explains why names must match (diagram), why a panic (Get/Post
  return no error; httprouter, chi and gin panic on conflicting wildcards),
  why only `end` calls it, and that it runs before the Add changes the tree.
  Each method has its own tree, so names may differ across methods.
- Tests: the branch's 9 (`core/rtr/parameter_validation_test.go`) and
  `TestConsecutiveParameters` (`RadRouter_test.go`), unchanged; new
  `TestParamConflictMessageNamesRoute`, `TestParamConflictLeavesTreeIntact`,
  `TestParamNamesPerMethod`, `TestParamNamesRandomized` (the probe as a
  regression test: 2,000 + 2,000 sets, ≈ 0.06 s), and root-package
  `route_param_names_test.go` (`s.Get` clash, `Group` clash names the full
  joined path, consistent and per-method routes over `s.Request`). Six fail
  with the check removed.
- Docs: README (new "Route Parameters" section), SKILL.md (Route Parameters,
  v0.2.1+, with ✓/✗ examples and an upgrade note), CLAUDE.md (Key Patterns 5).
- Left out: the `append` copy, `arch/`, the `group_test.go` rewrite,
  `examples/param-validation` and the `hello` sermons route, and
  `PARAMETER_VALIDATION.md` (condensed into the docs).

Behaviour change: an app with a clash now panics at startup instead of serving
empty params. The branch's note shows one: a sermons group with `/:id` beside
`/:year/:filename`. Rename to match.

Pushed `b2d59c3..fbe5489` (`3381bad` + next-list `fbe5489`), then deleted the
branch on origin.

## Checks (N-078, final tree)

- gofmt clean; build and vet on darwin, windows, linux.
- `go test -count=1 ./...` and `-race`: ok.
- 4 example modules build and vet; router benchmarks smoke-run.
- All 9 examples started from built binaries: each registered its routes with
  no panic and was serving after 2 s, then stopped with SIGTERM. (A first
  attempt via `go run` under a perl `alarm` relied on 8080 being taken; the
  other app had exited by then, so `groups` bound 8080 and served for ~5 min
  until the task was stopped. Nothing left running; 8080 free.)

## Next

Closed: N-078. Declined: None. Raised: N-078, N-079, N-080. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
