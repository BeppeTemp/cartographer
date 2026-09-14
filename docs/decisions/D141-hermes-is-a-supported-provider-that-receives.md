---
topic: client-configurator
---

# D141 — Hermes is a supported provider that receives deliveries, not installations

`hermes` is a provider in the registry (D137) with exactly one supported kind, `skill`, and four
explicitly unsupported cells. Its skills are **delivered** to
`$HERMES_HOME/skill-inbox/<name>/cartographer/` — the skill's files, provenance-stamped as
everywhere else (D138), plus a generated `SOURCE.md` — and the agent adopts what it chooses through
its own `skill_manage` tool. **Nothing is ever written under `$HERMES_HOME/skills/`.**

**Why delivery and not installation.** That directory belongs to Hermes' own curator, which
archives unused skills, keeps telemetry and honours pins by rewriting what it owns from its
learning loop. Materializing there would put Cartographer and the curator in a permanent fight —
one overwriting from the server, the other rewriting from its own experience — and the loser is the
agent's accumulated learning, which no sync can restore. The convention for *proposing* a skill from
outside already existed (`skill-inbox/`), and it is a curatorial handoff, not an install.

**The four unsupported cells**, each for a stated reason rather than by omission: `agent` — no
native subagent directory; `hook` — no hook mechanism at all, nothing fires at conversation start;
`mcp` — `config.yaml` is rendered by an Ansible role and recreated on the next playbook run;
`instructions` — `SOUL.md`, its always-on slot, is operator-owned and rendered from a template.
Anything Cartographer wrote into the last two would be silently destroyed by the next deploy.
`Unsupported` already means "no approval unblocks it" everywhere in the client (D50), so nothing
else changes. Its trigger is therefore the scheduled timer from D140: with no session hook, the
timer is Hermes' Layer 1.

**No timestamp in the delivery path**, deliberately departing from the
`skill-inbox/<skill>/<timestamp>/` convention. Provisioning must be idempotent: a timestamped
directory per sync would accumulate a fresh copy every timer tick and no prune could tell stale from
current. One stable directory per (skill, source) is updated in place — the last path segment names
the proposer, so another source never collides — and the history of the proposal lives in the KB's
git log, which is where it belongs. `SOURCE.md` carries the artifact's content hash, so the agent
distinguishes an unchanged re-delivery from a new proposal. `SOURCE.md` is generated, so a KB skill
shipping its own is a collision: that one artifact fails with a warning naming it and is not
recorded in the lockfile, while the rest of the sync completes.

**Per-provider base directories.** Every other provider materializes under one shared base dir;
Hermes' root is elsewhere on the machine (`/opt/data` in the reference deployment), so `Lock` gains
an optional `base_dir` recording the directory its `ManagedFile` paths are relative to. Empty — every
lockfile written before this, and every other provider — means "the lockfile's own directory", so
no migration runs and nothing changes meaning. Prune and on-disk verification (D139) read it through
`LockBaseDir`; the lockfile itself stays a **single v2 file** in the client's target directory with
one `Lock` per provider. `$HERMES_HOME` unset makes `connect hermes` fail naming the variable,
before anything is written: falling back to the home directory would scatter files where the agent
never looks.

**Connect says what it does not do.** Hermes has no MCP emitter, so `connect hermes` writes no MCP
entry and reports that its endpoint stays the operator's job — silently doing nothing there would
read as a bug. For the same reason it is absent from the interactive connect form, which offers the
providers whose MCP configuration `connect` actually writes; Hermes is connected from the CLI, where
the missing-`$HERMES_HOME` failure can be stated plainly.
