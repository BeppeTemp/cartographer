---
topic: deployment-release
---

# D227 — The embedded Atlas UI

**Decision.** Cartographer ships a read-only web Atlas inside the one binary,
served at `/ui/` in HTTP mode and disableable with `web.enabled: false`. The
frontend is React + Vite + TypeScript with Sigma.js over Graphology; its
production bundle is committed under `internal/webui/dist` and embedded with
`go:embed`. `make gate` stays Go-only: a pure-Go test compares a provenance
hash of `web/` against the one the bundle carries, and the frontend build and
tests run in a separate CI job scoped to the frontend paths.

**Why.** An operator had no way to see what a KB holds. The UI has to reach
every installation path — Homebrew, winget, the install script, the container,
`go install`, the native service — and `go install` cannot run npm, so the
bundle has to be in the repository. The cost is a generated artifact under
version control and a rebuild step a contributor can forget; the provenance
test is what makes forgetting loud instead of silent.

**Alternatives rejected.** Building the UI in CI and shipping it only in
release archives: it would leave `go install` without a UI, which is the path
most contributors use. Diffing the whole rebuilt bundle in CI: Vite output is
not byte-stable across Node patch releases, so the check would fail for reasons
unrelated to the change, and a gate that cries wolf gets bypassed — only
`provenance.json`, a hash of the sources, is compared. Putting Node in `make
gate`: it would make every pure-Go change depend on a toolchain it does not
need. Exempting `/` from authentication so a bare address reaches the UI: it
would weaken the middleware's default for every future endpoint, so the
courtesy redirect is applied outside the auth chain instead
(`webui.RedirectRoot`), while `/ui/` itself is exempt because it is the page
that asks for the token.

**Consequences.** The SPA fallback serves the shell for any unknown path below
`/ui/` and for nothing outside it; a route-boundary test asserts that `/mcp`,
`/health`, `/ready`, `/clients`, the RFC 9728 metadata and `/api/` all still
reach their own handlers with the UI mounted. The CSP is `default-src 'self'`
with no inline script and no eval, plus `worker-src 'self' blob:` — without
that one directive the force-layout worker is blocked and the graph silently
stops moving. Two failures found while building this are now design rules with
tests behind them: the appearance functions may never emit a node type stock
Sigma has no program for, because an unknown type throws inside the renderer
and takes the page down to a blank background; and a missing layout worker must
degrade to a still graph, never to a dead page. Graph layout stays
deterministic — the same KB state seeds the same coordinates — while the
resting "breath" is written on the graph's coordinates around that base, which
is kept aside so the cache never bakes one frame of the animation into the
layout.
