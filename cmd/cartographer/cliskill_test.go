package main

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

func TestWithoutCLISkill(t *testing.T) {
	m := contract.Manifest{Artifacts: []contract.Artifact{
		{Kind: "skill", Name: "cartographer-ops", Source: "bundle"},
		{Kind: "skill", Name: cliSkillName, Source: "bundle"},
		{Kind: "skill", Name: cliSkillName, Source: "kb:kb-a"}, // a KB's own skill of that name is not ours
	}}
	names := func(m contract.Manifest) []string {
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
	usesCLI = func(p configurator.Provider) bool { return p == "cli-provider" }
	t.Cleanup(func() { usesCLI = old })
	if got := names(withoutCLISkill("cli-provider", m)); len(got) != 3 {
		t.Errorf("a CLI provider keeps the skill, got %v", got)
	}
}

// The skill is in the bundle manifest every client receives, and the predicate
// drops it for every built-in provider: the whole point of shipping it unset.
func TestCLISkillIsBundledAndFilteredForBuiltIns(t *testing.T) {
	m, err := contract.BuildManifest(skillbundle.FS, nil, contract.BuildOptions{IncludeCLISkill: true})
	if err != nil {
		t.Fatal(err)
	}
	has := func(m contract.Manifest) bool {
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

// A provider that reaches Cartographer through the command gets no MCP entry on
// connect or sync, nothing to remove on disconnect, and no warning; the MCP
// provider next to it is unaffected. The predicate is injected: no built-in is one.
func TestCLIProviderHasNoMCPEntry(t *testing.T) {
	old := usesCLI
	usesCLI = func(p configurator.Provider) bool { return p == "codex" }
	t.Cleanup(func() { usesCLI = old })
	cfg := bridgeConfigFor(t, "claude", "codex")
	entries, err := entriesByProviderForKBs(cfg, []string{"claude", "codex"}, "cartographer", cfg.ServerURL, cfg.KnownKBs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries["codex"]) != 0 || len(entries["claude"]) == 0 {
		t.Fatalf("entries = %v", entries)
	}
	dir := t.TempDir()
	written, warnings, err := applyMCPEntries(map[string][]mcpEntry{"codex": {{Name: "cartographer", URL: cfg.ServerURL}}}, []string{"codex"}, dir, nil, false, "", false)
	if err != nil || len(written) != 0 || len(warnings) != 0 {
		t.Fatalf("applyMCPEntries = %v, %v, %v", written, warnings, err)
	}
	names, removed, err := removeMCPEntries("cartographer", cfg.KnownKBs, []string{"codex"}, dir, nil, false, "", false, nil)
	if err != nil || len(names) != 0 || len(removed) != 0 {
		t.Fatalf("removeMCPEntries = %v, %v, %v", names, removed, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("files were created: %v", ents)
	}
}

// A sync verifies the manifest the server built with the CLI skill, then the
// per-provider filter decides: no warning, none installed for a non-cli
// provider, installed for a cli one (#1774).
func TestCLISkillSurvivesVerificationAndIsFilteredPerProvider(t *testing.T) {
	m, err := contract.BuildManifest(skillbundle.FS, nil, contract.BuildOptions{IncludeCLISkill: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := provisioning.VerifiedManifest(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Skipped) != 0 {
		t.Fatalf("warnings: %v", v.Skipped)
	}
	has := func(m contract.Manifest) bool {
		for _, a := range m.Artifacts {
			if a.Kind == "skill" && a.Name == cliSkillName {
				return true
			}
		}
		return false
	}
	if has(withoutCLISkill("claude", v)) {
		t.Error("a non-cli provider must not receive the skill")
	}
	old := usesCLI
	usesCLI = func(p configurator.Provider) bool { return p == "cli-provider" }
	t.Cleanup(func() { usesCLI = old })
	if !has(withoutCLISkill("cli-provider", v)) {
		t.Error("a cli provider must receive the skill")
	}
}
