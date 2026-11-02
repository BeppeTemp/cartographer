---
topic: concurrency-git
---

# D1412 — `kb create --branch X` puts a KB on an orphan branch of a repository with history

**Decision.** When the remote has commits that are not just a forge's initial commit and
`--branch X` names a branch the remote lacks, `kb create` creates the scaffold as an orphan
branch X (no shared history), writes `branch: X` in its `settings.yaml`, pushes only X with a
plain push, and records `kbs[].branch: X` in the server config, which must exist and is
checked before anything is pushed. An X that exists is refused (naming X) unless it is the
D710 adoption; without `--branch` the refusal gains `--branch <name>` as a third way out.
`kb clone --branch X` checks X out before the KB layout is checked, so it mounts a KB when the
default branch holds none, and `kb clone` without `--branch` lists the branches that carry one.

**Why.** A repository that cannot take a force-push cannot be emptied, nor take the scaffold on
its default branch. The `kbs[]` entry is mandatory because a discovered KB follows the remote's
default branch: without it the first sync would switch the clone to the customer's default
branch and the KB would vanish from the tree.

**Alternatives rejected.** Pushing the default branch with `settings.yaml` (D1093): touches the
customer's branch. Reading `branch` from X itself: the server never reads it under an override,
and a guard in `switchToCanonical` is a separate hardening.

**Consequences.** Amends D1093 (the default branch is not pushed for an orphan X; the `branch:`
line on X is informational) and D710 (non-empty remotes are refused only without `--branch`, or
with an existing one). A push is never forced and never touches a branch other than X;
`setup`/`connect` still have no `--branch`.
