---
topic: project-governance
amends: D1451
---

# D2416 — An accepted bug is the standing approval of its one fix, and the issue queue runs without the maintainer

**Decision.** A `bug` opened by a maintainer's session, the lander or a workflow's bot is `accepted` at creation (`make issue`, the lander's flaky issues); a bug from anyone else is reproduced by `cartographer-investigator` and accepted by the maintainer. An `accepted` `bug` opened by a maintainer or `github-actions[bot]` is the standing approval of `make ready` for its one `fix-<n>` change (an outsider's accepted bug still needs the maintainer's design comment and `make ready`), except when the triage's `Profile:` line names a profile whose row of `scripts/profiles.tsv` has standing `no` (today `process` and `security`), the fix is over 150 production lines or needs more than one change, it is breaking, it was rejected a third time, or the investigator returns more than one option that changes a public contract: then the session asks with the `needs-maintainer` label and one comment. The fix implementer never gets the number of an issue someone other than a maintainer or a bot opened, only verified findings. `make issues` lists the queue read-only, and `make ready` copies `Found-by:`/`Introduced-by:` from a trusted issue body.

**Why.** Only an `open` plan carried a standing approval, so every fix outside a plan session waited for a yes, and a defect found on the way waited for the next triage. 93% of bug issues carried no `Found-by:` line, so the weekly escape report counted them as `missing`. The cost, stated plainly: a session can open a bug, see it accepted at creation and queue its fix with no maintainer step; the label is then a policy, not a per-issue approval. The gates that remain are the `standing` column of `scripts/profiles.tsv`, the 150-line bound, the review tier and the train gate.

**Alternatives rejected.**
- Restrict the standing approval to bugs whose `Found-by:` is `gate`, `main-ci` or `nightly` (a failing test proves them): it leaves the defects a reviewer or an agent finds by reading the code waiting for a yes.
- A state store for the queue: the `fix-<n>` slug, the labels, the assignee and the `ready/`/`failed/` refs already say it.
- Let the implementer read an external issue whose design comment a coordinator posted: the coordinator posts with the maintainer's token, so public text would become an instruction.
- Queue a `test/quarantine.tsv` line under the same approval: quarantining stays the maintainer's call.

**Consequences.** `needs-maintainer` is a label that changes who acts, so the label table names it. The trust rule for an issue body is one: author association OWNER, MEMBER or COLLABORATOR, or the workflows' bot `github-actions[bot]`, in `ready.sh` as in `cartographer-fix-implementer`. A flaky issue of the lander is `accepted` and a repeat of the last 14 days is titled `flaky (<n>th): …`, so the queue takes it first.
