---
topic: deployment-release
---

# D21 — Configuration via environment variables

All CLI options have a corresponding env var (`CARTOGRAPHER_KB`, `CARTOGRAPHER_HTTP`, `CARTOGRAPHER_TOKENS`). The CLI flag takes precedence over the env var. `CARTOGRAPHER_AUTH` is env-var-only with three states: `true` (requires auth, fatal if no tokens), `false` (disables), unset (auto — enabled if tokens are present). Resolution happens upfront in `main()` via `envFallback(flag, envKey)`. No changes to `internal/auth`.
