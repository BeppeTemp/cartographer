---
topic: client-configurator
---

# D2118 — `sync` exits 3 when the symlink guard refused a write

**Decision.** A real `cartographer sync` exits 3 when a provider ended with a part the guard refused (D148): the MCP entry or a kind of artifact in `AppliedResult.Refused`, the same set `connect` names (D1722, `skippedParts`). One stderr line names provider and parts. A `--dry-run` exits as before, a partial sync (exit 2) wins over 3, and a refused hook registration or removal stays a warning.

**Why.** `sync` printed the refusal as a warning and exited 0, so the timer and any script saw a clean run over an agent that stayed unwired, the case D1722 closed for `connect` (#2099). A link that stays refused now fails every run, which is the point.

**Alternatives rejected.** A new code 4: it diverges from `connect`, which D1722 fixes at 3. Exit 2: it means a KB failed or the configuration is wrong, and the other providers were written. A flag to opt in: the script that ignores the flag is the one that needs the signal.

**Consequences.** Exit 3 of `sync` is a contract scripts may read. 3 also means "client lock denied" (#1961): the stderr line tells them apart (`sync refused to write: <provider> (<parts> skipped)`). The session-start hook scripts always `exit 0` and are unaffected. `upgrade-repair` keeps calling `runSync` and ignores the refused set. `approve … --sync` returns the sync's code, so an approval followed by a refused write exits 3 too. A scheduled sync shows a failed run while a link stays refused, which is the point.
