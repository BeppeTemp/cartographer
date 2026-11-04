---
topic: control-plane
---

# D1638 — A KB's git token, a cached repo path and a repair commit are each identified by what the server controls

**Decision.** The injected token helper follows an empty `credential.helper` value, so it is the only helper git consults for that KB. A cached repo path is served only while the clone's `origin` is still the key looked up. `repair_revert` accepts a commit whose subject starts `kb_repair: ` or is exactly `auto-repair (background)`. Two `auth.tokens` entries with one secret value fail validation.

**Why.** Each check used to rest on something another party could change: the machine's own helper list, the content of a directory, a free-text `Reason` trailer, a silent last-wins overwrite. Each now rests on a value the server wrote or compared itself.

**Alternatives rejected.** Rejecting a reserved-prefix `reason` in the commit path: the subject check is enough and touches no write tool. Making `NewScopedTokenStore` return an error for duplicates: it changes a signature used widely, and config validation runs before it.

**Consequences.** Other helpers (a keychain) no longer answer for a KB that has a token file. A `Reason` of `auto-repair ...` on an ordinary write no longer makes it revertible. Two tokens that happened to share a value now stop the boot and must be made distinct.
