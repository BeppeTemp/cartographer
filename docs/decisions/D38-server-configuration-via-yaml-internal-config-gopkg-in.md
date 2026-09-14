---
topic: deployment-release
---

# D38 — Server configuration via YAML (`internal/config`, `gopkg.in/yaml.v3`)

**Decision.** `cartographer serve --config file.yaml` (or `CARTOGRAPHER_CONFIG`) loads
`internal/config.Config` with precedence **CLI flags > env > YAML > default**; `kbs: []` accumulates
across levels, scalar fields follow the standard precedence. New direct dependency
`gopkg.in/yaml.v3` (infrastructure config does not have `internal/okf`'s stdlib-only constraint).
**Rationale.** Flags/env do not scale beyond a few scalar options: `kbs: []` with mixed
`remote`/`path` and the git/audit config require nested structure. YAML is consistent with
`config.example.yaml` mounted from a ConfigMap; the flag>env>YAML precedence preserves full
backward compatibility with pure flag/env usage.
Details: `docs/deployment.md` §Configuration: flags, env, YAML.
