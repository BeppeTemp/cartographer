# Skills, services and secrets decisions

Bundled skills, service secret resolution and operational skill distribution. Current behavior: [`../skills-services-secrets.md`](../skills-services-secrets.md).

These records explain why choices were made and may describe superseded behavior.
For the supported interface, follow the current-state page linked above.

<a id="d26"></a>
## D26 — Bundled skills embedded in the binary

**Decision.** Recovery and operator skills are embedded through `internal/skillbundle`
using `//go:embed all:bundled`; `skill_list` and `skill_install` can therefore
serve a known bundle without relying on files beside the executable.

**Rationale.** The skills needed to bootstrap or recover a KB must remain available in
single-binary installations and after an incomplete client provisioning cycle.

---

<a id="d47"></a>
## D47 — Per-KB SOPS: `AgeKeyEnv` (env wins), flat `service_get(resolve_secrets)`, resolve requires rw

**Decision.** Connects `KBSpec.SopsAgeKeyFile`/`SopsConfig.AgeKeyFile` (D44) to the first real
invocation of `internal/sops`. `Decrypt`/`DecryptAll`/`ResolveRefs` gain a variadic
`env ...string` (same pattern as `gitx.runGitEnv`, D46: the caller's env wins). `service_get`
gains the `resolve_secrets` parameter (default `false`): if `true`, it reads `secrets_source` from
the `Service`'s frontmatter and calls `sops.Decrypt` with the per-KB age key. OKF frontmatter
supports only `string`/`[]string`, so structured per-ref `secret_refs` remain out of scope:
`resolve_secrets` always decrypts the entire `secrets_source`.
`service_get` stays classified `ReadOnly` (D45) for the default path; with `resolve_secrets:
true` the HTTP guard (`mcpAccessGuard`) forces `needWrite=true` as a special case, without touching the
per-tool-name classification. Defense in depth: `filepath.IsLocal(secretsSource)` rejects
path traversal on `secrets_source` before decryption.
**Rationale.** Propagating the `env` instead of mutating `os.Environ()` prevents one KB's age key
from contaminating requests on another in a multi-KB server (same reason as D46). The special case
in the guard keeps all r/rw enforcement in one place, consistent with D45.
Details: `docs/skills-services-secrets.md` §SOPS secrets, `docs/transport-auth.md`.

---

<a id="d96"></a>
## D96 — Operations knowledge ships as a bundled skill

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
