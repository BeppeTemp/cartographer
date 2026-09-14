---
topic: client-configurator
---

# D51 — MCP server name fixed to "cartographer"; auto-trust hint with the exact command

**Decision.** The name under which the server is registered in the providers' MCP configs is no longer
configurable via flag/form: it is always **`cartographer`** (escape hatch: `server_name` in
`.cartographer.yaml`). Every "needs approval" message now prints the exact command to run
(`cartographer sync --auto-trust`) via `autoTrustCommand`, instead of the vague "use --auto-trust".
**Rationale.** The name was a knob with no real use cases that lengthened the form and risked
duplicate MCP entries on every rename.

*(Superseded state: `--global` removed from `autoTrustCommand`/everywhere → D52.)*
