---
topic: client-configurator
---

# D42 — `cartographer disconnect`: inverse of `connect`, inverse JSON merge, full provider prune

**Decision.** New subcommand `cartographer disconnect [provider|all]`, business logic
shared (`doDisconnect`) between CLI and TUI. Per provider: `configurator.Remove` (inverse of
`Apply`) removes only the server's entry from the MCP config file without destroying the rest;
`provisioning.PruneManaged` removes **all** the provider's managed files from the lockfile — not just
the diff against the manifest — because `disconnect` must not depend on the server being
reachable.
**Rationale.** Reuse instead of duplication: `PruneManaged`/`Remove` are the same functions used
by `Apply`, applied in the opposite direction. The "full" prune (the whole managed set) is the
fundamental difference from `sync`: disconnecting is a local operation.
Details: `docs/configurator.md` §`cartographer disconnect`.
