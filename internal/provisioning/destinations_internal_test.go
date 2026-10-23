package provisioning

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Destinations is what the Atlas UI shows as an artifact's clients (D238): it
// must name exactly the supported cells of the matrix sync writes from, in
// registry order.
func TestDestinationsFollowTheMatrix(t *testing.T) {
	for kind, row := range destinationMatrix {
		var want []configurator.Provider
		for _, d := range configurator.Providers() {
			if cell, ok := row[d.Provider]; ok && !cell.unsupported {
				want = append(want, d.Provider)
			}
		}
		if got := Destinations(kind, "x"); !slices.Equal(got, want) {
			t.Errorf("Destinations(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestDestinationsSpotChecks(t *testing.T) {
	agent := Destinations("agent", "reviewer")
	if !slices.Contains(agent, configurator.ProviderClaudeCode) || slices.Contains(agent, configurator.ProviderCrush) {
		t.Errorf("agent destinations = %v, want Claude Code and not Crush", agent)
	}
	if hook := Destinations("hook", "guard"); !slices.Contains(hook, configurator.ProviderKiro) || !slices.Contains(hook, configurator.ProviderClaudeCode) {
		t.Errorf("hook destinations = %v, want Kiro (D300) and Claude Code", hook)
	}
	for _, kind := range []string{"template", "unknown", ""} {
		if got := Destinations(kind, "x"); len(got) != 0 {
			t.Errorf("Destinations(%q) = %v, want none", kind, got)
		}
	}
}

// Every directory a destination cell lives under is a boundary pruneEmptyDirs
// never removes (D63): its first segment, or the first two under .config, where
// one directory per client sits (#1138: .copilot was missing, so an emptied
// $HOME/.copilot could be pruned). A cell that is a file at the base dir itself
// (.claude.json, AGENTS.md) has no root to keep.
func TestEveryDestinationRootIsAPruneBoundary(t *testing.T) {
	for scope, matrix := range map[string]map[string]map[configurator.Provider]destination{
		"global": destinationMatrix, "project": projectDestinationMatrix,
	} {
		for kind, row := range matrix {
			for provider, cell := range row {
				if cell.unsupported || (len(cell.dir) == 1 && !cell.named) {
					continue
				}
				root := cell.dir[0]
				if root == ".config" && len(cell.dir) > 1 {
					root = filepath.Join(cell.dir[:2]...)
				}
				if !provisioningRootDirs[root] {
					t.Errorf("%s %s/%s lives under %q, which is not in provisioningRootDirs", scope, kind, provider, root)
				}
			}
		}
	}
}
