# Skills, services, and secrets in the KB

Every KB is a self-contained package: besides knowledge, it carries domain **skills**, external **services**, and their **secrets**. The configurator (→ [`interoperability.md`](interoperability.md)) installs and connects them.

## Domain skills (SKILL.md)

**Format**: the open `SKILL.md` standard (agentskills.io). A skill is a folder `skills/<kb-ns>--<skill-name>/SKILL.md` (folder name == `name`, lowercase-hyphenated). Minimal frontmatter: `name` + `description` (rich in activation keywords), plus `license`, `metadata.version` (semver, aligned with the KB's git tags), `compatibility`. Body `< 500 lines / < 5000 tokens`.

**Skill folder structure**:

```
skills/<kb-ns>--<name>/
├── SKILL.md           # manifest + instructions
├── scripts/           # service clients
├── references/        # on-demand docs
├── assets/            # templates
└── evals/             # tests
```

**Progressive disclosure**: ~100 tokens/skill catalog at startup; body loaded on activation; resources on demand.

**Namespace**: `kbiam--…` prefix to avoid collisions between KBs and with wiki-management skills.

**Distribution**: the KB's git repo **is** the registry; materialization into the providers' native directories is handled by provisioning with drift detection and pruning (→ [`sync.md`](sync.md)). The core stays portable with only `name`+`description`+relative paths.

**Supply-chain security**: skills (and hooks) are **executable code** with the privileges of the process.
- **Signing** required (signed commits or Sigstore/cosign) with **pre-execution verification**.
- **Pinning to a reviewed tag/sha** (not symbolic `HEAD` → would propagate a malicious commit).
- A gate that rejects unsigned artifacts.
- **Least-privilege**: a skill only receives the `secret_refs` of the service it declares.
- **No secrets** in skill files.

## Services — `type: Service` descriptors

A service describes how to connect to an external system. It's an OKF concept of `type: Service`:

```yaml
---
type: Service
id: svc-keycloak
title: "Keycloak (IAM)"
version: "1.2.0"
kind: idp                           # idp | db | queue | object-store | http-api …
protocol: https
base_url: "https://keycloak.example.internal"
endpoints:
  token:    "/realms/{realm}/protocol/openid-connect/token"
  admin:    "/admin/realms/{realm}"
auth:
  method: oauth2_client_credentials
  token_endpoint: token
  scopes: ["openid"]
secret_refs:
  - { name: KEYCLOAK_CLIENT_ID,     secret: keycloak/client_id }
  - { name: KEYCLOAK_CLIENT_SECRET, secret: keycloak/client_secret }
secrets_source: "secrets/keycloak.sops.yaml"
expose_as_mcp: false
---
```

**Skill ↔ service**: the skill references the service with a bundle-relative OKF link (`metadata.service_ref` + a link in the body), without duplicating endpoints/credentials.

**Direct-vs-skill rule**: for a one-off/exploratory operation, the agent reads the descriptor and goes **directly** to the service; for a recurring/multi-step operation, it uses the **skill**. Pick **one** primary mode to avoid divergence.

**`expose_as_mcp: true`** only when access is stable, recurring, shared, and benefits from typed tools.

## SOPS secrets

**Where**: inside the KB, versioned encrypted, in `secrets/<service>.sops.yaml`. Consistent with auth-via-git: whoever has the repo + the SOPS key has everything.

**Format**: YAML with encrypted values only and cleartext keys (readable PRs/diffs). Governed by a `.sops.yaml` at the root with:
- **Ordered** `creation_rules` (first match wins; no catch-all at the top).
- `path_regex` on the full path from the root (e.g. `^secrets/[^/]+\.sops\.ya?ml$`).
- Targeted `encrypted_regex` (`*_secret`, `*token*`, `password`).
- `mac_only_encrypted: true` (cleartext metadata can vary without false `MacMismatch`).

**Key backend**: `age` (X25519) by default; cloud KMS or HashiCorp Vault Transit optional. Multi-recipient for teams (dev key + CI/server key).

**Runtime**:
- Go server: decrypts in memory with `github.com/getsops/sops/v3/decrypt`.
- Skills as external processes: `sops exec-env` on encrypted dotenv/yaml, exposes `secret_refs` as env vars.
- Runtime key via `SOPS_AGE_KEY_FILE` or `SOPS_AGE_KEY_CMD` (never in the repo).
- Always invoke `sops` **from the root** (footgun with `path_regex`, issues #465/#480).

**Per-KB key**: the server resolves the age key from `kbs[].sops_age_key_file` (per KB) with a fallback to the global `sops.age_key_file` (YAML) or `CARTOGRAPHER_SOPS_AGE_KEY_FILE` (env); propagated to the `sops` process as `SOPS_AGE_KEY_FILE` (`internal/sops.AgeKeyEnv`, D47), never in the repo.

**Tool `service_get(service_id, resolve_secrets=false)`**: reads the `Service` descriptor; with `resolve_secrets: true` it also decrypts the service's `secrets_source` (the entire flat SOPS file) and includes the values in the result. **Known limitation**: OKF frontmatter only supports `string`/`[]string` (no map/list of objects), so per-ref structured `secret_refs` (`{name, secret}`) **cannot be parsed** — per-ref least-privilege is deferred; `resolve_secrets` always decrypts the entire `secrets_source`. Requires a KB with `sops_age_key_file` configured and the `sops` binary in PATH; without it, returns an explicit error (never a panic). **Requires `rw` scope** on the KB (secret access ≥ write access): enforced in the HTTP guard (`mcpAccessGuard`), not in the per-tool-name classification (`service_get` remains `ReadOnly` for the normal path).

**Rotation**:
- Onboarding → `sops updatekeys` (re-encrypts only the data key).
- Offboarding/compromise → `sops rotate -r` **and then rotate the actual secrets upstream** (the encrypted values remain in git history).
- Defenses: pre-commit anti-leak, `.gitattributes` `diff=sopsdiffer`. Minimum version **SOPS 3.13.x**.

**Recovery / break-glass (mandatory)**: multi-recipient with at least one **break-glass key** kept offline (escrow, optionally Shamir). Without it, losing the keys means **irreversible** loss of the secrets. Verify ≥1 valid recipient after every offboarding; run a **periodic test** of decryption from backup.

> **Scope limit**: SOPS is not a dynamic secret manager (no automatic rotation, no ephemeral credentials). For short-lived secrets, use a dedicated downstream secret manager.
