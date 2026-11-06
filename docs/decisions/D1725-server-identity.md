---
topic: control-plane
---

# D1725 — the server git identity lives in git.identities.default, and a settings key can be Moved

**Decision.** The server file has one place for the git identity the server commits as: `git.identities`, whose `default` entry stands for every KB without one of its own. `git.identity` is retired as a `settingschema.Moved{From, To}` entry: the realignment moves its value to `git.identities.default`, and `Load` keeps reading it as that entry until then. `Moved` is a generic primitive of `internal/settingschema` (a `Registry.Moved` table next to `Removed` and `Off`), usable by any settings file.

**Why.** `git.identity` and `git.identities.default` said the same thing, and `IdentityFor` silently preferred the second when both were set. A move is not a removal: `Removed` drops a key and only names the replacement in text, so every move so far was hand-written (`token_env`). `Moved` makes the move one declarative entry that keeps the value and the comment a person wrote, and refuses the one case where a silent choice would lose data: both paths set with different values, which `Load` and the realignment both reject with a message naming the two keys (like `RefuseAny`). With equal values the old path is dropped.

**Alternatives rejected.** Keeping both keys and documenting the precedence: the defect is that two spellings exist, not that the order is unwritten. Warning and ignoring `git.identity` (a `Removed` entry with `Replacement`): it would drop the operator's identity from a file on a read-only mount, where nothing is ever realigned, and the forge would start rejecting commits. Moving only by hand in `announceConfig`: another one-off that the next move would copy.

**Consequences.** `Config.GitIdentity` is gone; `GitIdentities["default"]` carries the value and `IdentityFor` is the only reader. `Moved` paths are dotted mapping keys without list segments; a moved key's last known `Description` goes in the entry, as for `Removed`, and `TestDescriptionsAreRecordedWhenTheyChange` checks it. Old paths are read for at least one minor; dropping a `Moved` entry later is a separate change. The client file's own moves (plan `client-settings-layout`) reuse the primitive.
