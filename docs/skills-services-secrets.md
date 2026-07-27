# Skills, services and secrets

A KB can distribute agent procedures, describe external services and keep
encrypted service values beside its versioned knowledge.

## Skills

An installed skill lives at `skills/<name>/SKILL.md`. Cartographer validates
the Agent Skills frontmatter (`name` and `description`) and exposes installed
and binary-bundled skills through `skill_list`. `skill_install` copies a
bundled skill into the KB.

Client provisioning materializes KB skills into each provider's native
directory. See [synchronization](sync.md) for the manifest, trust and pruning
rules.

Skills and hooks can execute with the agent's privileges. The current `signed`
manifest field is a trust-policy result, not a cryptographic signature:

- bundled artifacts are trusted;
- KB artifacts require the user's persisted trust choice or an explicit
  one-shot override;
- Cartographer does not verify signed commits or Sigstore attestations.

Keep executable content reviewable, pin external dependencies inside the skill
where appropriate and never store plaintext credentials in skill files.

## Service concepts

Service descriptors are regular concepts under `services/` with
`type: Service`. Frontmatter supports Cartographer's scalar/list subset, so a
descriptor intended for `service_get` should stay flat:

```yaml
---
type: Service
title: Keycloak
kind: idp
base_url: https://keycloak.example.internal
secrets_source: secrets/keycloak.sops.yaml
---
```

The body can document endpoints, authentication flow and links to the skills
that operate the service. Cartographer does not validate a richer Service
schema beyond the normal concept and strict-map type rules.

`service_list` inventories Service concepts. `service_get` returns one
descriptor; with `resolve_secrets: true` it also decrypts the complete flat
file named by `secrets_source`.

## SOPS files

Encrypted values can be committed under `secrets/*.sops.yaml`. Cartographer
invokes the `sops` CLI and parses the decrypted document as flat key/value
pairs.

The age key is selected in this order:

1. `kbs[].sops_age_key_file`;
2. global `sops.age_key_file`;
3. `CARTOGRAPHER_SOPS_AGE_KEY_FILE`.

It is passed to the child process as `SOPS_AGE_KEY_FILE` and must never be
stored in the KB.

`service_get(resolve_secrets=true)` requires:

- `sops` in `PATH`;
- a configured key that can decrypt the file;
- `rw` scope for HTTP access.

The whole `secrets_source` is returned. Structured per-reference
`secret_refs` are not representable by the current frontmatter parser, so
there is no per-key least-privilege filter.

## Operational guidance

- Define `.sops.yaml` rules from the repository root and test their
  `path_regex`.
- Use multiple recipients when loss of one key must not make recovery
  impossible.
- On offboarding, update recipients and rotate the real upstream credentials;
  encrypted historical values remain in git.
- Keep at least one tested offline recovery key.

SOPS is encrypted storage, not a dynamic secret manager: it does not issue
short-lived credentials or rotate services automatically.
