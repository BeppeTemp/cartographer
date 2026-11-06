---
topic: sync-provisioning
---

# D1739 — An update never approves by itself, and a plugin whose tree cannot be read is not removed

**Decision.** `plugin update` moves `installed` only and leaves `approved` untouched, so the approved version stays active until a digest is approved; it prints the diff against the approved tree with every added permission flagged, and approves only on a typed `y` or an explicit `--yes`. Without a terminal and without `--yes` it stops after the diff (exit 0) instead of failing, so a scheduled update can never approve. `plugin remove` retires what the plugin installed in a fixed order (its providers through the disconnect path, then its lock entries in every other provider section through `PruneManaged`, then the config entry and the stored trees) using only paths the lockfile recorded, stops at the first refused prune, and refuses a plugin whose approved tree cannot be read or one of whose providers is not registered this run. Every lifecycle command that changes a state registers the plugin providers again.

**Why.** The consent is of a tree: a newer fetch is information, not consent, and a job that fetches must not be able to widen what runs. Retiring from the lockfile is the existing rule for what Cartographer owns; computing paths from a tree would delete what a tampered or newer tree names. A tree that cannot be read hides which providers the plugin registered, so removing the entry would orphan their MCP entries and files with no way left to find them.

**Alternatives rejected.** Failing a terminal-less `update` with exit 2 (a cron job would need `--yes`, which approves). Removing an unreadable plugin and leaving its lock entries behind (nothing would ever prune them). Pruning by walking the plugin's tree (paths from an unapproved tree).

**Consequences.** A purged store makes `remove` ask for a `sync` first, which fetches the approved digest again. `depends` stays check-only: `enable` and `disable` refuse, they never cascade.
