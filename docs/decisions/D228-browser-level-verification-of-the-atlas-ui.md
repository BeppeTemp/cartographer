---
topic: project-governance
---

# D228 — Browser-level verification of the Atlas UI

**Decision.** The embedded Atlas is verified by a Playwright suite
(`web/e2e/`, `make e2e-web`) that drives Chromium against `bin/cartographer`
serving its embedded bundle, over a deterministic fixture, on Linux, in the
`web` CI job. It asserts behaviour, accessibility and security — including
fine-grained non-disclosure end to end — and has no screenshot baselines and
no retries.

**Why.** The component tests run under jsdom against mocked fetches: they
cannot see the CSP, the SPA fallback, the auth chain, a real policy or a real
browser's storage and network. Those are exactly the properties the UI
promises — no token leaks, nothing contacted outside the origin, nothing
disclosed past a narrowed token — so the suite is the only place they are
checked as shipped. Its first run found three defects no other level could:
signing in without "remember for this tab" never worked (the boot replaced
the typed token with an empty tab store); a role narrowed to a map *and* a
type saw no collection at all, so the UI said no KB was visible (collection
visibility passed an empty type to a typed rule); and a finding on a map's own
`index.md` was attributed to a concept named after the map, so the
Observatory opened a 404 instead of saying there is no node.

**Alternatives rejected.** Testing against Vite's dev server: different bytes
under different headers, so a pass verifies nothing that ships. Screenshot
baselines: cross-runner pixel drift makes them the one thing between the
maintainer and a green pipeline, while the behavioural assertions carry the
contract; a visual layer can come later on its own evidence. Retrying flaky
tests: it hides the timing bug instead of fixing it. Running the suite on the
Windows leg: the harness is a POSIX script like `test/smoke` and `test/e2e`,
and the browser behaviour under test is not OS-specific. An allow-list of
external origins: failing on any foreign request catches a font, CDN or beacon
the day it is added.

**Consequences.** `make gate` stays Go-only; contributors touching `web/` need
Node and Playwright's Chromium to run the suite locally. The graph exposes
`data-motion`, `data-entry` and `data-camera` attributes, because the camera
and the entry settle live inside a WebGL renderer a test cannot otherwise
observe. The fixture generator must stay a pure function of its source — the
determinism assertion compares layouts computed from scratch and means nothing
over a moving node set.
