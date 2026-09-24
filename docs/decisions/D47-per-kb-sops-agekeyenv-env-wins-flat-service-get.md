---
topic: skills-services-secrets
---

# D47 — Per-KB SOPS: `AgeKeyEnv` (env wins), flat `service_get(resolve_secrets)`, resolve requires rw

**Decision.** Connects `KBSpec.SopsAgeKeyFile`/`SopsConfig.AgeKeyFile` (D44) to the first real
invocation of `internal/sops`. `Decrypt`/`DecryptAll`/`ResolveRefs` gain a variadic
`env ...string` (same pattern as `gitx.runGitEnv`, D46: the caller's env wins). `service_get`
gains the `resolve_secrets` parameter (default `false`): if `true`, it reads `secrets_source` from
the `Service`'s frontmatter and calls `sops.Decrypt` with the per-KB age key. OKF frontmatter
supports only `string`/`[]string`, so structured per-ref `secret_refs` remain out of scope:
`resolve_secrets` always decrypts the entire `secrets_source`.
*(Superseded in part by [D166](D166-http-connection-timeouts-and-deleting-three.md): `DecryptAll` was deleted. It never gained a
caller — `service_get` resolves one `secrets_source` at a time through `Decrypt` — so the variadic
`env` this entry gave it was only ever exercised on the other two.)*
`service_get` stays classified `ReadOnly` (D45) for the default path; with `resolve_secrets:
true` the HTTP guard (`mcpAccessGuard`) forces `needWrite=true` as a special case, without touching the
per-tool-name classification. Defense in depth: `filepath.IsLocal(secretsSource)` rejects
path traversal on `secrets_source` before decryption.
**Rationale.** Propagating the `env` instead of mutating `os.Environ()` prevents one KB's age key
from contaminating requests on another in a multi-KB server (same reason as D46). The special case
in the guard keeps all r/rw enforcement in one place, consistent with D45.
*(Completed by [D260](D260-sops-runs-in-a-hermetic-environment.md): passing the key through `env`
isolated only the key Cartographer passes — the child still inherited the whole server
environment, so an ambient `SOPS_AGE_KEY`, default `keys.txt` or SSH key could decrypt any KB.
The child environment is now built from an allowlist.)*
Details: `docs/skills-services-secrets.md` §SOPS secrets, `docs/transport-auth.md`.
