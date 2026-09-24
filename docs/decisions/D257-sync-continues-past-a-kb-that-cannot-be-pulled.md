---
topic: sync-provisioning
---

# D257 — Sync continues past a KB that cannot be pulled

**Decision.** When the `sync_pull` of a named KB fails (the call errors, or the response does not
decode or verify), `cartographer sync` goes on without that KB. Every provider with a projection
bound to it is dropped from the run before anything is written, so its MCP entries, artifacts and
lockfile entry stay exactly as they were. The other providers are applied. The run then fails
(exit 2) with one error listing each failed KB and the providers it skipped. If no provider is left,
or no KB answered, nothing is written and the error says so, as before. Only `runSync` behaves this
way; `connect`, `status` and the TUI still treat any failed pull as fatal.

**Why.** One KB with a bad remote (#348: a fetch that hung) blocked every provider, including those
not bound to it, and left `upgrade-repair` unable to finish. `--client` filters by provider, so it
could not route around a KB. The binding already says which providers depend on which KBs, so the
set a failure can affect is known before anything is written. Dropping the whole provider, not only
the affected projection, keeps "kept its previous state" literally true: a half-applied
workspace-scoped provider would have new MCP entries next to old artifacts. What it costs: a run can
now end in an error after writing some providers, so "an error means nothing changed" no longer
holds for sync. The error states which providers were left untouched, and the ones it does not
name were applied.

**Alternatives rejected.**
- A `-skip-kb <name>` flag with all-or-nothing as the default: the operator first has to see the
  failure, then rerun by hand, and the unattended paths (session-start hook, sync timer,
  `upgrade-repair`) never pass flags, so they would still be blocked.
- Skipping only the projection bound to the failed KB: a workspace-scoped provider would be partly
  updated, and its MCP entries would no longer match its artifacts.
- Exiting 0 with a warning: the session-start hook and the timer would report success while a
  provider silently fell behind.

**Consequences.** `fetchCandidates` keeps its all-or-nothing contract through
`fetchCandidatesPartial`, which returns per-KB failures in target order; a new caller that can
continue without a KB uses the partial form explicitly. A failure that belongs to no single KB (the
unnamed endpoint, a cross-KB signature conflict, `/health`) stays fatal. `upgrade-repair` reports the
sync as pending on a partial run, which is accurate for the skipped providers.
