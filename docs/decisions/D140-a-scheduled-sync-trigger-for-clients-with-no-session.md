---
topic: sync-provisioning
---

# D140 — A scheduled sync trigger for clients with no session hook; Kiro has no registrable one

**WP1 finding (documentation only, 2026-08-27 — no Kiro installation available at the time).** The
docs were self-contradictory: the trigger table (<https://kiro.dev/docs/hooks/types/>) named a
CLI-only `agentSpawn` trigger, while the only complete JSON examples on the feature page
(<https://kiro.dev/docs/hooks/>) used `"trigger": "PostFileSave"` and `"trigger": "Stop"` for
triggers that table calls `fileSave` and `agentStop`. The literal value a spawn hook must carry was
therefore not determinable, and WP2 was deferred rather than implemented on a guess.

**WP1 confirmed empirically (Kiro CLI 2.20.0, installed 2026-08-27).** Three facts, each verified
against the binary rather than the documentation:

1. **The trigger set is exactly `agentSpawn`, `userPromptSubmit`, `preToolUse`, `postToolUse`,
   `stop`** — the serde variant table in `kiro-cli-chat`, confirmed by `kiro-cli-chat agent
   validate`, which accepts those five and rejects `AgentSpawn`, `Spawn`, `SessionStart`,
   `sessionStart`, `agentStop` and `fileSave`. The documented `PostFileSave`/`Stop` examples are
   simply wrong for the CLI; the camelCase table is right.
2. **`~/.kiro/hooks/` plays no role.** It appears nowhere in the CLI's path table. Hooks are a
   `hooks` map **inside an agent config** — `~/.kiro/agents/<name>.json` globally, or
   `<workspace>/.kiro/agents/<name>.json` — each entry an object with a `command`. A global agent
   config is discovered from any working directory, and its `agentSpawn` hook does fire (verified
   with a sentinel file: "1 of 1 hooks finished").
3. **Hooks are per agent, and the agent that runs by default cannot carry one.** The hook fires
   only for the agent selected with `--agent`; running the default agent does not fire it. The
   built-in `kiro_default` cannot be shadowed by a global config of the same name (it stays
   `(Built-in)` and its hook never runs), and `~/.kiro/settings/cli.json` has no global hooks key —
   only display settings such as `hooks.showStatus`.

**Decision.** WP2 (the Kiro session-start hook) is **not implemented**, and this is now settled
rather than pending. `hook` × `kiro` stays `unsupported` in the destination matrix. The mechanism
the plan assumed — a hook file in a directory Cartographer owns — does not exist; the mechanism
that does exist cannot deliver an unconditional session-start hook, because the only way to reach
the agent the user actually runs is to write into an agent configuration Cartographer does not own.
Claiming a user's agent config to install a sync hook is a larger intrusion than the problem
warrants, and a Cartographer-owned agent nobody selects is a hook that never fires — the same
silent failure the deferral existed to prevent.

**Decision.** The scheduled trigger is implemented and is a first-class Layer 1 alternative:
`cartographer service sync-timer install|uninstall|status`, with `--interval` (default 30 minutes),
reusing the launchd/systemd machinery of the server service under its own label
(`com.cartographer.sync`, `cartographer-sync.timer`) and its own log destination. It is opt-in and
explicit: `connect` and `status` name the command once per invocation when a connected provider has
no session-hook mechanism, and never install it — putting a background job on someone's machine as
a side effect of connecting is out of proportion. It runs `cartographer sync` **without**
`--auto-trust`: an unattended job must not grant a trust the user never gave, while the persisted
`trust` setting still applies (D54). Install is idempotent; uninstalling what is not installed
succeeds.

**Rationale.** Kiro was the one supported provider syncing only when a human remembered to, and
D141's Hermes is a second by design — its configuration is owned by an Ansible role, with no place
to register a hook. Both need the same thing: a trigger that does not depend on the client having
hooks. For Kiro the timer is not a fallback but the answer: `agentSpawn` is CLI-only and would
never have covered the IDE surface anyway, and it fires per agent rather than per machine.
