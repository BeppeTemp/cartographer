---
topic: client-configurator
---

# D2409 — The created-files ledger records the provider roots Cartographer created

**Decision.** The created-files ledger (`.cartographer-created.json`, D1269) gains a `dirs` list beside `files`: the provisioning root directories (`.claude`, `.codex`, `.config/opencode`, `.github`, ...) that Cartographer itself created, as slash paths relative to the base dir. Only the boundary set is recordable, and the set (`rootDirs`) now lives in `internal/configurator` next to the ledger, readable through `IsRootDir` and `RootDirs`. The ledger file goes with its last entry of either kind.

**Why.** After `workspace unbind` + `sync` or `disconnect` the roots Cartographer created stayed behind empty, because `pruneEmptyDirs` cannot tell an emptied root from the user's own (D63 point 1: roots are never removed). Nothing recorded which directories were ours. A record of the directories we created, kept where the files we created are already recorded, is what a later prune needs to remove only those. The set moves down to the configurator because `provisioning` imports it and the contract package imports it, never the reverse.

**Alternatives rejected.**
- Record every directory the writers create: the levels under a root already follow D63 (removed when empty, whoever made them), and a ledger applied to them would stop removing, after an upgrade, levels a `disconnect` removes today.
- Keep the set in `provisioning` and pass it to the ledger: the configurator cannot import provisioning, and the ledger must validate what it reads.
- Trust the ledger as read: it is untrusted input like the lockfile; an entry that is empty, absolute, contains `..` or is not a root is dropped on read, so a hand-edited ledger cannot name `$HOME` or `.ssh`.

**Consequences.** This change adds the format and the API only (`NoteCreatedDirs`, `WasDirCreated`, `ForgetCreatedDir`, `ReconcileCreatedDirs`); nothing records or removes yet. A directory absent from the ledger is unknown and is never removed. An older client rewrites the ledger from its `files` map and drops `dirs`, which only makes the directories unknown again. Adding a root means adding it to `rootDirs`, with `TestEveryDestinationRootIsAPruneBoundary` as the net.
