# Frequently asked questions

## What happens when two agents write the same page?

It is not a silent last-write-wins. There are two layers.

**On one server, optimistic concurrency.** A write can carry `if_match`, the content
hash of the version the agent read. If the page changed in the meantime the write is
refused with `stale_write`, and the agent re-reads and merges. Nothing is overwritten
quietly.

**Across machines, git conflicts.** Each KB is a git repository and every write is one
commit. When a sync hits a real conflict the rebase stops, the KB is marked `degraded`
and `conflicts_list` shows what is in conflict. `git_conflict_resolve` then requires an
explicit choice per concept (`ours`, `theirs` or `edit`, a merged text), and only then
completes the merge, commit and push. The `cartographer-kb-conflict-resolve` skill walks
an agent through this. Writes are small, separate commits, so a conflict usually touches
one page and any resolution can be reverted.

The tooling guarantees that nothing gets lost, not that every merge is trivial. See
[concurrency.md](concurrency.md).

## Is `cartographer sync` atomic across clients?

No, and it does not claim to be. `sync` downloads and verifies the manifest first, then
updates the MCP entries and the client configuration, then materializes the artifacts
provider by provider, with a preflight and checkpoints in the lockfile.

If a provider fails, what already completed stays, and that provider may leave partial
files. There is no automatic rollback of the whole projection: running `sync` again
reconciles it, and `cartographer status` shows where things stand. See
[sync.md](sync.md).

## Can `cartographer sync` live next to chezmoi or another dotfiles manager?

Yes, as long as the two do not write the same paths. A dotfiles manager owns your
hand-written configuration; Cartographer owns the files it generates, which its lockfile
tracks. If generated files must stay under a dotfiles manager, it will see them as
drift, so ignore those paths on its side.

## Why not just a `CLAUDE.md`, or Notion, or Obsidian?

They are good choices for what they cover. Cartographer starts where they stop.

- **`CLAUDE.md` / `AGENTS.md`** belongs to one repository and is mostly read-only for the
  agent. Knowledge that crosses repositories (infrastructure, incidents, decisions) has no
  natural home there. Keep a small instruction file anyway: it is where the pointer to the
  KB lives.
- **Notion** is reached through connectors that are not built to govern many automated
  writers: there is no per-write commit, schema, link check or lint.
- **Obsidian** is typically one person on one machine. A KB is plain Markdown and YAML in a
  git repository, so you can read it in Obsidian, but the point is that several people and
  agents on different machines and clients share one KB.

What Cartographer adds is governance over many automated writers: agents write only
through MCP tools, and Meridian enforces schema, link checks, lint, optimistic
concurrency, one commit per write, conflict handling across machines, a tamper-evident
audit log and tokens scoped per KB. It is client-neutral: the same KB hands each agent the
same knowledge, skills and instructions in the client's native format.

If your knowledge fits in one repository and one person, a plain instruction file is
simpler and you should use it.

## Why does the sync say the remote refused the push, and why is it not retried?

Some refusals are the remote's decision and retrying only repeats them: a token without write access (HTTP 403,
`Permission to … denied`, a read-only deploy key), a repository rule such as GitHub's `GH013` (required signatures,
pull-request-only branches), a file over the size limit (`GH001`), a credential the remote rejects (`Authentication
failed`, HTTP 401: check the token or credential helper for this forge) or that git has none of to offer (`could not
read Username`: configure a token or credential helper, or use an ssh remote). A bare HTTP 403 is still retried, it can be a rate limit. The KB is `failed` after one attempt and
`sync_status` quotes the remote's own line with the way out; the change stays committed in the local clone and is
pushed by the next write once the cause is fixed. `cartographer setup` checks push access with a dry run before it
creates anything.

Cartographer does not sign commits, and it ignores your global `commit.gpgsign` and `core.hooksPath` when it commits
(a background service cannot answer a pinentry prompt or run a hook written for your own commits). A remote that
requires signed commits cannot be pushed to: lift that rule for the KB's branch, or set `branch` to one it does not
cover. A TLS error (`x509`, `SSL certificate problem`) names `SSL_CERT_FILE` and git's `http.sslCAInfo`.

## Can I turn off the offer to report a bug from an agent?

Yes. Set `feedback.destination: off` on the server (`CARTOGRAPHER_FEEDBACK_DESTINATION=off`): the served instructions stop naming when to offer a report and internal-error results lose their report hint. Set it to an `https://` URL template with `{title}` (and optionally `{kind}`, `{body}`) to send agents to your own tracker instead of GitHub. Nothing is ever sent by Cartographer: the user opens the link and submits it. See [deployment.md](deployment.md).

## The background service cannot reach the network behind my proxy or internal CA. What do I do?

A terminal works because your shell sets `HTTPS_PROXY` or `SSL_CERT_FILE`; a launchd, systemd or Windows service does not inherit the shell. Run `cartographer service install` again from a shell that has them set: it records `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` and `SSL_CERT_FILE` (and their lowercase forms), and nothing else, for the service, and `cartographer service status` shows their names on an `env:` line. A variable you later unset is removed at the next `service install` (an `upgrade-repair` keeps what was recorded). The values are stored in `service.env` next to `server.yaml`, readable by you only (on Windows the mode is not enforced: the file inherits the ACL of its directory, per-user under the default `%APPDATA%`, but readable by others if you point the config at a shared directory). Git built on OpenSSL does not always read `SSL_CERT_FILE`; point it at the CA with `git config --global http.sslCAInfo /path/to/ca.pem`. See [configurator](configurator.md) §`cartographer service <action>`.

## Can I use Cartographer from both Windows and WSL agents?

Yes, with one server inside the WSL2 distro: install with `install.sh` and run `cartographer service install` there, which also registers a Windows task that keeps the distro alive. Run `cartographer connect` in the distro for the WSL agents and in PowerShell, with the Windows binary, for the Windows ones; both reach the server at `http://127.0.0.1:39273/mcp`. `cartographer.exe service install --wsl <distro>` lets the Windows `service` commands act on it. See [deployment.md](deployment.md) §Windows with WSL.

## `serve` says the address is already in use, or `service status` says another program answers. What do I do?

Something else holds the server's listen address (`127.0.0.1:39273` by default): another `cartographer serve` started by hand, a second copy of the service, or an unrelated program. `cartographer serve` stops at once with `cannot listen on <addr>: the address is already in use by another process`; `cartographer service status` prints `healthy: false (another program answers on <addr>…)`, and `doctor` and the tray name the same address. A server only counts as Cartographer's when its `/health` carries `status: "ok"` and a `version`, so a program that merely answers 200 is never shown as healthy. Find the holder with `lsof -nP -iTCP:39273 -sTCP:LISTEN` (macOS, Linux) or `Get-NetTCPConnection -LocalPort 39273` (Windows) and stop it, or give the server another address: set `http:` in the server config (`cartographer service status` prints its path), run `cartographer service restart`, then `cartographer reconnect` so the clients follow. See [configurator](configurator.md) §`cartographer service <action>` and [deployment.md](deployment.md).
