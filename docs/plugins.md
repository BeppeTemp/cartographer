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
