package main

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestWithoutCLISkill(t *testing.T) {
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{
		{Kind: "skill", Name: "cartographer-ops", Source: "bundle"},
		{Kind: "skill", Name: cliSkillName, Source: "bundle"},
		{Kind: "skill", Name: cliSkillName, Source: "kb:kb-a"}, // a KB's own skill of that name is not ours
	}}
	names := func(m provisioning.Manifest) []string {
		var out []string
		for _, a := range m.Artifacts {
			out = append(out, a.Source+"/"+a.Name)
		}
		return out
	}
	for _, p := range configurator.ProviderList() {
		if got := names(withoutCLISkill(string(p), m)); len(got) != 2 || got[1] != "kb:kb-a/"+cliSkillName {
			t.Errorf("%s: %v, want the bundle copy dropped", p, got)
		}
	}
	old := usesCLI
	usesCLI = func(p configurator.Provider) bool { return p == "cli-variant" }
	t.Cleanup(func() { usesCLI = old })
	if got := names(withoutCLISkill("cli-variant", m)); len(got) != 3 {
		t.Errorf("a CLI provider keeps the skill, got %v", got)
	}
}
