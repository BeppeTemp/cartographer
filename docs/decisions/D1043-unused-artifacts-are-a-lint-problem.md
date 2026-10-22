---
topic: data-plane
---

# D1043 — Unused skills and agents are a lint problem

**Decision.** `artifact_unused` is a lint check again (a Health problem, in the
Artifacts category): a skill or agent that no client invoked since it was added,
or none for longer than `usage_stale_days`, is a finding on its file. The
Artifacts cards stop judging: they show a neutral "last used" line and link to
Health. This partly supersedes D947, which deleted the check; D947 itself stays
as history.

**Why.** D947 deleted it because "a usage signal ages with no page to fix". It
does have a definite fix: delete the artifact, or fix its description so a client
selects it, and a skill nobody invokes is weight in every session's catalogue.
The cost is accepted: a KB with an unused artifact does not pass a whole-KB
`gate_check` until it is dealt with.

**Alternatives rejected.** Keeping it an observation on the cards: Health, where
a maintainer looks for what to fix, never showed it. Bringing `usage_stale_days: 0`
back as an off switch: D906 and D907 forbid escaping a check, the floor stays 7.
Making it avoidable at write: adding a skill must not be refused for not having
been used yet.

**Consequences.** Three guards keep it honest: an empty usage store reports
nothing (no client scans there); an artifact is "never used" only once its birth
(the first commit that added its file) is older than the threshold, and one with
no history is skipped; a catalogue load is not a use. The finding depends on the
clock and on `.cartographer/usage.json`, so the lint cache stamp includes that
file alone and the UTC date. `lint.UsageState` stays the one classifier, shared
by Health, `kb_status.usage` and the cards. KBs with usage reports may show new
Health problems after upgrading.
