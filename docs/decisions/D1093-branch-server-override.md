---
topic: concurrency-git
---

# D1093 — A server can override the KB's branch for itself, and a clean clone follows the effective branch

> **Amends [D903](D903-one-way-to-write-any-provider.md)**, [D335](D335-kb-writes-follow-a-configured-branch.md) and [D264](D264-kb-writes-follow-the-remote-default-branch.md).

**Decision.** The branch Cartographer commits and pushes to is, highest precedence first: `kbs[].branch` of the
server file, `branch` of the KB's `settings.yaml` read from the remote's default branch, the remote's default branch.
When `kbs[].branch` is set the KB's `branch` is not read at all, so a malformed one cannot refuse the writes of a
server that overrides it. The override is validated like `branch` (an invalid or blank value stops the start, naming
`kbs[N].branch`), exists only in the YAML (`CARTOGRAPHER_KB_REMOTES` is bare URLs with no per-entry option) and is
held by `KBSpec.BranchOverride`, never by `KBSpec.Branch`, which is the KB's setting shown by the settings view.
`branch_source` of `sync_status` gains the value `server`. The removed `kbs[].git_branch` stays a refusal, now
saying to rename it `kbs[].branch` or to set the KB's `branch`.

A clone that sits on another branch than the effective one is switched to it by the first sync, from any source,
when that loses nothing: HEAD is on a named branch, the working tree is clean, nothing is unpushed on it (a branch
never pushed counts as unpushed) and the target exists locally, on the remote, or can be created from the remote's
default branch (never from the current branch). Otherwise it stays `degraded` and `last_error` ends with the reason.
The first push to an empty remote creates the override instead of `main` when one is set. `kb create --branch X`
writes `branch: X` in the new KB's own `settings.yaml`, pushes the default branch that carries it, then creates and
pushes `X`; `kb clone --branch X` checks `X` out and adds a `kbs[]` entry with `branch: X` to the server file,
which must exist and have no entry for that KB.

**Why.** Someone trying Cartographer on a shared KB had no way to write to a branch of their own without changing
the team's setting. A key meaning "the team's choice" and a key meaning "this machine's choice" are two decisions,
so two homes do not break the principle of one home per key (D903 P2): the removed `git_branch` was refused because
it meant the same thing as `branch`. The switch closes the case a changed setting left a perfectly safe clone
`degraded`: with a clean tree and nothing unpushed, checking out another branch can lose nothing.

**Alternatives rejected.**
- Accepting `kbs[].git_branch` as an alias of the new key: it would silently re-activate a key D903 removed and that
  an old installation may carry with a stale value; the refusal costs one rename, made once.
- An environment form: the only `kbs` env is a CSV of bare URLs; a branch would need an invented syntax.
- Honouring the override only after the KB's `branch` has been read and found valid: a malformed team setting would
  then block a server that opted out of it.
- Switching only for the override: the conditions make every switch lossless, so the rule need not depend on where the
  branch comes from.
- Merging or resetting to reconcile a clone that cannot be switched: merging histories stays the operator's decision.

**Consequences.** A clean, fully pushed clone on a non-effective branch is now switched instead of staying
`degraded` (release notes). `kbs[].branch` needs a restart, as every `kbs[]` key. A KB found by discovery
(`kb clone` writes no entry) needs an entry with `remote:` and `branch:` to be overridden. Anything that adds a
branch source must set `BranchSource*` and keep `resolveCanonical`, `ConfiguredBranch` and the divergence message
in step; the switch must keep never merging, resetting or deleting.
