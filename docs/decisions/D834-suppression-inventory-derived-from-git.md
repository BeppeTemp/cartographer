---
topic: data-plane
---

# D834 — The suppression inventory counts in lint and reads its dates from git

**Decision.** A whole-KB lint run counts, per `lint_ignore` / `lint_accept`
entry, the findings it dropped (`lint.RunWithSuppressions`). A `lint_ignore`
entry that hid nothing is reported as `lint_ignore_invalid`, as a stale
`lint_accept` already was. `kb_status` always carries `suppressions: {entries,
hidden, stale}` and, with `suppressions: true`, a list whose `added`, `commit`
and `reason` are read from the git history of the file that holds the entry.

**Why.** There was no way to see what a KB suppresses, or whether an old entry
still hides anything: a dead entry silently swallows the next real finding of
that name. The date and reason of an entry already exist, in the commit that
added it (the `Reason:` trailer, D272); storing them next to the entry would
add a second copy that can drift, and would break "no review state besides the
KB itself".

**Alternatives rejected.**
- *An expiry or `reason` field in the frontmatter*: new state to keep in step
  with the commit, and a contract change for every KB.
- *A new check for a stale `lint_ignore`*: `lint_ignore_invalid` already means
  "this entry does nothing" for `lint_accept`; one finding is one meaning, and
  `kb_repair` and the doctor already treat it.
- *A new tool*: the summary belongs in the session-start call; only the list,
  which walks history, is opt-in.
- *Reporting stale on scoped runs*: a scoped run does not evaluate every check,
  so it would call fine entries stale.

**Consequences.** The `kb_review` kinds are never reported stale (a lint run
cannot see what they would hide); the set lives next to `perConceptChecks`
(`neverStale`) and a check produced only by the write path would join it. KBs
with dead `lint_ignore` entries get new `lint_ignore_invalid` warnings after
the upgrade. `added` is omitted for an entry older than the last 200 commits of
its file.
