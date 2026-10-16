---
topic: control-plane
---

# D782 — `artifact_move` moves artifacts between KBs on the two-KB transaction of D760, as a sibling tool

**Decision.** `artifact_move(target_kb, paths[], [reason])` moves whole artifacts
from the KB it is called on to another KB mounted on the same server: a
`skills/<slug>` or `hooks/<name>` directory, or one `agents/<slug>.md`,
`mcp/<slug>.json` or `templates/<slug>.md`. It is a tool of its own and not an
`artifacts[]` on `concept_transfer`. The two-KB state machine of D760 (git locks
in KB-root order, target committed first, compensating revert commit and never a
reset, HEAD and not the returned SHA deciding commit success, `Moved-To` and
`Moved-From` trailers) is extracted into `twoKBMove` (`twokbmove.go`);
`concept_transfer` and `artifact_move` are two clients of it, each supplying only
what travels. `twoKBMove.run` takes both git locks itself, so neither handler
spells the ordering out, and it holds the target commit to what the client said it
would write (below). Bytes are copied verbatim and the path is the same on both sides:
there is no rename, and `SKILL.md` is never rewritten. A unit already in the
target refuses the whole call. `instructions.md`, `paths.yaml` and `glossary.yaml`
describe the KB they live in and never move.

**Why.** D760 left skills out and named this as the follow-up on the same
mechanism. A skill is a directory of scripts and resources whose frontmatter `name`
must equal its directory (D191), so a move that renames would have to edit a file
the caller asked to move verbatim. The safety rules of D760 came out of review
(HEAD decides success, no revert after a failed discard, no gitignored files);
copying the state machine would have made every later fix a two-place edit, which
is how the two copies drift. Extracting it costs one refactor commit that the
existing `concept_transfer` tests, unmodified, hold to no behaviour change.

**Alternatives rejected.**
- A copy of `runTransfer` for artifacts: two transaction bodies to keep in step, and the review fixes of D760 would have to be re-found in the copy.
- `artifacts[]` on `concept_transfer`: one tool with two policy shapes (concept grants versus the artifact-write right) and a changed audit record, the objection D760 raised against `target_kb` on `concept_move`.
- Renaming on arrival (`target_path`): needs a rewrite of `SKILL.md` and of every path inside the skill that names itself, and a verbatim copy is the property that makes the move auditable.
- Overwriting an occupied slot: an `if_match`-style guard per unit would have to exist for each file of a directory; a move that can destroy a target skill is not a move.
- Rewriting references to a moved skill in the source: a path in prose or a script is not a link, and which KB it should name is the author's call. They are reported (`references_in_source`).
- Refusing on lint findings in the target: they depend on the target's `instructions.md` perimeter and sibling roots, and a move whose bytes validate should not be blocked by advice. Reported instead (`findings_by_kb`).
- Touching clients: a client bound to the source loses the skill at the next `sync`, which is what removing it from the KB means.
- Trusting the target's ignore rules only through a `git check-ignore` before or after the copy: it answers one question about the rules, and `git add -A` can leave a file out for reasons it does not see (an embedded `.git` turns a directory into a gitlink). The listing of the target commit is the ground truth, and `twoKBMove` checks it (below).
- Linting the whole target (`lint.Run`) after the copy: it walks every concept and builds the graph with both locks held, for findings about files the move did not touch. `lint.ArtifactFindings` runs the artifact checks only, for the moved files.
- One bound per artifact only: twenty artifacts each within it could still copy twenty times as much under both locks, and the locks are what the bound protects. The bound is the call's, so a single artifact meets it first and the refusal of a larger call says to split it.
- Bounding the reference scan only by its hit limit: a scan that finds nothing never reaches it and reads the whole source under both locks. A file and byte budget stops it, at the cost of a list flagged incomplete on a large KB.

**Consequences.** The tool registers only where `artifact_write` does
(`kbs[].allow_artifact_write`, or `allow_template_write` for `templates/` alone),
is `advanced` tier like `concept_transfer`, and is classed whole-KB: the caller
needs the whole-KB write grant on the source (the dispatch gate) and on the target
(`authorizeArtifactMoveTarget`, again under the locks). The target must hold the
artifact-write right itself, so a move cannot reach what `artifact_write` could
not. Like `concept_transfer` it needs a server mounting both KBs, both git-backed
with auto-commit, and on the routed endpoint a target inside the connection's KB
set. It refuses before any write: symlinks in a unit or its parents on either
side, non-regular files, a file over the 256 KiB artifact cap, a call whose
artifacts together exceed `artifactMoveMaxFiles` files or `artifactMoveMaxBytes`
bytes (`tools_artifact_move.go`; for one artifact over them the refusal gives the
limits, because the walk stops as soon as one is crossed and the actual size is
not known; for several it gives the running total and says to split the call),
a gitignored file
(in the source, the removal would destroy the only copy; in the target, whose
own ignore rules would leave the copy out of its commit; the check feeds
`git check-ignore` its paths on stdin, NUL-separated, so it does not grow with
the argument list), a structured file that fails
`validateArtifactContent`, a target slot whose parent already holds a name that
differs from it only by case (a checkout on a case-insensitive filesystem could
not hold both; checked on every filesystem, and the refusal names the existing
entry), duplicate paths, more than 20 units, and control
characters in a path (a newline could forge a commit trailer).

The refusal above is the clear message; it is not the guarantee. After the target
commit `twoKBMove` lists it (`gitx.TreePaths`) and compares it with
`TargetFiles`, the files the client said it wrote. A file the commit does not carry
(an ignore rule that Plan could not see) is a target-step failure: the target
commit is undone with a revert commit, the source is untouched, and the error
names the missing paths. On that path and on a failed target commit the planned
files git ignores are removed from the target tree too, because `git clean -fd`
keeps them and a retry would meet an occupied slot. Removing them is safe only
because Plan refuses an occupied destination, so every planned path was free.

It reports and
never refuses: placeholder keys the target's `paths.yaml` does not declare,
sops files, text left in the source that names a moved unit (the activity log is
history and is not scanned; the scan stops at `artifactRefsMax` hits or after
reading `artifactRefsScanFiles` files or `artifactRefsScanBytes` bytes, and flags
`references_truncated`, also when it skipped a file too large to read), and the
target's artifact lint findings for the moved paths (`lint.ArtifactFindings`, the
artifact checks only). The audit trail records `paths` as the joined list, bounded
in items and bytes (`joinAuditList`, `audit.go`), like every allow-listed
string-array argument. Anything that later changes the
transaction changes `twoKBMove`, and has to keep `concept_transfer`'s tests green
unmodified.
