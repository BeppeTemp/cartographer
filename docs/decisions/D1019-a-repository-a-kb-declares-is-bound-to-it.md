---
topic: sync-provisioning
---

# D1019 — A repository a KB declares is bound to it

**Decision.** A repository a KB declares in `paths.yaml` `repos:` is bound to that KB, for every
provider in workspace scope, with no `workspace bind` run there. `sync` resolves each declared key
with the resolver the placeholders use (the `paths:` entry, the repo index by the declared remote,
the declared default), merges the keys that resolve to one canonical path into one binding with the
union of their KBs, and stores the list apart from `.cartographer.yaml`, in `workspace-derived.json`
beside the repo index cache, recomputed by every `sync`. The clone is bound by path and the declared
remote is the guard: another clone or worktree of the repository is not bound, and a clone whose
origin differs is refused. A `repos:` entry opts out with `bind: false`. An explicit `workspace bind`
on the same path replaces the derived binding entirely (`--no-kb` is the local opt-out); otherwise
the deepest binding wins across explicit and derived ones. A provider in provider scope is unchanged.

A derived binding never edits a file git tracks. Where the client's instructions file is tracked, the
projection writes no instructions and `status`/`doctor` say so; Claude Code gets its block in
`CLAUDE.local.md`, owned and excluded. Probe, 2026-10-22, Claude Code 2.1.304: in a repository with a
tracked `CLAUDE.md`, an untracked `CLAUDE.local.md` holding a unique token is repeated by `claude -p`.

Trust: `paths.yaml` is unsigned KB content, so a binding needs a declared `remote:` and a clone whose
origin is non-empty and matches it; an entry without `remote:` binds nothing and is listed as
unresolved. Without that, a `default:` pointing at any `$HOME` clone would let one edit of a KB project
files into an unrelated checkout.

**Why.** Declaring a repository is the strongest statement a KB makes about it, yet activating the KB
there took a `workspace bind` per provider and per repository. Binding a folder that contains the
repositories is no substitute: probed on five clients, four look for project skills only up to the
repository root (`harnesses.md`), so the repositories below a folder binding get the tools through the
bridge (D737) but not the skills or agents. An explicit bind puts the instructions block inside a
tracked `CLAUDE.md`/`AGENTS.md` (D193); a derived one would do it in every declared repository with no
command run there, and a KB name in a tracked file of a public repository is a leak (D259).

**Alternatives rejected.** Binding every clone of the remote: a worktree or a second clone is another
checkout the user did not choose, and the explicit bind covers it. Storing the derived list in
`.cartographer.yaml`: that file is the user's (D169), and a list the next `sync` rewrites would churn
it. Writing the block into a tracked file anyway: a leak by default. Skipping instructions for Claude
Code too: the probe shows `CLAUDE.local.md` is read, so it keeps its instructions.

**Consequences.** On the first `sync` a provider already in workspace scope gains a binding, and a
projection, in every local clone its KBs declare: KB authors whose `repos:` lists repositories they
only cite add `bind: false`. A derivation needs the KBs' registries, so `sync` pulls the KBs a
workspace-scoped provider receives (not only the ones its explicit bindings name) and reuses those
pulls for the manifests. A KB whose pull fails leaves its providers' derived list as it was. If a
repository starts tracking its instructions file after a block was written there, the earlier block
stays until the next prune.
