---
topic: client-configurator
---

# D190 — The KBs are chosen before the first write, not narrowed after it

**Status: implemented.** Closes #244.

**Context.** A first `connect` against a multi-KB server had an over-exposure window that no
ordering of the existing commands could avoid:

1. a fresh `.cartographer.yaml` carries no explicit per-provider binding, so `BoundKBs` resolves to
   "every known KB";
2. `doConnect` enumerated the mounted KBs, wrote the MCP entries and materialized every artifact of
   that default projection;
3. `cartographer client bind` refuses a provider that is not connected yet — correctly, since a
   binding nobody reads would be meaningless.

So the operator could only narrow a provider **after** the first `connect`, which is after every
skill, agent, hook, instructions block and MCP descriptor of every known KB had already been
delivered. On the machine that produced this audit, that is how a WORK-specific skill reached a
HomeLab-only client. [D169](D169-per-provider-kb-binding-a-user-owned-preference-beside.md)/[D170](D170-selection-before-the-merge-each-provider-is-projected.md)'s binding fixes the steady state, not the first
write.

The default was also **sticky**: "all known" includes KBs mounted later, so a projection chosen
implicitly on day one silently widened as the server grew.

**Decision.**

- **The selection is part of `connect`, and it is applied before anything is written.** `--kb`
  (repeatable or comma-separated) is resolved and validated immediately after the KB enumeration and
  persisted into the provider bindings **before** the MCP entries and before materialization. The
  window is closed by moving the choice, not by moving `bind` earlier.
- **A *new* multi-KB connect fails rather than defaults.** With two or more KBs mounted, no `--kb`
  and no provider already bound, `connect` errors naming the mounted KBs and the flag, and writes
  **nothing** — a test asserts the target directory is untouched. A single-KB server keeps working
  with no flag: there is nothing to choose.
- **A provider that already carries an explicit binding is not a first connect.** Its recorded
  choice stands and a re-run or a `reconnect` never re-opens a catalogue the operator narrowed. This
  is also what keeps existing installations working unchanged: nobody is migrated, and no existing
  binding changes meaning on upgrade.
- **`--kb all` is a real, recordable value.** It is persisted as an explicit binding to the
  currently mounted names, not as the implicit default. That difference is the whole point: a KB
  mounted tomorrow does not widen a client that already exists.
- **The selection is validated against what the server mounts, before any write.** A typo is an
  error naming the available KBs, not an empty projection. `--kb ""` is an error rather than
  "none", and `--kb all` cannot be combined with a name.
- **The interactive choice is a step after the probe, not a field of the connect form.** The KB
  names do not exist until the server has been probed, and the probe runs after that form. A
  separate selection step keeps the choice where the information is; nothing is pre-selected,
  because a pre-ticked list is how "all of them" quietly becomes the answer again.
- **`--dry-run` applies the binding in memory** so the projection it reports is the one the
  selection would produce. Only the write is skipped.

**Invariants kept.** `requireConnected` still refuses bindings for unconnected providers — this
removes the *need* to call `bind` after `connect`, it does not weaken `bind`. The bootstrap hook
stays independent of the server manifest. Stale MCP entries are still removed before the new ones
are written. The MCP entry naming still keys off the server's **full** mount list, not the
selection: collapsing a single-KB choice to the bare, unscoped entry would have produced a client
that reaches every KB on the server — the opposite of the intent, and caught by a test.

**Consequences.** A scripted first `connect` against a multi-KB server now requires `--kb`;
that is breaking for automation and belongs in the release notes. No ordering of commands can
produce a materialized artifact from a KB the operator did not name.
