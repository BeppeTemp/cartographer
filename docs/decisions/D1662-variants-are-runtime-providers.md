---
topic: client-configurator
---

# D1662 — A client variant is a runtime provider registered beside the built-ins, never inside their lists

**Decision.** `configurator.RegisterVariant` builds a `Descriptor` from the variant's base (its emitter, wrapped to rewrite `EmitResult.Provider` and `FilePath`), with the variant's name, binaries, MCP file and detection directories (a last segment holding a glob character fills `ConfigDirGlobs`, any other `ConfigDirs`). `Lookup` falls back to the registered variants; `Variants()` and `AllProviders()` list them; `agents.Detect` appends one `Agent` per variant (`Variant`, `Base`) after the built-ins, using the same probes as any provider. `Providers()`, `ProviderList()`, `DetectionOrder()` and `EmitAll` stay built-in. Amends D137: identity lives in `configurator`, destinations stay in `provisioning`.

**Why.** Every completeness test and every "for each provider" loop (capability table, ledger parity, the destination matrix) assumes the built-in set. A variant that entered those lists would change what they check, or fail them. Registration is data-driven so a KB can declare a fork the repository cannot name.

**Alternatives rejected.** Adding variants to `descriptors`: breaks the completeness tests and `EmitAll` would write a fork's file on every connect. A second lookup function only for variants: every caller of `Lookup` (`EmitServer`, `UsesCLI`, `DeleteOnlyIfCreated`) would need to know it. Importing `internal/clientvariant` into `configurator`: an import cycle, so the registry takes a plain `VariantSpec`.

**Consequences.** A call site asking "which provider owns this file" must use `AllProviders()`; triaging them is the destination-cells and sync steps. A name that is a built-in, already registered, or a base that is not a built-in is refused. A fork of a probed base (Pi) is probed as the base. Nothing registers a variant yet, so no user sees a change.
