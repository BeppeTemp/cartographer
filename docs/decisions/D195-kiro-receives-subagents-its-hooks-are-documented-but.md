---
topic: sync-provisioning
---

# D195 — Kiro receives subagents; its hooks are documented but not shipped

**Status: implemented.** Closes #248.

**Context.** D140 settled Kiro's empty `agent` and `hook` cells against **CLI 2.20.0**, on three
findings: the trigger set was camelCase, `~/.kiro/hooks/` played no role because hooks were a `hooks`
map inside an agent config, and hooks were per agent — so no hook Cartographer owns could fire for
the agent a user actually runs. Kiro's documentation for CLI 3.0 claims all three changed:
standalone `.kiro/hooks/*.json` with a versioned schema, `~/.kiro/hooks/` firing in every workspace,
and any custom agent invocable as a sub-agent.

The plan made empirical verification a precondition rather than a formality, because D140's own
history is the argument. That verification was performed on **Kiro CLI 2.21.3**, 2026-09-11, and it
split the plan in two: one half is true and shipped here, the other is not true of any released
client.

**What was verified.**

*Subagents — true, and available today.* `~/.kiro/agents/<name>.json` is reported by
`kiro-cli agent list` as `Global`, and the configuration `kiro-cli agent create -f kiro_default`
writes documents a "Subagent System" with a `use_subagent` tool that delegates to agents **selected
by their `description`**. D140's third finding no longer holds. The format is **JSON**
(`name`, `description`, `prompt`), not the Markdown the vendor documentation describes: a Markdown
agent dropped in the same directory is not discovered.

*Hooks — not implemented in the shipped client.* A hook placed in `~/.kiro/hooks/` **and** in the
workspace's `.kiro/hooks/`, with each of `AgentSpawn`, `SessionStart`, `PromptSubmit`,
`UserPromptSubmit` and `PreToolUse`, never fired — in a `--v3` session that completed normally. The
agent log never mentions hooks, the generated agent config has no `hooks` key, and the shipped agent
binary contains no `.kiro/hooks` string at all (it does contain `.kiro/agents`, `.kiro/skills`,
`.kiro/steering`, `.kiro/settings`). The vendor's own text explains it: the v1 hook format was
*"introduced in IDE 1.0 and CLI 3.0"*, and the CLI changelog puts **3.0 in early access** with
2.21.x on the release channel. `kiro-cli --v3` launches the next-generation *agent* (KAS 0.60.10);
that is not CLI 3.0.

**Decisions.**

- **`agent × kiro` becomes `perName(".json", ".kiro", "agents")`**, in both the global and the
  workspace scope (D193). `translateAgentForKiro` emits JSON with `name`, `description` and
  `prompt`; `tools` and `model` are dropped, as they are for Codex and Antigravity, because their
  names are not portable and inventing a mapping would hand an agent capabilities its author never
  granted. Encoding as JSON rather than concatenating text is what makes a description containing a
  colon or a quote safe.
- **The format is taken from the client, not from the documentation.** Where the two disagree, the
  client is what the user runs. The comment next to the cell records how it was established, so the
  next person can re-run the check instead of re-deriving the conclusion.
- **The provenance block (D138) travels inside `prompt`**, exactly as it does inside Codex's
  `developer_instructions`: a JSON file has no comment syntax, and the prompt is the only field that
  carries free text.
- **`hook × kiro` stays `unsupported`, and the scheduled timer stays its trigger.** Writing a v1
  hook file and reporting it as installed would reproduce precisely the silent no-op D140 refused to
  ship and the false-positive class D189 exists to eliminate. D140's conclusion about the *trigger*
  is therefore not withdrawn — it remains the answer for Hermes, for Antigravity (D194) and for
  Kiro.
- **No version probe, and no `--v3` invitation.** The plan specified
  `kiroGlobalHooksActive(version) bool`, true for major ≥ 3. On the release channel that helper
  would return false on every machine, because the version string stays `2.21.3` with and without
  `--v3` — the version is not the discriminator, and an invitation built on it would state a false
  reason. Nothing here needs one: subagents work on the shipped client, and hooks work on none.
- **`installedSubagentSentence` needed no change.** It derives the sentence from `destDir`, so
  giving Kiro a cell makes the sentence appear on its own — which is what D154 designed it for. The
  comment naming "Kiro and Hermes" as the providers without a subagent directory was corrected to
  Hermes alone.

**Invariants kept.** `skill × kiro` and `instructions × kiro` are untouched — they worked before and
work now. `kiroFlatNamespaceWarning` (D102) concerns the flat **MCP tool** namespace and is
orthogonal. `hookMechanism.noSessionStartEvent` and `SupportsSessionHook` are unchanged, so Kiro
keeps getting the sync-timer advice it needs.

**Consequences.** Kiro clients start receiving subagents they did not have, and a `connect`/`sync`
writes into `~/.kiro/agents/` for the first time — a behaviour change worth a release note. Nothing
about hooks changes. A test that used Kiro as its example of a provider with no `agent` destination
now uses Hermes, which is the last one left.

**Re-check when CLI 3.0 ships.** The hook half of the original plan is not refuted in principle,
only against every released client. It is tracked separately rather than left open here, so this
decision records what was true when it was made.

Details: `docs/interoperability.md` §Kiro hooks, `docs/sync.md` §Kind × provider matrix,
`docs/configurator.md`.
