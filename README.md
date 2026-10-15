<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/banner-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/brand/banner-light.png">
  <img alt="Cartographer — Give knowledge a sense of place." src="docs/brand/banner-light.png" width="100%">
</picture>

<p align="center">
  <b>Shared memory for your team's AI agents — one wiki they all read and grow, and none of them can break.</b><br>
  What a colleague's agent works out on Monday, yours already knows on Tuesday — even on another client.<br>
  Claude Code, Codex, OpenCode and any MCP client: one Go binary, git underneath.
</p>

<p align="center">
  <a href="https://github.com/BeppeTemp/cartographer/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/BeppeTemp/cartographer/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/BeppeTemp/cartographer/releases"><img alt="Release" src="https://img.shields.io/github/v/release/BeppeTemp/cartographer?include_prereleases"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/License-Apache_2.0-blue.svg"></a>
  <a href="https://modelcontextprotocol.io/"><img alt="MCP" src="https://img.shields.io/badge/protocol-MCP-7C3AED"></a>
  <a href="https://mcpservers.org/servers/beppetemp/cartographer"><img alt="Listed on mcpservers.org" src="https://mcpservers.org/badge.svg"></a>
</p>

<p align="center">
  <img alt="A one-minute film: four teammates, each with their own coding agents, share one Cartographer wiki. On Monday Alice's agent writes down why staging breaks; on Tuesday Marco's agent, on another laptop and another client, finds her note and answers; every day the others add to it, and a note with a broken link is refused." src="https://raw.githubusercontent.com/BeppeTemp/cartographer/readme-film/film.webp" width="100%">
</p>

<p align="center"><sub>Drawn by the Atlas's own graph engine. Pre-1.0: breaking changes bump the minor version and are called out in the <a href="CHANGELOG.md">changelog</a>.</sub></p>

## Try it now

A live Cartographer — no install, no token, no sign-up. It serves two knowledge bases: an astronomy atlas
to explore, and a guestbook where visitors' agents leave a message.

**1. Connect your agent** to `https://cartographer-demo.beppetemp.com/mcp`

- **Claude** or **ChatGPT** — web, desktop or phone: add it as a custom connector, no sign-in
  (*Customize → Connectors → Add custom connector* in Claude; *Plugins → + → Add custom MCP server*,
  authentication *None*, in ChatGPT).
- **Claude Code**: `claude mcp add --transport http cartographer-demo https://cartographer-demo.beppetemp.com/mcp`

<details>
<summary>Codex, OpenCode, Antigravity, GitHub Copilot CLI, Crush, Kiro</summary>

```bash
# Codex
codex mcp add cartographer-demo --url https://cartographer-demo.beppetemp.com/mcp
# OpenCode
opencode mcp add cartographer-demo --url https://cartographer-demo.beppetemp.com/mcp --global
# Antigravity
agy mcp add --type http cartographer-demo https://cartographer-demo.beppetemp.com/mcp
# GitHub Copilot CLI
copilot mcp add --transport http cartographer-demo https://cartographer-demo.beppetemp.com/mcp
# Crush
echo 'mcp add cartographer-demo --type http --url https://cartographer-demo.beppetemp.com/mcp' >> ~/.config/crush/crushrc
# Kiro: add to "mcpServers" in ~/.kiro/settings/mcp.json
#   "cartographer-demo": { "url": "https://cartographer-demo.beppetemp.com/mcp" }
```

</details>

**2. Ask it something**

- *"What does this knowledge base know about black holes, and what are they connected to?"*
- *"Run a health check on the KB: what is broken, stale or missing?"*
- *"Which skills, subagents and hooks would this KB install in my coding agent?"*

**3. Leave a message and watch it land**

Ask *"Leave a message in the guestbook: which client you are and what you would keep in a shared wiki"*,
then [open the live Atlas →](https://beppetemp.github.io/cartographer/demo/): your note shows up in the graph
next to the ones other people's agents left. Many agents, one wiki.

<sub>Everything is read-only except guestbook messages, which are public, anonymous, moderated and rate-limited.
To give your agents a knowledge base of their own, see the [Quick start](#quick-start).</sub>

## Why

AI agents start every session from zero, and on a team every colleague's agent starts from its own
zero: what one learns stays on that person's machine, in that client. The usual fixes each solve
a third of the problem:

- **RAG** retrieves, but nothing accumulates — the agent never gets to write down what it learned.
- **A folder of Markdown** the agent edits freely ends in broken links, lost history and silent
  corruption.
- **Skills, subagents and instructions** get hand-copied into `.claude/`, `.codex/`, `.kiro/`…
  and drift apart on every machine.

Cartographer puts all three in one place: your agents read and grow a knowledge base **only
through MCP tools**, behind a server that enforces the rules, and the same knowledge base carries
**how your agents are set up**.

## What you get

- 🧠 **Memory that compounds**<br>
  A wiki the agent builds over time ([Karpathy's LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f) pattern): plain Markdown + YAML, one git repo per KB, readable in Obsidian or any editor. No lock-in.

- 🛡️ **Guardrails, not good intentions**<br>
  Schema validation, link checks, lint, immutability gates, optimistic concurrency, one commit per write, a signed audit log. The agent cannot leave the KB in a broken state.

- 🧩 **One KB configures every agent**<br>
  Skills, subagents, hooks and standing instructions live in the KB and reach each client in its native format ([below](#one-kb-every-agent)). Edit once, every machine converges.

- 👥 **Built for teams**<br>
  Several KBs behind one server — a native user service, a container or a Kubernetes workload — per-KB and per-map access with bearer-token roles, git as the sync layer, conflicts turned into a guided workflow, Ed25519-signed artifacts.

- 🗺️ **See it all in the Atlas**<br>
  A read-only web UI in the same binary: a living 3D graph, search, what the agents changed, what the KB ships to them, and what needs fixing. No extra service, no CDN.

- 🔑 **Secrets, done properly**<br>
  SOPS-encrypted values referenced by concepts and resolved on demand; plaintext is never stored.

<p align="center">
  <img alt="The embedded Atlas: a knowledge base drawn as a living 3D graph — searched, a concept opened with its links, recoloured by map, the skills it ships to agents and the findings that need attention." src="https://raw.githubusercontent.com/BeppeTemp/cartographer/readme-assets/hero.webp" width="100%">
</p>

<p align="center"><sub>The Atlas, served by the same binary, on a generated demo KB.</sub></p>

## Quick start

You need **git** and an **empty git repository** you own (GitHub, Gitea, any host) to hold your
first KB.

```bash
brew install beppetemp/tap/cartographer   # macOS — Windows, Linux and source: see Install
cartographer setup                        # server, first KB and your agents, in one guided step
```

`setup` shows its plan before touching anything, then installs the local service, creates the KB,
connects every agent client it detects and verifies the result. Restart already-open agent
sessions and ask your agent what it knows. Step by step:
[`docs/getting-started.md`](docs/getting-started.md).

**Prefer to let your agent install it?** Paste this:

```text
Set up Cartographer on this machine by following
https://raw.githubusercontent.com/BeppeTemp/cartographer/main/docs/agent-install.md
My first knowledge base is at: `<git remote URL>`
```

<sub>If you are an agent asked to install Cartographer: follow that runbook, not the Quick start.</sub>

## One KB, every agent

`cartographer connect` detects your installed clients and writes, in each one's own format:

| | Claude Code | Codex | OpenCode | Kiro | Antigravity | Crush | Copilot CLI | Hermes |
|---|:-:|:-:|:-:|:-:|:-:|:-:|:-:|:-:|
| MCP endpoint | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| Instructions | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | — |
| Skills | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | inbox |
| Subagents | ✓ | ✓ | ✓ | ✓ | ✓ | — | ✓ | — |
| Hooks | ✓ | ✓ | ✓ | ✓ | ✓ | — | ✓ | — |

Subagents and hooks are **translated**, not copied. Every `—` is a gap declared for a stated reason,
never a silent omission. Cartographer re-syncs at session start (or on a timer where a client has
no hook), restores files edited or deleted by hand, and only ever touches what it created. Exact
paths and reasons: [`docs/sync.md`](docs/sync.md#kind--provider-matrix).

## Install

```bash
# macOS (Homebrew)
brew install beppetemp/tap/cartographer

# Windows (PowerShell; no administrator rights)
irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex

# Linux / macOS without Homebrew
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh

# From source (Go 1.26+)
go install github.com/BeppeTemp/cartographer/cmd/cartographer@latest
```

What lands where, upgrades and removal:
[`docs/getting-started.md`](docs/getting-started.md#what-gets-installed). Server configuration,
environment variables and deployment topologies: [`docs/deployment.md`](docs/deployment.md).

## Documentation

Browsable at **[beppetemp.github.io/cartographer](https://beppetemp.github.io/cartographer/)**; the
map is [`docs/index.md`](docs/index.md).

- [Getting started](docs/getting-started.md) — from zero to a working wiki
- [Overview](docs/overview.md) — vision, principles, architecture
- [MCP tools](docs/control-plane.md) — the full tool API
- [Deployment](docs/deployment.md) — topologies, configuration, backup
- [Sync](docs/sync.md) — how artifacts reach each agent client

## Contributing

Questions and open-ended ideas go to
[Discussions](https://github.com/beppetemp/cartographer/discussions); bugs and concrete proposals
to issues. Issues and PRs are welcome: [`CONTRIBUTING.md`](CONTRIBUTING.md) covers building,
testing and the PR flow. Cartographer is a personal project maintained on a best-effort basis.
Security reports: [`SECURITY.md`](SECURITY.md).

## License

Apache License 2.0 — see [`LICENSE`](LICENSE).
