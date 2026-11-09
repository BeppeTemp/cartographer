# Plugins

A **plugin** is a folder or a git repository with a manifest, `cartographer-plugin.yaml`, at its root. It extends
the client of one machine; a server never loads it. A plugin adds agent clients (a fork or an in-house build of one
Cartographer does not know), skills, subagents and hooks, and, with a script, commands, MCP tools and sync handlers.
It is installed with one command, pinned to a commit, and enabled only after the user approves what it does, version
by version. This page describes the manifest, the version ranges it speaks, the contribution points, the tree
reader, the store, the plugin commands, the script model and a worked example (`internal/plugin`).

A plugin is trusted to the extent it was approved: the approval preview lists every permission, every file that runs
and every script in full, and a script can reach nothing its approved manifest does not grant. Nothing about it
runs, and nothing is delivered, before that approval.

## Manifest

Schema 1, read strictly: an unknown key, a YAML alias, a second YAML document or a file over the artifact file cap
is an error, and every problem it can read is reported as `path: reason`, sorted (a YAML type error, such as a list where a string belongs, is reported alone until it is fixed). The first check is the
`cartographer` range: when the running version does not meet it, that is the only error reported, so a manifest of
a newer schema says what it needs instead of which key is unknown.

| Key | Rule |
|---|---|
| `schema` | Required, `1`. Any other value: `unsupported plugin schema N`. |
| `name` | Required, `^[a-z][a-z0-9-]{1,40}$`, a valid artifact name (so no Windows device name), not `bundle` or `kb`, not a built-in client id, not a built-in command name. |
| `version` | Required, `X.Y.Z`, three non-negative integers without leading zeros. Shown to the user; the git commit pins the content. |
| `display_name` | Required, one line of printable characters, at most 60. |
| `description` | Required, one paragraph of printable characters, at most 300. |
| `cartographer` | Required version range (below). |
| `depends` | Optional map of plugin name to version range, at most 16 entries. Checked as data. |
| `permissions` | Optional, checked as data. |
| `settings` | Optional, checked as data, at most 32 keys. |

### Version ranges

A range is comparators separated by spaces, all of which must hold. A comparator is `>=`, `>`, `<=`, `<` or `=`
followed by `X.Y.Z`; a bare `X.Y.Z` means `=`. Examples: `>=0.28.0`, `>=0.28.0 <0.30.0`.

The running version is compared by its `X.Y.Z` core, so a release candidate or a `git describe` build of a version
meets a range that asks for that version. A running version that does not parse (a `dev` build) never refuses. A
malformed range is a manifest error that names it. The order of versions is the one of `updatecheck.Cmp`.

### Permissions

Declared by the plugin and validated here; nothing in this package grants or enforces them. The grammar is `D1723`.

| Key | Rule |
|---|---|
| `permissions.kb` | `read` or `write`. |
| `permissions.net` | At most 16 host patterns: an exact lowercase host or `*.suffix`, each of at least two labels, no scheme, port or path. An IP address in any spelling and a single-label host such as `localhost` are refused: a name whose last label starts with a digit is an address (dotted decimal, a decimal such as `2130706433`, hex or octal parts such as `0x7f.0x1` or `127.0x1`). A public suffix such as `*.github.io` is accepted: the user approves each permission. |
| `permissions.files.read`, `permissions.files.write` | At most 16 globs each. A glob starts with one of `{home}`, `{xdg_config}`, `{app_data}`, `{local_app_data}`, `{documents}`, continues with `/` separators, holds no other placeholder and no `..`. A base directory other than `{documents}` must be followed by a path; an empty or `.` segment (`{home}/`, `{home}//x`, `{home}/./x`) is refused, and so is a segment with `~` or `:` (Windows short names and streams). Trailing dots and spaces of a segment are ignored when matching Cartographer's own state, as Windows ignores them. A glob that reaches Cartographer's own state is refused: the client config file, the `cartographer` config, state and cache directories (the plugin store is inside the cache one). A literal `cartographer` or `.cartographer*` segment is refused at any depth; a wildcard segment (`*`, `**`, `c*`) only while the segments before it still lead into a known location of that state (`{home}/*`, `{home}/.config/c*`, `{xdg_config}/*`, `{home}/Library/Application Support/*`), so `{xdg_config}/vendor-cli/**` and `{home}/.vendor-cli/*/y` pass. The rule is lexical and best effort (a custom `XDG_CONFIG_HOME` is invisible to it); the enforcer refuses the real state directories whatever the glob. Other sensitive paths, such as `.ssh` or a shell rc file, can be declared: the approval preview shows them and the enforcer limits them. |
| `permissions.env` | At most 16 names matching `^[A-Z][A-Z0-9_]*$`. `CARTOGRAPHER_*`, `LD_*`, `DYLD_*` and `XDG_*`, and `PATH`, `PATHEXT`, `HOME`, `USERPROFILE`, `APPDATA`, `LOCALAPPDATA`, `SYSTEMROOT`, `COMSPEC`, `TMPDIR`, `TEMP` and `TMP`, are refused as pass-through names: the variables a loader or a shell resolves programs and files by. |

### Settings

Each key matches `^[a-z][a-z0-9_]{0,40}$` and maps to `type` (`string`, `bool` or `int`, required),
`description` (required, at most 300 characters) and an optional `default` of the declared type.

## Validators shared with the KB

A skill file and an agent file are checked by `provisioning.ValidateSkillFile` and `provisioning.ValidateAgentFile`,
the same functions the MCP artifact tools call, so a plugin and a KB cannot disagree on what a valid skill is.

## Contribution points

A point is a top-level folder of the plugin that contributes something, registered in code (`plugin.Register`; a
duplicate or invalid name panics at start). A point has a side: `client` points are loaded, `server` is declared
and never loaded. 0.28 registers `skills`, `agents`, `hooks` and `providers`, and a test pins the exact set.

| Point | Layout | Check |
|---|---|---|
| `skills` | `skills/<name>/SKILL.md` plus any files; the whole folder is the artifact, the executable bit is kept | `provisioning.ValidateSkillFile` |
| `agents` | `agents/<name>.md` | `provisioning.ValidateAgentFile` |
| `hooks` | `hooks/<name>/hook.json` plus the files it runs | `ValidateHookJSON` and `ValidateHookRequires`; unlike in a KB, a failure is an error, not a warning. The install preview shows the hook's command, event and `requires`, and lists every file with the executable bit (in a hook, every file but `hook.json`, as the client materializes it, so the artifact hash equals the one sync computes). `cartographer-bootstrap` and `cartographer-write-findings` are refused. |
| `providers` | `providers/<id>.yaml` | `plugin.ParseProvider`; registers an agent client, installs no artifact (see Providers below) |
| `mcp` | planned | refused today: `mcp/` is not a contribution point in this version |
| `instructions` | planned | refused today: `instructions.md` is not a contribution point in this version |
| `plugin.star`, `lib/*.star` | loaded | the script registers commands, tools and event handlers (D1746); a call reaches the network, the environment, files and KB tools only as the manifest grants (below) |

Every artifact name is a valid artifact name, and a plugin cannot shadow the bundle: a skill or agent named like a
bundled one (the CLI skill included) is refused. Reserved and bundled names are compared ignoring case, since a filesystem may fold it.

## The tree reader

`plugin.ReadTree` takes the files of a plugin in memory (`TreeFile`: slash path, content, executable bit and a
flag for a symlink or non-regular file) and returns the validated `Plugin` and every problem found, sorted; the
order of the files does not matter. It is the one function that knows the tree rules, so validating, installing and
loading share them.

- At the top: the manifest and one folder per registered point. `README*`, `LICENSE*`, `CHANGELOG*`, `docs/` and `.git*`
  are ignored and never stored, checked or part of the digest. `plugin.star` and flat `lib/*.star` files are read and loaded (Starlark, 20 million steps and 256 MiB of heap growth at most; `load()` reaches only `lib/` files of the tree, and the capability modules refuse every call at load in this version), and run only on an otherwise valid tree (after the caps and the manifest); a script that does not load is a problem of the tree (a step cap reads as at a call, `plugin.star:4:11: the script ran more than N steps`), and the approval preview shows each script in full with its registrations. `mcp/`
  and `instructions.md` are not contribution points in this version, and any other entry is an error naming it and listing what a tree may hold.
- Paths are canonical slash paths (`provisioning.ValidArtifactFilePath`) without control or format characters (bidi overrides, zero-width), unique ignoring case; a symlink or
  non-regular file, a file over the artifact file cap, more than 2000 files or 8 MiB in all is an error.
- Each contribution becomes an artifact with source `plugin:<name>`, its files in memory and
  `provisioning.ContentHashFiles` as hash, the same function the client applies to pulled files.
- `Digest` is the sha256 of the sorted (path, executable bit, sha256 of content) of every stored file, manifest
  included: it identifies the validated tree.

## Acquisition

`plugin.ParseSource` reads the text a user typed (a command-line argument or their own config, never a KB or a
plugin) and `plugin.Fetch` brings the files into memory as `[]TreeFile` for `ReadTree`. Nothing is stored yet.

| Spelling | Source |
|---|---|
| An existing directory | A folder, whatever it is called. The path is made absolute and symlink-resolved and recorded as such. |
| `https://host/path.git`, `ssh://…`, `git://…`, `file://…`, `git@host:path.git` | A git remote, at its default branch. |
| any remote followed by `@<ref>` | The same at a branch, a tag or a full commit sha. The text splits at the **last** `@`, and only when what follows holds neither `:` nor `/`, so `git@host:team/x.git` stays whole and `git@host:team/x.git@v1.2` has ref `v1.2`. An empty ref is an error. |

Anything else, and any remote `gitx.ValidateRemote` refuses (an option, a `ext::` helper, a scheme outside
`https`, `http`, `ssh`, `git`, `file`), is an error.

**Git.** `gitx.FetchTree` makes a throwaway repository under the directory the caller names, runs
`git fetch --depth 1 -- <remote> <ref>` (no ref: `HEAD`) and reads the tree from the object database with
`git ls-tree -r -z -l` and `git cat-file --batch`; it is never checked out. The bytes approved are therefore the
committed bytes, whatever `core.autocrlf`, `.gitattributes` (`eol`, `filter`, `export-subst`) or the host say. The
fetch uses the machine's own git and ssh configuration and credentials, as `kb clone` does, with no token helper
and no prompt. A symlink, a submodule, a blob over the artifact file cap or more than 2000 files is refused naming
the path before any blob is read; `README*`, `LICENSE*`, `CHANGELOG*`, `docs/` and `.git*` at the top are skipped. The
executable bit is mode `100755`. The resolved commit (40 hex) is returned. Without `git` on PATH a git source
fails with a message saying so; a folder never needs git.

**Folder.** Walked with `Lstat`: a symlink or non-regular file is read as a `Special` entry, so `ReadTree` lists it (`"path": a symlink or non-regular file`) with the other findings, the same entries are skipped,
and the files are read once, a snapshot: a later edit of the folder needs an update to be seen.

## The store and the `plugins` key

`plugin.Store` keeps fetched trees in `<user cache dir>/plugins/<name>/<digest>/`, exactly the files `ReadTree`
validated, manifest included. It is a cache: it can be deleted and fetched again from the recorded source.

- `Put` validates the files, writes them to `plugins/.tmp-<name>-<random>/` through `safewrite`, reads that
  directory back through `ReadTree`, requires the expected digest, and renames it into place, never over an
  existing directory (an existing one that verifies is kept; a corrupt one of the same digest is replaced). An
  interrupted write leaves only a `.tmp-*` directory, swept by the next `Put` or `GC` once it is an hour old.
- `Open(name, digest)` reads the tree back and compares the digest: `ErrNotFetched` (nothing there),
  `ErrModified` (the digest differs, or the directory holds a link or a special file), `ErrInvalid` (same digest,
  but `ReadTree` now refuses it, for example because the running version no longer satisfies `cartographer`).
  Readers take no lock. `Put` and `GC` run under the client lock, held by the caller.
- `GC` keeps the `installed` and `approved` digest of each configured plugin and removes the rest, and any plugin
  directory that is not configured. It is best effort and never removes the approved tree.
- A name is checked again at every path join, so `../x` never reaches `filepath.Join`. A host without the POSIX
  execute bit cannot record it and refuses to store a tree with an executable file.

The user's `.cartographer.yaml` holds a `plugins` map, `name -> {source, ref, sha, version, installed, approved,
enabled, settings}`, written only by the plugin commands, never by `sync`. `source` is a git remote or an absolute
folder, `ref` as typed (empty is the default branch), `sha` the resolved commit (40 hex, empty for a folder),
`installed` and `approved` tree digests (64 hex; `approved` empty is pending), `settings` scalars. A malformed
entry, an empty `source`, or an `approved` without an `installed` is a load error naming the key. The key is
a row of the schema in `internal/clientconfig/schema.go`, so `cartographer config` and the settings report list it.

`plugin.State(name, entry, store, opts)` is the one rule for whether a plugin runs:

| Status | When |
|---|---|
| `disabled` | `enabled` is false |
| `pending approval` | `approved` is empty |
| `not fetched` | the approved digest is not in the store |
| `modified` | the stored tree no longer has that digest |
| `invalid` | the tree fails `ReadTree` now |
| `active` | none of the above; the only status that yields the `*Plugin` |

A newer `installed` digest never affects it. Why: `D1727`.

## The `plugin` command

`cartographer plugin` is listed in `help` and documented in [configurator.md](configurator.md#cartographer-plugin-subcommand).

| Command | What it does |
|---|---|
| `plugin new <name> [<dir>]` | Scaffolds a plugin: manifest (schema 1, version 0.1.0, `cartographer: ">=<running>"`), a README and one example skill. It checks the name with the manifest rules (and the names of the commands) and the scaffold with `ReadTree` before writing, and refuses a non-empty `<dir>` (default `./<name>`). A point other than `skills/` gets no folder: git does not keep an empty one, and a `.gitkeep` in a point is a file the reader refuses. |
| `plugin validate <dir>` | Runs `ReadTree` on the folder as it is, offline, storing nothing, and lists **every** problem as `path: reason`. Exit 0 clean, 1 with problems, 2 on usage. Run it before pushing. |
| `plugin install <git-url>[@<ref>] \| <folder> [--yes]` | Acquires, validates, stores and records the plugin as `installed`, then prints the preview and asks `Enable <name> <version>? [y/N]` on a terminal. `y` or `--yes` sets `approved` to `installed` and `enabled`. Before asking it prints the content of what runs (hook scripts, `hook.json`, frontmatter that declares commands). Otherwise it stays pending (exit 0, last line `pending approval: run cartographer plugin install <arg> again on a terminal, or with --yes`); a declined update keeps the approved version active. A name installed from another source is refused with exit 2 and nothing written; the same source is an update and keeps the approval. |
| `plugin list` | One row per configured plugin: version, `State`, short digest, redacted source, contributions per point, `update available` when `installed` differs from a non-empty `approved`. |
| `plugin show <name> [--files]` | The preview again, from the store: the approved tree when active, else the installed one, and it says which. `--files` adds every file with its size and execute bit, and the content of the files that run, capped per file. |
| `plugin approve <name> [--yes]` | Approves the **installed** digest: reads that tree back from the store (a tree that is not stored, modified or invalid is refused with `run cartographer plugin update <name>` and nothing is approved), prints the preview and the content of what runs, asks `Enable <name> <version>? [y/N]`. Without a terminal it needs `--yes` (exit 2 otherwise). Sets `approved` to `installed` and `enabled`, then collects the store. Nothing waiting: says so, exit 0. |
| `plugin update [<name>] [--yes]` | Fetches again from the configured source and ref (a folder is re-snapshotted), validates, stores and moves `installed`, `sha` and `version` only. **It never resets `approved`: the approved version stays active until a new digest is approved.** It prints the diff against the approved tree: files added, removed and changed (sizes; an executable-bit change counts), the `cartographer` range and `depends`, **permissions added flagged `!! PERMISSION ADDED`** and removed, and the provider destinations added and removed on this machine. When the version is unchanged (an edit without a bump) each side of the `approved X -> new X` line also shows the first 12 hex of its digest. It asks the same question as `approve`; on a terminal-less run without `--yes` it stops there (exit 0, `not approved`) rather than failing, so a scheduled update never approves. A tree that fails validation, or a source whose manifest names another plugin, leaves the entry untouched, is reported, the others go on and the exit is 1. A digest equal to the approved one changes nothing. |
| `plugin config <name> [<key>=<value> ...] [--unset <key> ...]` | Writes `plugins.<name>.settings` only, under the client lock from the load to the save. A value is parsed as the manifest declares (`bool` is `true` or `false`, `int` base 10 that fits the platform int); an unknown plugin, an unknown key or a bad value is exit 2 naming the manifest's keys. With no argument it prints every setting with its value and `set` or `default`; `plugin show` prints the same. It never changes the digest, the approval or the permissions, so it asks for no approval, and a setting never widens a grant. Settings live in `.cartographer.yaml`: put no credential there, use the `env` permission. |
| `plugin enable <name>` | `enabled = true`. Refused, naming the state, when there is no approval or the approved tree is `not fetched`, `modified` or `invalid`, and when a `depends` entry is unmet (`<name> needs <dep> <range>: <dep> is not enabled`, or `is <version>`). |
| `plugin disable <name>` | `enabled = false`; the plugin stays installed. Its artifacts leave the active set and are pruned by the next sync; its providers are retired by the next sync (registered inactive). Refused naming the enabled plugin that `depends` on it. |
| `plugin remove <name>` | Under the client lock, in an order a failure can be retried from: (1) every registered provider of the plugin through the disconnect path (MCP entry, managed files, lock section); (2) in every other provider section, `PruneManaged` on the lock entries whose `source` is `plugin:<name>` against the section's base dir, then the entries are dropped from the lockfile; a refused prune (a symlinked destination) is reported, exits 1 and **stops there**, the refused entries staying in the lockfile; (3) the config entry and the stored trees. Paths come from the lockfile, never from a tree. A plugin whose approved tree cannot be read, or one of whose providers is not registered this run, is refused: its providers are unknown and what it installed could not be retired. Refused naming an enabled dependent. Unknown name: exit 2. |

`depends` is check-only: no command installs, fetches or enables a dependency. Every command that changes a state
runs `registerPluginProviders` again, so a sync in the same process sees the providers of the new state.
`approve`, `update`, `enable`, `disable` and `remove` take `--output table|json` too.

`validate`, `list` and `show` take `--output table|json`; the JSON carries `"schema": "cartographer.plugin/v1"`.

The preview prints the identity, the redacted source with ref and short commit, the short digest, the `cartographer`
range and whether this build meets it, `depends` and which are installed, the permissions in plain words, the
settings keys, every contribution through its point's `Preview`, and one "runs code on this machine" list of the
files with the execute bit. Every plugin-supplied string goes through an escape, so a control character never
reaches the terminal; a test pins it.

## Onboarding: `setup` and `connect`

`setup --plugin <src>` (repeatable) and `connect --plugin <src>`, with `--approve-plugins`, install plugins as part of
the normal run. Both call `preparePlugins` (`plugins_local.go`) before the providers are resolved:

1. Each source not yet configured is installed as `plugin install` does, without approving. A source already
   configured (whatever its ref) is skipped: moving it is `plugin update`. Another source under an installed name
   stops the run (exit 2). A source that does not read or validate stops the run (exit 1) naming it.
2. An approved plugin missing from the store is fetched (a warning on failure, the run goes on).
3. Consent covers only the plugins installed **in this run**. With `--approve-plugins` each one's full approval content
   (preview, hook scripts, `hook.json`, frontmatter that declares commands) is printed first and then the plugin is
   approved: the flag is the consent, the printed block its record. Without it, on a terminal and without
   `--no-input`, each block is followed by `Enable <name> <version>? [y/N]`; otherwise nothing is approved and the
   run prints `pending approval: cartographer plugin approve <name>` and goes on. A plugin pending from other
   sources is not approved by the flag; `plugin approve` is the way. A rerun of the same `--plugin` source counts
   as this run's when the plugin was installed and never approved and the source still yields the stored tree, so a
   run that stopped at a later source does not strand the earlier ones.
4. Approval is one locked pass over a reloaded config. A first approval enables a plugin only when its `depends` are
   met, in dependency order across the sources of the run; one that stays unmet is not approved (the message of
   `plugin approve`: enable what it depends on, then approve it) and the run exits 1 after saving the others. Then the store is collected and `registerPluginProviders` runs again, so
   `agents.Detect` and `--agents` see the approved providers in the same process.

`setup` runs the step as `==> plugins` after the KB and before the agents. With plugins named or configured and no
client detected, `planSetup` does not stop: the plan says `agents: detected after the plugins are installed`, the
step detects again, and only an empty result fails with the existing message. Without `--agents` the step detects
again whenever plugins are named or configured, and connects the union of the clients found before and after it. A
name given to `--agents` together with `--plugin` that no provider has yet is checked after the step, with the usual
unknown-provider message. While a plugin of this run is pending and nothing is detected, setup finishes without
connecting an agent, says so, and names `cartographer plugin approve <name>` then `cartographer setup`. `--dry-run` prints the sources and writes
nothing. The agents step forwards `--approve-plugins` to `connect`. `sync` never approves.

## Delivery

`sync`, `connect`, `status`, `approve` and `doctor` see the plugins through `loadActivePlugins`
(`cmd/cartographer/plugins_local.go`): for each configured, enabled and approved plugin it calls `plugin.State`, and
an `active` one contributes its skills, agents and hooks with their files in memory and the manifest version.

- **Where they land.** In the global projection of every connected provider, including the bundle-only global
  projection of a workspace-scoped provider, and in no workspace projection. They join after `SelectForSources` and
  `kb_options.<kb>.artifacts` have run, so neither touches them, and a machine bound to no KB still receives them.
  The lockfile records `plugin:<name>` as the source, and the next `sync` prunes a file whose plugin left the active
  set (disabled or removed).
- **Collisions.** A kind and name claimed by a plugin and by a KB or another plugin is installed from none of them,
  and against a bundled artifact only the plugin's is dropped; each is printed once per run as `warning: skill x is declared by plugin a and KB b: installed
  from neither`. KB against KB still stops the sync.
- **A plugin that cannot be read** (not in the store, modified, invalid, no store) contributes nothing in that run,
  is reported with a warning, and retires nothing: its lock entries are carried forward and nothing it installed is
  pruned. A pending or disabled plugin is a state shown by `plugin list`, not a warning.
- **A missing tree.** Before it takes the client lock, `sync` reads an approved plugin that is not in the store from
  its configured source and ref (a network fetch must not eat the time the lock is waited for); under the lock it
  stores the bytes only if their digest equals the approved one. A different digest is reported and nothing is
  installed. `sync` never approves. `--dry-run` fetches nothing and says `would fetch`.
- **Status.** `status`, `doctor` and the TUI judge a projection without the lock entries of an unreadable plugin
  (`splitHeldPlugins`), so a purged store is not drift.
- **Not announced.** The instructions block names no plugin agent or hook.
- **Authorization.** A plugin artifact installs only while its plugin is in `ApplyOptions.ActivePlugins`, which a
  projection derives from the plugin artifacts in its own manifest (see `sync.md`); `D1732` holds the
  reasoning.

## Providers

A `providers/<id>.yaml` describes an agent client as data: a fork or an in-house build of a built-in client. `<id>` is
the provider id and the file stem. The descriptor is read by `plugin.ParseProvider` and resolved for the running
machine by `plugin.ResolveProvider`; the registry (`configurator.RegisterPlugin`) holds the result beside the
built-in providers. `providers` is a contribution point of the tree: a provider is registered at start (below) and
installs no artifact.

### Descriptor

Strict like the manifest: the artifact file cap, no YAML alias, one document, an unknown key is an error, and every
problem is listed as `path: reason`, sorted. `seed` and `allow_call` are not keys.

| Key | Rule |
|---|---|
| `schema` | `1`. |
| `name` | Equal to the file stem, `^[a-z][a-z0-9-]{1,30}$`, not `all`, not a built-in provider id. A second provider of the same plugin with the same name is reported by `plugin.ProviderNameClashes`. |
| `display_name` | One line of printable characters, at most 40. |
| `base` | A built-in provider id: the formats and emitters the provider shares. |
| `detect` | `binaries` (bare names, at most 16, no `/`, `\` or leading `-`) and `dirs` per OS (at most 16 each); at least one binary or dir. |
| `paths.global`, `paths.project` | Cells `skill` (`dir`), `agent` (`dir`, `format` one of `claude`, `opencode`, `antigravity`, `copilot`, `cline`, default `claude`), `instructions` (`file`, a `.md` file, the extension of every built-in provider's instructions file; the global one may start with `{documents}`); the global scope adds `hook` (`dir`, `scan: base`). The project scope takes no `hook` and no MCP cell. |
| `capabilities` | Map of capability id to `verified`, `unverified` or `absent`, at most 64. |
| `mcp.mode` | `native` (default), `cli` or `none`. |
| `mcp.config`, `mcp.stale` | Native only. `config` is the fork's own MCP file when it keeps one, with the base's file extension (the format is picked from it); `stale` lists at most 3 files per OS that the client derives from it and regenerates, in any format (a stale file is only deleted, never parsed, so it need not carry the base's extension). Both are refused with `cli` or `none` and take no `{documents}`. The MCP paths follow the rules of a global cell (below); a `stale` file lies below the directory of `config` (the part before any `*`) with the extension of `config`, or is the storage of the editor extension whose folder holds `config`, and holds no `.git` segment. The storage tie: `config` is `<.vscode|.vscode-insiders|.cursor|.windsurf|.vscode-oss>/extensions/<id>-<anything or *>/...` and the stale path is `<editor user dir>/User/globalStorage/<id>/<...>/<file>` with the same `<id>` (literal ASCII, compared after ASCII lowercasing, no `*` up to it, at least one segment after it), where what follows `<id>-` in the folder name is `*` or a version starting with a digit (`vendor.ext-evil-*` is another extension) and `<id>` is not an extension a built-in provider owns; the editor user dir is `Code`, `Code - Insiders`, `Cursor`, `Windsurf` or `VSCodium` under `Library/Application Support` (macOS), `.config` (Linux) or `AppData/Roaming` (Windows). Any other path outside the directory of `config` is refused. A stale file is never `config` itself, and exists only with a `config` of its own. |
| `invoke_hint` | Optional, one line, at most 200. |

A value is per OS (`darwin`, `linux`, `windows`, `default`, each key once) or one value for all. A global path starts with an anchor
`{home}/`, `{xdg_config}/`, `{app_data}/`, `{local_app_data}/` or `{documents}/`; a project path is relative and
takes none. A segment is not empty, `.` or `..`, does not end in a dot or a space, is not a Windows device name,
does not start with `~`, holds no `~` followed by a digit (a Windows 8.3 short name such as `SSH~1`, refused on every OS) and holds none of `\ : ? [ ]` or a control character. A `*` is allowed in `detect.dirs`,
`mcp.config` and `mcp.stale` only: in one segment, never the first, as `*` or `prefix*`, and never in the file name
of an MCP path. No segment of any path reaches Cartographer's own state, by the rule of the manifest globs
(`reachesOwnState`): the client config file, the `cartographer` config, state and cache directories, by name, or through
a wildcard that can still lead into one of them (a wildcard deep inside the client's own folder passes).

### The `{documents}` anchor

`{documents}` is the Documents folder as Cline resolves it for its hooks (`safewrite.DocumentsDir`: the known folder
on Windows, a redirected one followed, `$HOME/Documents` elsewhere). Cline's own dispatchers and the anchor share
that one function. Only `detect.dirs` and the global `instructions.file` take the anchor; a skill, agent, hook, project instructions, `mcp.config` or `mcp.stale` path under it is refused, because a skill or hook directory in the Documents folder would be executable content next to Cline's dispatchers. A global instructions file under `{documents}` resolves only when the folder lies below the home directory (otherwise the cell is left out with the reason `{documents} resolves outside the home directory`), passes the global-cell checks as if the folder were the literal `Documents` (a redirected `OneDrive/Documents/PowerShell` is refused like `Documents/PowerShell`), and stays out of `Documents/Cline/Hooks`. Base Cline's loader also reads `Documents/Cline/Rules`, so a file there is loaded by Cline sessions too.

### Resolving

`ResolveProvider(spec, env)` picks the values of the running OS and expands the anchors. A path that cannot be
used is left out with a reason and the rest still resolves: an anchor variable that is unset or that points outside
the home directory, `{app_data}` and its kin off Windows, a path whose walk from its root meets a link
(`safewrite.CheckPath`); a path with a `*` is checked up to the segment before the first one. Every path is rooted at
the home directory (a project path at the project root). A detect dir or global instructions file under `{documents}` is kept only when the
folder lies below home, and a link is not looked for in a detect dir, which is only read. A home that
holds `[` or `*` is a literal path. The reasons stay in `ResolvedProvider.LeftOut`, and a cell left out never makes the registration incomplete: the provider is registered without it, and `sync`, `connect` and `status` print `[<provider>] warning: cell left out: <reason>` once per run (never at process start, which `serve` and the bridge share), `doctor` reports it as a `plugin-provider` warning. A link the user lists under `providers.<provider>.allow_symlinks` is followed: the start of a command reads that key alone (`clientconfig.LoadAllowedSymlinks`) before the providers are resolved, because the full load that validates it must wait for them (D898). `Paths(home)` lists the destinations of the machine, one line per cell, for the
install preview.

### Registry

`configurator.RegisterPlugin` and `ReplacePlugins` register a provider beside the built-ins and never inside their
lists: `Providers`, `ProviderList`, `DetectionOrder` and `EmitAll` stay built-in, `Lookup` falls back to the
registered ones after the built-ins, and `AllProviders` lists both. A name that is a built-in or already registered,
a base that is not a built-in, an MCP mode other than `native`, `cli` or `none`, a file with `cli` or `none`, and an
MCP file whose extension disagrees with the base's format are refused. Each entry carries the plugin that declares
it (`Origin`) and `Active`.

`ReplacePlugins` swaps the whole set in one step. A provider name declared by two or more plugins is refused for every
claimant, as `DropPluginCollisions` does for artifacts, and returned (sorted) for the caller to report; every other
provider of those plugins and of the rest still registers. An invalid spec fails the whole call and changes nothing.

An MCP file with a `*` segment (a version folder) is a pattern, carried in `Descriptor.MCPConfigGlob`.
`MCPConfigPath`, which callers join onto the home directory literally, stays empty for it, so such a provider has no
literal path; its emitted result carries `FileGlob` instead of `FilePath`, `configurator.Apply` expands it, and a
caller that needs the files asks `Descriptor.MCPFiles` (see MCP modes and hooks below).

Agent detection lists the registered providers after the built-ins, by the same probes (binaries, then directories,
one segment of a directory may be a pattern); a binary is probed as the base's, since a name alone is no more
evidence for a fork than for its base. A provider that is not `Active` is listed and never reported installed, and
stays resolvable. `cartographer agents` labels such a row `<id> (plugin <name>, base <base>)`.

### Destination cells

`provisioning.ReplacePluginCells` (data built by `ResolvedProvider.Cells`) makes the cells of the registered
providers visible to every destination lookup: `cellFor` answers from the static matrix first, then from these. The
static matrices and the capability table stay built-in. A provider whose global roots equal, contain or lie under a
built-in's or another provider's is refused whole (both, when two candidates collide); a global cell may not sit in
the home directly, in a built-in client's directory or in a credential, shell or CI path, and no cell may lie in the
folder of the Cline dispatchers (`Documents/Cline/Hooks`, also the redirected Documents folder). A provider writes one
agent layout for both scopes. Its capability rows stay `absent` or `unverified` until probed; a fork of a probed base
is probed as its base where the layout is the same. The MCP file is a destination cell (for the KB's MCP descriptors)
only when `mcp.config` is the provider's own literal file.

The MCP paths (`mcp.config`, a pattern's fixed part, every `mcp.stale` entry) pass the same global-cell checks as a
cell, in `configurator.CheckPluginMCPPaths`: below a client directory of their own, outside the denied directories,
outside a built-in client's directory, no `.git*`. With `XDG_CONFIG_HOME` set to a directory under the home, the part of a `{xdg_config}` cell or MCP path below that root is checked as if it lay below `.config` (`{xdg_config}/git/x` is refused as `.config/git/x` is), and a provider whose `mcp.config` is left out for any reason writes no MCP entry (mode `none`). A pattern holds one `*`, in one segment, never within the client
directory (`{home}/.config/*/config.json` would reach every application's file). No MCP path equals or matches the
MCP file of a built-in provider, and a `stale` entry equal to `config` or matching it is refused. Every `stale` entry also may not equal, contain or lie under the global root of a built-in or of another registered provider (`RegisterPlugin` checks it with the cells). The check runs
where the descriptor is resolved (`ResolveProvider`: a provider that fails it writes no MCP entry, and the install
preview shows the reason), where it is registered (`RegisterPlugin` refuses it) and again before any stale file is
removed.

### MCP modes and hooks

A plugin provider's project scope takes no `hook` cell, so in workspace scope its KB hooks are not projected anywhere: `workspace bind` and `connect --workspace` warn and `sync` reports them as skipped (sync.md §Workspace scope).

`native` writes Cartographer's entry in the base's format: at the base's path, at `mcp.config`, or, when
`mcp.config` holds a `*`, into every existing regular file it matches (one file per installed version of the client;
a symlinked entry is not a match, no match is the warning "open the client once"). A pattern writes at most three
files: over that it writes none and warns with the pattern and the count; a match that is a built-in provider's MCP
file is skipped with a warning. The files in `mcp.stale` are
removed after a write that changed bytes, never after a no-op, and only when at most three exist and none lies
behind a link; servers added through the client's UI are dropped at its next start. Disconnect edits only the files
the entry is in. `cli` writes no entry and projects the bundled `cartographer-cli` skill, and the instructions block
names the provider to pass to `cartographer call`, whose KB set the folder's workspace bindings narrow, derived ones included, with the projection scope unchanged (#1804); `none` writes neither, and `connect`, `sync`, `disconnect` and
`doctor` skip it without a warning.

In `cli` mode the provider name reaches the agent only through the instructions block, so a `cli` provider needs:

1. a global `instructions` cell (`paths.global.instructions`), the file Cartographer writes the block into;
2. a file there that the client loads in every session (check the client's own docs; for a base client,
   `docs/harnesses.md` lists where it reads its instructions), otherwise
3. the plugin adds the file to the client's own context setting from a `sync.after` handler, which needs
   `permissions.files.write` on that settings file (a `sync.after` handler with `files.write` looks like the one in
   `test/e2e/fixtures/plugin-example/`, see "Add a script" below; that one writes its own file, not a client setting).

Without it the agent never sees the name: the `cartographer-cli` skill has none to pass to `cartographer call`, so the
agent guesses and may call another installed provider, which is a different scope.

A provider whose base has a shared hook dispatcher (Cline) contributes its `hook` cell as one more root of the
dispatcher, which stays shared and is never written by the plugin. The roots come from the registry and only for a
provider of an active plugin, so retracting a plugin stops its hooks at once, even with files on disk. On any other
base the hook cell is dropped with a note.

### Registration at start

`registerPluginProviders` (`cmd/cartographer`) runs in `main`, before any command loads the client config, because
`Load` drops every provider name the registry does not know (D898). It reads the `plugins` key alone
(`clientconfig.LoadPlugins`), opens the tree of every approved digest from the store (no git, no network, no lock) and
registers each provider with the registry and the destination cells: active when the plugin is enabled, inactive
when it is disabled (so what it installed can be retired). A plugin that is pending approval registers nothing.
It runs at every process start, for every subcommand (`serve` and the MCP bridge included), reads each approved tree
once, writes nothing and reports its problems on stderr. Registration happens once per process; calling it again
replaces the whole registered set, which a command that changes a plugin's state does after the change.

A plugin whose approved tree is missing, modified or invalid registers nothing, and a provider the registries refuse
is not registered (two plugins declaring one name, a colliding cell, a spec this machine cannot resolve); the run
warns, and the registration is **incomplete**. While it is incomplete `clientconfig.PluginProvidersComplete` answers
false and neither `Load` (agents, clients, scopes, unbound modes, workspaces) nor the lock-section clean-up drops any
name: a purged store never costs the user a provider. With no plugin configured the answer is true and an unknown name
is cleaned as before; with plugins configured and no registration run (`serve`, a test) the default answers false.

`sync` retires the providers of a disabled plugin before it computes its targets: `doDisconnectLocked` removes their
MCP entry, their managed files and their lock section, and drops them from `agents`, scopes and workspaces. `--client`
leaves it to the next full sync. A plugin whose tree cannot be read has registered nothing and retires nothing.

### Preview

The install preview prints, per provider, the detection (binaries and the directories as absolute paths on this
machine, `{documents}` included), every path it writes (global ones absolute, project ones relative to the project
root), the MCP mode with the file it writes and the stale files it removes, the hook root, the capabilities it claims
and the paths the resolver left out with the reason. Every string passes through `esc`. `plugin validate` runs the
registry and cell checks as a dry run against the built-ins, so a provider whose roots collide with one is a problem
of the tree.

## Seeing plugins: `status`, `agents`, `doctor`

None of the three prints anything about plugins while the `plugins` key is empty, and the JSON carries nothing for them.

**`status` and `agents`.** With at least one plugin configured they print a `plugins:` section, one line per plugin
sorted by name: name, version, state, the short commit (seven characters, or the short digest of the installed tree
for a folder), the redacted source with its ref, and `update available` when the installed tree differs from the
approved one. A plugin that waits for consent adds `pending approval: cartographer plugin approve <name>`, and each
provider it declares adds `provider <id> (plugin <name>, base <base>): connected | detected | not detected`.
`status` prints the section also when no agent is connected. The state is the one of `plugin list`, plus
`store unavailable` when the user cache directory the store lives in cannot be used. In `agents` the provider column
labels a plugin provider `<id> (plugin <name>, base <base>)`, and the dashboard (`cartographer` with no arguments)
shows the same section under the agents, with the plugin and its display name escaped.

The JSON snapshot keeps its schema (`cartographer.status/v1`) and adds, additively, `plugins` (name, version, source,
ref, sha, installed, approved, state, `update_available`) and, on a provider a plugin declares, `plugin` and `base`.
Every string of the table output is escaped for the terminal; JSON carries it as is.

**`doctor`.** Read-only like the rest of it; each finding names the command that fixes it. A disabled plugin is a
state and produces none, except for a pending or invalid installed tree.

| Check | Severity | When | Fix |
|---|---|---|---|
| `plugin-store` | error | the plugin store cannot be used at all | make the user cache directory usable |
| `plugin-store` | warning | an enabled, approved plugin is not in the store, or its stored tree was modified | `cartographer plugin update <name>` |
| `plugin-invalid` | error | the approved tree does not pass the tree reader, with the reason (for example a `cartographer` range this build does not satisfy) | `cartographer plugin update <name>` |
| `plugin-invalid` | warning | the installed tree, newer than the approved one, does not validate: it cannot be approved | `cartographer plugin update <name>` |
| `plugin-pending` | warning | the plugin was never approved | `cartographer plugin show <name>`, then `plugin approve <name>` |
| `plugin-pending` | info | the installed tree is newer than the approved one, which stays active | the same |
| `plugin-collision` | warning | a skill, agent or hook name is claimed by an active plugin and by another plugin, the bundle or a KB file the lockfile records | rename it, or disable the plugin |
| `plugin-provider` | warning | a provider was not registered (a name two plugins claim, colliding cells, a spec the machine cannot resolve), or is registered without a cell the resolver left out (a symlink not listed under `providers.<provider>.allow_symlinks`, a path outside its root), with the reason | fix the provider and `plugin update`; for a link, list it under `allow_symlinks` or replace it |

`doctor` does not ask the server, so a collision with a KB artifact is seen only while the lockfile still records the
KB file; the sync that dropped both claimants printed it, and the file is gone from the lockfile after it.
`--provider <id>` narrows the plugin findings to the refusals about that provider.

## Write your first plugin

A plugin that gives every connected client one skill, and that you can try without a server or a git host.

1. Scaffold it: `cartographer plugin new team-tools` creates `./team-tools` with the manifest (schema 1, version
   0.1.0, `cartographer: ">=<this version>"`), a README and one example skill under `skills/team-tools-example/`.
2. Edit `skills/team-tools-example/SKILL.md`: its `name` is the folder name and its `description` says when an agent
   should use it. Add `agents/<name>.md` for an agent, or `hooks/<name>/hook.json` plus its script for a hook; the
   Contribution points table says what each is checked against.
3. Check it as often as you like: `cartographer plugin validate ./team-tools` lists every problem as `path: reason`
   and stores nothing.
4. Install it from the folder: `cartographer plugin install ./team-tools`. The preview shows what it contributes and
   every file that would run; answering `y` (or `--yes`) approves exactly that version. A folder is snapshotted, so
   after an edit run `cartographer plugin update team-tools`: the changed content is shown before you are asked again.
5. Run `cartographer sync`: the skill reaches the global scope of every connected client. `cartographer status` lists
   the plugin as `active`; `cartographer plugin disable team-tools` and the next sync take it away.
6. Share it by pushing the folder to a git repository and giving colleagues `cartographer plugin install
   https://gitlab.example.com/team/team-tools.git@v0.1.0`; they approve what they read, and nobody else can.

### Add a script: the example plugin

The repository ships a plugin that does everything above, `test/e2e/fixtures/plugin-example/` (named `example-forks`,
for the fictitious fork Acme Code): a skill, a client descriptor, the command `hello`, the tool `echo` and a
`sync.after` handler. Its manifest asks for the least it needs, `kb: read` and `files.write` for
`{home}/.example-forks/*.txt`. Its `plugin.star` loads `lib/greeting.star`, registers the three scripts, and `hello`
greets with the `greeting` setting. Try it from a checkout of the repository; the end-to-end scenario
`28_plugin_scripts` runs exactly these steps.

1. `cartographer plugin validate test/e2e/fixtures/plugin-example` lists the registrations: the command, the tool
   (as `example_forks_echo`, the name it is served and called by) and the event.
2. `cartographer plugin install test/e2e/fixtures/plugin-example`, answering `n`, leaves it pending:
   `cartographer example-forks hello` exits 2 with "installed but not approved".
3. `cartographer plugin show example-forks --files` shows the permissions and the scripts; then
   `cartographer plugin approve example-forks` and answer `y`.
4. `cartographer plugin config example-forks greeting=hi`, then `cartographer example-forks hello ada` prints
   `hi, ada!`; no re-approval was asked.
5. `cartographer sync` fires the `sync.after` handler, which writes `~/.example-forks/last-sync.txt`;
   `cartographer sync --dry-run` fires nothing.
6. `cartographer call example_forks_echo --provider claude --json '{"kb":"kb-a","text":"hello"}'` runs the tool the way the bridge serves it; `plugin validate` printed its served name, with the hyphens of
   the plugin name turned into underscores.
7. Change one byte of the stored `plugin.star` and the plugin stops: the state is `modified` and the command exits 2.

A script is written like the example: declare the least in the manifest, register in `plugin.star`, read the preview
as the user will, and keep secrets out of settings.

A plugin that declares a client of its own (a fork or an in-house build of a built-in one) adds a
`providers/<id>.yaml` as described in Providers above. Two worked examples are the fixtures the tests use,
`fork-cli` (a CLI fork that takes the `cli` MCP mode: skill, agent and instructions, no MCP entry) and `fork-ext` (an
editor extension that keeps its own MCP file in a versioned folder).

## The script model

`plugin.star` at the root is the entry; flat `lib/*.star` files are loadable only from the plugin's own tree with
`load("lib/<x>.star", "name")`, no other source. The language is Starlark (Python-like, `go.starlark.net`, D1746):
no `while`, no recursion, no top-level control flow, globals frozen after load, so every loop is over a finite value.
The pure modules `json`, `math` and `time` are always there. A script runs in two phases:

- **LOAD** runs `plugin.star` once, with the registration builtins and nothing that does I/O. It reads no clock
  (`time.now` is unavailable), no setting and no file, so what is registered is a pure function of the tree: what the
  user approved is what runs. `plugin validate` runs the load and lists the registrations; the approval preview
  lists them with the script files in full.
- **CALL** runs a registered function on demand, on a fresh thread, with the capability builtins the approved
  manifest grants and nothing else.

Registration builtins, valid only while loading; a duplicate or invalid name is a load error:

| Builtin | Registers |
|---|---|
| `command(name, impl, help, usage="")` | `cartographer <plugin> <name>`; `impl(args)` gets the arguments as a list of strings |
| `tool(name, impl, description, params={...}, writes=False)` | the MCP tool `<plugin>_<name>`; `params` is a JSON schema of type `object`, `impl(params)` gets the validated arguments as a dict; `writes=True` makes read-only sessions refuse it |
| `on(event, impl)` | a handler of `sync.after` or `connect.after`; `impl(ctx)` gets the summary |

Capability builtins exist only if the manifest grants them, and a denied one is an error naming the permission, never
a silent no-op:

| Builtin | Needs | Described in |
|---|---|---|
| `kb.call(tool, **args)` | `permissions.kb` | KB tools for scripts |
| `http.request(method, url, ...)` | `permissions.net` | Network and environment for scripts |
| `env.get(name)` | `permissions.env` | Network and environment for scripts |
| `files.read`, `files.write`, `files.remove`, `files.glob` | `permissions.files` | Files for scripts |

Always available: `settings` (the values of the manifest's `settings`, typed, defaults applied; set with
`plugin config`, never re-approved, never a widening of a permission), `log` (to stderr), `print` and `fail`. **There
is no process execution of any kind**: a plugin runs a program only through a hook it ships, approved with the plugin.

A tool returns a string (sent as text) or any JSON-able value (sent as its JSON text); `None` is `ok`. A tool error
(`fail`, a runtime error, a denied capability or a cap hit) is an MCP result with `isError: true` and the message, so
the model reads it. A command returns `None` (exit 0), an int (the exit code) or a string (printed). An event's
return value is ignored.

Limits per call are named constants in `internal/plugin/star/limits.go`: 20 million steps for a tool or an event and
500 million for a command (and for a load: 20 million), 30 s wall clock for a tool and 10 s for an event (a command
runs until done or Ctrl-C), 64 KiB of captured `print`, 1 MiB of tool result text, and the HTTP and file caps below.
Memory is bounded by a watchdog, not by the language: while a load or a call runs, the live heap is sampled every
few milliseconds and the run is cancelled (a tree that does not load, or a `limit exceeded` call) when it has grown
256 MiB past its value at the start. It is approximate in both directions: the heap is the whole process's and counts garbage not yet
swept, so it can fire on a script that did little in a busy process, and one huge allocation between two samples can
pass it. A call has one baseline, taken before its private re-run of `plugin.star`. It stops a script that keeps allocating; approve a script as you would run it.
The runtime itself writes nothing (no cache, no log file), which is why `cartographer call` still works with a
read-only home.

## Network and environment for scripts

`http.request(method, url, headers={}, body="", timeout=30)` exists only with `permissions.net`, `env.get(name)` only
with `permissions.env`; without them the call is an error naming the permission. `http.request` accepts GET, HEAD, POST,
PUT, PATCH, DELETE and OPTIONS and returns `status`, `headers` (lowercase names, repeated values joined by `, `) and
`body` (text; a body that is not UTF-8 is an error). The scheme is `https`; plain `http` only to a loopback host. The
host must match a `net` pattern, and so must every redirect target (at most 10; a redirect outside the list is not
followed, and Go drops `Authorization` on a change of host). A name can resolve anywhere, so the dial refuses
loopback, private, carrier-grade NAT, link-local, multicast and unspecified addresses unless the manifest lists that very
literal, and connects to the address it checked. The system proxy variables are honoured, and the allowlist is on the
request host, not on the proxy; behind a proxy Cartographer resolves the target and refuses a forbidden answer, but the
proxy may resolve differently, so the final address check is the proxy's. Addresses that embed IPv4 (NAT64, 6to4) are
judged by it. On a redirect to another host only `Accept`, `Accept-Language`, `Content-Type` and `User-Agent` follow; every other
header the script set is dropped. Caps per call: 50 requests (every redirect hop counts), 8 MiB of response, 4 MiB of request body, 30 s per request
and never beyond the call's deadline. `Host` and the framing and hop-by-hop headers cannot be set. `env.get` returns a
listed variable or `None`; there is no way to list the environment.

## Files for scripts

`files.read(path, binary=False)`, `files.write(path, data)`, `files.remove(path)` and `files.glob(pattern)` exist with
`permissions.files`: `read` grants `read` and `glob`, `write` grants `write` and `remove`; without the list the call is
an error naming it, and in a read-only session `write` and `remove` are refused whatever the manifest says. A path is
only the placeholder form of the manifest, `{home}/...`, `{xdg_config}/...`, `{app_data}/...`, `{local_app_data}/...` or
`{documents}/...` (the anchors expand as for providers, under the home directory only); an absolute path, `~`, a `..`,
empty or dot segment, a backslash, a colon, a segment ending in a dot or space and a Windows device name are refused on
every OS. The path must match a granted glob (`*`, `?`, `[..]` inside a segment, `**` for whole segments). Whatever the
grant, these are never reachable: a `.ssh`, `.gnupg` or `.git` component (a write into `.git/hooks` would run code),
any component starting with `.cartographer` (config, lock files, token, state, approvals, a KB's own directory), every place a plugin provider may not write into (`.aws`, `.kube`, `.docker`, Desktop, LaunchAgents, `.config/git`, autostart and shell directories, `.cargo`, `.local/bin`), the shell start-up files (`.zshrc`, `.bashrc`, `.profile`, ...), the server's default data (`~/cartographer-data`, `~/.local/share/cartographer`) and the
`cartographer` directory under the user config and cache directories, `$HOME/.config`, `{xdg_config}`, `{app_data}` and
`{local_app_data}` (server config, update cache, plugin store). A link anywhere below home, in a directory or as the
file, is refused, and a path whose resolution leaves home or lands in a denied place too; the final open does not
follow a link where the OS has the flag. The check and the open are not atomic, as for every writer in the client.
`read` returns text (invalid UTF-8 is an error naming the file; `binary=True` returns `bytes`); `write` creates parent
directories (0700), replaces the file atomically, makes a new file 0600 and keeps the mode of an existing one; `remove`
deletes a file, never a directory, and returns `False` for a missing one. `glob` returns the regular files a pattern
matches, as placeholder paths, sorted, never entering a denied directory or following a link, at most 1000 (the walk
stops there) and at most 100000 entries looked at. Caps: 8 MiB per read and per write.

## KB tools for scripts

`kb.call(tool, **args)` (or `kb.call(tool, {args})`) exists with `permissions.kb`; without it the call is an error naming
the permission. The script passes `kb` itself, as every routed tool requires. It returns the tool's text parsed as JSON
when it parses, else the string; a server error is a script error carrying the server's message, and a result over 1 MiB
is a limit error. With `kb: read` only a tool the server annotates read-only (`readOnlyHint` of its `tools/list`, kept
for 5 minutes and refetched at most every 5 seconds for a name it does not know) runs: an unannotated or unknown tool is denied, matched on the exact name (the list is the server's own
classification, not a copy kept in the plugin code). With `kb: write` every tool the session may call is allowed, and a
read-only session is `read` whatever the manifest says. The gate is the plugin's declared limit, not the security
boundary: the server still enforces the token's scopes and the session's access. The `kb` argument is not
restricted by the plugin layer: the token's scopes and the session's KB set, on the server, decide which KBs a script
reaches (commands and events apply the client's `known_kbs`, see Commands and events; the bridge adapter applies the session's KB set: a `kb` outside it is refused before any request). A successful result reaches the script
as the KB wrote it, unescaped: KB content is data to treat as such. When the call's deadline ends or Ctrl-C comes, the
request is abandoned, but a write the server had already received may still complete.

## Commands and events

`cartographer <plugin> <command> [args...]` runs a command an **active** plugin registered; a built-in name always
wins, and a word that names no installed plugin gives the usual unknown-command error. `cartographer <plugin>` lists
the commands, `<command> -h` prints its usage and help, and `help` and the unknown-command text carry a `Plugins`
group. The remaining arguments reach the script as a list of strings. `print` goes to stdout, `log` to stderr, a
returned string is printed, a returned int is the exit code, `fail()` and a denial or limit print to stderr and exit 1,
Ctrl-C exits 130; everything a script or a limit message supplies is escaped line by line. An installed plugin that is
not active exits 2 naming its state. The config is read without the client lock. Until a newer tree is approved the
approved one runs.

`kb.call` from a command or an event goes to the routed endpoint with the user's token, and the adapter refuses a `kb`
argument that is missing or not one of `known_kbs` of the client config: a command has no provider, so that list is
the client's perimeter on top of the server's scopes.

Events run after the work they follow, outside the client lock, in plugin-name order, one handler per plugin, each with
the event deadline. A failure (including an unloadable script) is a warning on stderr naming the plugin and the event
and never changes an exit code. `sync.after` fires at the end of `runSync` (after the reports, the lock released), not
in `--dry-run`, not for a run that fails before the apply, not for the dashboard's sync (it holds the lock itself); a
partial sync fires it with the failures in `errors`. `connect.after` fires from the one place `connect`, the
interactive retry and `reconnect` print their result, not in `--dry-run`. The handler receives `ctx.event`,
`ctx.providers`, `ctx.installed`, `ctx.updated`, `ctx.removed`, `ctx.pending` and `ctx.errors`; for `connect` there is
no earlier lockfile to compare with, so what was written counts as installed. `connect.after` also fires on a partial
connect, with the providers that connected (not those the write guard skipped). The dashboard fires no plugin event,
for sync or for connect.

## Tools in the bridge

A plugin's tools are served by the local stdio bridge (`cartographer mcp`) that a connected client starts, never by
the server; how a session lists and runs them is in Tools in a session below.

A provider with no workspace binding keeps the same access it had through its HTTP entry: when an enabled, approved
plugin lists tools, the provider's global MCP entry becomes the bridge at the next `sync` (it needs `cartographer` on
`PATH`; otherwise the entry stays HTTP and sync warns), with the provider's KB set, read-write, and no question about
folders (D1757 amends D737; sync.md §Workspace scope). The limits, by design:

- A provider with an access profile (`clients.<provider>.access`) keeps the HTTP entry and sees no plugin tool.
- A client connected by hand with a direct URL ([mcp-clients](mcp-clients.md)) never receives a plugin tool; the
  server has no plugin tools of its own.
- A plugin tool is invisible until `sync` has switched the entry; a client restarts to load it.

## Tools in a session

A tool a plugin registers is served by the local stdio bridge (D737) as `<plugin>_<tool>`, hyphens turned into
underscores, at most 64 characters (a longer name is dropped, never truncated). `tools/list` carries it after the
server's tools, on the last page, with the description prefixed `[plugin <name>] ` and `readOnlyHint` true unless the
tool is registered `writes=True`. A name equal to a server tool, or already taken by an earlier plugin in name order
(`a` + `b_c` against `a_b` + `c`), is dropped and one line goes to the bridge log; `cartographer call --verbose` shows it.
The served names are settled per session scope and again whenever the scope changes (a read-only listing omits the server's write tools); a name the client saw as a plugin tool and the server owns after a change is refused with "list the tools again", never forwarded. A write tool is neither listed nor run in a read-only session, and a folder bound to no KB lists nothing. The tool runs
in the bridge process, with the 30 s limit; its `print` and `log` go to the bridge's stderr, never to the protocol on
stdout; `notifications/cancelled` stops it. Its `kb.call` carries the session's `kbs=`/`access=`/`readonly=`, the token and the
`Cartographer-Author`. `cartographer call <plugin>_<tool>` runs the same path and writes nothing. `kb.call` of a tool registered `writes=False` runs read-only whatever `permissions.kb` says (the client is told the tool is
read-only and may run it unasked); a denied write names `writes=False`. A server tool called without a `kb` argument is
refused (the session's KB set is applied to the `kb` argument). `cartographer call --list` asks the bridge one page at a time,
so a collision with a server tool listed on an earlier page is not seen there. A session started
before an approval keeps its tool list. A client with a direct HTTP entry (an access profile, `cartographer` not on `PATH`,
a hand-written client) does not see plugin tools; D737's probe covered Claude Code, Codex, Kiro CLI, OpenCode, Crush and
Copilot CLI, not Antigravity or Kiro IDE.

## Limits

- No workspace projection: a plugin's skills, agents and hooks reach the global scope of every connected client and no
  project scope.
- No `mcp/` and no `instructions.md` contribution point.
- A script runs no process, and a plugin tool reaches a client only through the stdio bridge (above).
- The instructions block does not announce a plugin's agents and hooks.
- A server never hosts or loads a plugin; `sync_pull` refuses an artifact whose source is `plugin:`. There is no
  plugin catalog and a KB cannot recommend a plugin.
- Plugins are never signed; the approval of one digest is the only consent, and it is per version.
