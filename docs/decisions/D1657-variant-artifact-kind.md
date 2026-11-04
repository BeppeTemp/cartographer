---
topic: sync-provisioning
---

# D1657 — A client variant is a declarative KB artifact in `variants/`

**Decision.** A KB declares a fork of a supported client as `variants/<name>.yaml`, a new artifact kind `variant` that travels in the manifest like any other (hashed, signed) and is validated against the closed vocabulary of `internal/clientvariant`. An invalid descriptor is left out of the manifest with a named issue instead of failing it, `artifact_write` refuses it, and the `variant_invalid` lint check reports one that arrived by git. The kind is declarative: `KindInfo.Declarative` marks a kind the client consumes itself, with no destination row and not KB-only.

**Why.** Provider knowledge is compile-time (D137) and a closed fork cannot be named in this repository, so its facts must live in the team's own KB as data. Reusing the artifact pipeline gives provenance, hashing and signing for free, and the write gate keeps a bad file out of the KB. Leaving an invalid file out, unlike `mcp` which fails the whole manifest, means one typo does not stop the KB's skills.

**Alternatives rejected.**
- `clients/<name>.yaml`: `clients/` is a concept-map name in real KBs, so the scanners would claim a directory users already use for pages.
- Failing `BuildManifest` on an invalid descriptor, as `mcp` does: a descriptor is optional metadata for one fork, a skills outage is the wrong blast radius.
- A `Declarative` kind inside `artifactKinds` (the matrix): it would need a destination for every provider, which a variant by definition does not have.

**Consequences.** Older clients and servers ignore the kind (`FilterForProvider` drops a kind with no destination; `unsupportedKindWarnings` skips `variant`). Nothing applies a descriptor yet: registration, approval (D1141 is not amended) and detection are later changes. A new declarative kind must satisfy "in the matrix xor KBOnly xor Declarative" in the kinds tests. The binary now imports `internal/clientvariant`.
