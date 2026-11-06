# Plugins

A **plugin** is a folder or a git repository with a manifest, `cartographer-plugin.yaml`, at its root. It extends
the client of one machine; a server never loads it. This page describes what the code reads today: the manifest
and the version ranges it speaks (`internal/plugin`).

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
