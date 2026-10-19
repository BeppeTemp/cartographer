---
topic: concurrency-git
---

# D903 — Git has one way to write, on any provider, and old settings files are realigned

**Decision.** The server git profile (D117) is removed with its forge, its keys and the `pr_status` and
`pr_finalize` tools: Cartographer commits every write to the KB's branch and pushes it itself, never opens a
pull request, and names no provider. `git.autocommit` and `git.sync` are removed (every write is a commit; a KB
syncs exactly when it has a remote), `git.in_window` and `git.out_debounce` become constants (30s and 3s), and
`kbs[].path` is removed (`kbs[]` entries name a remote; a folder under `data` is discovered; `--kb` and
`CARTOGRAPHER_KB` mount one folder from the command line, with no remote and so no sync). `kbs[].git_branch`
becomes `branch`, a key that exists only in the KB's `settings.yaml` and is read only from the file committed on
the remote's default branch. A `branch` that is not a valid branch name is not read as "no setting", which would
send writes to the default branch: the sync and the push are refused with an error naming the setting until it is
fixed on the default branch. A push the remote refuses because the branch is protected is reported as that and
is not retried.

Upgrading realigns the installation. One mechanism (`settingschema.Schema.Realign`) rewrites a settings file
into its commented form: the keys of a per-file registry of removed keys are dropped, the values of a registry
of "off" values are reset to the default, every other value, comment and key order is kept, the commented block
of unset settings is rebuilt, and a report says what changed. It is idempotent. The server YAML is realigned at
the first start, keeping the original as `<file>.bak-<version>`; the client `.cartographer.yaml` at the first
`sync`; a KB's `settings.yaml` as one commit by the server, through the normal commit and push, so the commit is
the backup. A removed key met again (a file edited back by hand, an old environment variable, an old flag) is
ignored with a warning that names its replacement, never an error. The one exception is a value that cannot be
honoured: the server profile itself. A configuration that asks for it (`git.profile: server`,
`kbs[].git_profile: server`, `CARTOGRAPHER_GIT_PROFILE=server`) stops startup, and a KB whose `settings.yaml`
carries it does not mount, with the message to unprotect the branch or set `branch`; the file is not realigned
first, so the evidence survives until someone deals with it.

On Cloud Run, which freezes an idle instance and so strands a debounced push, every push is made before the write
returns. The server decides it from `K_SERVICE`, which the platform sets, with no key to remember.

**Why.** The settings had grown with the code, about 140 keys across four files, often repeated on two or three
levels, many of them switches that turn a feature off, so long-lived installations were configured almost at
random. Three principles decide what stays: the lint cannot be escaped (P1), a key has one home (P2), and
Cartographer is not tied to one git provider (P3). The profile broke the third and fought the agent's write loop
(a pull request per write); it was GitHub only. Cartographer's own gates already review every write: the lint
gate, `commit_gate`, the token roles, the audit trail and signed artifacts, with every write a revertible commit.
`branch` is a team decision, the same for every server that mounts the KB, hence its only home is the KB, read
from the default branch so a stray commit on a feature branch cannot redirect writes. The realignment is a
mechanism and not a one-off script because the plans that follow remove and reset keys,
and each only adds entries to the registries (#904, #905 and the rest of #890).

**Alternatives rejected.**
- Keeping the profile and abstracting the forge (GitLab, Gitea): it keeps a pull request per write and a second
  review boundary beside the gates, for a use no one asked for; the lost thing, mandatory human review before
  merge, is accepted.
- Ignoring `git.profile: server` with a warning like any removed key: it writes straight to a branch the operator
  protected. The push would then be rejected, and the rejection is reported as such, but a configuration that asks
  for a review boundary must not start without it (#575 made the same point for an incomplete profile).
- Regenerating the file from the schema and the effective values (`Document` and `Set`) in place of editing the
  document: it loses every comment a person wrote and the order of the keys, for no gain; the old file is kept but
  nobody should have to diff it. The realignment edits the YAML document, so the survivors stay as written.
- Dropping values equal to their default as well: it makes the file canonical, but `Save` of the client file
  writes some of them back, so every sync would rewrite the file and a backup would pile up.
- A marker or version number in the file to run the realignment once: the idempotence needs none, and a marker is
  one more thing to edit by hand.
- A `sync_out_inline` or `cloud_run: true` key for the Cloud Run rule: the next operator forgets it, and the
  deployment that needed it is exactly the one whose environment already says so.
- Honouring `kbs[].path`: a KB without a remote is a folder under `data`, which is discovered, and the entry
  then names nothing the file system does not already say; `--kb` covers development.

**Consequences.** Breaking: `pr_status` and `pr_finalize` are gone, the keys above are gone, the `git_workflow`,
`git_profile` and `git_sync` fields and the pull-request fields of `kb_status` and `sync_status` are gone, and
`branch_source` reads `settings` where it read `config`. The first start of a new version rewrites the server YAML
(backup kept) and commits a realigned `settings.yaml` to each KB that holds a removed key or lacks the comments;
a file that cannot be written (a read-only mount) keeps working. `kb create --no-remote --artifact-write` writes
`allow_artifact_write: true` into the new KB's `settings.yaml` (an entry in `kbs[]` needs a remote), and with a
remote or in `kb clone` the entry names the remote. A branch-protected remote makes every write `failed` in
`sync_status` until the branch is unprotected or `branch` is set; the write itself stays committed locally.
A future change to this area must keep the realignment idempotent, never drop a value it was not told to, and
leave a refused configuration untouched. A plan that removes a key adds it to the registry of its file
(`config.Retired`, `kb.SettingsRetired`, `clientconfig.Retired`) in the same change, and an "off" value to
the `Off` list of the same registry.
