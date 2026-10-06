---
topic: control-plane
---

# D323 — The server applies the safe repairs by itself; a person is asked only what needs one

**Decision.** A KB with no `auto_repair` key has the default list
(`nonstandard_field`, `tool_param_field`, `invalid_field_value`,
`duplicate_link`, `prose_value`); `auto_repair: []` is the explicit "none".
Every `doctor_auto_interval` (default one day, `"0"` off) the HTTP server runs
`kb_repair` for each listed check, at most 50 concepts per check, through the
tool's own write path, one commit per check, `Reason: auto-repair (background)`,
and appends the run to `.cartographer/auto-repair-log.jsonl`. The Atlas gains a
read-only Maintenance panel (last runs, last and next doctor session, the
deferred `open_question` concepts, the repair commits with the command that
undoes each); the undo is `repair_revert` (an advanced tool) or
`cartographer kb repair <kb> --revert <sha>`, accepted only for `kb_repair` and
auto-repair commits.

**Why.** A KB drifted from zero findings to ten in three days of ordinary
writes, and every piece of upkeep (D299) needed someone to start it: no
operator had ever listed an `auto_repair` check, and the doctor proposal
needs an agent session to read it. The five defaults are those whose fix is
deterministic, never rewrites body text and never removes a link; the two
excluded fixable checks that do (`broken_link`, `reciprocal_link_item`) stay
opt-in because D309 showed a link-dropping repair can lose hundreds of edges
in one commit. Going through `kb_repair` instead of a second repair path means
the lock, the stale-write guard, the commit and the sync are the ones every
write already has; a background run that bypassed them would be a second
writer to keep in step.

**Alternatives rejected.**
- One check per interval, round-robin: a full cycle would take five days to
  reach a KB that the plan wanted corrected daily, and each check is cheap and
  idempotent. The per-run cap (50 concepts per check) is the budget instead:
  an imported KB is worked down over several days, not in one huge commit.
- A client hook or an operator cron: hooks fire only in some clients and
  modes (D300) and a cron needs setting up, which is why nothing ran.
- An Atlas "revert" button: the Atlas stays read-only; a write route would
  put a destructive action behind a UI token. The panel shows the exact
  command and copies it.
- Writing the run into `log.md`: it would be a concept-adjacent file, move
  `last_doctor` (D290) and silence the doctor proposal for the judgement work
  the heartbeat cannot do. A local JSONL under `.cartographer/` is neither
  linted nor committed.
- Running the heartbeat in stdio mode: that process belongs to one client
  session, and a write nobody asked for during it would surprise the person
  watching.
- Skipping the run on any `failed` git state: it is sticky after one network
  blip and a quiet KB would never recover; the write path already refuses to
  write when its sync-in fails, so only an open conflict or a `degraded`
  state skips a run.

**Consequences.** An operator who upgrades and runs `kb repair --apply` now
applies the five defaults where the same command did nothing before; the
report says so. `auto_repair` nil versus empty is a distinction of the loaded
config (`KBSpec.AutoRepairChecks`), pinned by a test through the YAML loader;
`kb.KB.AutoRepair` is already resolved. `repair_revert` is hidden from the
agent profile so the D285 `tools/list` budget does not move. A revert that
conflicts with a later edit is undone and refused, never forced: the operator
reconciles by hand. Anything that adds a check to `DefaultAutoRepair` needs a
decision of its own.
