---
topic: transport-auth
---

# D130 — The handshake era is not retired: the plan was overtaken by D168

**Status: closed without implementing (2026-09-06). The number is retired and
must not be reused.**

**Decision.** The handshake era stays. Plan issue #118 reserved `D130` to remove
it — `initialize`/`ping` gone, a single supported revision, unconditional
envelope — and the work was written on `feat/retire-handshake-era`, never merged,
and closed as *not planned*. This record exists because three decisions cite
`D130` as the thing that specifies what happens after the retirement
([D128](D128-serve-the-2026-07-28-revision-alongside-the-handshake.md),
[D129](D129-report-the-protocol-era-and-client-identity-of.md),
[D133](D133-the-version-header-selects-the-era-by-value-not-by.md)), and a
reference that resolves to nothing is worse than no reference at all.

**Why.** The plan existed to remove a *cost*, not to remove a feature: serving two
eras meant every result was serialized conditionally on the era, and the duality
was paid on every response. [D168](D168-the-mcp-wire-format-comes-from-the-official-sdk.md)
replaced the hand-written wire implementation with the official Go SDK, which
serves every revision from `2024-11-05` to `2026-07-28` and decides the era per
request. With the SDK carrying both, the duality costs this project nothing — so
there is nothing left to retire, and no provider has to migrate first.

That mattered, because the plan's entry condition was never met and was not
close. The `/clients` roster on 2026-08-27 showed **every** row on the handshake
era, including two client versions released that same day; merging would have
turned a working KB into an unreachable one for every un-migrated provider, and
the failure would have surfaced at the agent, far from the server.

**Alternatives rejected.**

- *Merge the branch anyway.* The entry condition existed precisely to prevent
  this, and the roster evidence falsified it repeatedly. The branch is left
  unmerged on the remote as the record of what the removal would have been.
- *Treat 130 as a numbering gap.* [D206](D206-d163-was-a-copied-off-by-one-not-a-missing-record.md)
  did, and that was wrong: a gap is a number nothing depends on, and three
  records depend on this one. Left as a gap, those three would cite a deliberate
  hole — the exact defect D206 was written to fix, in the same archive.
- *Write the record the plan planned.* It would describe a retirement that did not
  happen, in the present tense, in an archive whose whole job is to say what is
  true. The honest record is this one.
- *Renumber the three citations to D168.* They are historical statements that were
  accurate when written — "deferred to D130", "D130 cannot be scheduled on a
  guess". Rewriting them would erase the fact that the retirement was planned,
  scheduled against evidence, and then made unnecessary, which is the part worth
  keeping.

**Consequences.** `D130` is retired for the same reason as
[D188](D188-retired-without-ever-being-written.md): released `CHANGELOG.md`
entries, git history and three decision records discuss it as the handshake-era
retirement, and a future `D130` about something else would make them read as a
description of it. Anything that assumed the era would be retired — the `era`
dimension of the client roster, the conditional serialization — stays as it is;
the SDK owns that choice now, and a change to it is a change to D168, not a
revival of this plan.
