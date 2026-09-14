---
topic: client-configurator
---

# D23 — Multi-provider configurator: CLI flags + per-provider JSON adapters, non-destructive merge

**Decision.** The configurator accepts CLI flags (`--name`, `--transport`, `--url`, `--auth`, `--token-env`) with sensible defaults (`DefaultConfig()`) and generates the MCP configuration files for Claude Code (`.claude.json`, `mcpServers` key), Codex CLI (`.codex/config.json`), Kiro (`.kiro/settings/mcp.json`), and OpenCode (`.opencode/config.json`). Claude Code reads `mcpServers` from `~/.claude.json`, not from a separate `.claude/mcp_servers.json` file. Merging with existing files is non-destructive: entries for other servers are preserved. Implemented as the `internal/configurator` package + the `cmd/configure` binary.
**Rationale.** CLI flags are the simplest and most composable configuration point — no YAML file to maintain in the KB, no extra parsers. The non-destructive merge is essential to avoid overwriting the user's existing configurations. The JSON format uses stdlib `encoding/json`. The `mcp/wiki.yaml` (previously the source of truth) was removed together with the `mcp/` and `raw/` directories from the KB structure (June 2026, D28). *(Superseded by D37: `cmd/configure` was deleted — the client subcommands now live in `cmd/cartographer`, HTTP-only transport.)* *(Bug discovered in D58: `.codex/config.json` was never the file Codex CLI reads — Codex reads only `config.toml`. `emitCodex` now generates managed-block TOML; see D58.)*
