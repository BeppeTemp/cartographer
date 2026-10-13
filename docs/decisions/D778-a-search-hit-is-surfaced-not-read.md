---
topic: control-plane
---

# D778 — A search hit is surfaced, not read: only `concept_read` stamps a read

**Decision.** The read-access log of D322 keeps two maps. `read` is stamped by
`concept_read` alone, `surfaced` by every `search` hit; navigation tools stamp
neither. The file (`.cartographer/read-access.json`) becomes
`{since, read, surfaced}`, `since` being when `read` began counting reads only.
`kb_status.read_access` keeps `tracked_concepts`, `oldest` and `newest` for the
reads and adds `since` and `surfaced` (the same three keys). A file in D322's
flat shape is migrated on load: its entries become `surfaced`, `read` starts
empty and `since` is the upgrade.

**Why.** D322 counted a hit as a read because a hit is a concept the agent will
see. It mostly is not: in one installation's transcripts (123 sessions, two
clients) searches returned 1,469 hits and 123 of them were then opened. The log
exists to tell which pages nobody reads any more, and counting hits makes every
page a search keeps offering look alive, the opposite of what it is for.
Localization studies of coding agents correct the same bias for the same reason:
a result list is a pointer, not an arrival (Bhola et al., *Code Isn't Memory: A
Structural Codebase Index Inside a Coding Agent*, arXiv:2606.22417, 2026).
Nothing consumed the log yet, so the change costs a telemetry migration, not a
behaviour anyone relies on.

**Alternatives rejected.**
- Dropping hits from the log: "offered, never opened" and "opened, never
  offered by search" are the two findability signals it can give, and both need
  hits kept, apart from reads.
- Migrating the flat file into `read`: it keeps the confusion this fixes for
  every concept surfaced before the upgrade.
- Deleting the flat file: each entry was at least handed to an agent, which is
  what `surfaced` says; only the claim that it was read is wrong.
- Stamping `surfaced` from `graph_context` too: its results are navigation
  (D322), and keeping `surfaced` to `search` keeps "found by search" meaning
  exactly that.

**Consequences.** An abandonment or staleness score reads `read`, and treats a
concept with no entry as unknown before `since`. A concept reached by id (a
link, an index, the instructions) has a `read` entry and may have no `surfaced`
one. Any new place that records access picks one of the two maps: a list of
pointers is never a read.
