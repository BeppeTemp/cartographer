# Concurrency, commits and git synchronization

## Writer boundary

Each mounted KB has one in-process mutex. Write tools acquire it for the
filesystem change and the associated git operation, so one server process is
the sole writer of that working copy.

Do not mount the same local checkout into multiple writer processes. Separate
clones can synchronize through the same remote, but high-contention
multi-writer operation is not a supported scaling model; partition KBs across
instances instead.

## Git profiles

`git.profile: local` is the default and the historical behavior. When
`git.auto_commit` is enabled, successful write tools are wrapped by `gitWrap` and
produce one commit per logical operation, on the branch currently checked out in
the KB clone. Multi-file operations are committed together, individual file
replacement is atomic, and Cartographer creates no working branch, opens no pull
request and never merges into `main`: repository review policy belongs to the
remote hosting workflow.

`git.profile: server` (D117) is an opt-in GitHub review boundary. It requires a
base branch, a dedicated working branch, GitHub owner/repository/API URL and the
name of an environment variable holding a token. On mount Cartographer fetches
`origin`, checks out or creates the dedicated branch from the remote base, and
refuses dirty, detached, ambiguous, mismatched or shared-working-branch checkouts.
The `origin` path must exactly match the configured `github_owner/github_repository`
for both HTTPS and SSH remotes (credentials are never logged). The base branch is
never a push target.

Each successful server-profile push creates or reuses exactly one open PR from the
working branch to the base. `pr_status` and `sync_status` expose the non-secret PR
identity and forge degradation. `pr_finalize(head_sha)` is an advanced operator
action: it requires the caller's current PR head, satisfied reviews and checks, a
fresh rebase and validation, then uses force-with-lease only on the working branch
before requesting a GitHub squash merge. After the merge the clean working branch is
reset to the fetched base for the next PR cycle.

If a forge timeout happens after the merge request, the profile enters
`merge_uncertain`: new writes are refused until startup or `pr_status` verifies the
merged PR's resulting commit on the base and safely resets the working branch. It
never opens a replacement PR during that recovery.

## Remote synchronization

When `git.sync` is enabled and `origin` exists:

1. `SyncIn` fetches and runs pull/rebase/autostash before a local-profile write;
   server profile rebases the dedicated working branch onto `origin/<base>`. A freshness
   window can skip repeated fetches within the configured interval.
2. The write and local commit run under the same KB lock.
3. `SyncOut` queues a debounced background push. Sync-sensitive operations and
   graceful HTTP shutdown flush pending work.
4. A rejected push retries through fetch + pull/rebase + push, with bounded
   backoff. Server profile incorporates a concurrent working-branch update and
   replays it on the base; automatic force is never used outside finalization.

`sync_status` is the authoritative read-only view of replication: it reports
whether sync is disabled, no remote exists, a push is pending, or the latest
commit/push failed, together with the best available ahead count. A failure is
cleared only after a real push succeeds. Offline writes remain local-successful;
in debounced mode their response reports `pending` because the later result is
not yet knowable.

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

After every registered conflict has a resolution, Local Core performs one merge
transaction, materializes the selected contents, commits and attempts the push.
On failure it aborts the merge and keeps the registry so resolution can be
retried. Server profile instead commits the selected content on its working
branch, rebases that branch onto the current base and updates the PR; it never
merges or pushes the base.

## Optimistic content concurrency

`concept_read` returns normalized content hashes. In server profile they reflect
the working branch after its latest base rebase, not a frozen PR-open snapshot.
Write tools that accept `if_match` reject an update with `stale_write` when the
stored content no longer matches.

There are no advisory per-concept leases or session locks. Concurrency safety
comes from the KB mutex, content hashes and git conflict handling.

## Operator recovery

Cartographer does not promise automatic repair of arbitrary interrupted git
state. Before mounting a KB, the checkout should have no active merge/rebase
and no unexplained working-tree changes. Back up the repository and follow the
[deployment recovery procedure](deployment.md#backup-and-disaster-recovery)
for storage or remote failures.
