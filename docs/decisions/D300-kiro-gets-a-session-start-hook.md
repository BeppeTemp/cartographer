---
topic: sync-provisioning
---

# D300 — Kiro gets a session-start hook through a standalone file Cartographer owns, limited to the V3 TUI

**Decision.** KB hooks and the bootstrap hook (D60) reach Kiro as entries of
`~/.kiro/hooks/cartographer.json`. This is a standalone `"version": "v1"` hook
file that Cartographer owns whole. It is regenerated from the set of registered
hooks and removed with its last entry. The hooks' files sit in
`~/.kiro/hooks/cartographer/<name>/`. Kiro fires these hooks only in
`kiro-cli chat --v3 --tui`, so the hook mechanism declares that limit
(`SessionHookLimit`). `status` qualifies the hook count with it, and `connect`,
`status` and `doctor` keep advising the scheduled timer (D140) for Kiro, naming
the limit instead of calling Kiro hookless (D189). `SessionStart`,
`UserPromptSubmit` and `Stop` are declared to reach Kiro. Its tool events are
not, because nothing exercised them.

This supersedes D140's Kiro conclusion, "Kiro has no registrable hook". In the
V3 TUI it does. On 2.26.1 the built-in `kiro_default` can also be shadowed in
v2, which D140 had found impossible on 2.20.0. Shadowing stays refused.

**Why.** The question D140 asked was never whether Kiro has hooks, but whether a
hook Cartographer writes fires in normal use. Re-probes on Kiro CLI 2.26.1 and
2.27.0 split the answer by mode. Standalone hook files fire under
`chat --v3 --tui` and nowhere else: not in the default interactive UI (a new TUI
over the v2 engine since 2.27.0), and not in any `--no-interactive` run. An
agent's own `hooks` fire in every mode, but only for that agent. The standalone
file is the only mechanism that needs nothing of the user's: no agent config,
no setting, no file it does not own. The cost is coverage. Most Kiro sessions
still sync only on the timer, and saying "installed" without the limit would be
the false positive D189 exists to stop.

The loader reads only the `*.json` files directly inside `~/.kiro/hooks/`. This
is KAS `loadHooksDir`, and a probe confirmed that a file in a subdirectory was not
loaded. So the scripts can live one level below the registration without being
parsed as hook files themselves. The schema requires at least one entry, so the
file must go rather than stay empty.

**Alternatives rejected.**
- *Shadow `kiro_default` with a copy that adds a hook.* This replaces the
  built-in's prompt and tools, which is the intrusion D140 refused.
- *An opt-in Cartographer-owned default agent (`chat.defaultAgent`) with an
  `agentSpawn` hook.* This was the plan's WP2. It was dropped on its own
  verify-first step (#493). An agent with no `tools` key gets no tools (0
  against `kiro_default`'s 14), and the default's prompt is compiled into the
  binary. Matching the default means declaring the tool set and copying a
  vendor prompt that changes between versions.
- *Write a hook into the user's own agent configs.* An edit to a file the user
  curates, firing only for the agents Cartographer happened to find.
- *Mirror the registration into the workspace `.kiro/hooks/`.* It fires in the
  same single mode as the global file, so it reaches no session the global one
  misses. The workspace cell stays `unsupported`.
- *Record `cartographer.json` as a managed file of each hook.* Prune removes a
  managed file by path, so pruning one hook would delete every other hook's
  registration. The hooks' own files are the managed record, and the shared
  file is edited by entry name, like Antigravity's `hooks.json`.
- *Rewrite hook commands to forward slashes on Windows (D267).* Kiro runs
  command hooks through PowerShell or `cmd.exe` on Windows, not a POSIX shell.

**Consequences.** A non-recursive loader is now a dependency of the matrix
cell. If Kiro ever reads subdirectories of `~/.kiro/hooks/`, every materialized
`hook.json` becomes a malformed hook file, and the cell has to move out (the
comment at the cell and `harnesses.md` say so). When the default `kiro-cli chat`
starts firing standalone hooks, drop the limit on the Kiro hook mechanism, and
the timer advice goes with it. Kiro IDE and Kiro Crew stay out of the matrix
until they are probed on a machine that has them. Cross-references:
[D140](D140-a-scheduled-sync-trigger-for-clients-with-no-session.md),
[D195](D195-kiro-receives-subagents-its-hooks-are-documented-but.md),
[D189](D189-instructions-written-correctly-are-not-reported-as.md).
