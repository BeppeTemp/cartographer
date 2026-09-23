# Getting started

From zero to a working agentic wiki in a few minutes: one local server running
as a native service, one KB, your agents (Claude Code in this walkthrough —
OpenCode, Codex CLI, Kiro, Antigravity, Hermes and Crush work the same way).
Two commands: install the binary, then `cartographer setup`.

You need **git**, and an empty git repository you own to be the first KB's
remote — a KB is a git repository, and that remote is what makes it durable and
syncable (D134). `sops` is needed only if the KB will hold encrypted values, and
Go 1.26+ only for the from-source install. What each installation method puts on
the machine, and how to remove it, is in the README under §Install.

## 1. Install the client/server binary

```bash
# macOS (Homebrew)
brew install beppetemp/tap/cartographer

# Windows (PowerShell; per-user, no administrator rights)
irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex

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

### Windows

`install.ps1` is the Windows counterpart of `install.sh`, with the same contract:
the newest release, verified against `sha256sums.txt` (a missing or wrong
checksum is a stop), installed into `%LOCALAPPDATA%\Cartographer\bin` and added
to the **user** `PATH` — no administrator rights, nothing outside your profile
([D252](decisions/D252-windows-installs-through-install-ps1-not-winget.md)).
Piped through `iex` it runs in your own shell, so `cartographer` works on the
next line; other already-open windows see it only once reopened. It runs under
Windows PowerShell 5.1 and PowerShell 7 alike.

`update` and `uninstall` go through the same script:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) update
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1))) uninstall
```

- **An update needs no service stop.** Windows refuses to overwrite a running
  executable but allows renaming it, so the script moves the old
  `cartographer.exe` aside, puts the new one in its place and runs
  `cartographer upgrade-repair`, which restarts the native service on it.
- **`uninstall` refuses while the Scheduled Tasks are registered** and names the
  teardown to run first, with the binary still in place: `cartographer
  disconnect`, `cartographer service sync-timer uninstall`, `cartographer service
  uninstall`. `-BinaryOnly` deletes the binary anyway. Your KBs are git
  repositories in the data directory: nothing here deletes them.
- `CARTOGRAPHER_INSTALL_DIR` picks another directory; `GITHUB_TOKEN` avoids the
  API rate limit.

## 2. Set up the server, the first KB and your agents

```bash
cartographer setup
```

`setup` is a short interview followed by a plan you approve before anything
changes:

1. **Where the first KB lives.** Paste the git remote. An **empty** repository
   gets a new KB created and pushed to it; a repository that already holds a
   Cartographer KB is mounted instead — setup tells them apart by asking the
   remote itself (`git ls-remote`), which also proves this machine's
   credentials work *before* anything is installed. Leave it blank for a
   local-only trial KB (not backed up, never synced), which it asks you to
   confirm (D134).
2. **Which agents.** Every agent client detected on the machine is proposed;
   narrow the list if you want.
3. **The plan**, one line per step, each saying whether it will run or is
   already done. Confirm, and setup runs it: the native service (launchd on
   macOS, a systemd user unit on Linux, a per-user Scheduled Task on Windows —
   none needs administrator rights) listening on `127.0.0.1:39273`, the KB, the
   agent configuration, then a health check. It ends on the Atlas URL and on
   the one thing left to you: restarting your agent sessions.

A step that fails stops setup with its cause; fix it and run `cartographer
setup` again — finished steps are skipped. For a scripted or agent-driven
install, every answer is a flag:

```bash
cartographer setup --remote git@github.com:you/wiki.git --agents claude --yes
cartographer setup --no-remote --name trial --agents claude --yes
cartographer setup --remote <url> --dry-run      # print the plan, change nothing
```

> On Linux the unit is a **user** unit, so it stops when you log out and does not
> come back on boot unless lingering is enabled for your account:
> `loginctl enable-linger <user>` — setup prints this when it applies.

### Step by step, instead

`setup` only chains existing commands, and each one stays available:

```bash
cartographer service install                               # the native service
cartographer kb create my-kb --remote <git-remote-url> --restart   # a KB in an empty repository
cartographer kb clone <git-remote-url> --restart           # …or mount one that already exists
cartographer connect claude                                # one agent client
```

`kb create` scaffolds the KB in the service's data dir: a git repository with
`data/index.md` and `data/log.md`, plus a local search index under
`.cartographer/` (never committed), pushed to `--remote`, which becomes its
`origin` (`--no-remote` for a throwaway local trial). `--restart` makes the
running server pick it up. `connect` registers the `cartographer` MCP server in
the client's configuration (`~/.claude.json` for Claude Code), materializes the
bundled skills and writes a managed instructions block so the agent knows the
wiki exists; `cartographer connect` with no arguments opens an interactive form,
`cartographer status` checks for drift, `cartographer sync` re-aligns. Every
write from now on will be one git commit — the KB is a plain folder of Markdown
you can open in any editor or in Obsidian.

> **Running it by hand instead.** For development you can skip the service and
> run a one-off server on a KB of your choice:
> `cartographer serve --kb ~/my-kb --init --http :39273` (`--init` scaffolds
> it). The service path above is the one to use daily.

## 3. First session

**Restart Claude Code first.** The MCP tools and the provisioned skills are
loaded at session start, so a session that was already open when you ran
`setup` sees none of them — the same closing step the agent runbook makes
explicit. In the new session, ask something that produces knowledge worth
keeping, for example:

> Explore this repository and write a concept page about its architecture
> in the wiki. Close the session with a log entry.

Behind the scenes the agent will call the MCP tools: `atlas_overview` to
orient itself, `map_create` / `concept_write` to create the page,
`log_append` to journal the session. Next session, ask it something related —
it will `search` and `concept_read` its way back to what it wrote, and build
on it. That accumulation is the whole point.

## 4. Look at what happened

```bash
cd ~/cartographer-data/my-kb   # the service's data dir
git log --oneline              # one commit per write operation, revertible
ls data/                       # plain Markdown with YAML frontmatter
```

Or open the Atlas in a browser — `cartographer service status` prints its
address on the `ui:` line (by default `http://127.0.0.1:39273/ui/`): the graph
of the KB, each concept with its links, and the lint findings, read-only.

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
