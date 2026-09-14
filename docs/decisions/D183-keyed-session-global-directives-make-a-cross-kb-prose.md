---
topic: sync-provisioning
---

# D183 — Keyed session-global directives make a cross-KB prose conflict detectable

**Decision.** A curated `instructions.md` may declare a session-global
directive with a key anywhere in its body: `<!-- cartographer:directive:<key>:<value>
-->`, recognised as a full line (after trimming) so a KB can document the
syntax itself without triggering it — the same defensive shape as
`preambleNoneRe` and D182's `cartographer:kb:<name>:begin/end` markers, and
the same D162 metasyntax trap both had to account for. `<key>` and `<value>`
are each required to be non-empty and to contain no `:`, so the split stays
unambiguous; a malformed line (empty key or value, an extra `:` inside either
part) is not recognised and is left as ordinary prose. Lines inside a fenced
code block (` ``` `/`~~~`) are never eligible either, so a marker shown alone
on its own line as a worked example — the natural way to document the syntax
— does not declare anything; full-line matching alone only protects a marker
embedded mid-paragraph. `DetectDirectiveCollisions`
scans every `kind: instructions` artifact's generated content for these lines
and groups the declared `(key, value)` pairs by key; `MergeArtifactsStrict`
fails with a `*CollisionError` naming the key, every declared value, and the
KB that declared each, when two `kb:` sources in the same merge disagree on a
key's value. Two sources declaring the same key with the same value are not
reported — they agree, there is nothing to adjudicate. The recognised line is
**not** stripped from the rendered block: unlike `preamble: none`, which is a
control signal meaningful only to the generator, a directive's value is
content an agent reading the block may want to see verbatim, and stripping it
would cut against D182/D154's byte-identical delivery of the curated body.

**Context.** D182 made a cross-KB prose conflict *attributable* — an agent
reading the block now knows which KB said what — but left it undetectable by
the server or the client: for `kind: instructions`, `Name` is the KB name,
unique by construction, so `DetectCollisions` ([D171](D171-a-cross-kb-collision-is-an-error-not-an-alphabetical.md)) can never see
two KBs claim it. #233 asked for a narrower, mechanisable slice of that gap:
not adjudicating arbitrary conflicting prose (still not mechanisable, still
left to "the more specific source wins"), but letting a KB opt a specific
fact into structured comparison by giving it a key.

**Rationale.**

- **Comparison happens on the already-provider-scoped slice, no new
  plumbing.** `DetectDirectiveCollisions` takes the same `[]Artifact`
  `DetectCollisions` does, and every caller already narrows that slice to one
  provider's bound KBs before calling `MergeArtifactsStrict`
  ([D170](D170-selection-before-the-merge-each-provider-is-projected.md)/[D171](D171-a-cross-kb-collision-is-an-error-not-an-alphabetical.md)); two KBs that never reach the same client
  already cannot collide on `kind`+`name` for the same reason, and directives
  inherit it for free.
- **Extraction reads `Artifact.Files`, not a new wire field.** The `kind:
  instructions` artifact's content — generated per KB, curated body included
  — already travels through `sync_pull` in `Files[0].Content` for the on-disk
  write. Re-scanning that content at merge time needed no change to the
  `Artifact` struct, the `sync_pull` JSON shape, or a KB's materialized
  files: strictly additive. The generated wrapper prose around the curated
  body (fixed, hardcoded English, no HTML comments) can never itself match
  the marker, so scanning the whole generated artifact is equivalent to
  scanning only the curated section.
- **Fence-aware, not just full-line.** Full-line matching alone stops a
  marker embedded mid-paragraph but not one shown alone on its own line
  inside a fenced code block — the natural way to document a syntax by
  example. `extractDirectives` tracks fence state with the same pragmatic
  CommonMark subset `okf.headingEligibleLines` uses for the identical
  problem with markdown headings (kept as a small local duplicate rather
  than an export from `okf`: one caller, no other reason for the two
  scanners to share code).
- **Last declaration wins within one KB's own prose.** A KB repeating the
  same key twice with two different values in its own `instructions.md` is
  not a cross-KB collision — there is only one author to ask, and
  `extractDirectives` keeps the last one, silently, the same way a later
  assignment would read in ordinary prose.
- **Error, not warning, matching D171.** A warning about a directive the
  agent then actually reads is worse than a failed sync, for the same reason
  D171 gives for a structural collision: at that point the wrong answer is
  already silent. `CollisionError` grew a second section instead of a second
  error type, so a caller printing "Error: %v" still gets one self-contained,
  actionable report even when both kinds of collision fire in the same
  merge.
- **Visible, not stripped.** `preamble: none` is removed because it is a
  control signal aimed at the generator, not at the reader — leaving it would
  read as a stray, uninterpreted instruction. A directive's value is the
  opposite: it is the KB's own claim about a session-wide fact, and hiding it
  would make the rendered block disagree with what
  `DetectDirectiveCollisions` just verified about it.

**Consequences.** `feat:` — new authoring convention, and no existing curated
body matches it by accident (the marker's tight `<key>:<value>` shape, no
extra `:` or empty part tolerated, keeps a KB's own prose about markers from
tripping it, same as D162/D182). A deployment with two KBs bound to the same
provider that declare the same directive key with different values — working
today, silently, left to a human to notice and resolve via "the more specific
source wins" — starts failing its sync with a report naming the key, both
values and both KBs. Everything else is unchanged.
