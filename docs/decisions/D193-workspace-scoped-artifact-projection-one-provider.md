---
topic: sync-provisioning
---

# D193 — Workspace-scoped artifact projection: one provider, different KBs per repository

**Status: implemented.** Closes #247.

**Context.** A session working in the HomeLab perimeter activated `new-microservice`, a skill
belonging to `work-kb` and specific to WORK. Cartographer tracked the source KB correctly
everywhere it records provenance — the manifest, the lockfile's `source` (D170), the materialized
footer — but the provider chooses a skill from **name and description alone**, and the provenance
block is in the body, read only *after* activation. The description was generic and did not name
WORK.

D169/D170's per-provider binding closes this for a provider dedicated to one perimeter. What it
cannot close is the case that produced the incident: the binding is **static per provider** and does
not depend on the current directory. `.cartographer.yaml` is machine-wide by design, every provider
destination is under `$HOME`, there is no notion of an active workspace, and two concurrent sessions
of the same provider in two perimeters share one global catalogue.

The audit verified that every supported provider *does* have a project-local scope — Claude Code
`.claude/`, Kiro `.kiro/`, OpenCode `.opencode/`, Codex `.agents/skills` and `.codex/` along
cwd→repository root — and that this holds for instructions, agents, hooks and MCP descriptors, not
only skills. Sources are recorded in `docs/interoperability.md` §Project-local scopes.

**Decisions.**

- **An explicit binding between workspace and KBs, per provider**, with the scope selected per
  provider. Absent means the historical global catalogue, so no existing installation changes on
  upgrade; migration is a deliberate `workspace bind` or a `connect --workspace`.
- **The bind takes a path, resolves it once, and persists the canonical form.** For a git repository
  it also records the normalized remote as a **guard** against moves and accidental reuse — never as
  a selector, because one remote has many clones and worktrees and choosing between them by remote
  would pick the wrong one. A bound path that is gone, or whose remote has changed, is an **error**.
- **Fail closed, everywhere.** An unbound workspace receives the transversal bundle and no KB
  artifact. "No KBs" and "several KBs" are distinct explicit states and neither is ever "every KB" —
  `workspace bind` requires `--kb` or an explicit `--no-kb` rather than defaulting. A provider with
  no project-local scope (hermes, antigravity) **cannot** be bound and is refused with the reason,
  never degraded to the global catalogue: degrading is precisely the exposure this closes.
- **Only Cartographer's own transversal bundled skills stay global.** `cartographer-ops`,
  `kb-create` and their siblings belong to no perimeter, and a session outside every bound workspace
  still needs them. Nothing KB-sourced is materialized globally under workspace scope, and nothing
  bundled is copied into a workspace — the E2E scenario caught the first implementation doing
  exactly that, which would have multiplied `cartographer-ops` by the number of bindings.
- **The lockfile gets a separate namespace, not an extended key space.** Prune deletes every managed
  file its lock names, so a key that *could* resolve to the wrong projection deletes another
  workspace's files. Two maps — `providers` and `workspaces` — make that mistake unrepresentable
  rather than merely unlikely. Each workspace lock carries its `base_dir` and a `project_scope` flag,
  the latter persisted rather than derived because verifying or pruning through the wrong matrix
  looks for the wrong paths entirely.
- **Unbinding prunes.** The declaration and the files are two different things: a later sync only
  iterates the *declared* projections, so without an explicit orphan pass the files of an unbound
  workspace would stay in that repository forever. The pass considers workspace projections only —
  a provider's global catalogue is `disconnect`'s decision, and reaching it from here would delete a
  connected provider's artifacts because it was absent from one `sync --client`.
- **Collision detection is scoped to the projection, not weakened.** Two KBs owning a same-named
  skill in two different workspaces is legal and now works; the same two bound to **one** workspace
  is still refused by the strict merge (D171), because that is the only place a collision can
  actually confuse an agent.
- **Repository hygiene is two rules with teeth.** Cartographer excludes only its **own untracked**
  paths, only in `.git/info/exclude`, inside a marker block — never `.gitignore`, which is shared
  with everyone who clones the repository. A shared file the repository already tracks is the
  user's: the block goes inside it and it is not excluded; one Cartographer created itself is
  untracked and is excluded, or every sync would leave the working tree dirty. A path Cartographer
  would own **entirely** that git already tracks is a refusal, before anything is written.
- **Honest reporting where correct files are not enough.** Codex ignores a project's `.codex/` layer
  unless the project is trusted, so `status` and `doctor` report such a projection as `inactive` and
  name the fix. Reporting it as installed is the false-positive class D189 exists to eliminate.
- **Kiro keeps its unsupported agent and hook cells in the project scope**, for the same D140 reason
  as globally. #248 tracks whether Kiro 3.0 changes that and is blocked on an empirical
  verification; giving it a cell here would be shipping that finding without its evidence.

**Invariants kept.** D169's statement that a projection is **not an authorization boundary** — a
process running as the same user can read any file on the machine; what this prevents is accidental
exposure and activation, and that is all it claims. The signature and trust chain applies per
projection unchanged, the `safepath.go` symlink refusals hold for project-local destinations, and
the bundled transversal skills keep working with no workspace binding at all. A provider in the
default scope resolves to exactly one projection whose behaviour is byte-identical to before.

**Consequences.** New opt-in scope; existing installations are unchanged until someone binds a
workspace. The lockfile gains a `workspaces` key, absent in every file written before this and
therefore needing no migration. Release-note the `workspace` subcommand and `connect --workspace`.

**What the acceptance scenario found.** `18_workspace_projection` is the mandatory E2E from the
plan, and it failed on its first run with three defects no unit test could see: the bundled skills
were being copied into every workspace, a `CLAUDE.md` Cartographer created itself was left untracked
and dirtying the repository, and unbinding a workspace left its projected files behind. All three
lived in the wiring between components, which is exactly what the scenario exists to exercise.

Details: `docs/sync.md` §Workspace scope, `docs/configurator.md`, `docs/interoperability.md`
§Project-local scopes, `docs/testing.md`.
