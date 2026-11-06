# Plugins

A **plugin** is a folder or a git repository with a manifest, `cartographer-plugin.yaml`, at its root. It extends
the client of one machine; a server never loads it. This page describes what the code reads today: the manifest
the version ranges it speaks, the contribution points and the tree reader (`internal/plugin`).

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
| `permissions.files.read`, `permissions.files.write` | At most 16 globs each. A glob starts with one of `{home}`, `{xdg_config}`, `{app_data}`, `{local_app_data}`, `{documents}`, continues with `/` separators, holds no other placeholder and no `..`. A base directory other than `{documents}` must be followed by a path; an empty or `.` segment (`{home}/`, `{home}//x`, `{home}/./x`) is refused, and so is a segment with `~` or `:` (Windows short names and streams). Trailing dots and spaces of a segment are ignored when matching Cartographer's own state, as Windows ignores them. A glob that reaches Cartographer's own state is refused: the client config file, the `cartographer` config, state and cache directories (the plugin store is inside the cache one), by name or through a wildcard. Other sensitive paths, such as `.ssh` or a shell rc file, can be declared: the approval preview shows them and the enforcer limits them. |
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
and never loaded. 0.28 registers `skills`, `agents` and `hooks`, and a test pins the exact set.

| Point | Layout | Check |
|---|---|---|
| `skills` | `skills/<name>/SKILL.md` plus any files; the whole folder is the artifact, the executable bit is kept | `provisioning.ValidateSkillFile` |
| `agents` | `agents/<name>.md` | `provisioning.ValidateAgentFile` |
| `hooks` | `hooks/<name>/hook.json` plus the files it runs | `ValidateHookJSON` and `ValidateHookRequires`; unlike in a KB, a failure is an error, not a warning. The install preview shows the hook's command, event and `requires`, and lists every file with the executable bit (in a hook, every file but `hook.json`, as the client materializes it, so the artifact hash equals the one sync computes). `cartographer-bootstrap` and `cartographer-write-findings` are refused. |

Every artifact name is a valid artifact name, and a plugin cannot shadow the bundle: a skill or agent named like a
bundled one (the CLI skill included) is refused. Reserved and bundled names are compared ignoring case, since a filesystem may fold it.

## The tree reader

`plugin.ReadTree` takes the files of a plugin in memory (`TreeFile`: slash path, content, executable bit and a
flag for a symlink or non-regular file) and returns the validated `Plugin` and every problem found, sorted; the
order of the files does not matter. It is the one function that knows the tree rules, so validating, installing and
loading share them.

- At the top: the manifest and one folder per registered point. `README*`, `LICENSE*`, `CHANGELOG*` and `.git*`
  are ignored and never stored or checked. `plugin.star` and `lib/` are refused (plugin scripts are not supported yet), `mcp/`
  and `instructions.md` are not contribution points in this version, and any other entry is an error naming it.
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
the path before any blob is read; `README*`, `LICENSE*`, `CHANGELOG*` and `.git*` at the top are skipped. The
executable bit is mode `100755`. The resolved commit (40 hex) is returned. Without `git` on PATH a git source
fails with a message saying so; a folder never needs git.

**Folder.** Walked with `Lstat`: a symlink or non-regular file is an error naming it, the same entries are skipped,
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
`Hidden` in `internal/clientconfig/schema.go`: no comment, `cartographer config` row, settings report entry or
example-file line shows it.

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

`cartographer plugin` is dispatched but not yet listed in `help`, in the usage table or in `docs/configurator.md`.

| Command | What it does |
|---|---|
| `plugin new <name> [<dir>]` | Scaffolds a plugin: manifest (schema 1, version 0.1.0, `cartographer: ">=<running>"`), a README and one example skill. It checks the name with the manifest rules (and the names of the commands) and the scaffold with `ReadTree` before writing, and refuses a non-empty `<dir>` (default `./<name>`). A point other than `skills/` gets no folder: git does not keep an empty one, and a `.gitkeep` in a point is a file the reader refuses. |
| `plugin validate <dir>` | Runs `ReadTree` on the folder as it is, offline, storing nothing, and lists **every** problem as `path: reason`. Exit 0 clean, 1 with problems, 2 on usage. Run it before pushing. |
| `plugin install <git-url>[@<ref>] \| <folder> [--yes]` | Acquires, validates, stores and records the plugin as `installed`, then prints the preview and asks `Enable <name> <version>? [y/N]` on a terminal. `y` or `--yes` sets `approved` to `installed` and `enabled`. Before asking it prints the content of what runs (hook scripts, `hook.json`, frontmatter that declares commands). Otherwise it stays pending (exit 0, last line `pending approval: run cartographer plugin install <arg> again on a terminal, or with --yes`); a declined update keeps the approved version active. A name installed from another source is refused with exit 2 and nothing written; the same source is an update and keeps the approval. |
| `plugin list` | One row per configured plugin: version, `State`, short digest, redacted source, contributions per point, `update available` when `installed` differs from a non-empty `approved`. |
| `plugin show <name> [--files]` | The preview again, from the store: the approved tree when active, else the installed one, and it says which. `--files` adds every file with its size and execute bit, and the content of the files that run, capped per file. |

`validate`, `list` and `show` take `--output table|json`; the JSON carries `"schema": "cartographer.plugin/v1"`.

The preview prints the identity, the redacted source with ref and short commit, the short digest, the `cartographer`
range and whether this build meets it, `depends` and which are installed, the permissions in plain words, the
settings keys, every contribution through its point's `Preview`, and one "runs code on this machine" list of the
files with the execute bit. Every plugin-supplied string goes through an escape, so a control character never
reaches the terminal; a test pins it.

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
built-in providers. Nothing in a plugin tree is read as a provider: the point is not in the registry of
contribution points, so a `providers/` folder is an unknown entry of the tree.

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
| `paths.global`, `paths.project` | Cells `skill` (`dir`), `agent` (`dir`, `format` one of `claude`, `opencode`, `antigravity`, `copilot`, `cline`, default `claude`), `instructions` (`file`, a `.md` file, the extension of every built-in provider's instructions file); the global scope adds `hook` (`dir`, `scan: base`). The project scope takes no `hook` and no MCP cell. |
| `capabilities` | Map of capability id to `verified`, `unverified` or `absent`, at most 64. |
| `mcp.mode` | `native` (default), `cli` or `none`. |
| `mcp.config`, `mcp.stale` | Native only. `config` is the fork's own MCP file when it keeps one, with the base's file extension (the format is picked from it); `stale` lists at most 3 files per OS that the client derives from it and regenerates. Both take the base's extension, are refused with `cli` or `none`, and take no `{documents}`. |
| `invoke_hint` | Optional, one line, at most 200. |

A value is per OS (`darwin`, `linux`, `windows`, `default`, each key once) or one value for all. A global path starts with an anchor
`{home}/`, `{xdg_config}/`, `{app_data}/`, `{local_app_data}/` or `{documents}/`; a project path is relative and
takes none. A segment is not empty, `.` or `..`, does not end in a dot or a space, is not a Windows device name,
does not start with `~` and holds none of `\ : ? [ ]` or a control character. A `*` is allowed in `detect.dirs`,
`mcp.config` and `mcp.stale` only: in one segment, never the first, as `*` or `prefix*`, and never in the file name
of an MCP path. No segment of any path reaches Cartographer's own state, by the rule of the manifest globs
(`reachesOwnState`): the client config file, the `cartographer` config, state and cache directories, by name or through
a wildcard.

### The `{documents}` anchor

`{documents}` is the Documents folder as Cline resolves it for its hooks (`safewrite.DocumentsDir`: the known folder
on Windows, a redirected one followed, `$HOME/Documents` elsewhere). Cline's own dispatchers and the anchor share
that one function. Only `detect.dirs` takes the anchor: Cline's dispatcher is the one file Cartographer writes in the
Documents folder, so a skill, agent, hook, instructions, `mcp.config` or `mcp.stale` path under it is refused.

### Resolving

`ResolveProvider(spec, env)` picks the values of the running OS and expands the anchors. A path that cannot be
used is left out with a reason and the rest still resolves: an anchor variable that is unset or that points outside
the home directory, `{app_data}` and its kin off Windows, a path whose walk from its root meets a link
(`safewrite.CheckPath`); a path with a `*` is checked up to the segment before the first one. Every path is rooted at
the home directory (a project path at the project root). A detect dir under `{documents}` is kept only when the
folder lies below home, and a link is not looked for in a detect dir, which is only read. A home that
holds `[` or `*` is a literal path. `Paths(home)` lists the destinations of the machine, one line per cell, for the
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
literal path and its entry is not emitted.

Agent detection lists the registered providers after the built-ins, by the same probes (binaries, then directories,
one segment of a directory may be a pattern); a binary is probed as the base's, since a name alone is no more
evidence for a fork than for its base. A provider that is not `Active` is listed and never reported installed, and
stays resolvable. `cartographer agents` labels such a row `<id> (plugin <name>, base <base>)`.
