---
topic: control-plane
---

# D1866 — kb_review reports an old ID still cited, as an observation read from git

**Decision.** `kb_review` lists, as an observation with `check: moved_id_cited`, each page (concept or artifact text file) that still names outside a link an ID a recorded move renamed, with the current ID as the suggested name. Moves are read from the KB's git history (a commit whose subject starts with `concept_move`, `concept_archive` or `kb_repair` and that renames a concept file, within the last 200 rename commits); nothing is stored in the KB. Only a caller that sees the whole KB receives the item.

**Why.** `concept_move` reports the plain-text mentions of the old ID once, in its own result; a page written later from a stale excerpt is invisible, while a link to the old ID is already `broken_link`. The item states history (old ID to current ID), and the review list is cached caller-agnostic, so it is whole-KB only. A journal entry keeps a historical ID on purpose, which is what an observation (D947) is for, with its closing sentence "leaving them as they are is a legitimate decision".

**Alternatives rejected.** A new top-level review kind: its closing text asks for the state to be written, wrong for a journal. A `kb_repair` fix rewriting old to new: the rewrite is deterministic but a journal or a quotation must keep the ID (#1070: report, never rewrite). Reading `log.md`: it is parsed only for archives and supersedes, and the maintainer chose git. A new file or frontmatter recording moves: new state in the KB. The subject of the commit as the pair: it carries none (a batch move has no `source_id`), the git rename is the pair.

**Consequences.** The pair is git's rename pairing (`-M`, 50 % similarity): a page rewritten a lot in its move can read as delete plus add and is missed (a silent false negative), and identical pages moved in one commit may be paired crosswise, so a suggestion can name the wrong one of them. The history read is bounded on commits (the last 2000, then at most 200 rename commits), runs under a timeout, is extended incrementally while HEAD fast-forwards, and an unreadable history drops the observation, never the review. Moves older than that are not seen, and at most the 100 newest missing old IDs are scanned for. `concept_transfer` is out of scope (the successor lives in another KB). The Surveyor `doctor` task has no rule for the check, so it is left to an operator or a prompt task. A change to the subject of the three move tools must keep `gitx.MoveRenames` in step.
