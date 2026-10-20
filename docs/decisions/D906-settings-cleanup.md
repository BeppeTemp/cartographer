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
- **A KB that does not exist is an error.** `init` (and `serve --init`) is removed. `serve` opens the KBs it is
  told to mount and never creates one: a path that is not a KB fails the start with *create it with `cartographer
  kb create`*. A `kbs[]` entry whose remote is still empty is the same case: `kb create <name> --remote <url>` is
  what fills it.
- **The tool set is fixed.** `tools.profile` (`CARTOGRAPHER_TOOLS_PROFILE`, `--tools-profile`) is removed.
  `tools/list` always shows the core set (what the `agent` profile showed, `advancedToolNames`), and an advanced
  tool absent from the list is served when a call names it (the SDK bridge only filters the list; a test and the
  e2e scenario 20 pin that). The `full` profile is gone: whoever wants the plumbing tools calls them by name.
- **The origin rule is fixed.** `mcp.allowed_origins` (`CARTOGRAPHER_MCP_ALLOWED_ORIGINS`) is removed. A request to
  `/mcp` with an `Origin` header is accepted only when the origin's host and port are the request's `Host`
  (scheme ignored); one with no `Origin` header is accepted. The Atlas behind an SSO proxy keeps working (D308),
  because the proxy serves the page and the API from one origin. `*` and a list of foreign origins no longer exist.
- **The old names of renamed KBs are state, not a setting.** `kb_aliases` leaves the server YAML for
  `<data>/.cartographer/state.json` (`config.ServerState`), beside `update-check.json`, written atomically by
  `kb rename` and `kb unmount` and read by the server at its start. A server without a `data` directory has no
  state file and no aliases. The realignment moves the list a file still carries into the state file (an item the
  state already has for the same old name wins) before the key is dropped, and a file whose list cannot be moved is
  not realigned; until it is, the YAML list still resolves. A command that edits the state file settles the YAML
  list first and drops its key, so the old list cannot come back behind the edit.
- **Off-loopback needs a token.** Right after the auth mode is resolved, a server whose listen address is not
  loopback (a host in `127.0.0.0/8`, `::1` or `localhost`; `:port` and `0.0.0.0` listen on every interface) with
  authentication off (`auth.mode: off`, or `auto` with no token) does not start, and the message says why. The
  local service pins auth off on `127.0.0.1` (D268) and is unaffected; a stdio server listens on nothing.

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

`init` switched on a side effect that made a typo in a path create an empty KB, and a `kbs[]` entry on an empty
remote become a KB without anyone choosing it; the command that creates a KB already exists and chooses its remote.
`tools.profile` was a switch between a working set and everything, and the second was never needed: a tool the list
omits is callable, so the profile only decided what a model is shown. `mcp.allowed_origins: ["*"]` turned the
browser protection off, and a list of other origins needed an operator to know which browser pages should script a
server; the only legitimate cross-origin case is a proxy, which is same-origin. The aliases are written by a command
and read by the server, never meant to be edited, which is what a state file is. A server that listens on every
interface with no token serves every KB, writes included, to anyone on the network: the configuration that reaches
that state by omission (`auto` and no token, the default) is the one that must not start.

**Alternatives rejected.**
- Keeping `allow_artifact_write` with a default of true: a switch that nobody needs to turn off is a key the
  configuration does not need (P4), and the off position would keep the silent refusal.
- Keeping `--artifact-write` as an ignored flag: the command is run by hand or by the `kb-create` skill, which
  ships with the binary, so no stored invocation outlives the release.
- Keeping `usage_stale_days: 0` as the way to silence `artifact_unused`: the finding is `info`, it blocks
  nothing, and a KB that wants no retirement candidates is a KB that wants no usage signal (P1).
- Keeping `tools.profile: full` for the clients that read only `tools/list` (D123): the core set already carries
  what those clients need, and the rest is plumbing for the CLI and hooks that call it by name.
- Keeping `kb_aliases` in the YAML and only changing who writes it: a file that a command rewrites on every
  rename is a file an operator's dotfiles tool fights over; the state file is the server's own.
- Refusing to start when the YAML still holds `init`, `tools.profile` or `mcp.allowed_origins`: a removed key is
  never an error (below). Only the absence of a KB, and an unauthenticated off-loopback listener, stop a start.
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

A server started with `--init` or `--tools-profile <value>` in a service definition still starts: both flags are
dropped with a warning, the value with its flag. A server that relied on `init` to create its KB, or on `auto`
auth with no token on a non-loopback address, stops at the start with a message that names the fix (release note).
Breaking: `init`, `tools.profile` and `mcp.allowed_origins` are removed, `kb_aliases` moves, and an off-loopback
server with no token refuses to start.

Supersedes the server allow-list of D115 and the tool profile of D65, revises the Origin rule of D128 and the
alias storage of D772, amends D746 and D352.

A future change here must preserve all of these:
- a removed key is never an error that stops a start or a mount: it is ignored, named in a warning with what to
  do instead, and dropped by the realignment;
- the token's scope stays the gate of every write, artifacts included;
- a server never creates a KB, never listens beyond this machine with authentication off, and never accepts a browser
  request from another origin;
- an advanced tool stays callable by name whatever `tools/list` shows;
- the server never leaves a valid descriptor out of what it distributes, and a KB's policy is read from its own
  `settings.yaml` alone.

**Host rule on loopback (hardening of the same-origin rule).** A server that listens on a loopback address with
authentication off requires, on every route, a `Host` naming this machine (`localhost`, `127.0.0.0/8`, `[::1]`,
any port), and answers 403 otherwise (`mcpserver.LoopbackHostGuard`, installed outermost by `serve`). It is not
installed with authentication on or off-loopback. An `Origin` with userinfo is refused. The documented SSO-proxy
setup keeps `/mcp` on bearer auth, so it is unaffected.
