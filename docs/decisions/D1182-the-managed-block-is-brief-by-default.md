---
topic: sync-provisioning
---

# D1182 — The managed instructions block is brief by default

> **Amended by [D1246](D1246-routed-kb-clause-is-short.md)**: the routed `kb` clause of the routing sentence is shortened, so the sentence is no longer kept verbatim; the routed 9-KB block is capped at 8192 bytes.

**Decision.** Each KB's region of the managed block carries its routing sentence, its archives and the
names of its valid skills (sorted, at most 40, then `+<n> more (kb_guide)`). The operational bullets are
written once per block by the client, after the last region, with one rule (procedures, runbooks and
incidents are read whole with `concept_read`) and one line saying `kb_guide` returns the full guide, each
KB's own directives and its skills on demand. A KB keeps its `instructions.md` in the block only by
declaring `<!-- cartographer: block: full -->` on the leading lines of the file; `preamble: none` still
means "no generated operational text" and the brief region of such a KB starts with the single comment
`<!-- cartographer: preamble: none -->` so the client can tell. `status` and `sync` warn when a block is over
16 KiB (bytes/4 tokens, approximate); the warning informs and changes no state or exit code.

**Why.** The block is paid on every model call of every session and grew linearly with the bound KBs: nine
KBs measured at 40.7k tokens per call, one at 3.0k, against 0.23k for the routing sentence and four lines
pointing to `kb_guide`. A brief block without the skill names and without the "read whole" rule loses the
procedures (6/12 on procedure tasks against 12/12 with both), which is why the names and the rule stay.

**Alternatives rejected.** A global "full" mode or a `.cartographer.yaml` key or server flag: a second
place to set it, and the client does not hold the curated text (the server builds the artifact). The
per-KB directive travels with the KB and needs no key. Dropping the skill catalog: loses the procedures.
No cap on the catalog: one skill-rich KB would defeat the budget. A configurable warning threshold: one
constant is enough and cannot be mis-set.

**Consequences.** The `ContentHash` of every `instructions` artifact changed once, so every client re-syncs
and rewrites its block a single time on upgrade. The shared section is client-owned text with no artifact
hash: it is folded into the hash the lockfile records for the client-written tail (`PathsSectionHash`),
which rewrites the block once after a release that edits it and then converges. Mixed versions degrade
and never break (a new client skips its shared section when a region already carries `Operational
instructions:`; an old client writes the brief regions without bullets until upgraded). Known mixed-version limitation: a new client against an old server where every KB opted out with
`preamble: none` writes the shared section, because the old server sends no marker comment and no
`Operational instructions:` line; the next server upgrade removes it. Directive collisions
(D183) are scoped to what is in the artifact: two brief KBs may declare different values for one key, each
being read through `kb_guide` one KB at a time; `block: full` KBs still collide. A KB whose directives must
be in every session adds the `block: full` line (release note). The routing sentence naming every KB of a
connection is kept verbatim (it is what the 12/12 measurement used).

Measured budgets, pinned by `TestBlock_Budget` (measured size plus at most 10 %, with a hard cap): 5
archives and 12 skills per KB, none `block: full`, the written block is 3377 bytes for one KB and 6801
bytes for nine (about 0.85k and 1.7k tokens; the local-paths paragraph is about 0.9 KiB of it); the shared
section alone is 2146 bytes and one brief region 357 bytes. The end-to-end fixed-cost budget (block plus
`tools/list`) is #1206's, which may tighten these but not loosen them.
