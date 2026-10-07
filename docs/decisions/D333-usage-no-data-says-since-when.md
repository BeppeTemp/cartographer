---
topic: deployment-release
---

# D333 — An empty usage store says since when, and stays local state

**Decision.** With no usage report, `kb_status.usage` carries
`no_report_since`, the server process's start time, beside `no_data`.
`docs/deployment.md` names `usage.json` among the `.cartographer/` files a
restart on an ephemeral data dir loses. The store itself stays where D326 put
it.

**Why.** A container that clones its KBs at start onto an `emptyDir` empties
the store at every restart: `kb_status` then looked exactly like a KB no
client had ever reported to, right after an upgrade rollout. The start time
lets a reader tell "nothing in the last minute" from "nothing for weeks"
without a second store.

**Alternatives rejected.**
- *A configurable state dir separate from the KB clone*: a new config key and
  path rule for a signal D326 already calls disposable; an operator who wants
  it to survive mounts `.cartographer/` on the persistent volume, which also
  keeps the conflict registry.
- *Persist a "reports seen" marker elsewhere*: it would live on the same
  ephemeral disk, or in git, where D326 decided usage does not belong.

**Consequences.** `no_report_since` is a fact about the process, not about the
KB: on a persistent store with no reports it is still true, just not the
cause.
