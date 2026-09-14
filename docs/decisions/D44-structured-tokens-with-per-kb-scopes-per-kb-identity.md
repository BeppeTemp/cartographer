---
topic: transport-auth
---

# D44 — Structured tokens with per-KB scopes + per-KB identity/SOPS fields

**Decision.** `config.TokenSpec{Token, Scopes}` replaces `[]string` in `AuthConfig.Tokens`,
with a backward-compatible custom `UnmarshalYAML` (legacy scalar = admin, or mapping with scopes). Format
`token|scope1;scope2`, scopes `kb:<nome>:r|rw`; a token without scopes = admin. `KBSpec` gains
optional per-KB overrides (git identity, `sops_age_key_file`); new `SopsConfig{AgeKeyFile}` —
the zero-value of each override = fallback to the global.
**Rationale.** This milestone is *config plumbing* only: the runtime stays unchanged,
the r/rw enforcement arrives in D45. Separating config from enforcement keeps each milestone green and
committable; the token format remains backward compatible with existing deployments.
Details: `docs/transport-auth.md` §Per-KB authorization (r/rw scopes, HTTP enforcement).
