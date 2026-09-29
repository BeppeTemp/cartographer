---
topic: sync-provisioning
---

# D282 — A key absent on this machine is ignored by name, and a sync warns in one line

**Decision.** `cartographer paths ignore <key>` records the placeholder id
(`repo:<key>` / `path:<key>`) in a new `ignored_paths:` list of `.cartographer.yaml`.
An ignored key is still resolved, still left verbatim when it does not, and still
recorded in the lock; it is only not reported: the sync warning, `status` and the
connect step skip it, and `paths list` shows it as ignored. `paths unset` removes an
ignore; `paths set` on an ignored key lifts it. The sync warning is one summary line
(`N placeholder(s) not resolved on this machine … cartographer paths list`); only the
keys not already unresolved in the lock the projection started from get detail, with a
reason shared by several keys printed once.

**Why.** D162/D262 made the warning actionable; on a client bound to several KBs it
became the opposite, ~110 lines per sync for keys that can never resolve here (a work
repo on a personal laptop), burying the sync outcome and teaching the reader to skip it.
The remedy is to let the operator acknowledge a key and to say only what changed.

**Alternatives rejected.**
- *A sentinel value in `paths:` (e.g. an empty string).* It reuses the map but every
  resolver (`repoindex`, `expand`, `cartographer resolve`) reads `paths:` as a path and
  would have to learn the sentinel; one missed reader turns "absent" into a bogus path.
- *`paths set <key> --absent`.* Same storage question, and it makes `set` mean two things.
- *A separate ignore list for the warning only, hiding it from `paths list`.* The issue's
  point is that an ignored key stays visible; hiding it makes a mistaken ignore invisible.
- *Store the unprefixed key like `paths:`.* One slug can be a repo and a path in different
  KBs; ignoring one must not silence the other, so the id keeps its kind.
- *Diff against the previous sync in a new lock field.* The lock already records
  `unresolved_placeholders`; `Apply` starts from that lock, so the prior set is free.
- *Drop the per-key detail entirely.* A newly unresolved key is exactly the one that
  matters and must be named the first time.

**Consequences.** An ignored key that later resolves is simply resolved; an ignore never
blocks a sync or changes `status`'s exit code (D75 WP3). The first sync of a projection
with no prior lock names every unresolved key once. A key mapped under `paths:` cannot
also be ignored. The TUI done message still counts ignored keys, since it reads the lock
only.
