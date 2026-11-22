---
topic: project-governance
amends: D2040
---

# D2128 — The lander rebuilds the Atlas bundle after numbering a citation under web/

**Decision.** When a queued change adds a `D-new-<slug>` citation under `web/`, the lander builds the Atlas bundle again (`make -s web`, the build it already uses at pick time) after numbering the decision, and amends the result into the change. A failed build, or no Node, rejects the change with that reason. This amends D2040.

**Why.** The numbering rewrites the citation in tracked web sources after the author ran `make web`, so `internal/webui/dist/provenance.json` no longer matches `web/` and `TestBundleMatchesSource` failed in the train gate while the same commit passed alone. It costs a rebuild on the lander's machine for those changes only.

**Alternatives rejected.**
- Refuse a `D-new-` citation under `web/` at `make ready`: no Node needed, but it bounces every web change that names its own decision, the normal way to cite one.
- Run only the bundle generator on `web/`: it refreshes `provenance.json` but not the JS when the citation sits in a string literal.

**Consequences.**
- The trigger is the queued commit's own diff under `web/`, so it covers both rewrite sites (`number_decisions` and `remap_pick`).
- The lander now executes the queued commit's web build (`npm ci`, `npm run build`, `go run ./internal/webui/gen`) whenever the commit cites a pending decision under `web/`, under the lander's account and before the gate; the queue is reachable only by maintainers via `make ready`.
- Changes whose `web/` does not cite a pending decision never run the build.
