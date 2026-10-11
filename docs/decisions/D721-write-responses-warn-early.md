---
topic: control-plane
---

# D721 — A write says what lint will flag and where the commit is

**Decision.** A write response says what the agent would otherwise learn later and by
other means. `map_create` and `map_update` add a `warnings` entry when the title is one
`kb_review` would flag as `map_naming`. `source_register` adds one when the title is
flagged `title_quality` (length, decoration, forbidden terms) under the sources map contract. A write whose push
failed for a reason other than a conflict reports `committed`, `commit_sha`, `unpushed_commits` and a hint that the
commit is safe in the local clone. A write refused by D311, because local commits are
unpushed, says that nothing was written and that syncing failed, with the error.
`doctor_suggested` ignores `info` findings only while no doctor session has ever run.
`client bind` asks the server for the mounted KBs before it warns that a KB is unknown,
without rewriting the cached list (`sync` owns it).

**Why.** An agent importing a large source corpus learned each of these only
afterwards: from a `kb_review` run over hundreds of pages, from `git status` in a clone
it was told not to touch, or from a doctor nudge on a KB created minutes earlier. A
small model takes the nudge literally and stops to tidy instead of importing. The rule
already existed in each case; the agent just heard about it too late.

**Alternatives rejected.**
- Refusing a long map or Source title: the rule is a review heuristic, not an
  invariant, and a refused write costs a retry for something the agent can fix
  later.
- A timer that retries a failed push, so the message could say when the next
  attempt happens: it would be a new background mechanism, and today the next write
  or `sync` already retries.
- Leaving `info` findings in `doctor_suggested` and waiting a grace period after the
  KB is created: on a new KB `info` findings are the whole list, so the grace period
  would only delay the same false nudge.
- Refreshing the `client bind` cache on every bind, or persisting the fresh list: the
  cache belongs to `sync` together with the routed path.
- Excluding `info` findings from `doctor_suggested` for good: an established KB with
  only repairable info debt (`link_to_retired`) would never be nudged (D336).

**Consequences.** `MapTitleIssues` and `EffectiveTitleMaxLength` in `internal/lint`
are now the single home of these rules: `kb_review`, `title_quality` and the write
warnings must keep calling them, never copies. Every `warnings` array is advisory: a
client that ignores it behaves as before. Before the first doctor session
`doctor_suggested` turns on only for warning or error findings, or for review items;
afterwards every finding counts.
