---
name: harness-watch
description: Re-aligns Cartographer with the agent clients it supports (Claude Code, Codex, Kiro, OpenCode, Hermes, Antigravity, Crush) - what changed since the last review and what to do about it. Use when the user asks to check, audit or re-verify a client, to see what changed in a client release, or to refresh docs/harnesses.md.
---

# harness-watch — what changed in the supported clients, and what to do

The baseline is `docs/harnesses.md` (one `## <provider-id>` section per client);
the matrix it protects is `destinationMatrix`
(`internal/provisioning/provisioning.go`), `projectDestinationMatrix`
(`internal/provisioning/workspacescope.go`) and the descriptors in
`internal/configurator/registry.go`. Why this is a procedure and not a CI job:
D292.

This file is the single copy for every client; `.claude/skills/harness-watch` and
`.kiro/skills/harness-watch` are symlinks to it.

## Invariants

- Read-only on the machine, except probe sentinels under a temp directory that
  you remove afterwards.
- Never install, upgrade or log in to a client without the operator's explicit
  OK. An uninstalled client is reported, not installed.
- **Documentation is not evidence.** A change that would flip a matrix cell is
  confirmed with a probe (sentinel plus negative control), never from a changelog
  line.
- **Capability, not version.** A version string does not say whether a feature is
  live; probe the behaviour, in every mode that exists (default engine, opt-in
  engine, interactive, non-interactive).
- The repository is public (D259): reports, ledger edits and issues use
  placeholder names (`$HOME/...`, `user`, `example.com`), never real paths,
  hosts or KB names.

## Procedure

1. **Scope.** One client by name, or all of them. Read its section in
   `docs/harnesses.md`.
2. **Current version.** Use the ledger's package source. Report installed versus
   latest released versus the ledger's **Aligned with**.
3. **Delta.** Read the changelog or release notes **since the ledger version**
   only. Keep the entries that touch a Cartographer surface: the five kinds
   (skill, agent, hook, instructions, mcp) and their destinations; file formats
   (agent fields, hook schema, MCP config keys); restriction keys (D291); session
   triggers (`docs/sync.md` §Layer 1); project-local scopes (D193); instruction
   slots. Ignore the rest.
4. **Classify** each kept entry:
   - `no-impact` — noted in the report only;
   - `doc` — a current-state doc or a code comment is now wrong (renamed path,
     new canonical location, D192-style divergence);
   - `cell` — a matrix cell, translator or native key may change (new kind
     supported, format change, deprecation);
   - `watch` — matches the trigger of an open watch issue listed in the ledger.
5. **Verify** every `cell` and `watch` entry with the recipes in
   `references/probes.md`, from a temp directory unrelated to any project, no
   custom `--agent`, default engine first. A probe that needs an install, upgrade
   or login stops and asks the operator. Outcome per entry: `confirmed`,
   `refuted` or `not-verifiable` (say why).
6. **Act.**
   - Report, always, as a table: client · entry · class · probe outcome · action.
   - `doc` → fix in the same PR as the ledger update.
   - `cell` confirmed → a plan issue via the `plan-issue` skill. Never change
     matrix code from inside this skill.
   - `watch` → comment the outcome on the watch issue; close it only when its
     trigger fired **and** the follow-up plan exists.
   - Ledger → bump **Aligned with** to the reviewed version and date, update
     sources and watch items, never replace a version with a guess. One docs-only
     PR (`docs(harnesses): …`) for the whole pass.

A dry run (no PR) prints the report table and the ledger diff and touches no
other file.
