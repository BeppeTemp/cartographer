---
topic: client-configurator
---

# D906 — Settings cleanup: one home per key, and no setting that turns a safeguard or a feature off

**Decision.** The settings shrink to the keys teams legitimately need to set differently, each in one file: server
infrastructure in the server YAML, a KB's behaviour in its `settings.yaml`, a machine's choices in
`.cartographer.yaml`. Four principles decide what stays: the lint cannot be escaped (P1), one home per key (P2),
nothing tied to one git provider (P3), and a new KB works with no keys at all (P4). Every removed key goes into the
realignment registry of D903: it is ignored with a warning that names what replaces it, and dropped when the file
is realigned. What each part removes, and what replaces it:

- **Artifact writes are always allowed.** `allow_artifact_write` and `allow_template_write` are removed, from a
  KB's `settings.yaml` and from `kbs[]`. `artifact_write`, `artifact_delete` and `artifact_move` are registered on
  every KB, and a token's `rw` scope on the KB is the only gate, as for every other write. The repairs that
  rewrite artifact files apply like the others, from `kb_repair` and from the background repair. The
  `--artifact-write` flag of `kb create` and `kb clone`, which existed only to write the setting (D711), is
  removed, and `kb_status` no longer reports `artifact_write` and `template_write` capabilities.
- **MCP descriptors are always distributable.** `mcp_allowlist` is removed, from a KB's `settings.yaml` and from
  `kbs[]`. Every valid `mcp/<name>.json` goes into the manifest, `artifact_list` and `artifact_read`; the
  startup scan still reports a malformed one, and the "denied" and "stale entry" diagnostics are gone with
  the list. The client's hash-bound approval (D115) now covers every KB descriptor, a signed one too: the
  allow-list was what let a verified descriptor skip it, and with artifact writes always allowed any `rw`
  token can write a descriptor the server then signs. A signature proves which server a descriptor came
  from, not that anyone chose to run it.
- **Secrets are derived.** `secrets` is removed, from the KB file and from `kbs[]`. A KB declares secrets when
  at least one YAML file directly under `secrets/` carries sops metadata (`sops.Declared`: read, never
  decrypted, so neither the sops binary nor a key is needed). The "missing key" report of `kb_status` and the
  startup warning key on that.
- **A KB's policy has one home.** The `kbs[]` form of every KB-scope key (`write_gate`, `auto_repair`,
  `repair_on_write`, `doctor_mode`, `doctor_interval`, `doctor_auto_interval`, `doctor_budget`,
  `usage_stale_days`) is removed; the server's `kbs[]` keeps `name` and `remote`. The KB's `settings.yaml`
  is read alone (`config.ResolveKBSettings`), so a server value can no longer lose to it and the
  `ignored_server_settings` and `ignored_server_value` reports are gone. The schema's `kb` scope became its
  own table (`config.KBKeys`), the source of the commented `settings.yaml` that `kb create` writes.
- **`usage_stale_days` has a floor and no off.** Default 42, minimum 7 (`kb.MinUsageStaleDays`). A value
  between 1 and 6 is raised to 7 with a finding (`kb_status` `settings_findings`, startup warning); `0` or a
  negative number is read as 42, and the realignment resets a `0` by dropping the key. `artifact_unused`
  no longer has a disabled state, and a KB opened outside `serve` uses the default.

**Why.** The settings grew with the features, often on two or three levels, and many of them switched something
off. A long-lived installation ends up configured almost at random, and the Atlas cannot show a configuration
clearly when most of it is switches nobody chose (#890).

The artifact-write right guarded the fact that a written skill reaches the clients that sync the KB. It did not
guard it: anyone who can push to the KB's remote writes the same files through git, and an MCP token with `rw`
scope is granted per KB by the operator already (`auth.tokens`). What the switch did add was a KB that worked for
concepts and silently refused artifacts, which is what a discovered KB, with no `kbs[]` entry to carry it, always
was. The template right (D352) existed only to soften that default.

The MCP allow-list guarded a different consent than the one that matters. The server's per-endpoint list
asked the operator of a server to vouch for what a KB's authors write, while the party that executes or
connects to a descriptor is each client, which already binds its own approval to the exact file hash. A KB
whose descriptor the list refused was silently shorter, which is the same trap as the artifact-write switch.
`secrets` repeated a fact the KB's files already state. The `kbs[]` twins let a server and a KB disagree,
and the KB won anyway (D746): two places for one value, one of them always inert.

**Alternatives rejected.**
- Keeping `allow_artifact_write` with a default of true: a switch that nobody needs to turn off is a key the
  configuration does not need (P4), and the off position would keep the silent refusal.
- Keeping `--artifact-write` as an ignored flag: the command is run by hand or by the `kb-create` skill, which
  ships with the binary, so no stored invocation outlives the release.
- Keeping `usage_stale_days: 0` as the way to silence `artifact_unused`: the finding is `info`, it blocks
  nothing, and a KB that wants no retirement candidates is a KB that wants no usage signal (P1).
- Moving a `kbs[]` value into the KB's file during the realignment: the server rewrites its own file only,
  and a commit to a KB's repository on behalf of a setting of another machine is not its to make.

**Consequences.** Breaking: the removed keys and the `--artifact-write` flag. A token with `rw` scope on a KB can
now write what that KB provisions to its clients (skills, agents, hooks, MCP descriptors, instructions): an
operator who wants a token that only reads gives it `kb:<name>:r`. Every descriptor a KB ships reaches the
clients that sync it, each of which still approves it before installing (security note for the release
notes). A `kbs[]` setting that was the only place a KB's policy lived is dropped by the realignment: copy it
into the KB's `settings.yaml` first. A server that set `allow_artifact_write: false` (or
`allow_template_write: false`) gets a warning, not a refusal: its KBs become writable by `rw` tokens on
upgrade (release note).

Supersedes the server allow-list of D115, amends D746 and D352.

A future change here must preserve all of these:
- a removed key is never an error that stops a start or a mount: it is ignored, named in a warning with what to
  do instead, and dropped by the realignment;
- the token's scope stays the gate of every write, artifacts included;
- the server never leaves a valid descriptor out of what it distributes, and a KB's policy is read from its own
  `settings.yaml` alone.
