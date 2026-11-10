package contract

import (
	"path"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Scope selects which half of the matrix a lookup reads.
type Scope int

const (
	// ScopeGlobal is the historical destination set, under the user's home.
	ScopeGlobal Scope = iota
	// ScopeProject is the per-workspace destination set, under a bound
	// workspace's own directory.
	ScopeProject
)

// projectDestinationMatrix is destinationMatrix's project-local counterpart.
//
// Sources (D193 audit):
//
//   - Claude Code: .claude/skills/, .claude/agents/, .claude/settings.json,
//     .mcp.json, ./CLAUDE.md — https://code.claude.com/docs/en/skills,
//     /memory, /sub-agents, /hooks, /mcp.
//
//   - Codex: .agents/skills and .codex/{agents,hooks.json,config.toml} along
//     cwd→repository root — https://developers.openai.com/codex/skills,
//     /subagents, /hooks, /mcp. The project must be *trusted* or Codex ignores
//     the .codex/ layer entirely, which is why the projection reports itself
//     inactive there rather than claiming success (see CodexProjectTrusted).
//
//   - Kiro: .kiro/skills/ (workspace wins over global) —
//     https://kiro.dev/docs/skills/.
//
//   - OpenCode: .opencode/skills, .opencode/agent, .opencode/plugins, project
//     opencode.json and AGENTS.md — https://opencode.ai/docs/skills, /rules,
//     /agents, /plugins.
//
//   - Crush: .crush/skills, one of the project skill directories it scans by
//     default — https://github.com/charmbracelet/crush/tree/main/docs/config.
//     Its project `.crush.json` was probed on v0.98.0 (D363): its `mcp` and
//     `hooks` entries are used by the session, so the mcp and hook cells are
//     supported (hooks live in .crush/hooks/, registered in .crush.json). The
//     agent and instructions cells are not probed and still fail closed
//     (docs/harnesses.md, ## crush).
//
//   - Antigravity (D364, `agy` 1.3.2): the workspace `.agents/` layer — skills,
//     agents, hooks.json, mcp_config.json — plus AGENTS.md, all probed against a
//     negative control. Its rules directory is deliberately not used: a rule is
//     read only with `trigger: always_on` frontmatter, which is why the
//     instructions cell is the AGENTS.md block instead.
//
//   - Copilot CLI and VS Code's Copilot harness (D676, 1.0.94; VS Code 1.145.2): AGENTS.md ("Repository instructions") and
//     .agents/skills/ ("Project skills") are listed natively; in a trusted
//     workspace VS Code also loads .github/agents/*.agent.md and
//     .github/hooks/*.json (2026-11-09), the agent and hook cells. The mcp cell
//     stays unsupported: .mcp.json is not loaded with VS Code's default
//     settings and the CLI does not list it in an untrusted folder.
//
//   - Cline (bundle 4.1.26): every cell is unsupported. Cline declares no MCP
//     roots and starts the bridge with the extension host's cwd, so every
//     session is unbound and served the provider-default read-only tools; a
//     bound folder would receive skills and instructions that explain write
//     tools (D1018). The locations a later plan flips: skills .cline/skills or
//     .agents/skills, instructions AGENTS.md, agents .cline/agents/<n>.yaml,
//     hooks .clinerules/hooks. What flips it: Cline declaring roots, or a
//     per-workspace MCP settings file.
//
// A provider in workspace scope that cannot project a kind reports it as
// unsupported, never silently degraded to the global catalogue — degrading is
// exactly the exposure this plan exists to prevent.
var ProjectDestinationMatrix = map[string]map[configurator.Provider]Destination{
	"mcp": {
		configurator.ProviderClaudeCode: At(".mcp.json"),
		configurator.ProviderCodex:      At(".codex", "config.toml"),
		configurator.ProviderOpenCode:   At("opencode.json"),
		configurator.ProviderKiro:       At(".kiro", "settings", "mcp.json"),
		// antigravity: the global shape (`mcpServers`) in the workspace's
		// .agents/mcp_config.json; `agy mcp list` shows only the global servers
		// but the session has the project ones (probed on 1.3.2, D364).
		configurator.ProviderAntigravity: At(".agents", "mcp_config.json"),
		configurator.ProviderCrush:       At(".crush.json"),
		// copilot: `.mcp.json` / `.github/mcp.json` are documented but
		// `copilot mcp list` does not list them in an untrusted folder
		// (1.0.94, D676), and VS Code 1.145.2 did not load `.mcp.json` with
		// its default settings in a trusted workspace (2026-11-09): it reads
		// it only behind an experimental setting, so no cell is declared.
		configurator.ProviderCopilot: unsupportedDest,
		// pi: project `.pi/*` and `.agents/skills` load only after project trust (probe 3 on Pi 1.5.0).
		configurator.ProviderPi:    unsupportedDest,
		configurator.ProviderCline: unsupportedDest,
	},
	"instructions": {
		// The project root's own CLAUDE.md is the file Claude Code reads for a
		// project; .claude/CLAUDE.md is the alternative. The root file is
		// chosen because it is the one a repository already has, and the block
		// is marker-delimited so it coexists with the user's own text.
		// D293: creating this file hides a project's AGENTS.md from the
		// built-in agents-md@builtin plugin (on by default since 2.1.277),
		// so the managed block prepends an @AGENTS.md import when one exists.
		configurator.ProviderClaudeCode: At("CLAUDE.md"),
		configurator.ProviderCodex:      At("AGENTS.md"),
		configurator.ProviderOpenCode:   At("AGENTS.md"),
		configurator.ProviderKiro:       At(".kiro", "steering", "cartographer.md"),
		// antigravity: AGENTS.md, read by `agy` (D364). Not .agents/rules/: a
		// rule is read only with `trigger: always_on` in its frontmatter, so a
		// plain rule file would be written and silently ignored.
		configurator.ProviderAntigravity: At("AGENTS.md"),
		configurator.ProviderCrush:       unsupportedDest,
		// copilot: AGENTS.md is read natively as "Repository instructions"
		// (probed on 1.0.94, D676).
		configurator.ProviderCopilot: At("AGENTS.md"),
		// pi: AGENTS.md is read without trust, from every ancestor directory
		// (probe 2 on Pi 1.5.0). CLAUDE.local.md is not read.
		configurator.ProviderPi:    At("AGENTS.md"),
		configurator.ProviderCline: unsupportedDest,
	},
	"agent": {
		configurator.ProviderClaudeCode: PerName(".md", ".claude", "agents"),
		configurator.ProviderCodex:      PerName(".toml", ".codex", "agents"),
		configurator.ProviderOpenCode:   PerName(".md", ".opencode", "agent"),
		// kiro: the same JSON agent config as the global cell, in the
		// workspace's own agent directory, which `kiro-cli agent list` reports
		// as "Workspace" and which wins over the global one (D195).
		configurator.ProviderKiro:        PerName(".json", ".kiro", "agents"),
		configurator.ProviderAntigravity: PerName(".md", ".agents", "agents"),
		configurator.ProviderCrush:       unsupportedDest,
		// copilot: .github/agents is gated on folder trust (`copilot --help`,
		// D676); in a trusted workspace VS Code 1.145.2 lists the agent and
		// answers as it (2026-11-09). The untrusted control was not run.
		configurator.ProviderCopilot: PerName(".agent.md", ".github", "agents"),
		// pi: no subagents.
		configurator.ProviderPi:    unsupportedDest,
		configurator.ProviderCline: unsupportedDest,
	},
	"hook": {
		configurator.ProviderClaudeCode: PerName("", ".claude", "hooks"),
		configurator.ProviderCodex:      PerName("", ".codex", "hooks"),
		configurator.ProviderOpenCode:   PerName("", ".opencode", "hooks"),
		// kiro: a workspace .kiro/hooks/ fires in exactly the sessions the
		// global ~/.kiro/hooks/ does, `kiro-cli chat --v3 --tui` (probed on
		// 2.26.1), so a project-local copy of the global registration (D300)
		// would reach no session the global one misses. Kept unsupported until
		// a mode fires one and not the other.
		configurator.ProviderKiro: unsupportedDest,
		// antigravity: the files live in .agents/hooks/<n>/ and the entry in
		// .agents/hooks.json (same schema as the global file); the registration
		// file is picked from the hook's path (antigravitySettingsRel).
		configurator.ProviderAntigravity: PerName("", ".agents", "hooks"),
		configurator.ProviderCrush:       PerName("", ".crush", "hooks"),
		// copilot: .github/hooks is gated on folder trust (`copilot --help`,
		// D676); in a trusted workspace VS Code 1.145.2 fires
		// .github/hooks/*.json for the three events (2026-11-09). Copilot parses
		// every *.json below that directory, so as in the global cell only the
		// registration .github/hooks/cartographer-<n>.json lives there and the
		// hook's files are here (registerCopilotHook).
		configurator.ProviderCopilot: PerName("", ".github", "cartographer-hooks"),
		// pi: project `.pi/*` and `.agents/skills` load only after project trust (probe 3 on Pi 1.5.0).
		configurator.ProviderPi:    unsupportedDest,
		configurator.ProviderCline: unsupportedDest,
	},
	"skill": {
		configurator.ProviderClaudeCode: PerName("", ".claude", "skills"),
		// The repository path Codex documents for skills. The global cell still
		// points at .codex/skills for the reason D192 records; this is the
		// target that only became reachable once a workspace scope existed.
		configurator.ProviderCodex:    PerName("", ".agents", "skills"),
		configurator.ProviderKiro:     PerName("", ".kiro", "skills"),
		configurator.ProviderOpenCode: PerName("", ".opencode", "skills"),
		// The .agents/skills directory `agy` scans in a workspace, shared with
		// Codex: both write the same bytes, and a prune keeps what another
		// projection of the workspace still records (ApplyOptions.CoOwnedPaths).
		configurator.ProviderAntigravity: PerName("", ".agents", "skills"),
		configurator.ProviderCrush:       PerName("", ".crush", "skills"),
		// copilot: ".agents/skills/" is listed as "Project skills", shared with
		// Codex and Antigravity (probed on 1.0.94, D676).
		configurator.ProviderCopilot: PerName("", ".agents", "skills"),
		// pi: project `.pi/*` and `.agents/skills` load only after project trust (probe 3 on Pi 1.5.0).
		configurator.ProviderPi:    unsupportedDest,
		configurator.ProviderCline: unsupportedDest,
	},
}

// CellFor is the one read of a destination cell of a scope: the static matrix,
// then the cells a registered plugin provider declared (plugincells.go). A kind
// or provider neither holds is not found, which every caller treats as
// unsupported.
func CellFor(scope Scope, kind string, provider configurator.Provider) (Destination, bool) {
	if cell, ok := matrixFor(scope)[kind][provider]; ok {
		return cell, true
	}
	return pluginCell(scope, kind, provider)
}

// matrixFor returns the destination matrix for a scope.
func matrixFor(scope Scope) map[string]map[configurator.Provider]Destination {
	if scope == ScopeProject {
		return ProjectDestinationMatrix
	}
	return DestinationMatrix
}

// DestDirScoped is DestDir with the scope axis. DestDir is the ScopeGlobal
// case and keeps its signature, because every caller that has no workspace to
// speak of is asking exactly that question.
func DestDirScoped(kind, name string, provider configurator.Provider, scope Scope) string {
	cell, ok := CellFor(scope, kind, provider)
	if !ok || cell.Unsupported {
		return ""
	}
	// A destination is a slash path, not a host path: it is written into
	// provider configuration and into .git/info/exclude, both of which are
	// read on every platform and both of which want forward slashes.
	if !cell.Named {
		return path.Join(cell.Dir...)
	}
	segments := append(append([]string{}, cell.Dir...), name+cell.suffix)
	return path.Join(segments...)
}
