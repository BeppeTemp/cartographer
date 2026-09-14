---
topic: project-governance
---

# D100 — Community surfaces: Pages from `docs/`, Discussions on, Wiki off

**Decision.** Three GitHub surfaces settled at once. **Wiki: disabled.** **Pages: enabled**, built by MkDocs Material from `docs/` (`mkdocs.yml`, workflow `.github/workflows/docs.yml`, `mkdocs build --strict`) — the markdown in `docs/` stays the single source, the site is only a rendering of it. **Discussions: enabled**, as the place for questions whose answer isn't documentation yet; when an answer stabilizes it is promoted into `docs/` through a PR.

**Rationale.** The wiki is a *separate git repo*: no PR, no `test` check, no review, and — decisive here — no way to change it in the same commit as the code, which is the project's central documentation invariant (D98, `docs/index.md` §Maintenance rules). It would have re-created, in a place nobody diffs, exactly the drift D98 removed. It adds nothing for agents either (they read `docs/` from the repo or by raw URL; `agent-install.md` is already the raw-URL-safe runbook, D97), and it would put runbooks that run `curl … | sh` and handle tokens somewhere editable without review. Pages gives the readability a wiki promises without giving up the PR flow.

**Consequences.** `--strict` makes a broken internal link a build failure, so moving a page breaks CI instead of the published site: the two `../` links out of `docs/` (`config.example.yaml`, `test/e2e/README.md`) became absolute GitHub URLs. The nav in `mkdocs.yml` mirrors the grouping of `docs/index.md`, which remains the canonical map — a new page goes in both.
