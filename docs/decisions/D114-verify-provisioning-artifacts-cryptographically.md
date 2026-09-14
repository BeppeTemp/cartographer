---
topic: sync-provisioning
---

# D114 — Verify provisioning artifacts cryptographically

**Decision.** A KB may configure an operator-controlled 32-byte Ed25519 seed.
The server signs a deterministic, domain-separated binary envelope containing
format version, KB source, kind, name, version and canonical content hash. The
client pins public keys out of band per KB and verifies both reconstructed file
content (including paths and executable bits) and detached signature before
materialization. `Signed` is verification output only; bundle trust is the
separate `BuiltIn` origin.

**Failure semantics.** An absent signature is unsigned and remains eligible for
the existing explicit trust policy. A malformed, invalid, source-mismatched or
unknown-key signature, a duplicate conflicting signature, or a content-hash
mismatch is tampering: remote sync fails before provider files or lockfiles
change. Multiple client pins support rotation: pin the new key, switch the
server seed, then retire the old pin after clients have synchronized.

**Rationale.** A policy boolean sent by a server cannot authenticate an artifact
received over a compromised transport. Canonical length-prefixed bytes avoid
making JSON serialization the signed protocol and bind signatures to a precise
KB artifact identity, preventing replay across KBs, kinds or names.
