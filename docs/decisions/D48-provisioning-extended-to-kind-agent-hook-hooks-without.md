---
topic: sync-provisioning
---

# D48 — Provisioning extended to `kind: agent`/`hook`; hooks without auto-merge into `settings.json`; per-kind counts

**Decision.** Extends `internal/provisioning` (already kind-generic on Manifest/Lock/Diff, D27/D34/
D40) to two new kinds. `kb.Init` creates `agents/`/`hooks/` (optional/backward-compat); `BuildManifest`
scans `agents/<nome>.md` (one file = one agent artifact) and `hooks/<nome>/` (one directory = one
hook artifact). `destDir(kind, name, provider)` generalizes `skillDestDir`: for agent/hook only
`claude` has a known destination (`.claude/agents/<nome>.md` single file, `.claude/hooks/<nome>/`
directory) — other providers remain `needs_approval`. **Hooks with no automatic merge into
`settings.json`**: that file is user-owned with nested arrays and no safe merge/prune —
Cartographer materializes only the folder and prints a manual instruction (**superseded by D57**:
registration is now automatic). New `KindCounts` aggregates installed/total per kind, shown by
`status`/TUI.
**Rationale.** The model was already kind-generic by design (D27/D34): the extension only required
new emitters and a destination branch. The non-merge of hooks follows the same least-surprise
philosophy as prune (D40): a user file with nested structure is not a safe target for
silent automatic writes. The single file for agents mirrors the native format of a
Claude subagent, instead of forcing them into the skills' directory convention.
Details: `docs/sync.md` §Agents and hooks, `docs/interoperability.md` §Provider capability matrix.
