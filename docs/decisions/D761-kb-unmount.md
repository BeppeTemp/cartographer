---
topic: deployment-release
---

# D761 — `kb unmount` parks the clone outside discovery, backs up the config, refuses unpushed work

**Decision.** `cartographer kb unmount <name>` retires a KB from the local
service in one command: removes its `kbs[]` entry (after writing
`server.yaml.bak-<UTC timestamp>`), drops it from this machine's client and
workspace bindings, and moves the clone to `<data>/.unmounted/<name>` — or
deletes it with `--delete-clone`. It refuses, unless `--force`, when the clone
has uncommitted changes, commits ahead of its upstream, or no upstream or
`origin` to compare with.

**Why.** Removing the `kbs[]` entry alone does not unmount: the server mounts
every KB directory it finds under `data:` (discovery), so the next restart
would bring it back. A dot-directory is skipped by discovery and by `kb list`,
and keeps the local clone recoverable without a network. The refusals exist
because unmounting is the moment a never-pushed commit is lost.

**Alternatives rejected.** Refusing without `--delete-clone` (forces a delete
to retire a KB); always deleting (loses local-only state on a `--force`).
Editing other machines' configs (out of reach; D756 makes their stale URLs
degrade instead of fail).

**Consequences.** Like `kb rename` (D177) the command is offline and local and
lists, never edits, auth scopes and client pins naming the KB.
