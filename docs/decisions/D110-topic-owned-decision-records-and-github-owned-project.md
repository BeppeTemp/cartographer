---
topic: project-governance
---

# D110 — Topic-owned decision records and GitHub-owned project state

**Decision.** The monolithic `docs/decisions.md` becomes a short router to
topic-owned registers under `docs/decisions/`. Each D entry has one owner and a
stable `dNN` anchor; there is no duplicated global title/status table. The
obsolete roadmap page is removed. Issues own bugs, enhancements and plan
status; pull requests and releases own delivery; `CHANGELOG.md` summarizes
completed user-visible changes.

Plan issue titles reserve D numbers before implementation. Allocation therefore
checks both existing records and all plan issue titles. Implementation writes
the final entry to the one topic named by the plan. PRs in unrelated topics no
longer conflict merely because they both add decisions.

**Rationale.** The old register had grown beyond 1,700 lines while its duplicated
quick index stopped at D49, disagreed with later entries and hid stale product
claims. The roadmap repeated the same completed history while pointing mutable
work back to GitHub. Topic ownership keeps searches bounded and makes each
change update one current-state page, one rationale record and one external
status object at most.

**Consequences.** `docs/index.md`, MkDocs navigation, contributor guidance,
plan/implementation skills and the Plan issue template use the same routing.
Documentation tool-name validation walks nested current-state pages but skips
historical decision registers.
