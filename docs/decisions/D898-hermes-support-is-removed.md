---
topic: client-configurator
---

# D898 — Hermes support is removed, with the mechanisms only it used

> Supersedes [D141](D141-hermes-is-a-supported-provider-that-receives.md) and the Hermes half of
> [D364](D364-antigravity-and-hermes-project-cells.md) (Antigravity's project cells stay).

**Decision.** `hermes` is no longer a provider. The registry, the destination matrices, the folder-trust
check, the inbox delivery and its `SOURCE.md` are gone, and so are the mechanisms that existed for it
alone: `ManagesMCPConfig`, `BaseDirEnv` with `ResolveBaseDir` and `BaseDirFor`, the `provider-root`
detection heuristic, `perNameIn` and the matrix `tail`. `SOURCE.env` (D316) is generic and stays. A
provider name that the registry does not know is a generic case, not a Hermes one: `clientconfig.Load`
drops it, and its bindings, scopes and workspace bindings, from the in-memory config, and `sync`,
`connect`, `status` and `doctor` print once `<name>: this agent client is no longer supported …`. The
first command that writes the config removes the name from it, and `sync` drops the provider's lockfile
sections; no file Cartographer delivered earlier is touched.

**Why.** Hermes was an experiment that cost more than it gave: the only provider without a managed MCP
config, the only one with its own base dir, and the reason several mechanisms looked generic when they
were not (#737 had to exclude it from the stdio bridge). Anyone who wants Hermes can point it at the MCP
endpoint by hand, as with any MCP client. The cost is a breaking change for whoever connected it.

**Alternatives rejected.** Keeping it frozen: it still taxes every new provider feature. Failing on a
config that lists it: `sync` runs unattended (hook, timer), so a stale name would stop every other
client. Pruning its lock entries on upgrade: the lock's `base_dir` points at a directory Cartographer no
longer knows how to resolve, and deleting files without telling the user is the failure
`internal/provisioning/AGENTS.md` exists to prevent, so the files stay and the notice names where.

**Consequences.** `Lock.BaseDir` stays, read-only for global sections: it was not Hermes-only, a
workspace projection records its repository there (D193). A lockfile written before this change still
loads, and its Hermes section is kept as data until a `sync` drops it. A future provider removal needs
no code: the same path handles any name the registry no longer knows. Release note: Hermes is no longer a
supported provider; files delivered earlier into `$HERMES_HOME` are left in place.
