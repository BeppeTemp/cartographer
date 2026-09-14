---
topic: skills-services-secrets
---

# D96 — Operations knowledge ships as a bundled skill

**Status: implemented (2026-07-24).**

**Decision.** `cartographer-ops` is a single bundled skill containing the operational server/client
playbook: CLI surface, configuration precedence and load-bearing environment variables,
health-based diagnosis, drift recovery, conflict routing, and native/k8s upgrades. It travels in
the embedded bundle and is provisioned with the other bundled skills; the local test suite loads
and validates every bundled skill and asserts the manifest inventory.

**Rationale.** A bundled skill stays aligned with the installed binary, whereas documentation on
`main` can describe a newer pre-1.0 CLI and tool surface. One concise operational reference keeps
the end-to-end runbook available to agents on client machines that do not have the repository
checked out.
