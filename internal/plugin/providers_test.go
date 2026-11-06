package plugin

import (
	"bytes"
	"strings"
	"testing"
)

// fakeMachine points the point's loader and preview at a fake Linux home.
func fakeMachine(t *testing.T) {
	t.Helper()
	old := providerEnv
	providerEnv = func() ProviderEnv { return fakeEnv("linux", nil) }
	t.Cleanup(func() { providerEnv = old })
}

func providerTree() []TreeFile {
	return []TreeFile{tf(ManifestFile, validManifest), tf("providers/fork-cli.yaml", forkCLI), tf("providers/fork-ext.yaml", forkExt)}
}

func TestProvidersPointLoadsDescriptorsAndInstallsNoArtifact(t *testing.T) {
	fakeMachine(t)
	p, probs := ReadTree(providerTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if got := p.Contributions["providers"]; len(got) != 2 || got[0].Name != "fork-cli" || got[1].Name != "fork-ext" || got[0].Provider == nil {
		t.Fatalf("contributions: %+v", got)
	}
	if len(p.Artifacts) != 0 {
		t.Errorf("a provider is registered, not installed as an artifact: %+v", p.Artifacts)
	}
	if rs := p.Providers(fakeEnv("linux", nil)); len(rs) != 2 || rs[0].Name != "fork-cli" {
		t.Errorf("Providers: %+v", rs)
	}
}

func TestProviderPreviewPrintsThePathsOfThisMachine(t *testing.T) {
	fakeMachine(t)
	p, probs := ReadTree(providerTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	var buf bytes.Buffer
	Preview(&buf, p, PreviewInfo{Source: "https://example.com/x.git", Have: func(string) (string, bool) { return "", false }}, testOpts)
	out := buf.String()
	for _, want := range []string{
		"providers/:", "provider fork-cli (Fork CLI), a fork of crush",
		"writes global skill: /home/user/.config/fork-cli/skills",
		"writes global instructions: /home/user/.config/fork-cli/FORK-CLI.md",
		"detected by directory: /home/user/.config/fork-cli",
		"mcp: cli",
		"provider fork-ext (Fork Ext), a fork of cline",
		"mcp file: /home/user/.vscode/extensions/acme.fork-ext-*/settings/mcp.json",
		"removes after a change (the client regenerates it): /home/user/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json",
		"hook root: /home/user/.fork-ext/cartographer-hooks",
		"capabilities claimed: agent.delegate=absent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	for _, r := range out {
		if r == 0x1b {
			t.Fatal("a raw escape reached the preview")
		}
	}
}

func TestValidateReportsAProviderTheRegistriesRefuse(t *testing.T) {
	fakeMachine(t)
	clash := strings.ReplaceAll(strings.ReplaceAll(forkCLI, "fork-cli", "fork-clash"), "{xdg_config}/fork-clash/skills", "{home}/.claude/skills")
	_, probs := ReadTree([]TreeFile{tf(ManifestFile, validManifest), tf("providers/fork-clash.yaml", clash)}, testOpts)
	if len(probs) == 0 || !strings.Contains(strings.Join(probs, "\n"), "providers/fork-clash.yaml") {
		t.Errorf("a provider whose root is a built-in's was accepted: %v", probs)
	}
	// A stem that differs from the name, a stray file and a nested folder.
	_, probs = ReadTree([]TreeFile{tf(ManifestFile, validManifest), tf("providers/other.yaml", forkCLI), tf("providers/notes.txt", "x"), tf("providers/sub/a.yaml", forkCLI)}, testOpts)
	if len(probs) < 3 {
		t.Errorf("problems: %v", probs)
	}
}
