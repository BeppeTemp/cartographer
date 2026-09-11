# Encrypted values: the SOPS flow, end to end

## 0. Do you need this at all?

A KB that holds no service credentials needs **none** of this. Skip the whole
reference and come back when a concept needs to reference a real secret.

You need it when the KB describes a service whose credentials an agent should be
able to resolve — a client secret, an API token, a database password — and you
want that value versioned with the KB rather than pasted into a chat.

## 1. The boundary: what you do, what Cartographer does

This is the part that traps people, so it comes before the procedure.

| Step | Who |
|---|---|
| Generate the age key | **you**, with `age-keygen` |
| Write the root `.sops.yaml` creation rules | **you** — Cartographer never writes this file |
| Create the **first** encrypted file and choose its recipients | **you**, with the `sops` CLI |
| Rotate or add a pointer in an **existing** encrypted file | Cartographer (`secret_set`) |
| Declare which values a concept uses | Cartographer (`concept_write`, `secret_refs`) |
| Resolve declared values | Cartographer (`secret_resolve`, `service_get`) |

`secret_set` **refuses** any path that is not a local `secrets/*.sops.yaml`, and
refuses a file that does not exist: *"bootstrapping an encrypted file and its
recipients is an operator action"*. Do not try to create the first file with it.

## 2. Operator-only steps

### 2.1 Generate an age key

```bash
age-keygen -o ~/.config/cartographer/kb-<kb-name>.age
```

Keep it **outside the KB**. It is passed to the `sops` child process as
`SOPS_AGE_KEY_FILE` and must never be committed. The server selects it in this
order, first match wins:

1. `kbs[].sops_age_key_file` in the server config;
2. global `sops.age_key_file`;
3. the `CARTOGRAPHER_SOPS_AGE_KEY_FILE` environment variable.

On a GitOps deployment with `sops.age_key_dir` set, the convention is
`<age_key_dir>/<kb-name>.age` and no per-KB config key is needed.

### 2.2 Write the root `.sops.yaml`

At the **root of the KB repository**, not inside `secrets/`:

```yaml
creation_rules:
  - path_regex: secrets/.*\.sops\.yaml$
    age: age1qqqq...   # the public key printed by age-keygen
```

Test the regex before relying on it — a rule that does not match means `sops`
writes the file in **plaintext** and you will not be told:

```bash
sops --verbose encrypt --in-place secrets/probe.sops.yaml
```

### 2.3 Create the first encrypted file

```bash
mkdir -p secrets
sops secrets/keycloak.sops.yaml    # opens $EDITOR on a new encrypted file
```

Write plain YAML in the editor; `sops` encrypts the values on save. Confirm the
result really is encrypted before committing:

```bash
grep -q 'ENC\[' secrets/keycloak.sops.yaml && echo encrypted
```

Commit and push it like any other KB file.

## 3. Cartographer-side steps

### 3.1 The Service concept

Service descriptors are ordinary concepts under `services/` with `type: Service`.
**`Service` is a reserved type**, matched case-insensitively (D158), so
`type: service` works too. Keep the frontmatter **flat** — Cartographer's
scalar/list subset is what `service_get` reads:

```yaml
---
type: Service
title: Keycloak
kind: idp
base_url: https://keycloak.example.internal
secret_refs:
  - CLIENT_SECRET=secrets/keycloak.sops.yaml#/dante_client/DEV/client_secret
  - ADMIN_PASSWORD=secrets/keycloak.sops.yaml#/admin/0/password
---
```

Each `secret_refs` entry is exactly `NAME=secrets/file.sops.yaml#/json-pointer`.
Resolution returns only the declared `NAME` values — that is the least-privilege
form, and it is the one to use. The older `secrets_source: secrets/file.sops.yaml`
declares the *whole file* and is kept only for descriptors that already use it.

### 3.2 Resolve

```
service_get(service_id: "services/keycloak", resolve_secrets: true)
secret_resolve(concept_id: "services/keycloak")
secret_resolve(concept_id: "services/keycloak", names: ["CLIENT_SECRET"], reveal: true)
```

`secret_resolve` works on **any** concept, not only a Service: a task or dossier
page can own its own `secret_refs`.

`secret_resolve` **redacts by default**: you get the sorted key names with
`<redacted>` values, which is what verifying that resolution works actually
needs. `reveal: true` returns the values and is **recorded in the audit trail** —
printing a credential is a decision, and the transcript keeps it. `names` filters
the keys and composes with redaction, so you can confirm *which* of several keys
resolve without printing any of them.

A decrypted value must never be written back into a concept body.

### 3.3 Rotate

```
secret_set(path: "secrets/keycloak.sops.yaml", key: "/dante_client/DEV/client_secret", value: "<new>")
```

It runs `sops set --value-stdin`, verifies the result is still encrypted, and
commits through the normal KB write flow. Requires `rw` scope over HTTP and a
`sops_age_key_file` configured for the KB.

## 4. JSON Pointer rules

Cartographer flattens the decrypted YAML's scalar leaves into RFC 6901 JSON
Pointers. Given:

```yaml
dante_client:
  DEV:
    client_secret: s3cr3t
admin:
  - client_secret: other
```

the pointers are `/dante_client/DEV/client_secret` and `/admin/0/client_secret` —
a list index is a path segment. In a mapping key, `~` is escaped as `~0` and `/`
as `~1`. A null leaf is the empty string.

## 5. Failure modes, by the symptom you will actually see

| Symptom | Cause and fix |
|---|---|
| `sops` not found | The `sops` CLI is not in the server's `PATH`. Resolution shells out to it; install it on the machine or image running the server. |
| resolution fails to decrypt | The configured age key is not a recipient of that file. Re-encrypt with `sops updatekeys`, or point at the right key (§2.1 order). |
| resolution refused over HTTP | `service_get(resolve_secrets)` and `secret_set` need **`rw`** scope. A `kb:<name>:r` token cannot resolve. |
| `lint` reports `secrets_on_non_service` | `secret_refs` or `secrets_source` was declared on a concept whose `type` is not `Service`. This is the most common mistake; fix the type or move the refs. |
| a `NAME` resolves to nothing | The JSON Pointer does not exist in the decrypted file. Check it with `sops decrypt secrets/<file>.sops.yaml` and re-read §4. |
| `secret_set: file does not exist` | You are trying to create the first encrypted file with it. Go back to §2.3. |
| the committed file is readable plaintext | The `.sops.yaml` `path_regex` did not match. Fix the rule (§2.2), re-encrypt, and treat the plaintext value as **compromised** — git history is forever: rotate it upstream. |

## 6. Hygiene

- **Multiple recipients** whenever losing one key must not make recovery
  impossible.
- **On offboarding**, update recipients *and* rotate the real upstream
  credentials: encrypted historical values remain in git forever.
- Keep at least one **tested** offline recovery key. Untested is unrecovered.
- SOPS is **encrypted storage, not a dynamic secret manager**: it issues no
  short-lived credentials and rotates no service on its own.

Full reference: `docs/skills-services-secrets.md`.
