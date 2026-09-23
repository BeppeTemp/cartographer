package provisioning

import (
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
	if !slices.Contains(agent, configurator.ProviderClaudeCode) || slices.Contains(agent, configurator.ProviderHermes) {
		t.Errorf("agent destinations = %v, want Claude Code and not Hermes", agent)
	}
	if hook := Destinations("hook", "guard"); slices.Contains(hook, configurator.ProviderKiro) {
		t.Errorf("hook destinations = %v, Kiro has no hook destination", hook)
	}
	for _, kind := range []string{"template", "unknown", ""} {
		if got := Destinations(kind, "x"); len(got) != 0 {
			t.Errorf("Destinations(%q) = %v, want none", kind, got)
		}
	}
}
