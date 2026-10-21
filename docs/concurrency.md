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
observe either the complete batch or the exact pre-call KB state. The curated
index entries a batch asks for (`index_entry`, D714) are part of that: they
are written last, and a failure restores the indexes already patched before
the files and log are rolled back. It does
not open a nested git commit; a rolled-back call also leaves no commit,
since `gitWrap` only commits after the handler returns success.

`concept_transfer` (D760) and `artifact_move` (D782) are the two writes that hold **two** KB locks, and the
only write tools that do not go through `gitWrap`; both run the one transaction in `twokbmove.go`: it takes the git lock of the
source and of the target itself, in one global order (the KBs' root paths, whichever
side is the source), so `A → B` and `B → A` running together cannot wait on each
other. Inside both locks it syncs both KBs, writes and commits the target, then
writes and commits the source, then pushes both. A failure of the second step
leaves a revert commit on the target, never a reset. `artifact_move` is the same
for skills, hooks, agents, mcp servers and templates.

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
- **a holder never waits on itself.** `import` keeps the lock for its whole run and commits through the
  same path the server uses; a KB that already holds the lock goes straight to the write instead of
  polling its own lock until the timeout.
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

## How changes land

There is one way to write (D903): every successful write tool is wrapped by `gitWrap` and
produces one commit per logical operation, on the branch currently checked out in the KB clone,
whatever the configuration. No setting turns the commit off; the only KB that does not commit is
a folder that is not a git repository. Multi-file operations are committed together, individual
file replacement is atomic, and Cartographer creates no working branch, opens no pull request,
talks to no forge and never merges into `main`: it pushes to the KB's branch itself, and the review
that matters happens before the write, in the lint gate, `commit_gate`, the token roles and the
audit trail, with every write a revertible commit. Repository review policy belongs to the remote
hosting workflow, and a remote that protects the branch Cartographer writes to is answered in
§Remote synchronization.

Each commit names the writer as author and committer (D904, D950): the request's
`Cartographer-Author` header, else the identity configured for the KB in the server file
(`git.identity`, `git.identities`), else the token (`<token id> <cartographer@<host>>`). A forge with a membership push rule checks the
committer too, so it is the writer, not the server; the instance that recorded the commit is in a
`Cartographer-Host` trailer, and a write's message ends with a `Cartographer-Token: <token id>`
trailer, both appended by the server. A call that commits to two KBs (`concept_transfer`,
`artifact_move`) gives both commits the writer of its header; without one each KB's own configured identity applies. A commit that is not a write (the `external
changes` commit, a sync merge, conflict resolution) is by the KB's configured identity, else the
server. A sync that rebases local commits onto the remote gives each its author back as committer
(`gitx.RealignCommitters`). Format and validation: [control plane](control-plane.md).

A KB syncs exactly when it has a remote, and that branch must be the KB's **canonical branch**:
the `branch` of the `settings.yaml` committed on the remote's default branch when it sets one
(D335, D903), otherwise the remote's default branch (D264). `branch` lives only in that file, never
in the server's `kbs[]`, because it is a decision of the team that owns the KB, the same for every
server that mounts it; and it is read **only from the default branch**: the working copy's file
is ignored, so a change of `branch` committed on any other branch has no effect until it reaches
the default one. Without the key, Cartographer asks the remote on every sync (`git ls-remote --symref`, and
re-points `origin/HEAD` at the answer, since the one `git clone` wrote is never
refreshed by a fetch); when the remote's `HEAD` names a branch it does not have —
a self-hosted bare repository initialised with `master` and then pushed `main` —
the canonical branch is `main` if the remote has it, and unknown otherwise. Cartographer never
deletes or force-pushes a branch on the remote and never checks out a different branch in the
clone. The single exception to creating none is the first push of a KB to an **empty** remote,
which creates `main` with upstream tracking. A KB made for an empty remote by
`kb create --remote` is pinned to `main` and given its initial commit; a server never does that by itself (D906).

With `branch` set, the remote's default branch plays no part for that KB beyond holding the
file: the configured branch is the only one pulled and pushed, and the first push creates it on
the remote, with upstream tracking, when it is missing — on an empty remote as on one that already
has other branches. No other branch is ever created. A fresh clone is checked out on it (tracking
it when the remote has it, branched from the remote's default branch otherwise); an existing clone
is never switched. A name git would not accept is a finding in the file and never a branch.

Each commit subject is `<tool_name>: <resource>`, so the history of a KB is
readable as an audit trail. The resource is built from the arguments that
identify what the write touched: `path` for the artifact tools, `concept_id/path`
for the asset tools, and otherwise the first of `id`, `name`, `source_id`,
`contradiction_id` present in the call. A tool invoked with none of them commits
as the bare tool name.

The server git profile that used to sit beside this one (D117: a dedicated working branch and a
GitHub pull request per write, `pr_status`, `pr_finalize`) was removed by D903. A KB or a server still
configured with it does not mount, and says to unprotect the branch or set `branch`.

## Remote synchronization

Every git command Cartographer runs gets `LC_ALL=C`, over the process environment and any per-KB
variable: outcomes such as "nothing to commit" or a rebase conflict are recognised from git's English
messages, and a git built with translations prints them in the user's locale.

A KB with an `origin` remote syncs; one without never contacts anything (D903). The two timings
below are constants, not settings: a 30s freshness window and a 3s push debounce.

1. `SyncIn` fetches and runs pull/rebase/autostash before a write. A freshness
   window of 30s skips repeated fetches in a burst. A read-only tool
   never waits for it (D258): when the window has expired it is served at once from the
   local clone and starts the same refresh in the background, one per KB at a time, under
   the same KB lock; what that refresh pulls is visible from the next call on, and a
   failure there is non-fatal. Every fetch is bounded (15s, the whole git process group
   killed on expiry; a push likewise, 60s, `gitx.PushTimeout`), and no git command ever
   prompts for a credential (`GIT_TERMINAL_PROMPT=0`, ssh in batch mode, D1004), and after a failed one reads skip the fetch for 60s, so a remote
   that is down costs one bounded fetch rather than one per call; a write still fetches,
   synchronously (D237). When that fetch fails, the write proceeds only if `HEAD` has no
   commit missing from `origin/<branch>` — the base is as current as the last successful
   fetch left it: it commits locally, its push is deferred, the state becomes `degraded`
   with `last_error` starting `fetch failed, writing on local base`, and the write
   response carries that warning. With unpushed commits, or no tracking ref to compare
   against, the write still fails. The next successful sync clears the state and rebases
   any fork a colleague's push created meanwhile (D311).
2. The write and local commit run under the same KB lock.
3. `SyncOut` queues a push 3s after the last write (a burst of writes is one push).
   Sync-sensitive operations and graceful HTTP shutdown flush pending work. On a platform
   that freezes an idle instance — Cloud Run, which sets `K_SERVICE` — a debounced push would
   be stranded until the next request, so every push is made before the write returns, with no
   setting to turn on (D903).
4. A rejected push retries through fetch + pull/rebase + push, with bounded
   backoff; automatic force is never used. A push the remote refuses because the
   branch is protected (GitHub `GH006`, GitLab "protected branches", or a
   `pre-receive hook declined`, matched on the wording, not tied to a provider) is
   reported as such and not retried: `sync_status` is `failed` and `last_error`
   says to unprotect the branch or set `branch`; the write itself stays committed
   and safe in the clone, and the response carries the same warning.

`sync_status` is the authoritative read-only view of replication: it reports
whether the KB is not a git repository (`disabled`), has no remote, a push is pending, the latest
commit/push failed, or writes are blocked (`degraded`), together with the best
available ahead count. A failure is
cleared only after a real push succeeds. Offline writes remain local-successful;
in debounced mode their response reports `pending` because the later result is
not yet knowable.

Before its pull, `SyncIn` compares the checked-out branch with the
canonical branch. An empty remote has nothing to pull and the sync succeeds. A
different branch is refused with `ErrBranchDiverged`: the state becomes `degraded`,
the write is not performed, and `last_error` names the clone, both branches and the
recovery — merge the stray branch into the default branch on the remote, or check
out the default branch in the clone, then restart. With `branch` the
recovery is to check out the configured branch in the clone, or change the key on the
default branch; a configured branch the remote does not have yet is not a divergence,
there is just nothing to pull. Divergence is never repaired
automatically, since merging two histories is the operator's decision. Reads keep
working from the local clone (their background refresh logs the error). `SyncOut`
repeats the guard for anything that bypassed `SyncIn` (the freshness window, a
checkout changed in between): it pushes only a branch the remote already has, or
`main` to an empty remote (or the configured branch, which it creates), and
otherwise leaves the commit local and reports the same error. `sync_status` shows
`branch` and `remote_default_branch` side by side, with `branch_source` saying
whether the canonical branch comes from `settings` (the `branch` key) or from the `remote`;
the next sync that finds the clone back on the canonical branch clears the
`degraded` state.

Without a remote, synchronization is a no-op and local commits still work.

## Conflict registry

A rebase conflict is aborted immediately. Reserved files (`log.md`, `index.md`,
`_map.md`, `_archive.md`) are not concepts, so the server reconciles them itself
(D311):

- `data/log.md` carries `merge=union` in the KB's committed `.gitattributes`
  (written by `Init`; a KB created earlier gets it in a separate
  `chore: set merge=union for data/log.md` commit after its first successful
  sync in a process), so two concurrent `log_append`s merge into both entries.
- A conflict whose files are **all** reserved is settled in place by the local
  profile's pull (in `SyncIn` and in `SyncOut`'s retry): one merge commit of the
  remote SHA with `log.md` set to the union of both sides and the structural
  files to the remote's version — `index.md` is regenerated by the next write.
  The write goes on as if the pull had succeeded.
- A reserved file that conflicts **alongside** a concept stays out of the
  registry; the write's error names it, and it takes the same automatic
  resolution when the concept conflicts are finalized.
- A reserved-only conflict the server could not settle is registered with
  `kind: reserved` and its path as `concept_id`; `git_conflict_resolve` accepts
  `union` (only `log.md`), `ours` or `theirs` for it, never `edit`.

No message hands the agent a git command. For conflicting concept files the
server records local/remote SHAs in
`.cartographer/conflicts.json` and marks the working copy's concept
`status: degraded`.

The KB remains available for unrelated concepts. `conflicts_list` exposes the
registry and `sync_check` reports `open_conflicts`.

`git_conflict_resolve(concept_id, strategy, body?)` records one of:

- `ours` — retain the local version;
- `theirs` — retain the remote version;
- `edit` — use the supplied complete reconciled file;
- `union` — both sides' lines, for a reserved `log.md` entry only.

After every registered conflict has a resolution, one merge
transaction materializes the selected contents, commits and attempts the push.
On failure it aborts the merge and keeps the registry so resolution can be
retried.

## Optimistic content concurrency

`concept_read` returns normalized content hashes. Write tools that accept `if_match` reject an update with `stale_write` when the
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
