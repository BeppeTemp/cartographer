package provisioning

// Consistency tests for the declarative kind × provider matrix (D137): a
// forgotten cell must be a red test here, never a silent Unsupported at apply
// time.

import (
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestDestinationMatrixIsComplete(t *testing.T) {
	for _, kind := range artifactKinds {
		cells, ok := destinationMatrix[kind]
		if !ok {
			t.Errorf("kind %q has no row in destinationMatrix", kind)
			continue
		}
		for _, d := range configurator.Providers() {
			cell, ok := cells[d.Provider]
			if !ok {
				t.Errorf("destinationMatrix[%q][%q] is missing: name a destination or mark it unsupported", kind, d.Provider)
				continue
			}
			if cell.unsupported {
				continue
			}
			if len(cell.dir) == 0 {
				t.Errorf("destinationMatrix[%q][%q] is supported but names no path", kind, d.Provider)
			}
		}
		for provider := range cells {
			if _, known := configurator.Lookup(provider); !known {
				t.Errorf("destinationMatrix[%q] has a cell for unknown provider %q", kind, provider)
			}
		}
	}
	for kind := range destinationMatrix {
		found := false
		for _, known := range artifactKinds {
			if kind == known {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("destinationMatrix has row %q, absent from artifactKinds", kind)
		}
	}
}

// The matrix must reproduce, cell by cell, the paths in use before it existed.
func TestDestDirPaths(t *testing.T) {
	cases := []struct {
		kind     string
		provider configurator.Provider
		want     string
	}{
		{"mcp", configurator.ProviderClaudeCode, ".claude.json"},
		{"mcp", configurator.ProviderCodex, ".codex/config.toml"},
		{"mcp", configurator.ProviderOpenCode, "opencode.json"},
		{"mcp", configurator.ProviderKiro, ".kiro/settings/mcp.json"},
		{"instructions", configurator.ProviderClaudeCode, ".claude/CLAUDE.md"},
		{"instructions", configurator.ProviderOpenCode, ".config/opencode/AGENTS.md"},
		{"instructions", configurator.ProviderCodex, ".codex/AGENTS.md"},
		{"instructions", configurator.ProviderKiro, ".kiro/steering/cartographer.md"},
		{"agent", configurator.ProviderClaudeCode, ".claude/agents/demo.md"},
		{"agent", configurator.ProviderOpenCode, ".opencode/agent/demo.md"},
		{"agent", configurator.ProviderCodex, ".codex/agents/demo.toml"},
		{"agent", configurator.ProviderKiro, ".kiro/agents/demo.json"},
		{"hook", configurator.ProviderClaudeCode, ".claude/hooks/demo"},
		{"hook", configurator.ProviderCodex, ".codex/hooks/demo"},
		{"hook", configurator.ProviderOpenCode, ".opencode/hooks/demo"},
		{"hook", configurator.ProviderKiro, ""},
		{"skill", configurator.ProviderClaudeCode, ".claude/skills/demo"},
		{"skill", configurator.ProviderCodex, ".codex/skills/demo"},
		{"skill", configurator.ProviderKiro, ".kiro/skills/demo"},
		{"skill", configurator.ProviderOpenCode, ".opencode/skills/demo"},
		// hermes delivers skills to its inbox and supports nothing else (D141).
		{"skill", configurator.ProviderHermes, filepath.Join("skill-inbox", "demo", "cartographer")},
		{"agent", configurator.ProviderHermes, ""},
		{"hook", configurator.ProviderHermes, ""},
		{"mcp", configurator.ProviderHermes, ""},
		{"instructions", configurator.ProviderHermes, ""},
		// Antigravity supports every artifact kind; SessionStart bootstrap remains timer-based.
		{"mcp", configurator.ProviderAntigravity, ".gemini/config/mcp_config.json"},
		{"instructions", configurator.ProviderAntigravity, ".gemini/GEMINI.md"},
		{"skill", configurator.ProviderAntigravity, ".gemini/config/skills/demo"},
		{"agent", configurator.ProviderAntigravity, ".gemini/config/agents/demo.md"},
		{"hook", configurator.ProviderAntigravity, ".gemini/config/hooks/demo"},
		// A kind or provider this binary does not know is not materializable:
		// a manifest from a newer server must not land somewhere arbitrary.
		{"newkind", configurator.ProviderClaudeCode, ""},
		{"skill", configurator.Provider("nope"), ""},
	}
	for _, tc := range cases {
		if got := destDir(tc.kind, "demo", tc.provider); got != tc.want {
			t.Errorf("destDir(%q, demo, %q) = %q, want %q", tc.kind, tc.provider, got, tc.want)
		}
	}
}

// The precedence chain's last entry is the file Cartographer manages, by
// construction: the chain lists what takes precedence over it, then it. Drift
// between the two declarations would make the shadowing check compare a file
// against itself, or against one nobody writes (D189).
func TestInstructionsPrecedenceEndsAtTheManagedFile(t *testing.T) {
	for _, d := range configurator.Providers() {
		if len(d.InstructionsPrecedence) == 0 {
			continue
		}
		last := filepath.Join(d.InstructionsPrecedence[len(d.InstructionsPrecedence)-1]...)
		if got := InstructionsFile(d.Provider); got != last {
			t.Errorf("%s: precedence chain ends at %q but InstructionsFile says %q", d.Provider, last, got)
		}
	}
}
