# Skills, services and secrets

A KB can distribute agent procedures, describe external services and keep
encrypted service values beside its versioned knowledge.

## Skills

An installed skill lives at `skills/<name>/SKILL.md`. Cartographer validates
the Agent Skills frontmatter (`name` and `description`) and exposes installed
and binary-bundled skills through `skill_list`. `skill_install` copies a
bundled skill into the KB.

**Naming rules** (D191). One validator answers for both channels — a write over
`artifact_write` and a skill that arrived through git are held to the same rules,
because a skill only one of them accepts breaks the other later:

| Rule | Severity |
|---|---|
| `name` is required | error |
| `name` matches `[a-z0-9]` segments joined by single `-` — no uppercase, no `_`, no leading/trailing `-`, no `--` | error |
| `name` is at most 64 characters | error |
| `name` equals the directory name | error |
| `description` is required | error |
| `description` is at most 1024 characters | warning |
| body is at most 500 lines | warning |

The rule set is the **intersection of what the supported clients accept**, not
the most permissive union: a skill Cartographer accepts and a client silently
ignores is a no-op in the agent's catalogue, and that failure is invisible from
here. The name/directory equality is the one with teeth — the manifest registers
the artifact under the frontmatter name but hashes and reads the directory, so a
mismatch sends the read to a path that does not exist.

An error excludes that skill from the manifest and nothing else: the KB's other
artifacts still sync, and the reason is reported with the KB, the skill and the
rule named. Warnings never exclude anything.

The historical `<namespace>--<skill-name>` directory convention is **retired**
(D191): it was unreachable from production code and incompatible with the `--`
rule above.

Skills may include executable `scripts/` files and binary `assets/`; provisioning
preserves each source file's executable bit and transports auxiliary files as raw bytes.

Client provisioning materializes KB skills into each provider's native
directory. See [synchronization](sync.md) for the manifest, trust and pruning
rules.

Authoring a KB's artifacts — the six accepted `artifact_write` paths, the minimum
shape of each kind, the `if_match` protocol, and the manifest → trust →
projection → materialization chain — is written out in the bundled `kb-create`
skill's `references/artifacts.md` (D196).

A materialized `SKILL.md` carries a provenance block naming the KB it came
from, its path there, and its content hash ([D138](decisions/sync-provisioning.md#d138)).
Editing that copy is **not** a supported channel: the next sync replaces it. The supported
channels are `artifact_write` on the owning KB (or a git push to the KB repo) — the block
states which, with the exact path.

Skills and hooks can execute with the agent's privileges. Two distinct things
decide whether one is materialized, and they are not the same:

- **Signature.** Artifacts from a KB with a configured signing key carry an
  Ed25519 signature, verified client-side against the pinned public key
  (`VerifiedManifest`, local key pins) — see [sync](sync.md) for the trust chain.
  Cartographer does **not** verify signed git commits or Sigstore attestations;
  the signature covers the artifact's identity and content hash.
- **Trust policy**, which the `signed` manifest field feeds into: bundled
  artifacts are trusted by construction, and KB artifacts require the user's
  persisted trust choice or an explicit one-shot override.

Keep executable content reviewable, pin external dependencies inside the skill
where appropriate and never store plaintext credentials in skill files.

## Service concepts

Service descriptors are regular concepts under `services/` with
`type: Service`. **`Service` is a reserved type**, and `service_list`/`service_get`
match it **case-insensitively** (D158): a KB whose type vocabulary is lowercase —
a likely outcome of an import, or of a non-English domain vocabulary — used to
get zero services and unusable secret resolution with no error anywhere. A
near-miss such as `services` is still not a service. Lint reports
`secrets_on_non_service` when a concept declares `secrets_source` or
`secret_refs` under any other type, so the mistake surfaces from the KB instead
of from a source read.

Frontmatter supports Cartographer's scalar/list subset, so a
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
descriptor; with `resolve_secrets: true` it resolves declared `secret_refs`.
Any concept may own secret references and `secret_resolve` exposes them for
task- and dossier-scoped credentials. **It redacts by default** (D158): the
output is the sorted key names with `<redacted>` values, which is what verifying
that resolution works actually needs. `reveal: true` returns the values, and is
recorded in the audit trail — printing a credential is a decision, and the
transcript (and any log that captures it) keeps it. `names` filters the keys and
composes with redaction, so a caller can confirm *which* of several keys resolve
without printing any of them.

## SOPS files

Encrypted values can be committed under `secrets/*.sops.yaml`. Cartographer
invokes the `sops` CLI from the KB root and flattens decrypted YAML scalar
leaves using RFC 6901 JSON Pointers, for example
`/dante_client/DEV/client_secret` and `/admin/0/client_secret`. `~` and `/`
in mapping keys are escaped as `~0` and `~1`; null is an empty string.

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

Use `secret_refs` for least privilege: each list item is
`NAME=secrets/file.sops.yaml#/json-pointer`. Resolution returns only the
declared `NAME` values. Existing descriptors without `secret_refs` retain the
legacy `secrets_source` whole-file behavior.

### The operator/Cartographer boundary

The encryption flow is split, and the split is where onboarding actually fails
(D196). Cartographer does **not** bootstrap encryption: it operates what an
operator has already set up.

| Step | Performed by |
|---|---|
| Generate the age key | operator (`age-keygen`) |
| Write the root `.sops.yaml` creation rules | operator — Cartographer never writes this file |
| Create the **first** encrypted file and choose its recipients | operator (`sops` CLI) |
| Rotate or add a pointer in an **existing** encrypted file | Cartographer (`secret_set`) |
| Declare which values a concept uses | Cartographer (`concept_write`, `secret_refs`) |
| Resolve declared values | Cartographer (`secret_resolve`, `service_get`) |

`secret_set(path, key, value)` rotates or adds a pointer in an existing
encrypted `secrets/*.sops.yaml` file. It uses `sops set --value-stdin`, checks
that the result remains encrypted, and commits through the normal KB write
flow. It **refuses** any path that is not a local `secrets/*.sops.yaml` and any
file that does not yet exist — bootstrapping one is an operator action, not a
tool call.

The operator-side half is written out step by step in the bundled `kb-create`
skill's `references/secrets.md`, which is where an agent is sent when it has to
perform it.

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
