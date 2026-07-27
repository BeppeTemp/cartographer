# Concurrency, commits and git synchronization

## Writer boundary

Each mounted KB has one in-process mutex. Write tools acquire it for the
filesystem change and the associated git operation, so one server process is
the sole writer of that working copy.

Do not mount the same local checkout into multiple writer processes. Separate
clones can synchronize through the same remote, but high-contention
multi-writer operation is not a supported scaling model; partition KBs across
instances instead.

## Local commit

When `git.auto_commit` is enabled, successful write tools are wrapped by
`gitWrap` and produce one commit per logical operation. Multi-file operations
are committed together. Individual file replacement is atomic.

Cartographer writes to the branch currently checked out in the KB clone. It
does not create working branches, open pull requests or merge into `main`.
Repository review policy belongs to the remote hosting workflow.

## Remote synchronization

When `git.sync` is enabled and `origin` exists:

1. `SyncIn` fetches and runs pull/rebase/autostash before a write. A freshness
   window can skip repeated fetches within the configured interval.
2. The write and local commit run under the same KB lock.
3. `SyncOut` queues a debounced background push. Sync-sensitive operations and
   graceful HTTP shutdown flush pending work.
4. A rejected push retries through fetch + pull/rebase + push, with bounded
   backoff. Cartographer never force-pushes automatically.

Without a remote, synchronization is a no-op and local commits still work.

## Conflict registry

A rebase conflict is aborted immediately. For conflicting concept files the
server records local/remote SHAs in
`.cartographer/conflicts.json` and marks the working copy's concept
`status: degraded`.

The KB remains available for unrelated concepts. `conflicts_list` exposes the
registry and `sync_check` reports `open_conflicts`.

`git_conflict_resolve(concept_id, strategy, body?)` records one of:

- `ours` — retain the local version;
- `theirs` — retain the remote version;
- `edit` — use the supplied complete reconciled file.

After every registered conflict has a resolution, Cartographer performs one
merge transaction, materializes the selected contents, commits and attempts
the push. On failure it aborts the merge and keeps the registry so resolution
can be retried.

## Optimistic content concurrency

`concept_read` returns normalized content hashes. Write tools that accept
`if_match` reject an update with `stale_write` when the stored content no
longer matches.

There are no advisory per-concept leases or session locks. Concurrency safety
comes from the KB mutex, content hashes and git conflict handling.

## Operator recovery

Cartographer does not promise automatic repair of arbitrary interrupted git
state. Before mounting a KB, the checkout should have no active merge/rebase
and no unexplained working-tree changes. Back up the repository and follow the
[deployment recovery procedure](deployment.md#backup-and-disaster-recovery)
for storage or remote failures.
