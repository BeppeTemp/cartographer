---
topic: architecture
---

# D1753 — a plugin script has only the capabilities its manifest grants, and none is a process

**Decision.** A plugin script reaches the outside world through five modules
(`kb`, `http`, `files`, `env`, `log`), each of which decides at call time, from the
permissions of the call, whether it may act. A capability the manifest does not
grant refuses with a denial naming the missing permission; no module starts a
process, and nothing else is reachable. `files` takes only placeholder paths
(`{home}/...`), checks them against the manifest's globs and then against a hard
deny list the manifest cannot override; `kb.call` is read-only unless the manifest
says `kb: write`.

**Why.** An approved plugin is code Cartographer runs for the user, so the approval
screen must describe what that code can do. Granted-or-absent makes the manifest the
whole description; a process would make it a lie, because any program the user has
could do anything. The hard deny list exists because the broadest grant the manifest
validator accepts, `{home}/**`, would otherwise reach the user's keys and
Cartographer's own state:

- any `.ssh`, `.gnupg` or `.git` component. A write into `.git/hooks` of a
  repository under home is code execution, which "no process execution" rules out;
- any component starting with `.cartographer` (the client config, its lock files,
  token, state, approvals, the variant cache, a KB's own directory): a prefix rule, so
  a file added later is covered without a list;
- the `cartographer` directory under the user config dir, the user cache dir (update
  cache and plugin store), `$HOME/.config`, `{xdg_config}`, `{app_data}` and
  `{local_app_data}` (the server config).

- everything the provider cells already refuse (`configurator.DeniedHomePath`, one
  list for both: `.aws`, `.kube`, `.docker`, Desktop, PowerShell profiles,
  LaunchAgents, `.config/{git,autostart,systemd,fish,...}`, `.cargo`, `.local/bin`),
  the shell start-up files (`.zshrc`, `.bashrc`, `.profile`, ...) and the server's data
  at its default places (`~/cartographer-data`, `~/.local/share/cartographer`); a
  configured data directory is the server's and the client cannot know it.

The names are compared case-insensitively on every OS. A path is checked in a fixed
order, each step only narrowing the next: lexical form, grant, deny list, the walk
from home through no link (`safewrite.CheckPathStrict`, which ignores the user's
allow list), the same checks after symlink resolution, then the access, which opens
without following a final link where the OS has the flag. The anchors expand through
the one authority the providers resolver uses (`plugin.ExpandAnchor`), under home only.

The network follows the same rule at the point of the connection, not of the
request: the address is checked when dialled (proxies, embedded IPv4, redirect hops
and redirect headers included), so a name that resolves to a private address, or a
redirect to one, is refused whatever the allowlist says. `kb.call` reaches the KB
through two adapters, one for the CLI and one for the bridge, which share the
read-only decision: the server's own `readOnlyHint` annotation from `tools/list`,
fetched by the adapter (`star.MCPKB`) and kept for five minutes, refetched early for a name it does not know, so no list of read tools is copied into the
client. An unannotated or unknown tool counts as a write: a `read` manifest, or a
read-only session, refuses it before any request is sent. The gate is the plugin's
declared limit; the token's scopes and the session's access stay the server's.

**Alternatives rejected.**

- Process execution behind a permission (`exec: [git]`): the permission cannot
  describe what the program does with its arguments; it would turn every plugin
  into "runs anything the user has".
- A deny list only for the exact files Cartographer has today: it misses a lock or
  state file added next time, which is why the rule is structural.
- Following links the manifest granted: the grant is a path in the user's mind, a link
  is somebody else's decision; refusing every link is simpler to state and to test.
- Checking the network allowlist on the request's host only: DNS rebinding and a
  redirect would bypass it.

**Consequences.** The check-then-open race is the repository's accepted one
(`internal/safewrite`): a process that can swap a directory for a link between the
check and the open already runs as the user; the final open refusing a link narrows
it, nothing here claims more. `files.glob` is bounded (1000 results, 100000 entries
looked at, 32 levels) and silently stops there. A new capability module must decide
from the thread's context at call time and be listed in the approval preview; a new
state file of Cartographer's under home must keep the `.cartographer` prefix or be
added to the deny list in the same change. The memory a script uses is not bounded
(see D1746, #1741).
