---
topic: client-configurator
---

# D1722 — `connect` names what a refused destination left unwired and exits 3

**Decision.** After a real run `connect` prints one status per provider: `connected` (nothing refused), `partially connected` (some parts written, some refused by the symlink guard, D148) or `not connected` (the MCP entry was refused, whatever else was written). The parts come from what the run refused: the MCP entry, the hooks, and the kinds in `AppliedResult.Refused`. `wrote MCP entry` is printed only for an entry that was written, in `sync` too. When at least one provider ends partially or not connected, `connect` (and `reconnect`, which reuses its output) exits 3; `setup` carries on with the other providers, then ends with the providers not wired and exits 3 instead of announcing "Cartographer is set up.".

**Why.** The per-provider "warn and continue" of D148 is right, but the summary after it said `connected` and exited 0 while the agent could not reach Cartographer at all, so a script or `setup` reported success over nothing. The signal has to be in the exit code, not in an abort, because the other providers must still be wired.

**Alternatives rejected.** A hard failure (exit 1 or 2): one linked config would abort a run that wired every other agent, and 1 and 2 already mean failure/drift and configuration. A flag to opt in to the distinct code: a script that does not know the flag is the one that needs it. Treating every refusal as "not connected": a refused hook leaves the agent able to use Cartographer, so "partially" is the true word.

**Consequences.** Exit 3 of `connect` is a contract scripts may read; 0, 1 and 2 keep their meaning. A new part the guard can refuse must be named in the status line (it comes from `AppliedResult.Refused` or the hook refusals). The `doctor` fix for a missing MCP entry behind a link names the link: `reconnect` refuses it as `connect` does, and the opt-in to write through a link is not decided here (#1692).
