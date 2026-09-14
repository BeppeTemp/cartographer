---
topic: project-governance
---

# D111 — Repository E2E tests are deterministic and model-free

**Decision.** Remove the four OpenCode/LLM-driven scenarios and their
agent/sandbox helpers. Keep the six operator scenarios as the repository's
end-to-end suite because they deterministically exercise client configuration,
provisioning drift, git synchronization/conflicts and scoped authorization
through the compiled binary. Run that suite and the HTTP smoke test in CI.

Model interpretation and provider quality are evaluated in real usage, not as
a required repository check. `test/e2e/` now means protocol/process
end-to-end, not "an LLM agent performed the task".

**Rationale.** The LLM scenarios required an external endpoint, were never
executed by GitHub Actions and had no committed maintenance after the initial
public release. Their outcome depended on model availability and behavior.
The operator scenarios require no credentials and cover cross-component
boundaries that isolated Go tests do not.

**Consequences.** `make e2e` is deterministic and mandatory in CI;
`make e2e-quick` and the model-related environment variables are removed. The
HTTP smoke script moves from the otherwise-empty `scripts/` directory to
`test/smoke/`. The obsolete `make migrate` target is removed because its
referenced script no longer exists and wiki migration is provided by
`cartographer import`.
