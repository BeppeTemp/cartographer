---
topic: transport-auth
---

# D179 — Invalid auth configuration fails startup instead of widening access

**Decision.** `config.ValidateAuth` runs on the effective auth configuration —
after YAML, environment and flags are merged — and refuses empty credentials,
scopes that are not exactly `kb:<name>:r|rw`, token-less environment entries,
unknown roles and unknown modes, for every mode including `off`.
`auth.ParseScopesStrict` is the strict half of the existing grammar.
Unrestricted access is declared by the caller (`Policy.Admin`), never inferred
from an empty permission set, and `TokenStore.RequireAuth` makes an enforcing
store fail closed when it holds no usable token.

**Rationale.**

- **Two paths turned a configuration mistake into more authority.** A token
  whose declared scopes all failed to parse reached the store with an empty
  permission set, and the store read that as the pre-scopes admin token — so
  `scopes: ["kb:finance"]`, a missing `:r`, granted every KB. Separately,
  `resolveAuth` gated enforcement on the number of *configured records* while
  the store kept only *usable* ones, so `mode: on` with an empty token value
  produced an empty store, `IsEnabled()` false, and every request served with a
  local admin principal.
- **The ambiguity had to be removed at the layer that can see it.** Only the
  configuration layer can tell "the operator wrote no restriction" from "the
  restrictions the operator wrote produced nothing"; both reach the store as an
  empty policy. So the store stopped guessing and the caller now states intent.
- **A warning was not enough.** D118 already warned that such a token had full
  admin access (`scopedTokensWithRoles`). The hazard was correctly identified;
  a log line simply cannot stop a listener from starting, and this is a
  fail-open, so it became a startup error.
- **Validation moved to the effective configuration.** `ValidateAuthRoles` ran
  only inside `config.Load`, leaving every environment- and flag-supplied token
  unchecked — which is how a deployment configured entirely through
  `CARTOGRAPHER_TOKENS` was validated not at all.
- **An unknown access value is not read access.** `kb:docs:write` used to parse
  as read. Granting something nobody wrote is the same class of defect as
  granting everything, so the tolerant parser now discards it and the strict one
  rejects it.
- **A token-less `|scopes` entry is retained, not skipped.** Dropping it changed
  the record count that gates enforcement; carrying it through lets validation
  refuse it by name.
- **Validation runs with `mode: off`.** A typo that is harmless today becomes an
  exposure the day someone enables authentication, and by then nobody is
  re-reading the file.
- **Diagnostics never echo a token or a raw scope string.** A scope field is
  exactly where a credential gets pasted by mistake, and startup output is
  copied into issues and logs. Tokens are named by index or by `id`.

**Consequences.** A deployment whose auth configuration was invalid — and which
therefore ran with more authority than written, or with authentication silently
off — now fails to start with a message naming the offending token and problem.
That is the intent, and it is the migration note the release needs. Valid
configurations, including legacy unrestricted tokens and the auth-disabled local
mode, are unchanged.
