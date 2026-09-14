---
topic: client-configurator
---

# D52 — `.cartographer.yaml` always machine-wide (home): project/global scope removed

**Context.** Every client subcommand and the TUI had a `--global`/`g` flag/key to choose
between `.cartographer.yaml` in the cwd or in the home. The project scope was never used: the
connection to a server is a property of the machine, not of the repo in which the command runs.

**Decision.** `.cartographer.yaml`/the lockfile **always** live in `~/`. `clientconfig.
TargetDir()` no longer takes a `global` parameter. Removed everywhere: the `--global` flag, the `g`
key in the TUI, the `global` parameter from `printApplySummary`/`autoTrustCommand`.

**Discarded alternatives.** Keeping the flag with a `true` default (it would have left a dead
project-scope codepath to maintain for no benefit).
Details: `docs/configurator.md` §`.cartographer.yaml`.
