---
topic: sync-provisioning
---

# D2162 — the lockfile records which plugin declares a provider

**Decision.** Every sync records, in the lock section of a provider a plugin declares, the plugin's name (`plugin`, `omitempty`). `plugin remove` uses it, when the plugin's approved tree cannot be read, to retire that section whole and drop the agent. A section without the key (a built-in provider, a lockfile written before it) is attributed by inference only when the evidence is unambiguous, and then only the entries whose `source` is `plugin:<name>` are pruned.

**Why.** An out-of-range or unfetched plugin could be neither synced nor removed (#2136): its tree was the only thing that named its providers, and the lock named none. The lock is the record of what we own and prune works from it, so the missing link belongs there.

**Alternatives rejected.** Storing provider names in the plugin's config entry: older installs do not have them either, and the approval record is the user's, not ours to extend for this. Fetching the out-of-range tree for removal only: it needs the network and a source that may be gone. Removing every unregistered provider: it deletes what no plugin is known to own.

**Consequences.** The key is `omitempty` and Cartographer 0.28 ignores it; a 0.28 write drops it, so after a downgrade and a sync the attribution comes from the inference (`pluginOfSection`: a provider unknown to the registry, one plugin named by the section's `plugin:` sources or the only plugin with an unreadable tree, no other plugin that could own it; otherwise the section stays and the command says so). The key is untrusted lock text like the rest of the file: a hand-edited lock can only redirect deletions to paths the lock already records, which `ReadLockFile` checks (non-local paths and untrusted base dirs are dropped) and the prune writes through `internal/safewrite`.
