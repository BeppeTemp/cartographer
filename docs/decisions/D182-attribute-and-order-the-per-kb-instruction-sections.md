---
topic: sync-provisioning
---

# D182 — Attribute and order the per-KB instruction sections

**Decision.** Each KB's snippet inside the shared instructions block is wrapped
in its own named markers, `<!-- cartographer:kb:<name>:begin -->` … `<!--
cartographer:kb:<name>:end -->` (`wrapKBSection`) — a marker family distinct
from the outer `cartographer:instructions:begin/end` pair, so the malformed-
block check that counts occurrences of the outer markers keeps counting
exactly one of each regardless of how many KBs contribute. Immediately before
its curated body, and only when curated content exists, `generateKBInstructions`
emits a one-line scope sentence naming the KB and stating that its directives
govern its own perimeter and that the more specific source wins on a conflict
with another KB's directives or a repository's own instruction file. No
markdown heading is introduced around the curated body — Cartographer wraps,
it does not edit, per [D61](D61-instructions-auto-generated-agents-section-curated.md)'s and [D154](D154-the-generated-steering-block-describes-what-this.md)'s principle that the KB
owns its prose. Section order follows the provider's explicit KB binding
(`clientconfig.ClientBinding.KBs`, [D170](D170-selection-before-the-merge-each-provider-is-projected.md)) when there is one — carried
into `provisioning.Apply` as the new `ApplyOptions.KBOrder` — falling back to
alphabetical by KB name otherwise; a KB present in the manifest but absent
from the binding sorts alphabetically after the declared ones, so a reorder
can never drop a section.

**Context.** `generateKBInstructions` emitted, per KB, a routing line, the
operational bullets, and the curated `instructions.md` verbatim, with nothing
marking where one KB's voice ended and the next began — a directive written
for one KB's perimeter reached the agent as an unqualified, session-wide rule.
Section order was alphabetical by KB name, an accident of directory naming
rather than a declared choice, even though position was already known to
matter: [D154](D154-the-generated-steering-block-describes-what-this.md) moved the generated preamble ahead of a curated body in
another language specifically because the first thing the model reads is the
worst position for an inconsistency, since it sets the expected output
language. With several KBs, whichever one happened to sort first occupied
that same position.

**Why [D171](D171-a-cross-kb-collision-is-an-error-not-an-alphabetical.md) cannot catch this.** `DetectCollisions` reports a
`kind`+`name` claimed by two `kb:` sources, and for `kind: instructions` the
`Name` *is* the KB name — unique by construction. Two KBs can never collide on
this kind, so the strict merge is structurally blind to a session-wide
directive smuggled into one KB's curated prose. D171's remedy for a real
collision is "rename one of them"; prose has no rename. This plan does not
attempt to adjudicate the meaning of two conflicting prose directives — that
is not mechanisable. It makes every directive attributable and scoped, and
makes precedence declared instead of accidental, which is what lets a model
apply the ordinary "more specific source wins" rule. Declared session-global
directives with a key — so that two KBs asserting the same key with different
values become a *detectable* collision under D171's "error, not warning"
stance — is deliberately left to a follow-up issue (#233): it needs a new
authoring convention in `instructions.md` plus plumbing in `DetectCollisions`
and touches the same file as this change, so it lands strictly after it.

**Rationale.**

- **A single managed region, rebuilt from scratch.** The per-KB markers live
  *inside* the existing outer block (`instructionsBlockBeginPrefix`/`End`,
  [D56](D56-instructions-kind-kb-imprinting-via-managed-block-in.md)), which stays the only region `writeInstructionsBlock` ever
  replaces; a removed KB still leaves no residue.
- **The scope sentence is generated content, not envelope.** It is written by
  `generateKBInstructions`, so it flows into `Artifact.ContentHash` like the
  rest of the block — existing clients see one instructions update on the
  first sync after upgrade, and it is self-limiting.
- **A reorder alone still has to rewrite the file.** Same KB set, same
  content hashes, only the sequence moved: invisible to `ComputeDiff`'s
  Added/Updated/Removed. `applyInstructionsGroup` compares the previous run's
  recorded section order — the sequence of `instructions` entries already
  carried in the incoming `Lock`, needing no new persisted field — against the
  newly computed one, and treats a mismatch as its own trigger
  (`instructionsOrderChanged`).
- **Uniform shape, not a conditional.** A single-KB client gets the delimiters
  and the scope sentence too, and a KB that opted out of the generated bullets
  (`preambleNoneRe`, [D154](D154-the-generated-steering-block-describes-what-this.md)) still gets both — the opt-out is about the
  bullets, not about attribution. The same file shape is what `doctor` and any
  future parser can rely on.
- **The recognizer matches a full line.** The per-KB markers are generated
  from the KB name, already sanitised by `config` before it reaches
  `BuildManifest`; curated content containing a similar-looking line embedded
  mid-paragraph is not itself a marker line, and nothing re-parses the body to
  look for one, so it cannot forge a section boundary.

**Consequences.** `fix:` — the generated block changes shape on every
provider, so every connected client shows one instructions update on the
first sync after upgrade. No configuration, CLI or MCP surface change; no KB
content is modified, ever — only wrapped.
