---
topic: sync-provisioning
---

# D717 — A workspace projection writes Cartographer's own MCP entry, restricted to its KBs

**Decision.** `sync` writes, into the project `mcp` cell of each bound workspace, an entry named like
the global one (`cfg.ServerName`) with the routed URL `?kbs=<workspace KBs>`, recorded in the
workspace lock as `kind: mcp`, `source: client` (`provisioning.SourceClient`). It is written only for
clients that `configurator.ProjectMCPShadowsGlobal` marks as letting a project server replace the
global one, only against a routed server, and never when the binding is empty or fully unmounted.

**Why.** Skills were scoped per workspace, the tools were not: a session in a workspace still saw
every KB of the global entry. A same name makes the project entry shadow the global one, so the
session sees one server. Shadowing is per client and was verified from vendor docs only (no probe, on
2026-10-11): Claude Code documents whole-entry replacement (`shadows`); Codex, Kiro, OpenCode,
Antigravity and Crush do not state entry-level replacement and are treated as not shadowing, with a
warning instead of an entry, until a probe says otherwise (outcomes in `docs/harnesses.md`). No
auto-approval: Claude Code asks the user to approve project servers, Codex needs a trusted project.

**Alternatives rejected.** A distinct entry name: both entries stay visible and the restriction
means nothing. Per-KB `?kb=` entries: different names, cannot shadow. A manifest-signed synthetic
artifact: the entry depends on client-local state (URL, token env) the server does not know.
Writing an entry without `kbs=` for `--no-kb`: empty means every KB, the opposite of the intent.

**Consequences.** The `source: client` lock value keeps the entry out of `ComputeDiff` removals and
carries it through `Apply`; unbind needs no new code. A KB `mcp` artifact with the server's name is
ignored with a warning in a workspace. Flipping a client to `shadows` is one line in
`ProjectMCPShadowsGlobal`, after a probe recorded in `docs/harnesses.md`. Like the global entry, the
workspace entry is fixed at session start.
