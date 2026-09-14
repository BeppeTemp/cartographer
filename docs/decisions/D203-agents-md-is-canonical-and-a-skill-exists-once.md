---
topic: project-governance
---

# D203 — AGENTS.md is the canonical instruction file, and a skill exists once

**Decision.** `AGENTS.md` is the only real instruction file. `CLAUDE.md` is a
one-line `@AGENTS.md` import (11 bytes). Each skill has exactly one copy, in
`.agents/skills/<name>/`, and `.claude/skills/<name>` and `.kiro/skills/<name>`
are symlinks into it. Kiro, Claude Code and Codex need nothing else: `AGENTS.md`
is read natively by Kiro and Codex, and `.agents/skills` by Codex.

**Antigravity is the exception, and D207 corrects what this record claimed about
it.** Its whole configuration root is global — `~/.gemini` — so it reaches
neither the repository's `AGENTS.md` nor `.agents/skills` from a clone.

**Why.** The repository previously had the direction inverted — `CLAUDE.md` was
the real file and `AGENTS.md` a symlink to it — and three divergent copies of
each skill, one per client, differing in the client-specific glue. The
divergence was intentional at first and then stopped being reviewable: the two
`plan-issue` copies differed only in the words "Codex-side" and "Claude-side"
while the third had drifted a paragraph further.

Inverting the direction is what the ecosystem does: in `apache/airflow`,
`vercel/next.js` and `home-assistant/core` the `CLAUDE.md → AGENTS.md` symlink is
literally the same 9-byte git blob, `47dc3e3d`. `AGENTS.md` is also the name with
the most consumers and is stewarded by the Agentic AI Foundation.

**An import, not a symlink, and this is the one place a public repository differs
from a private one.** On Windows, creating a symlink needs Administrator rights
or Developer Mode, and a `git clone` that detects that sets `core.symlinks=false`
and materialises the link as a small text file containing its target. On a
private repository that is a non-problem: there is one checkout and its owner
controls it. Here it would mean a contributor's `CLAUDE.md` silently contains the
string `AGENTS.md` instead of importing it — a defect paid by whoever arrives.
The 11-byte `@AGENTS.md` file works on every filesystem, is documented by
Anthropic, and is the shape `n8n` and `grafana` use (blob `43c994c2`).

The skill symlinks keep the same risk, but there is no import mechanism for a
skill directory, and Claude Code will not read `.agents/skills`. The mitigation
is the gate: `make test` fails when a bridge has become a copy. It reads the
**git index** rather than the working tree (see Consequences), so it needs no
special case for the unmaterialised-symlink layout and gives the same answer on
every platform, a Windows contributor's included.

**Alternatives rejected.**

- *Keep one skill file per client.* It is what produced the drift. The genuinely
  client-specific parts — how a subagent is spawned, where a worktree lives, how
  authorisation is requested — are now a four-row table at the end of one file,
  and the git commands they used to duplicate moved into `make worktree-add` /
  `make worktree-rm`, which is the same on every client.
- *A directory-level symlink* `.claude/skills → ../.agents/skills`. Not
  documented by any client; the per-entry symlink is, and Claude Code documents
  that a skill reached from several locations loads once.
- *A steering file for Kiro.* It already reads `AGENTS.md`, so a bridge would
  load the same text twice, and on Kiro that is worse than elsewhere: `AGENTS.md`
  is always included and does not support inclusion modes, and the documentation
  contradicts itself on whether a `manual` steering file stays out of context on
  the CLI. This rejects a *duplicate* of `AGENTS.md`, not the directory: a
  steering file carrying other content is fine, and `cartographer sync` owns
  `.kiro/steering/cartographer.md` when a workspace is bound to a KB.
- *A `GEMINI.md` for Antigravity.* Rejected here on the belief that Antigravity
  reads `AGENTS.md`, making the second name pointless. D207 revisits it: whatever
  Antigravity reads, it reads from `~/.gemini`, so a repository-local file of
  either name reaches it only if the client is documented to look here — which
  this repository has not established.
- *Generating the per-client files from a source*, as `home-assistant/core` does
  for Copilot. Justified there because Copilot cannot consume skills at all. Here
  three clients read the canonical files directly, so a generator would exist to
  serve one client that already has an import mechanism.

**Consequences.** `AGENTS.md` is now under a size gate (120 lines, 12.000
characters — attributed here to an Antigravity per-file ceiling, which D207
withdraws: the number stays as this repository's own limit), and the
whole instruction chain is checked against Codex's 32 KiB, because past that
limit Codex drops the *deepest* files: the guidance closest to the code, with no
error. Two areas carry their own nested `AGENTS.md`, `internal/mcpserver` and
`internal/provisioning`, holding invariants that are not derivable from the code;
the deepest chain measures ~10 KiB, well inside the limit. A skill's frontmatter
must satisfy Kiro's rules, the strictest set, so one file is valid everywhere.
`.agents/*` is git-ignored except `skills/`: nothing else under that directory
is ours to track.

Two costs are accepted knowingly. OpenCode, which is not one of the four but is
supported by the client configurator, is reported to read `.claude/skills` in
addition to its own `.opencode/skills`, in which case it would see each bridged
skill twice — not verified, and the audited matrix (D193) claims only
`.opencode/skills` for it. If it holds, there is no fix that keeps Claude Code
working, and the symlink is the form most likely to be de-duplicated. And on
a Windows checkout without Developer Mode git materialises each bridge as a text
file and the client loads nothing, silently — verified by simulating that layout.
The gate therefore asserts the **git index** mode (`120000`) rather than the
working tree, so it answers identically on every platform, and `CONTRIBUTING.md`
states the `core.symlinks=true` requirement. One interaction worth recording:
because of D148 the provisioning code refuses to materialize into a symlinked
destination, so a KB sync cannot overwrite these bridges — which is the right
outcome, and also means these two skills are repo-scoped and not syncable from a
KB.

That symlinks are followed at all is not assumed: verified empirically on Kiro
CLI 2.21.4 over 45 non-interactive runs with negative controls (dangling link,
missing `SKILL.md`, materialised text file) and a decoy, across relative and
absolute links and targets inside and outside the git root. Claude Code and Codex
document the behaviour; Kiro does not, which is why it was measured. The
measurement covers the CLI, not the IDE.
