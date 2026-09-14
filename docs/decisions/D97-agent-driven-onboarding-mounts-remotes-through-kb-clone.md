---
topic: deployment-release
---

# D97 — Agent-driven onboarding mounts remotes through `kb clone`

**Decision.** `cartographer kb clone <remote> [name]` is the local-service entry point for an
existing first KB. It derives and validates the destination name, clones only into the managed data
directory, validates the result as OKF, and removes a failed or invalid clone. The stable,
agent-addressed `docs/agent-install.md` runbook covers installation, service setup, mounting,
provider connection, and verification; README exposes it as a copy-paste prompt template.

**Rationale.** A hand-run `git clone` cannot enforce the data-directory, name, cleanup, and OKF
invariants that make a directory mountable by the service. Keeping the bootstrap runbook separate
from the human tutorial gives an agent a raw-URL-safe, imperative procedure before Cartographer or
its provisioned `cartographer-ops` skill exists locally.
