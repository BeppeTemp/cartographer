---
topic: client-configurator
---

# D137 — Declarative provider registry: two tables, owned by the packages that own the concepts

**Decision.** Provider knowledge becomes data in two tables rather than one global registry.
`internal/configurator/registry.go` owns provider **identity and config-file shape**: one
`Descriptor` per provider with its constant and wire value, display name, native MCP config file
and format (JSON with its server key, or a marker-delimited TOML block), whether that file may be
deleted once emptied, whether it can carry MCP auth headers, whether its tool namespace is flat
across servers, its detection evidence (executable names in probe order — a provider may ship its
IDE and CLI surfaces under different ones, as Kiro does with `kiro` and `kiro-cli` — config
directories in probe order, optional macOS app bundle), and its emitter. `internal/provisioning` owns the **kind × provider destination
matrix** plus the `hookMechanisms` table describing how each provider registers a hook natively.
Two orders are exported and both preserved because both are user-visible: `Providers()` (emission
and client iteration) and `DetectionOrder()` (`cartographer agents` and the TUI).

Every matrix cell either names a destination or is marked `unsupported`. A cell **missing** for a
known (kind, provider) pair is a programming error caught by a completeness test, not a silent
degradation to `Unsupported` at apply time — before this, a forgotten switch arm and a deliberate
"this provider cannot do hooks" were the same empty string. An unknown *kind* or *provider*
(a manifest from a newer server) still yields "" and is filtered upstream by `FilterForProvider`;
previously an unknown kind fell through to the skill arm and would have been materialized as one.

**What deliberately stays code.** The four MCP emitters and `translateAgentForProvider`: the output
formats genuinely differ, and data-driving them would only relocate the difference. Two documented
special cases keep naming a provider directly: `configurator.Remove`'s cleanup of the legacy
`.codex/config.json` an old version wrote, and `sync_apply`
(`internal/mcpserver/tools_sync.go`), where the server materializes for Claude Code by
construction — it is not choosing among providers.

**Rationale.** Adding a provider meant editing the same four constants across eight files, and the
matrix documented in `sync.md` existed in the code only as five nested switches whose `default` arm
returned a bare empty string — the shape was invisible to a reader and unenforced on a contributor.
A single global registry was rejected: `provisioning` imports `configurator` and never the reverse,
so putting destination paths in the lower layer would invert the dependency and place provisioning
knowledge in the wrong package. Two tables, each owned where the concept lives, keep the direction
intact. The refactor is behaviour-preserving by construction: the existing suite, including the
connect/disconnect round trip and the configurator goldens, passes unmodified.
