# Concurrency, commits and git synchronization

## Writer boundary

Each mounted KB has one in-process mutex. Write tools acquire it for the
filesystem change and the associated git operation, so one server process is
the sole writer of that working copy.

Do not mount the same local checkout into multiple writer processes. Separate
clones can synchronize through the same remote, but high-contention
multi-writer operation is not a supported scaling model; partition KBs across
instances instead.

`concept_batch` (D125) runs its whole multi-concept transaction under this
same single per-KB lock — it does not acquire a second one. Every operation
is validated and its content materialized in memory before the first byte is
written; if a write or the summary `log.md` entry then fails partway, every file the call already wrote (including any
implicit expanded-index stub a new directory triggered) and `log.md` are
restored to their exact pre-call bytes and mode before the error is
returned. Unlike `concept_move` — which documents that a `rewrite_links`
failure leaves the already-applied moves in place — `concept_batch` never
leaves a partially-applied batch on disk (and the search indexes follow the
files, D245, so they never see one either): callers
observe either the complete batch or the exact pre-call KB state. It does
not open a nested git commit; a rolled-back call also leaves no commit,
since `gitWrap` only commits after the handler returns success.

## The advisory KB lock

Inside one server process, git operations are serialised by a per-KB mutex. That mutex is blind to
any **other** process, and `cartographer import` is exactly that: a separate process writing into the
same directory the sync loop manages. The two interleaving corrupted the git index — 617 staged
deletions and 224 untracked entries for the same paths, with `HEAD` and the working tree verified
byte-identical — so since D155 both take an **advisory lock file**, `.cartographer.lock` in the KB
root (dot-prefixed, so every walker already skips it, and excluded via `.git/info/exclude` so it can
never dirty the working tree).

Advisory, not mandatory, on purpose:

- a lock whose recorded pid no longer exists is **reclaimed** with a note on stderr — a crash must
  not block a KB forever;
- the **server waits** for a bounded window and then proceeds under its mutex alone, reporting the
  condition: a lock held by something else must not stop the server from writing;
- **`import` fails fast**, naming the holder and how to stop it. An import is an operator action at a
  keyboard, and a silent ten-minute block is worse than an error. `--dry-run` writes nothing and never
  takes the lock.
- **contention waits, a failure does not.** Only "someone else holds it" is polled until the timeout;
  any other error from the lock call comes back immediately, naming itself, instead of costing a full
  wait and then reporting a holder that does not exist.

The exclusive lock is `flock(2)` on Unix and `LockFileEx` on Windows
(`internal/kb/lockfile_unix.go`, `internal/kb/lockfile_windows.go`). Windows byte-range locks are
mandatory rather than advisory, so the Windows side locks a **single byte far past the end of the
metadata**: the contending process must still be able to read the pid, timestamp and command line to
name the holder. `processAlive` follows the same asymmetry — on Windows a pid that exists but cannot
be opened counts as **alive**, because honouring a stale lock only costs a message while reclaiming a
live one is the corruption this section exists to prevent.

**An in-progress rebase makes sync refuse.** An aborted `pull --rebase --autostash` left a
`.git/rebase-merge` containing only an autostash, and from then on every MCP write failed with git's
own *"there is already a rebase-merge directory"* — a message that names no way out — until the
directory was removed by hand. `SyncIn`/`SyncOut` now detect the state first and return an error that
names the remedy: `rebase --continue`/`--abort` for a real rebase, or the `stash list` inspection
followed by removing the directory for an orphan autostash. **Detection plus instruction, never
automatic deletion**: the directory may hold a real operator rebase, or an autostash holding the only
copy of uncommitted work, and deleting another writer's state is the class of action that caused the
incident in the first place.

## The advisory client lock

The client side has its own advisory lock, for the same reason and with a different scope:
`.cartographer-client.lock` in the client's target directory, taken by every path that
read-modify-writes the lockfile or `.cartographer.yaml` (`sync`, `disconnect`,
`doctor --repair-hashes`, the TUI's sync actions). The concurrent writers are separate
`cartographer sync` **processes** — the session-start bootstrap hook starts one per agent session —
and before D172 the loser of that race silently dropped another provider's lockfile entry. It is an
`flock` on unix and a `LockFileEx` byte-range lock on Windows, released on every exit path, with a
bounded 30s wait that fails naming the file rather than proceeding. Details and the surrounding order of operations: `sync.md` §The client lock.

## Git profiles

`git.profile: local` is the default and the historical behavior. When
`git.auto_commit` is enabled, successful write tools are wrapped by `gitWrap` and
produce one commit per logical operation, on the branch currently checked out in
the KB clone. Multi-file operations are committed together, individual file
replacement is atomic, and Cartographer creates no working branch, opens no pull
request and never merges into `main`: repository review policy belongs to the
remote hosting workflow.

With a remote and `git.sync` on, that branch must be the KB's **canonical
branch**: the remote's default branch (D264). There is no configuration key for
it. Cartographer asks the remote on every sync (`git ls-remote --symref`, and
re-points `origin/HEAD` at the answer, since the one `git clone` wrote is never
refreshed by a fetch); when the remote's `HEAD` names a branch it does not have —
a self-hosted bare repository initialised with `master` and then pushed `main` —
the canonical branch is `main` if the remote has it, and unknown otherwise. In the
local profile Cartographer never creates, deletes or force-pushes a branch on the
remote and never checks out a different branch in the clone. The single exception
is the first push of a KB to an **empty** remote, which creates `main` with
upstream tracking. A clone of an empty remote (server bootstrap with `--init`) is
pinned to `main` and given its initial commit, exactly like `kb create`.

Each commit subject is `<tool_name>: <resource>`, so the history of a KB is
readable as an audit trail. The resource is built from the arguments that
identify what the write touched: `path` for the artifact tools, `concept_id/path`
for the asset tools, and otherwise the first of `id`, `name`, `source_id`,
`contradiction_id` present in the call. A tool invoked with none of them commits
as the bare tool name.

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
   window can skip repeated fetches within the configured interval. A read-only tool
   never waits for it (D258): when the window has expired it is served at once from the
   local clone and starts the same refresh in the background, one per KB at a time, under
   the same KB lock; what that refresh pulls is visible from the next call on, and a
   failure there is non-fatal. Every fetch is bounded (15s, the whole git process group
   killed on expiry), and after a failed one reads skip the fetch for 60s, so a remote
   that is down costs one bounded fetch rather than one per call; a write still fetches,
   synchronously, and fails (D237).
2. The write and local commit run under the same KB lock.
3. `SyncOut` queues a debounced background push. Sync-sensitive operations and
   graceful HTTP shutdown flush pending work.
4. A rejected push retries through fetch + pull/rebase + push, with bounded
   backoff. Server profile incorporates a concurrent working-branch update and
   replays it on the base; automatic force is never used outside finalization.

`sync_status` is the authoritative read-only view of replication: it reports
whether sync is disabled, no remote exists, a push is pending, the latest
commit/push failed, or writes are blocked (`degraded`), together with the best
available ahead count. A failure is
cleared only after a real push succeeds. Offline writes remain local-successful;
in debounced mode their response reports `pending` because the later result is
not yet knowable.

Before its pull, a local-profile `SyncIn` compares the checked-out branch with the
canonical branch. An empty remote has nothing to pull and the sync succeeds. A
different branch is refused with `ErrBranchDiverged`: the state becomes `degraded`,
the write is not performed, and `last_error` names the clone, both branches and the
recovery — merge the stray branch into the default branch on the remote, or check
out the default branch in the clone, then restart. Divergence is never repaired
automatically, since merging two histories is the operator's decision. Reads keep
working from the local clone (their background refresh logs the error). `SyncOut`
repeats the guard for anything that bypassed `SyncIn` (the freshness window, a
checkout changed in between): it pushes only a branch the remote already has, or
`main` to an empty remote, and otherwise leaves the commit local and reports the
same error. `sync_status` shows `branch` and `remote_default_branch` side by side;
the next sync that finds the clone back on the canonical branch clears the
`degraded` state. The server profile keeps its own validated branches and is not
subject to this check.

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

## Search index concurrency

The derived indexes are read and written by concurrent requests — under HTTP a
`search` and a `concept_write` land on different goroutines — and they sit
outside the writer boundary above: the KB mutex serialises the filesystem and
git work, not the in-memory index.

`search.Index` is a pair of plain maps with no synchronization of its own, so
the `liveIndex` wrapper in `internal/mcpserver` owns that discipline. Its read
lock is held for **the whole query**, not just long enough to load the index
pointer: ranking walks the same maps an incremental `add`/`remove` mutates, so
a search that ranks after releasing the lock races every concurrent write, and
the usual symptom is a `concurrent map read and map write` panic rather than a
stale result. The `allow` predicate a caller passes is invoked while that lock
is held and must not call back into the same `liveIndex`.

Incremental updates come from one place: the per-KB `searchReconciler`
(D245). Its mutex serialises reconciliations — a second concurrent `search`
waits, then finds an empty delta — and is taken outside both the KB mutex and
the graph-cache mutex, which `ConceptChanges` takes on its own. `reindex(full:
true)` rebuilds under the same mutex, so a rebuild and a reconciliation never
interleave.

## Graph cache concurrency

The link-graph cache (D241) has its own mutex on the KB, separate from the
writer boundary. It serialises validation — the stat walk and any re-parse —
and the publication of a new view. A view is immutable once published: every
graph reader takes the current one after validating and traverses it without
the lock, so a concurrent write can only make the *next* read see a newer
view. Writes are atomic renames, so a validation reads either the old file or
the new one, never a torn one. The maps a view exposes (`Links`,
`IncomingLinks`) are shared across readers and must never be mutated.

## Operator recovery

Cartographer does not promise automatic repair of arbitrary interrupted git
state. Before mounting a KB, the checkout should have no active merge/rebase
and no unexplained working-tree changes. Back up the repository and follow the
[deployment recovery procedure](deployment.md#backup-and-disaster-recovery)
for storage or remote failures.
