---
topic: client-configurator
---

# D2417 — The created-files ledger records every directory the client makes, and a prune removes only those

**Decision.** The `dirs` list of the created-files ledger (`.cartographer-created.json`) holds every directory a Cartographer writer made, a provisioning root or a level below one (`.kiro/settings`, `.claude/skills/<name>`), as slash paths relative to the base dir. `mkdirAllNoFollow` and the configurator's MCP writer record each component `safewrite.MkdirAllNoFollowCreated` made itself. `pruneEmptyDirs`, the configurator's parent removal after an MCP config goes and the dry-run `RootPredictor` remove an empty level only when the ledger lists it, and the climb stops at the first existing level it does not list. Roots keep their rules: announced, and never removed by an undo (`removeRoots=false`). A level that is already gone does not stop the climb.

**Why.** A user's own empty `.kiro/settings` was removed by `disconnect` without a word (#2403), because D63 removed every empty level below a root whoever made it and the ledger knew only roots. The ledger already answers "did we make it" for roots and files; extending it to every level makes the answer the same everywhere and removes the silent deletion of something the user owns.

**Alternatives rejected.**
- Keep D63 and announce each level a prune removes (D2409's rule for roots): the user's directory is still removed, only louder, and a dry run would have to list every skill directory.
- Close as intended and document it: leaves a user's directory removed on a `disconnect` that promised to undo only what `connect` did.

**Consequences.** Amends D2409 (its rejected alternative "record every directory" is adopted) and D63 point 1 (levels below a root go only when recorded). A level made by an older version is unknown to the ledger and stays, empty, after a prune: delete it by hand. The writers that call `safewrite` directly (blocktext's instructions file parent, `TrustCodexProject`, Cline's Documents folder, `.git/info`) record nothing, so what they make stays. An entry is validated as a local relative path and nothing more, because it only lets a climb from a managed file remove an empty directory it passes.
