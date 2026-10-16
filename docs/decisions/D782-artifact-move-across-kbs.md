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
what travels. Bytes are copied verbatim and the path is the same on both sides:
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

**Consequences.** The tool registers only where `artifact_write` does
(`kbs[].allow_artifact_write`, or `allow_template_write` for `templates/` alone),
is `advanced` tier like `concept_transfer`, and is classed whole-KB: the caller
needs the whole-KB write grant on the source (the dispatch gate) and on the target
(`authorizeArtifactMoveTarget`, again under the locks). The target must hold the
artifact-write right itself, so a move cannot reach what `artifact_write` could
not. Like `concept_transfer` it needs a server mounting both KBs, both git-backed
with auto-commit, and on the routed endpoint a target inside the connection's KB
set. It refuses before any write: symlinks in a unit or its parents on either
side, non-regular files, a file over the 256 KiB artifact cap, a gitignored file
(in the source, the removal would destroy the only copy; in the target, whose
own ignore rules would leave the copy out of its commit), a structured file that fails
`validateArtifactContent`, duplicate paths, more than 20 units, and control
characters in a path (a newline could forge a commit trailer). It reports and
never refuses: placeholder keys the target's `paths.yaml` does not declare,
sops files, text left in the source that names a moved unit, and the target's
artifact lint findings for the moved paths. Anything that later changes the
transaction changes `twoKBMove`, and has to keep `concept_transfer`'s tests green
unmodified.
