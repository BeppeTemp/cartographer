# Getting started

From zero to a working agentic wiki in about ten minutes, using the **Local
Core** profile: one server, one KB, one agent (Claude Code in this walkthrough
— OpenCode, Codex CLI, Kiro and Antigravity work the same way via `cartographer connect`).

You need **git**, and an empty git repository you own to be the first KB's
remote — a KB is a git repository, and that remote is what makes it durable and
syncable (D134). `sops` is needed only if the KB will hold encrypted values, and
Go 1.26+ only for the from-source install. What each installation method puts on
the machine, and how to remove it, is in the README under §Install.

## 1. Install the client/server binary

```bash
# macOS (Homebrew)
brew install beppetemp/tap/cartographer

# Linux / macOS without Homebrew
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh

# From source (Go 1.26+)
go install github.com/BeppeTemp/cartographer/cmd/cartographer@latest
```

`cartographer` is a single binary: it is the MCP server (`serve`), the
multi-provider client (`connect` / `status` / `sync`), and a TUI dashboard
(run it with no arguments in a terminal).

If an **agent** is performing the installation on your behalf, it follows the
imperative [agent-driven installation runbook](agent-install.md) instead of this
human walkthrough — a repository link is all it needs to start, and it will ask
you for the KB remote itself.

## 2. Run the server and create your first KB

Install it as a native service (launchd on macOS, systemd user unit on Linux),
so the server survives reboots and listens on `127.0.0.1:39273`:

> On Linux the unit is a **user** unit, so it stops when you log out and does not
> come back on boot unless lingering is enabled for your account:
> `loginctl enable-linger <user>`. On a desktop that logs in automatically this
> rarely shows; on a headless host it is the difference between the promise above
> and a server that is simply not there.

```bash
cartographer service install    # generates the config, installs and starts the service
cartographer kb create my-kb --remote <git-remote-url> --restart
```

`kb create` scaffolds the KB in the service's data dir: a git repository with
`data/index.md` and `data/log.md`, plus a local search index under
`.cartographer/` (never committed). `--remote` must point at an **empty**
repository (GitHub, Gitea, a bare repo — anything git can push to): it becomes
the KB's `origin`, which is what makes the KB durable and syncable, and the
initial commit is pushed to it right away. For a repository that already
contains a KB use `cartographer kb clone <url>` instead; for a throwaway local
trial, `--no-remote` skips the remote and warns that the KB is neither backed
up nor synced (D134). `--restart` makes the running server pick it up. Every write from now on will be one git commit — the KB is a plain
folder of Markdown you can open in any editor or in Obsidian.

> **Running it by hand instead.** For development you can skip the service and
> run a one-off server on a KB of your choice:
> `cartographer serve --kb ~/my-kb --init --http :39273` (`--init` scaffolds
> it). The service path above is the one to use daily.

## 3. Connect your agent

```bash
cartographer connect claude
```

This registers the `cartographer` MCP server in Claude Code's configuration
(`~/.claude.json`), materializes the bundled skills (procedural know-how the
agent loads on demand), and writes a managed instructions block so the agent
knows the wiki exists and how to use it. Run `cartographer connect` with no
arguments for an interactive form, `cartographer status` to check for drift,
`cartographer sync` to re-align.

## 4. First session

**Restart Claude Code first.** The MCP tools and the provisioned skills are
loaded at session start, so a session that was already open when you ran
`connect` sees none of them — the same closing step the agent runbook makes
explicit. In the new session, ask something that produces knowledge worth
keeping, for example:

> Explore this repository and write a concept page about its architecture
> in the wiki. Close the session with a log entry.

Behind the scenes the agent will call the MCP tools: `atlas_overview` to
orient itself, `map_create` / `concept_write` to create the page,
`log_append` to journal the session. Next session, ask it something related —
it will `search` and `concept_read` its way back to what it wrote, and build
on it. That accumulation is the whole point.

## 5. Look at what happened

```bash
cd ~/cartographer-data/my-kb   # the service's data dir
git log --oneline              # one commit per write operation, revertible
ls data/                       # plain Markdown with YAML frontmatter
```

Nothing is opaque: the KB is the files, git is the history, and any write the
agent made can be reviewed or reverted with ordinary git.

## Where to go next

- Multiple KBs, token auth, running in k8s →
  [`deployment.md`](deployment.md)
- The full MCP tool API → [`control-plane.md`](control-plane.md)
- How the KB is structured (atlas / map / journal, OKF) →
  [`data-plane.md`](data-plane.md)
- Connecting other agents and keeping them in sync →
  [`configurator.md`](configurator.md) and [`sync.md`](sync.md)
- Authoring the KB's own skills, subagents, hooks and MCP descriptors → the
  bundled `kb-create` skill's `references/artifacts.md`
- Keeping encrypted values the KB can resolve → the same skill's
  `references/secrets.md`
- Importing an existing wiki or docs folder → the bundled `kb-import` skill
