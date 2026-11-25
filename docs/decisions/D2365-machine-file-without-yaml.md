---
topic: client-configurator
---

# D2365 — A machine file is read without `.cartographer.yaml`, never replaced by defaults

**Decision.** `clientconfig.LoadOrDefault(dir)` is the one fallback for a command that works, and may save, without a client config: `Load`, and on a missing `.cartographer.yaml` only, `Default()` plus what the machine recorded beside it: the machine file (read the fail-closed way, so an unreadable one stays unreplaced and a changed opt-out's `Save` errors) and the state file's agents, known KBs and mounts. Any other error from `Load` is returned (D1514). `connect`, the opt-out commands (`service sync-timer`, `service tray`), setup's server-URL seed, `plugin` and the commands that work without a config (`reindex`, `kb repair`) use it. `Load` and `Default` are unchanged. Amends D1728.

**Why.** `Load` returns `ErrNotExist` before it reads the machine file, and the write paths fell back to a bare `Default()` and saved it: the machine file was rewritten without the opt-outs, the approvals and `approvals_migrated`, and the last one re-ran the pre-approval migration, which defeats D1728's fail-closed intent.

**Alternatives rejected.** Having `Load` read the machine file on `ErrNotExist`: its contract (nil config plus `ErrNotExist`) is relied on by callers that treat absence as "not connected". Refusing to save when only a machine file exists: a first connect on such a machine would stop for a state the operator cannot fix.

**Consequences.** A new write path that tolerates a missing YAML calls `LoadOrDefault`, never `Load` plus `Default()`.
