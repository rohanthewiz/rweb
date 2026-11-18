# N-065 declined, N-075: trusted proxy's scheme and host

Session id: `cb3c759d-8c84-478d-bf2e-8d35731ace4c`
Date: 2026-11-18 · Branch: `roh/once-over-fab5.2` · Start `80de5e6` → end `adf0536` (+ this doc)
Run: `/sl`, then the two Open items left waiting on the user's decision at the
end of the previous run (`2026-1114-1742-n076-n077-ws-read-after-close-bare-lf-blank-line`).
With both settled, Open is empty.

## Done

### N-065: relative `Location` and cookie `Path` through `Proxy` (declined)

- **Decision (user): option (a), Non-goal.** Absolute URLs on the target's
  host were already mapped (`0cd99bd`, `proxyRedirect`).
- Why it is not worth doing:
  - An upstream that does not know its prefix also writes root-relative
    links in its pages and scripts (`href="/login"`, `fetch("/x")`).
    Rewriting the redirect alone does not make it work.
  - The real fix is to set the upstream's base path (`root_url`,
    `BASE_PATH`), and then its relative `Location` is already right.
  - From the proxy, `/new` that needs the prefix cannot be told from
    `/api/new` that already has it. The item's "unless it already starts
    with the prefix" rule misfires on an upstream route that starts with the
    prefix's name, and a wrong guess makes `/api/api/new`.
  - A `Path=/` cookie often means the whole site, so narrowing it would break
    sessions without any error.
  - nginx and Apache leave both alone by default.
- Recorded in the Non-goals entry: if a real need comes up (a JSON API
  answering `201 Created` with `Location: /items/5`), use a general
  `ProxyOptions{ModifyResponse func(*http.Response) error}` hook, as in
  `httputil.ReverseProxy`, rather than rewrite flags.
- SKILL.md (Reverse Proxy) now tells apps mounted under a prefix to set
  their base path.

### N-075: a trusted proxy's `X-Forwarded-Proto` / `-Host` (`adf0536`)

- **Decision (user): on, using the trusted list.** That is
  `ServerOptions.TrustedProxies`, by default loopback and private ranges,
  the same rule as `ClientIP`. `NoTrustedProxies()` turns it off.
- **`Request().Scheme()`** now takes a trusted peer's `X-Forwarded-Proto`.
  - Order: absolute-form target, then the trusted header, then the
    transport. Resolved in `handleRequest`.
  - `forwardedProto` (`client_ip.go`) takes only the rightmost entry, across
    all lines. An appending proxy puts a client's forged `https` to the left
    of its own value, so the rightmost one is the true one.
  - Only `http` and `https` are accepted. An unknown rightmost value is not
    skipped in favour of an earlier entry, and empty list elements are
    skipped.
  - The header is scanned before the trust check, so a request without it
    pays nothing more than the scan.
- **WebSocket origin check** (`originAllowed`): also admits an Origin whose
  host is any entry of a trusted peer's `X-Forwarded-Host`
  (`forwardedHostHas`).
  - Every entry counts, because a chain of appending proxies puts the public
    host leftmost.
  - Safe on by default: the check only defends against pages in a browser,
    and a page cannot add headers to a WebSocket handshake.
  - `CheckOrigin` still decides alone.
  - An rweb `Proxy` in front of an rweb WebSocket upstream now works without
    `AllowedOrigins`.
- **`Host()` is unchanged on purpose.** nginx configs that set only `Host`
  pass a client's own `X-Forwarded-Host` through, which would let a client
  choose the host in an app's links (password-reset emails).
  `Forwarded` (RFC 7239) is not read, as for `ClientIP`.
- Behaviour change: behind nginx on the same host or in Docker, `Scheme()`
  reports `https` when nginx sends it.
- Tests: `forwarded_scheme_origin_test.go`:
  - parsing of both headers;
  - `Scheme()` over tcp and tls with trusted, untrusted and no-proxy servers,
    each followed by a keep-alive request without the header that must
    report the transport's scheme again;
  - synthetic requests (trusted unless the list is empty; an absolute-form
    target still wins);
  - origin over the wire: `s.WebSocket` and manual upgrade, chains, second
    line, port mismatch, untrusted;
  - `CheckOrigin` not overridden;
  - `Proxy` in front of a WebSocket upstream with default options: 101, and
    403 when the upstream trusts no proxy.
  - All four wiring tests fail with `Server.go` and `websocket.go` reverted.
- `wsUpstream`'s comment in `proxy_websocket_test.go` updated; it keeps
  `AllowedOrigins` so the tunnel tests do not depend on the new rule.
- Docs: SKILL.md (Request Information, WebSocket Safety Defaults, Reverse
  Proxy, Client IP), README (WSOptions table, trusted proxies), CLAUDE.md
  (client_ip.go), and the doc comments on `Scheme`, `Proxy`,
  `WSOptions.AllowedOrigins` and `setForwardedHeaders`.

## Checks

- `go test ./...`, `go test -race .`, `go vet ./...` (darwin, linux,
  windows), `gofmt -l .`.
- staticcheck v0.7.0, clean. It was installed into the session scratchpad
  with `GOBIN=<scratchpad> go install honnef.co/go/tools/cmd/staticcheck@v0.7.0`,
  since `~/go/bin` and `~/bin` still hold 2025.1.1 built with go1.23.2 (see
  N-061).
- Benchmarks:
  - Old, new and a control binary (only the `forwardedProto` call removed),
    run in turns, `BenchmarkKeepAliveHello` / `Browser`, three rounds.
  - Medians moved ±1–2.5% between rounds in both directions; in one round
    hello was fastest on the new binary. That is noise or code layout.
  - Measured directly: the scan costs 7 ns per request over the browser-like
    request's 9 headers, with no allocations (about 0.05% of a 15 µs
    request).
  - Allocations unchanged: 3 for hello, 11 for browser.

## Next

Closed: N-075. Declined: N-065. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
