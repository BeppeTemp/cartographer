---
topic: client-configurator
---

# D1720 — A fork of a client is client tooling, not knowledge

**Decision.** The KB-declared client variants are removed: the `variant` artifact kind, `variants/<slug>.yaml`, the descriptor package, the runtime registration, the approval chain and its path fingerprint, the cache, the cells and hook roots for forks, the seed MCP mode and the `variant_invalid` check. A KB's `variants/` folder is just a folder. This supersedes D1657, D1662, D1695, D1698, D1700 (except its two kept pieces), D1701 and D1702, which are left as written.

**Why.** A closed fork of a client is something a machine runs, not something a KB knows: tying it to a KB gives a KB write token the power to add a provider to every connected machine, and needs an approval chain to contain that. The work was unreleased, so it is deleted rather than migrated; a leftover `.cartographer-variants.json` is never read and a leftover `path_fingerprint` in `mcp_approvals` is ignored by the decode.

**Alternatives rejected.** Keeping the KB kind behind a flag (the approval chain stays and the trust model is still wrong); migrating existing variants (none shipped).

**Consequences.** Two general pieces stay because built-in providers use them: the instructions sentence for skills a client has no place for (`UnplacedArtifacts`; `pi` and `cline` have no project skill cell) and the `cli` MCP mode plumbing (`Descriptor.MCPMode`, `UsesCLI`, `include_cli_skill`), now set by no descriptor. Providers return later as a contribution of a client-side plugin, not of a KB: a plugin contributes providers.
