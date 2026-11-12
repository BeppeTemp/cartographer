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
returned (the stub file is what is snapshotted, so the new directory is pruned with it). Unlike `concept_move` — which documents that a `rewrite_links`
failure leaves the already-applied moves in place — `concept_batch` never
leaves a partially-applied batch on disk (and the search indexes follow the
files, D245, so they never see one either): callers
observe either the complete batch or the exact pre-call KB state. `concept_expand` follows the same rule on a smaller scale: it reads and rebases the concept first (CRLF content included), writes `<id>/index.md`, then removes `<id>.md`, and removes the directory it created on any error, so a retry never answers `already_expanded` for a concept that is still flat. `map_update` computes the retitled `index.md` before it writes `_map.md`. The curated
index entries a batch asks for (`index_entry`, D714) are part of that: they
are written last, and a failure restores the indexes already patched before
the files and log are rolled back. It does
not open a nested git commit; a rolled-back call also leaves no commit,
since `gitWrap` only commits after the handler returns success.

**A handler that fails after writing leaves nothing behind** (D1549). When a wrapped write returns an
error (a Go error or an application error) and the working tree was verifiably clean before the handler
ran, `gitWrap` restores the tree to HEAD under the lock it already holds: tracked changes reset,
untracked files the handler created removed, ignored files kept. Without it, the half-done state (applied
moves, rewritten concepts) was committed by the next write as `external changes`, outside the write gate.
If the pre-commit of external changes failed, or a conflict is open, the tree is not provably clean and
nothing is discarded. A handler whose work succeeded and whose commit failed is not discarded either.

`concept_transfer` (D760) and `artifact_move` (D782) are the two writes that hold **two** KB locks, and with
`git_conflict_resolve` (one KB: it takes the KB lock itself and finalizes merge, commit and push of the
conflicts it settles) the only write tools that do not go through `gitWrap`; the first two run the one transaction in `twokbmove.go`: it takes the git lock of the
source and of the target itself, in one global order (the KBs' root paths, whichever
side is the source), so `A → B` and `B → A` running together cannot wait on each
other. Inside both locks it syncs both KBs, writes and commits the target, then
writes and commits the source, then pushes both. A failure of the second step
leaves a revert commit on the target, never a reset. `artifact_move` is the same
for skills, hooks, agents, mcp servers and templates.

## The advisory KB lock

Inside one server process, git operations are serialised by a per-KB mutex. That mutex is blind to
any **other** process, and `cartographer import` is exactly that: a separate process writing into the
same directory the sync loop manages. Two processes interleaving git operations on one index corrupt
it, so both take an **advisory lock file** (D155), `.cartographer.lock` in the KB
root (dot-prefixed, so every walker already skips it, and excluded via `.git/info/exclude` so it can
never dirty the working tree).

The kernel lock is the only authority:

- **a busy lock is never reclaimed.** While the flock is held its holder is alive by definition; the
  pid recorded in the file is only text for the `LockHeldError` message (it can be a pid from another
  user or namespace, or not yet written). Nothing removes `.cartographer.lock`: a waiter that did so
  created a second inode and both processes held "the" lock. A crash needs no reclaim — the kernel
  drops the lock with its holder;
- the **server waits** for a bounded window and then proceeds under its mutex alone, reporting the
  condition: a lock held by something else must not stop Meridian from writing;
- **`import` fails fast**, naming the holder and how to stop it. An import is an operator action at a
  keyboard, and a silent ten-minute block is worse than an error. `--dry-run` writes nothing and never
  takes the lock.
- **a holder never waits on itself.** `import` keeps the lock for its whole run and commits through the
  same path Meridian uses; a KB that already holds the lock goes straight to the write instead of
  polling its own lock until the timeout.
- **contention waits, a failure does not.** Only "someone else holds it" is polled until the timeout;
  any other error from the lock call comes back immediately, naming itself, instead of costing a full
  wait and then reporting a holder that does not exist.

The exclusive lock is `flock(2)` on Unix and `LockFileEx` on Windows
(`internal/kb/lockfile_unix.go`, `internal/kb/lockfile_windows.go`). Windows byte-range locks are
mandatory rather than advisory, so the Windows side locks a **single byte far past the end of the
metadata**: the contending process must still be able to read the pid, timestamp and command line to
name the holder.

**Network git commands are all bounded.** Every `internal/gitx` command that talks to a remote (clone, ls-remote, fetch, push, the probe's shallow fetch) is built by `netCommand`, which kills the whole process group on cancel and sets `WaitDelay`; hand-building one let a deadline kill only `git` while `git-remote-http` held the pipe open. After `Fetch`, `SyncIn` and the `SyncOut` retry rebase locally (`RebaseOntoAutostash`): one network round-trip, none under the KB git lock outside the fetch deadline.

**A remote URL's credentials never reach error text.** `gitx` names a remote in its errors (clone, ls-remote, the probe, fetch, pull, `remote add`) and in the bootstrap and `kb`/`setup` messages only as `gitx.RedactRemoteURL` renders it (userinfo, query and fragment dropped, as `kb_status` shows it), and `RedactRemoteText` scrubs the raw URL from git's own output; the Meridian log and an agent transcript therefore never hold a `https://user:token@host/...` remote.

**An in-progress rebase makes sync refuse.** When `.git/rebase-merge` or `.git/rebase-apply` exists
(a real rebase, or an orphan autostash an aborted `pull --rebase --autostash` left behind), `SyncIn`/`SyncOut`
detect it first and return an error that names the remedy, where git's own *"there is already a
rebase-merge directory"* names none: `rebase --continue`/`--abort` for a real rebase, or the
`stash list` inspection followed by removing the directory for an orphan autostash. **Detection plus
instruction, never automatic deletion**: the directory may hold a real operator rebase, or an
autostash holding the only copy of uncommitted work, and deleting another writer's state is not the
server's call.

## The advisory client lock

The client side has its own advisory lock, for the same reason and with a different scope:
`.cartographer-client.lock` in Compass's target directory, taken by every path that
read-modify-writes the lockfile or `.cartographer.yaml` (`sync` and `upgrade-repair`, which load
the config only after taking it, `connect`, `reconnect`, `disconnect`, `doctor --repair-hashes`, `paths set|unset|ignore`,
`approve`, `client`, `workspace bind|unbind`, `kb rename`, `kb unmount`, `service` opt-outs). A command loads after taking the lock, never before (`withClientConfig`). The concurrent writers are separate
`cartographer sync` **processes** — the session-start bootstrap hook starts one per agent session —
so the lock keeps one process from overwriting another provider's lockfile entry (D172). It is an
`flock` on unix and a `LockFileEx` byte-range lock on Windows, released on every exit path, with a
bounded 30s wait that fails naming the file rather than proceeding. Details and the surrounding order of operations: `sync.md` §The client lock.

## How changes land

There is one way to write (D903): every successful write tool (all but the three that run their own
transaction, above) is wrapped by `gitWrap` and
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
(`git.identities`), else, on a local server, the git identity of the user running it (a KB with a remote and none is refused, D1974), else the token (`<token id> <cartographer@<host>>`). A forge with a membership push rule checks the
committer too, so it is the writer, not Meridian; the instance that recorded the commit is in a
`Cartographer-Host` trailer, and a write's message ends with a `Cartographer-Token: <token id>`
trailer, both appended by Meridian. A call that commits to two KBs (`concept_transfer`,
`artifact_move`) gives both commits the writer of its header; without one each KB's own configured identity applies. A commit that is not a write (the `external
changes` commit, a sync merge, conflict resolution) is by the KB's configured identity, else the
server. A sync that rebases local commits onto the remote gives each its author back as committer
(`gitx.RealignCommitters`). Format and validation: [control plane](control-plane.md).

A KB syncs exactly when it has a remote, and that branch must be the KB's **canonical branch**:
the `branch` of the `settings.yaml` committed on the remote's default branch when it sets one
(D335, D903), otherwise the remote's default branch (D264). `branch` lives only in that file, never
in Meridian's `kbs[]`, because it is a decision of the team that owns the KB, the same for every
server that mounts it; and it is read **only from the default branch**: the working copy's file
is ignored, so a change of `branch` committed on any other branch has no effect until it reaches
the default one. A failure to read that file is never read as "no `branch`": a git error is a sync error, and a file that does not parse but carries a top-level `branch:` key refuses writes until it is fixed on the default branch (a parse error without the key is ignored). Without the key, Cartographer asks the remote on every sync (`git ls-remote --symref`, and
re-points `origin/HEAD` at the answer, since the one `git clone` wrote is never
refreshed by a fetch); when the remote's `HEAD` names a branch it does not have —
a self-hosted bare repository initialised with `master` and then pushed `main` —
the canonical branch is `main` if the remote has it, and unknown otherwise. Cartographer never
deletes or force-pushes a branch on the remote and never checks out a different branch in the
clone, except the lossless switch of `SyncIn` (below). The single exception to creating none is the first push of a KB to an **empty** remote,
which creates `main` with upstream tracking. A KB made for an empty remote by
`kb create --remote` is pinned to `main` and given its initial commit; a server never does that by itself (D906).

With `branch` set, the remote's default branch plays no part for that KB beyond holding the
file: the configured branch is the only one pulled and pushed, and the first push creates it on
the remote, with upstream tracking, when it is missing — on an empty remote as on one that already
has other branches. No other branch is ever created. A fresh clone is checked out on it (tracking
it when the remote has it, branched from the remote's default branch otherwise); an existing clone
is switched only by the sync, and only when that loses nothing (below). A server's `kbs[].branch` takes the place
of `branch` for that server (D1093). A name git would not accept is a finding in the file and never a branch.

**A KB holds files, never repositories.** A directory carrying a `.git` of its own is a nested repository: the
Meridian's git never looks inside one, and its commits never record one. Three rules (`gitx/gitlink.go`):

- every git command of Meridian runs with submodules ignored, ahead of the repository's own configuration:
  `diff.ignoreSubmodules=all`, `submodule.recurse` and `fetch.recurseSubmodules` off, and `core.fsmonitor=false`
  (command-line configuration reaches a child git process too, so a nested repository's fsmonitor is not run
  even by a command that does look inside);
- the server's commits do not depend on the user's commit-time configuration: `commit.gpgsign=false`, `push.gpgSign=false` and an empty
  `core.hooksPath` ride in the same configuration, so a global signing setting or hook cannot block or fail a write
  from a service with no terminal. The override is command-line configuration, so it also wins over a KB's own
  `.git/config`: the server owns its clones, and per-KB hooks and signing are not supported on server commits. `git commit` is stopped after `gitx.CommitTimeout`, and the `index.lock` its SIGKILL leaves (newer than the commit's start; an older one is someone else's and is only named in the error) is removed so the next write does not fail on it. Signing the server's commits
  is not supported (D1975);
- a commit never records a gitlink (index mode 160000). The staging of a write leaves a change to one out, and a
  working tree that holds a nested repository new to the history makes the commit fail whole with
  `refusing to record a nested git repository (gitlink): <path>` (`gitx.ErrGitlink`): nothing is committed, the
  index keeps no trace of it, and every later write fails the same way, as a failed commit (`sync_status` is `failed`, with the
  path in `last_error`), until the nested `.git` is removed. Every commit path holds to it: `Commit`, `CommitPaths`, and the
  staging the write gate previews;
- a gitlink the history already holds is left alone: it is kept out of `git add -A`, the one command that looks
  inside it whatever the configuration says, so its pointer is never updated and its repository never
  inspected; removing its folder is recorded as any removal. A gitlink the other side of a merge brings is part
  of finishing that merge.

Each commit subject is `<tool_name>: <resource>`, so the history of a KB is
readable as an audit trail. The resource is built from the arguments that
identify what the write touched: `path` for the artifact tools, `concept_id/path`
for the asset tools, and otherwise the first of `id`, `name`, `source_id`,
`contradiction_id` present in the call. A tool invoked with none of them commits
as the bare tool name.

There is no pull-request flow: a `git_profile: server` (in a KB's `settings.yaml`, a `kbs[]` entry or Meridian's `git:`
block) or a `kbs[]` entry that sets `git_branch` keeps the KB from mounting, and the message says to unprotect the
branch or set `branch`.

## Remote synchronization

Every git command Cartographer runs gets `LC_ALL=C`, over the process environment and any per-KB
variable: outcomes such as "nothing to commit" or a rebase conflict are recognised from git's English
messages, and a git built with translations prints them in the user's locale.

The repository is always the one the command names with `-C`: the variables that pin another
(`GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`,
`GIT_COMMON_DIR`, `GIT_NAMESPACE`, `GIT_PREFIX`) are dropped from the inherited environment, and only a
per-call variable can set them (the temporary index of a path-scoped commit). File names handed to
`add`, `reset`, `status`, `log` and `ls-files` are literal (`--literal-pathspecs`): `img[1].png` is that
file, never a glob. Refs and branch names follow `--end-of-options`, remote names `--`, so a name that
begins with `-` is a name.

A KB with an `origin` remote syncs; one without never contacts anything (D903). The two timings
below are constants, not settings: a 30s freshness window and a 3s push debounce.

1. `SyncIn` fetches and runs pull/rebase/autostash before a write. A freshness
   window of 30s skips repeated fetches in a burst. A read-only tool
   never waits for it (D258): when the window has expired it is served at once from the
   local clone and starts the same refresh in the background, one per KB at a time, under
   the same KB lock; what that refresh pulls is visible from the next call on, and a
   failure there is non-fatal. Every fetch is bounded (15s; on Unix the whole git process
   group is killed on expiry, on Windows git is killed and `WaitDelay` bounds the pipes).
   A push is bounded the same way (60s, `gitx.PushTimeout`) and a timed-out push is not
   retried: `SyncOut` fails at once and the commit waits locally for the next write. No
   git command prompts for a credential (`GIT_TERMINAL_PROMPT=0`, ssh in batch mode,
   D1004). After a failed fetch reads skip the fetch for 60s, so a remote
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
   The other refusals no retry changes fail the same way, after one attempt, with a one-line remedy quoting the
   remote's line (`ErrPushRefused`, `gitx.PermanentPushRejection`): a credential without write access (HTTP 403,
   `Permission to … denied`, a read-only deploy key), a repository rule or required signature (GitHub `GH013`,
   `repository rule violations`; Cartographer does not sign commits), a file over the size limit (`GH001`) and a server-side hook that declined. A bare HTTP 403 (it can be a rate limit), a `[remote rejected]` for a
   lock, a ref update or an internal error, a network error, a timeout or a non-fast-forward is still retried.

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
default branch; with Meridian's `kbs[].branch` (D1093), which wins over the KB's `branch`,
to check it out, or change or remove that key; a configured branch the remote does not have yet is not a divergence,
there is just nothing to pull. Before refusing, `SyncIn` tries to switch the clone to the
effective branch itself (D1093), whichever the source: it does when HEAD is on a named
branch, the working tree is clean and nothing is unpushed on it (a branch never pushed counts as
unpushed), and the target can be built without merging, resetting or deleting anything: the local
branch, else `origin/<branch>`, else a new branch from the remote's default branch. It logs one line and goes on with
its pull. When it cannot, the state is `degraded` and `last_error` ends with the reason
(uncommitted changes, N commits unpushed, no branch to create it from). Other divergence is never repaired
automatically, since merging two histories is the operator's decision. Reads keep
working from the local clone (their background refresh logs the error). `SyncOut`
repeats the guard for anything that bypassed `SyncIn` (the freshness window, a
checkout changed in between): it pushes only a branch the remote already has, or
`main` to an empty remote (or the configured branch, `kbs[].branch` included, which it creates), and
otherwise leaves the commit local and reports the same error. `sync_status` shows
`branch` and `remote_default_branch` side by side, with `branch_source` saying
whether the canonical branch comes from `server` (`kbs[].branch`), `settings` (the KB's `branch` key) or from the `remote`;
the next sync that finds the clone back on the canonical branch clears the
`degraded` state.

Without a remote, synchronization is a no-op and local commits still work.

## Conflict registry

A rebase conflict is aborted immediately. Reserved files (`log.md`, `index.md`,
`_map.md`, `_archive.md`) are not concepts, so Meridian reconciles them itself
(D311):

- `data/log.md` carries `merge=union` in the KB's committed `.gitattributes`
  (written by `Init`; a KB that lacks it gets it in a separate
  `chore: set merge=union for data/log.md` commit after its first successful
  sync in a process), so two concurrent `log_append`s merge into both entries.
- A conflict whose files are **all** reserved is settled in place by the pull
  (in `SyncIn` and in `SyncOut`'s retry): one merge commit of the
  remote SHA with `log.md` and a curated `index.md` (the root's, or a map's without
  `index: generated`) set to the union of both sides, and the other structural
  files — a generated `index.md` included, which the next write regenerates — to
  the remote's version (D311 amendment). An expanded concept's `index.md` is its body, a
  concept: its conflict is registered, never auto-resolved.
  The write goes on as if the pull had succeeded.
- A reserved file that conflicts **alongside** a concept stays out of the
  registry; the write's error names it, and it takes the same automatic
  resolution when the concept conflicts are finalized.
- A reserved-only conflict Meridian could not settle is registered with
  `kind: reserved` and its path as `concept_id`; `git_conflict_resolve` accepts
  `union` (only `log.md` or `index.md`), `ours` or `theirs` for it, never `edit`.
- Any other conflicting file (a skill, a template, an expanded concept's asset,
  `instructions.md`, `settings.yaml`, `paths.yaml`) is registered with
  `kind: artifact` and its path as `concept_id`, whether or not a concept also
  conflicted, with `ours` or `theirs` (D1563). The write's error names the files
  and never promises a tool for something that was not registered.

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
- `union` — both sides' lines, for a reserved `log.md` or `index.md` entry only.

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
view. Writes are atomic renames (`writeFileAtomic`: temp file in the same directory, `fsync`, rename, `fsync` of the directory, so a crash cannot leave a renamed but empty file; on Windows the rename retries with backoff while a reader holds the destination open without `FILE_SHARE_DELETE`, and the directory sync is a no-op). A temp file orphaned by a kill is junk (`.tmp-wiki-*` in `JunkPatterns`, so never committed) and Meridian's start-up (`SweepStaleTempFiles`, not every `Open`) removes the regular, untracked ones named exactly as `os.CreateTemp` names them that are older than the lock timeout, when it can take the KB lock without waiting. So a validation reads either the old file or
the new one, never a torn one. The maps a view exposes (`Links`,
`IncomingLinks`) are shared across readers and must never be mutated.

## Operator recovery

Cartographer does not promise automatic repair of arbitrary interrupted git
state. Before mounting a KB, the checkout should have no active merge/rebase
and no unexplained working-tree changes. Back up the repository and follow the
[deployment recovery procedure](deployment.md#backup-and-disaster-recovery)
for storage or remote failures.
