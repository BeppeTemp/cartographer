---
name: kb-create
description: Operator procedure to declare and provision a new Knowledge Base GitOps-style, so it survives pod restarts.
version: "2.3"
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

### 2. Generate the KB skeleton locally (one-shot)
On any machine with the `cartographer` binary and git access to the new repo:

```
cartographer serve --kb <dir> --init   # creates the layout, then Ctrl-C
```

This creates the standard layout: `data/` (conceptual root), `services/`, `skills/`, `agents/`,
`hooks/` — content directories only, no `AGENTS.md`/`.gitignore` (D62; the local `.cartographer/`
index is excluded via `.git/info/exclude`, not a versioned `.gitignore`) — and initializes `<dir>`
as a git repo with a first commit
("init: KB inizializzata"). Optionally, add a curated `instructions.md` at the KB root (sibling of
`data/`, `skills/`, `agents/`) with free-form orchestration directives (e.g. delegation routing,
"large reads → explorer") — its body is folded into the generated `instructions` artifact after the
auto-generated archives/agent sections (D61, `docs/sync.md` §Instructions). Then push it to the
Gitea repo created in step 1:

```
git -C <dir> remote add origin <gitea-repo-url>
git -C <dir> push -u origin <branch>
```

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
- Add the KB to clients: `cartographer connect` (interactive form or flags) with the scoped token,
  or edit `.cartographer.yaml` directly (`server_url`, token env var pointing at the token from
  step 4).

## Optional: Maps and Journals
Once the KB is live, use the standard MCP tools to shape its content — this part is unchanged from
v1.x and can be driven by the agent, not just the operator:
- Ask the user to describe the **Maps** (thematic, mixed concept types) or **Journals**
  (chronological logs, e.g. incidents/notes) they need: name, description, `kind`
  (`map`/`journal`), `concept_types`, `ontology_mode`: `strict`/`emergent`/`off`, default
  `emergent`.
- Call `map_create` for each one.
- A concept that outgrows a single file becomes an **expanded concept** via `concept_expand`
  (turns `<id>.md` into `<id>/index.md` plus satellite concepts) — there is no separate
  "dossier create" step.
- Call `skill_list` / `skill_install` to offer relevant bundled skills.

## Reference

- Tools: `atlas_overview`, `map_create`, `concept_expand`, `concept_write`, `skill_list`, `skill_install`.
- Layout: `data/` is the conceptual root; concept IDs are relative to it.
- Multi-KB HTTP: endpoint is `/mcp?kb=<name>` when more than one KB is mounted; `<name>` is always
  the KB's basename (see step 1).
- Config reference: `config.example.yaml`, `docs/deployment.md` §Bootstrap KB da remote git e
  §Configurazione, `docs/transport-auth.md` §Autorizzazione per-KB, `decisions.md` D39/D44/D46/D47.
