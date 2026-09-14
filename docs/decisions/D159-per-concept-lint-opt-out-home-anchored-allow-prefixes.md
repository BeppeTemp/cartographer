---
topic: data-plane
---

# D159 — Per-concept lint opt-out, home-anchored allow prefixes, related size thresholds

**Status: implemented (2026-08-28).** Amends [D124](D124-machine-path-distinguishes-client-local-paths-from-a.md) and [D107](D107-declarative-lint-contracts-in-map-descriptors.md). Closes #180.

**Context.** Four findings an author could not legitimately clear, or a remedy the product itself
made unavailable.

1. **Lint fired on documentation *about* lint.** A concept documenting a `machine_path` false
   positive raised `machine_path`; one documenting the Mermaid/POSIX link collision raised
   `broken_link`. A KB's own "known false positives" page could not be written without generating the
   findings it described, and there was no per-concept escape: the only allowlist was per-map and
   covered path prefixes only.
2. **The `machine_path` allowlist rejected home-anchored paths.** `normalizeAllowPrefix` accepted
   only POSIX-absolute or Windows-drive-absolute prefixes, so `~/.ssh/config`, `~/.m2/settings.xml`
   and friends stayed flagged forever — twelve of 73 initial findings in the field. They are
   **conventional** paths: `~/.ssh/config` means "your ssh config" on every machine, exactly the
   reasoning `machinePathRe`'s own comment already applies to `/etc` and `/var`.
3. **The oversize remedy was structurally impossible for a satellite.** `concept_oversize` advised
   `concept_expand`, which requires exactly two segments, while the write path caps depth at three.
   The two largest concepts in the reporting KB were satellites, so the advice named an operation the
   product refuses.
4. **Two unrelated numbers decided "too big".** `conceptOversizeThreshold = 30000` advised a split;
   `conceptReadSizeGuard = 60000` switched `concept_read` to an outline. A concept between them was
   simultaneously "too big, split it" and "fine, here it is whole", and the lint constant's own
   comment claimed it "mirrors" the read guard — which is false, 30000 does not mirror 60000.

**Decision.**

- **The opt-out is per-concept frontmatter, `lint_ignore: [check, …]`.** Not per-map: the cases are
  individual pages, and a map-wide suppression would hide the same check on fifty concepts nobody
  inspected. Implemented as one `emit` closure at the top of the per-concept loop rather than a
  condition at each site.
- **`error`-severity checks cannot be suppressed** and route around `emit` on purpose:
  `missing_required_field` and `expanded_ambiguous` are contract violations, not judgements, and
  allowing an opt-out would let a KB declare its own contract void.
- **Directory-level checks are not suppressible either**, and that includes `orphan_asset`: it
  belongs to an expanded concept's asset set and is reported in the directory pass, so there is no
  single concept frontmatter that owns it. Listing it as suppressible would have been a promise the
  implementation does not keep.
- **An unsuppressible or unknown name in `lint_ignore` is itself a finding** (`lint_ignore_invalid`),
  with the reason: a typo that silently suppresses nothing is worse than no opt-out. A bare string is
  accepted as a one-element list, since the parser distinguishes the two and a bare string is the
  obvious authoring mistake.
- **`~/`-anchored prefixes are accepted, matched literally**, with **no home expansion anywhere**:
  expanding `~` would make the contract's meaning depend on the reader's home directory, the opposite
  of what an allowlist for portable paths is for. `~user/…` is rejected because the detector never
  produces that form, so such a prefix could never match. `matchesAllowedPrefix` needed no change —
  it already compares at segment boundaries — which a test asserts rather than assumes.
- **No shipped default list of well-known tool paths.** The field report offers it as an alternative;
  a built-in list is a policy every KB inherits without asking, and it would silently unflag
  `~/.ssh/id_rsa` in a KB that wants exactly that flagged.
- **The oversize suggestion is depth-aware**, and at maximum depth names what *is* available.
- **The two thresholds are related, not merged.** They answer different questions — should this be
  split for a human, can this be returned whole to a model — and merging them would couple lint
  policy to a client's token budget. `conceptOversizeThreshold` is now
  `okf.ConceptReadSizeGuard / 2`, in one expression, and the message names both bounds. The constant
  moved to `internal/okf` because two packages need it with no import cycle.

**Consequences.** Additive: one new frontmatter key, one widened allowlist form, two reworded
messages. **The numeric value is unchanged** (60000/2 = 30000), deliberately: the fix is coherence,
not a new policy, and a threshold change would belong to its own decision. A package-level test
asserts the two constants cannot drift apart again.
