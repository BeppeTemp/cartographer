package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
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

// The skill is in the bundle manifest every client receives, and the predicate
// drops it for every built-in provider: the whole point of shipping it unset.
func TestCLISkillIsBundledAndFilteredForBuiltIns(t *testing.T) {
	m, err := provisioning.BuildManifest(skillbundle.FS, nil, provisioning.BuildOptions{IncludeCLISkill: true})
	if err != nil {
		t.Fatal(err)
	}
	has := func(m provisioning.Manifest) bool {
		for _, a := range m.Artifacts {
			if a.Kind == "skill" && a.Name == cliSkillName && a.Source == "bundle" {
				return true
			}
		}
		return false
	}
	if !has(m) {
		t.Fatalf("%s is not in the bundle manifest", cliSkillName)
	}
	for _, p := range configurator.ProviderList() {
		if has(withoutCLISkill(string(p), m)) {
			t.Errorf("%s: the built-in provider would receive %s", p, cliSkillName)
		}
	}
}

// The skill's text teaches flags the command has and commands main dispatches.
func TestCLISkillTextMatchesTheCommand(t *testing.T) {
	data, err := fs.ReadFile(skillbundle.FS, "bundled/"+cliSkillName+"/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, flag := range []string{"--list", "--describe", "--json", "--provider", "--output"} {
		if !strings.Contains(text, flag) {
			t.Errorf("skill does not mention %s", flag)
		}
	}
	dispatched := map[string]bool{}
	for _, n := range dispatchedCommands(t) {
		dispatched[n] = true
	}
	for _, m := range regexp.MustCompile("cartographer ([a-z][a-z-]*)").FindAllStringSubmatch(text, -1) {
		if !dispatched[m[1]] {
			t.Errorf("skill cites `cartographer %s`, which is not a command", m[1])
		}
	}
}
