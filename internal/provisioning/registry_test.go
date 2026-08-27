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
		{"mcp", configurator.ProviderCodex, filepath.Join(".codex", "config.toml")},
		{"mcp", configurator.ProviderOpenCode, "opencode.json"},
		{"mcp", configurator.ProviderKiro, filepath.Join(".kiro", "settings", "mcp.json")},
		{"instructions", configurator.ProviderClaudeCode, filepath.Join(".claude", "CLAUDE.md")},
		{"instructions", configurator.ProviderOpenCode, filepath.Join(".config", "opencode", "AGENTS.md")},
		{"instructions", configurator.ProviderCodex, filepath.Join(".codex", "AGENTS.md")},
		{"instructions", configurator.ProviderKiro, filepath.Join(".kiro", "steering", "cartographer.md")},
		{"agent", configurator.ProviderClaudeCode, filepath.Join(".claude", "agents", "demo.md")},
		{"agent", configurator.ProviderOpenCode, filepath.Join(".opencode", "agent", "demo.md")},
		{"agent", configurator.ProviderCodex, filepath.Join(".codex", "agents", "demo.toml")},
		{"agent", configurator.ProviderKiro, ""},
		{"hook", configurator.ProviderClaudeCode, filepath.Join(".claude", "hooks", "demo")},
		{"hook", configurator.ProviderCodex, filepath.Join(".codex", "hooks", "demo")},
		{"hook", configurator.ProviderOpenCode, filepath.Join(".opencode", "hooks", "demo")},
		{"hook", configurator.ProviderKiro, ""},
		{"skill", configurator.ProviderClaudeCode, filepath.Join(".claude", "skills", "demo")},
		{"skill", configurator.ProviderCodex, filepath.Join(".codex", "skills", "demo")},
		{"skill", configurator.ProviderKiro, filepath.Join(".kiro", "skills", "demo")},
		{"skill", configurator.ProviderOpenCode, filepath.Join(".opencode", "skills", "demo")},
		// hermes delivers skills to its inbox and supports nothing else (D141).
		{"skill", configurator.ProviderHermes, filepath.Join("skill-inbox", "demo", "cartographer")},
		{"agent", configurator.ProviderHermes, ""},
		{"hook", configurator.ProviderHermes, ""},
		{"mcp", configurator.ProviderHermes, ""},
		{"instructions", configurator.ProviderHermes, ""},
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
