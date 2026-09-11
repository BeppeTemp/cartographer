---
name: kb-create
description: Operator procedure to declare and provision a new Knowledge Base GitOps-style, so it survives pod restarts; also covers authoring the KB's artifacts (skills, subagents, hooks, MCP descriptors) and the SOPS encryption flow.
version: "3.0"
---
# KB Create — Skill

## Purpose

Guide an **operator** through creating a new Knowledge Base (KB) the persistent, GitOps way: a
Gitea repo as source of truth, declared in the server's config (ConfigMap), rolled out, then
verified and connected. This replaces the old runtime-only creation flow (v1.x) — a KB that only
exists as an ad-hoc runtime mount on an emptyDir is **not durable**.

> **WARNING — data loss risk.** A KB mounted on an `emptyDir` volume that is not declared in the
> Deployment's ConfigMap (`kbs:` entry + `remote:`) dies with the pod: on the next reschedule /
> rollout the directory is recreated empty and every concept written to it is gone. The
> ConfigMap + Gitea repo declaration is what makes the KB persistent and reconstructible — the
> server clones it fresh on every pod start (`docs/deployment.md` §Bootstrap KB da remote git).

> **Local/native-service topology.** On a single machine served by the local service, the
> equivalent one-liner is `cartographer kb create <name> --remote <url>`: it scaffolds the KB and
> pushes it to the empty repository that becomes its `origin` (the remote is mandatory there too,
> D134). This skill remains the procedure for the GitOps/Kubernetes topology below, where the KB
> also has to be declared in the server's ConfigMap.

## Reference Files

Two tasks have a full procedure of their own. Read **only** the file that matches
what you are doing — not all of them upfront.

| Task | File |
|---|---|
| Put a skill, subagent, hook, MCP descriptor or instructions block into the KB | `references/artifacts.md` |
| Store an encrypted value the KB can resolve (age key, `.sops.yaml`, `Service` concept, `secret_refs`) | `references/secrets.md` |

## Steps

### 1. Create the Gitea repo and the per-KB service user
Create an empty repo `<nome>.git` on Gitea. The **KB name** is the `name:` field of its `kbs:`
entry (D53) — set it explicitly; if omitted it falls back to the repo basename (`resolveKBName` in
`cmd/cartographer/bootstrap.go`). The name is used everywhere downstream: token scopes
(`kb:<nome>:rw`/`kb:<nome>:r`), the `?kb=<nome>` query param on multi-KB HTTP, and the
`<nome>.token`/`<nome>.age` per-KB file conventions below. Choose it accordingly (kebab-case).

Then create a dedicated Gitea **service user** for the KB (convention: `kb-<nome>`), add it as a
collaborator with **write** permission on the repo only, and generate an access token for it
(scope `write:repository`). This token is the KB's git credential (one user per KB = per-repo
isolation; Gitea tokens are per-user, not per-repo).

### 2. Generate the KB skeleton and push it
On any machine with the `cartographer` binary and git access to the new repo, one command
scaffolds the KB, attaches the remote and pushes the first commit:

```
cartographer kb create <name> --remote <gitea-repo-url>
```

`<name>` accepts letters, digits, `-` and `_` only. `--remote` must point at the **empty**
repository from step 1 — it becomes the KB's `origin`, and the remote is mandatory (D134):
`--no-remote` is the explicit opt-out for a throwaway local KB that is neither durable nor synced.
The KB lands in the local service's data dir (`--data <dir>` overrides it); add `--restart` to
restart the local service and wait for it to come back healthy.

*Fallback* — a machine that cannot reach the remote, or where you want the skeleton somewhere of
your own choosing:

```
cartographer serve --kb <dir> --init   # creates the layout, then Ctrl-C
git -C <dir> remote add origin <gitea-repo-url>
git -C <dir> push -u origin <branch>
```

Either way the standard layout is: `data/` (conceptual root), `services/`, `skills/`, `agents/`,
`hooks/` — content directories only, no `AGENTS.md`/`.gitignore` (D62; the local `.cartographer/`
index is excluded via `.git/info/exclude`, not a versioned `.gitignore`) — plus a first commit.
Optionally, add a curated `instructions.md` at the KB root (sibling of `data/`, `skills/`,
`agents/`) with free-form orchestration directives (e.g. delegation routing, "large reads →
explorer") — its body is folded into the generated `instructions` artifact after the
auto-generated archives/agent sections (D61, `docs/sync.md` §Instructions). See
`references/artifacts.md` for that file's role among the KB's other artifacts.

### 3. Declare the KB in the server config (ConfigMap)
Add an entry under `kbs:` in the server's YAML config (`CARTOGRAPHER_CONFIG`, see
`config.example.yaml`) with the `remote:` pointing at the Gitea repo from step 1:

```yaml
git:
  token_dir: /etc/kb-git      # per-KB git credentials: <token_dir>/<nome>.token (D53)
sops:
  age_key_dir: /etc/kb-sops   # per-KB age keys: <age_key_dir>/<nome>.age (D53)
kbs:
  - name: <nome>
    remote: https://gitea.example.com/user/<nome>.git
    # optional per-KB overrides (zero value = fall back to the global git/sops config):
    # author_name / author_email, committer_name / committer_email
    # sops_age_key_file: /path/custom.age   # only to override the age_key_dir convention
```

No per-KB paths are needed in the config: with `git.token_dir` and `sops.age_key_dir` set once,
the server picks up `<nome>.token` and `<nome>.age` by convention. SSH remotes
(`ssh_key`/`known_hosts`) remain supported for setups without token auth.

### 4. Create/extend the per-KB secret and add a scoped token
- Add the Gitea token from step 1 to the k8s `Secret` backing `git.token_dir` (key
  `<nome>.token`); if the KB has SOPS secrets, add its age key as `<nome>.age` in the secret
  backing `sops.age_key_dir`. Never inline key material in the ConfigMap itself.
- **The age key is the deployment half only.** Generating that key, writing the root `.sops.yaml`
  creation rules and creating the first encrypted file are operator actions Cartographer never
  performs — `secret_set` refuses a file that does not exist. Do them **before** this step, and do
  not assume an encrypted file is already there: `references/secrets.md` is the full procedure. A
  KB with no service credentials needs none of it.
- Add a **scoped token** for the clients of this KB to `CARTOGRAPHER_TOKENS` (or `auth.tokens` in
  the YAML): format `token|kb:<nome>:rw` (read-write) or `token|kb:<nome>:r` (read-only), entries
  separated by comma/whitespace, scopes on one token separated by `;`. Do not reuse an admin
  (scope-less) token for a KB that should be access-limited.

### 5. Rollout
Apply the updated ConfigMap/Secret and roll out the Deployment so the server picks up the new
`kbs:` entry and clones the repo on startup (`ensureClonedKB`).

### 6. Verify and connect
- Verify the KB is mounted and healthy: call the MCP tool `atlas_overview` with `?kb=<nome>` (HTTP
  multi-KB) or `kb=<nome>` argument, confirm `data/index.md` exists.
- Add the KB to clients with `cartographer connect` (interactive form or flags) and the scoped
  token. Do **not** hand-edit `.cartographer.yaml`: `connect` owns it, and a manual edit is not
  tracked in the lockfile, so nothing prunes what it produced.
- On a server mounting two or more KBs, `connect` requires the choice explicitly (D190):
  `cartographer connect --agents <provider> --kb <name>` (repeatable, or `--kb all`). Pick the
  narrowest set that does the job — every skill, subagent, hook and instructions block of a bound
  KB is delivered to that client.
- To change which KBs an already-connected client receives, use `cartographer client bind` /
  `unbind` / `reset`, then `cartographer sync`. Bindings are enforced during sync (D170).

## Optional: shape the KB
Once the KB is live there are two independent things to shape, both drivable by the agent rather
than the operator.

### Content: Maps and Journals
- Ask the user to describe the **Maps** (thematic, mixed concept types) or **Journals**
  (chronological logs, e.g. incidents/notes) they need: name, description, `kind`
  (`map`/`journal`), `concept_types`, `ontology_mode`: `strict`/`emergent`/`off`, default
  `emergent`.
- Call `map_create` for each one.
- A concept that outgrows a single file becomes an **expanded concept** via `concept_expand`
  (turns `<id>.md` into `<id>/index.md` plus satellite concepts) — there is no separate
  "dossier create" step.

### Artifacts: skills, subagents, hooks, MCP descriptors
A KB also configures the agents that read it. Authoring those artifacts — the six accepted
`artifact_write` paths, the shape of each, the naming rules a client enforces, the `if_match`
protocol, and the manifest → trust → projection → materialization chain that is what actually makes
one appear in a client — is in `references/artifacts.md`.

`skill_list` / `skill_install` are a **different** operation: they copy a skill bundled in the
binary into the KB. That is installation, not authoring.

## Reference

- Tools: `atlas_overview`, `map_create`, `concept_expand`, `concept_write`, `skill_list`,
  `skill_install`; `artifact_list`/`artifact_read`/`artifact_write`/`artifact_delete` for artifacts;
  `service_get`, `secret_resolve`, `secret_set` for encrypted values.
- Layout: `data/` is the conceptual root; concept IDs are relative to it.
- Multi-KB HTTP: endpoint is `/mcp?kb=<name>` when more than one KB is mounted. `<name>` is the
  `name:` field of the KB's `kbs:` entry, which falls back to the repository basename only when it
  is omitted (see step 1) — not "always the basename".
- Config reference: `config.example.yaml`, `docs/deployment.md` §Bootstrap KB da remote git e
  §Configurazione, `docs/transport-auth.md` §Autorizzazione per-KB,
  `docs/decisions/deployment-release.md` D39,
  `docs/decisions/transport-auth.md` D44,
  `docs/decisions/concurrency-git.md` D46,
  `docs/decisions/skills-services-secrets.md` D47.
