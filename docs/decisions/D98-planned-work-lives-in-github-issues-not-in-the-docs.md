---
topic: project-governance
---

# D98 — Planned work lives in GitHub issues, not in the docs

**Decision.** The docs describe the **current state** only. A feature that is
not implemented — deferred, "future work", "Phase 3" — is a GitHub issue
labelled `enhancement`; the page that touches it keeps the *current* limit
(what the code does today, so a caller can rely on it) plus a link to the
issue, and nothing about the plan. At the time, completed milestones and known
bugs still lived in `roadmap.md`; D110 later removed that remaining mutable
status page.

Applied by extracting six backlog items from the prose into issues #51–#56 (fine-grained RBAC/permission-aware retrieval/compliance audit; `mcp` per-artifact approval + server-side allow-list; Server git profile; real signature verification; `mcp` stdio transport and `env` emission; per-ref secret least privilege), and by removing from the table the rows already marked ✅ implemented — history, which belongs to the git log.

**Rationale.** Backlog inside the docs rots in a way prose can't signal: a reader cannot tell "this exists" from "we intend this", and the two drift apart silently. Two real cases found while doing this: `docs/concurrency.md` described the Server git profile's PR flow affirmatively (a table row and an `if_match` paragraph) with the only disclaimer buried in an earlier sentence, and D3/D13 still announced as "future" two things implemented since (SQLite index → D32/D43, wiki-links → D72). Issues also have what prose lacks: a state, an assignee, a closing PR.

**Consequences.** `docs/index.md` §Maintenance rules gained the routing row
("feature not implemented → issue, never prose"). A plan issue (label `plan`)
remains the design→implementation handoff for *scheduled* work:
`enhancement` is the not-yet-scheduled backlog.
