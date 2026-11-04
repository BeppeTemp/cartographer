package provisioning_test

// Cline (bundle 4.1.26) as a sync target: global skills, instructions and agents
// land under $HOME/.cline, a prune removes only what the lock owns and keeps the
// directory Cline itself fills (data/, the user's own rules), and the hook cell
// is unsupported until the dispatcher slice.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func clineAgent(name, content string) provisioning.Artifact {
	return provisioning.Artifact{Kind: "agent", Name: name, Source: "bundle", BuiltIn: true, ContentHash: "a-" + name,
		Files: []provisioning.ArtifactFile{{Path: name + ".md", Content: []byte(content)}}}
}

func TestApply_ClineGlobalCells(t *testing.T) {
	home := t.TempDir()
	old := provisioning.ClineDispatcherCommand
	provisioning.ClineDispatcherCommand = func() (string, error) { return filepath.Join(home, "bin", "cartographer"), nil }
	t.Cleanup(func() { provisioning.ClineDispatcherCommand = old })
	cl := filepath.Join(home, ".cline")
	// What Cline and the user keep next to what Cartographer writes.
	userRule := filepath.Join(cl, "rules", "mine.md")
	settings := filepath.Join(cl, "data", "settings", "cline_mcp_settings.json")
	for path, body := range map[string]string{userRule: "# mine\n", settings: `{"mcpServers":{}}`} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	hook := provisioning.Artifact{Kind: "hook", Name: "guard", Source: "bundle", BuiltIn: true, ContentHash: "h",
		Files: []provisioning.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"PreToolUse","command":"./x.sh"}`)}}}
	m := provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h1", map[string]string{"SKILL.md": "# alpha\n"}),
		instructionsArtifact("kb-a", "KB content.\n"),
		clineAgent("helper", "---\nname: helper\ndescription: Does things\ntools: Read, Bash\nmodel: opus\n---\nYou help.\n"),
		hook,
	}}
	opts := provisioning.ApplyOptions{Provider: configurator.ProviderCline, BaseDir: home, SkipLockWrite: true}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unsupported) != 0 {
		t.Errorf("Unsupported = %+v, want none", res.Unsupported)
	}
	if _, err := os.Stat(filepath.Join(home, "Documents", "Cline", "Hooks", "PreToolUse")); err != nil {
		t.Errorf("dispatcher not wired: %v (warnings %v)", err, res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(cl, "cartographer-hooks", "guard", "hook.json")); err != nil {
		t.Errorf("hook files not at .cline/cartographer-hooks/guard: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cl, "skills", "alpha", "SKILL.md")); err != nil {
		t.Errorf("skill not at .cline/skills/alpha: %v", err)
	}
	rules, _ := os.ReadFile(filepath.Join(cl, "rules", "cartographer.md"))
	if !strings.Contains(string(rules), "KB content.") {
		t.Errorf("cartographer.md = %q", rules)
	}
	agent, _ := os.ReadFile(filepath.Join(cl, "agents", "helper.yaml"))
	// tools and model are Claude's names: dropped. The provenance block follows the body.
	want := "---\nname: helper\ndescription: Does things\n---\nYou help.\n"
	if !strings.HasPrefix(string(agent), want) {
		t.Errorf("agent = %q, want prefix %q", agent, want)
	}

	opts.Lock = res.NewLock
	if _, err := provisioning.Apply(provisioning.Manifest{Revision: "r2"}, opts); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"rules/cartographer.md", "skills/alpha", "agents/helper.yaml", "skills", "agents"} {
		if _, err := os.Stat(filepath.Join(cl, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Errorf("%s survived the prune: %v", gone, err)
		}
	}
	// The trap: .cline holds Cline's own data and the user's rules.
	for _, kept := range []string{userRule, settings} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed by the prune: %v", kept, err)
		}
	}
}

func TestApply_ClineInstructionsFileIsRemovedWhole(t *testing.T) {
	home := t.TempDir()
	opts := provisioning.ApplyOptions{Provider: configurator.ProviderCline, BaseDir: home, SkipLockWrite: true}
	res, err := provisioning.Apply(provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{instructionsArtifact("kb-a", "KB content.\n")}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Lock = res.NewLock
	if _, err := provisioning.Apply(provisioning.Manifest{Revision: "r2"}, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cline", "rules", "cartographer.md")); !os.IsNotExist(err) {
		t.Errorf("an instructions file holding only the block must be deleted: %v", err)
	}
}

func TestApply_ClineAgentNativeFields(t *testing.T) {
	src := "---\nname: helper\ndescription: Does things\nproviders:\n  cline:\n    maxIterations: 7\n    modelId: some-model\n---\nBody.\n"
	run := func(content string) (string, error) {
		home := t.TempDir()
		_, err := provisioning.Apply(provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{clineAgent("helper", content)}},
			provisioning.ApplyOptions{Provider: configurator.ProviderCline, BaseDir: home, SkipLockWrite: true})
		b, _ := os.ReadFile(filepath.Join(home, ".cline", "agents", "helper.yaml"))
		return string(b), err
	}
	got, err := run(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "maxIterations: 7") || !strings.Contains(got, "modelId: some-model") {
		t.Errorf("native keys missing: %q", got)
	}
	// name and description are Cartographer's own: reserved.
	_, err = run("---\nname: helper\ndescription: d\nproviders:\n  cline:\n    name: other\n---\nBody.\n")
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("a reserved native key must be refused, got %v", err)
	}
}

// D148: a symlinked rules directory is never followed.
func TestApply_ClineSymlinkedRulesDirIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".cline", "rules")); err != nil {
		t.Fatal(err)
	}
	res, _ := provisioning.Apply(provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{instructionsArtifact("kb-a", "KB content.\n")}},
		provisioning.ApplyOptions{Provider: configurator.ProviderCline, BaseDir: home, SkipLockWrite: true})
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Errorf("wrote through a symlink: %v (result %+v)", entries, res)
	}
}

// Cline cannot be shown to expand ${VAR}: a KB mcp artifact's headers are
// warned about and not written, so a literal credential never reaches the
// settings file in clear text.
func TestApply_ClineKBMCPHeadersAreNotWritten(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp","headers":{"X-Api-Key":"${WIKI_KEY}"}}`)
	mcp := findMCPArtifact(t, signedMCPManifest(t, kbRoot, "wiki-tools"), "wiki-tools")
	mcp.Signed = true
	home := t.TempDir()
	res, err := provisioning.Apply(provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{mcp}},
		provisioning.ApplyOptions{Provider: configurator.ProviderCline, BaseDir: home, KBRoots: map[string]string{"kb": kbRoot}, SkipLockWrite: true,
			ApprovedMCP: map[string]string{"kb:kb\x00wiki-tools": mcp.ContentHash}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, ".cline", "data", "settings", "cline_mcp_settings.json"))
	if !strings.Contains(string(cfg), "wiki-tools") || strings.Contains(string(cfg), "X-Api-Key") || strings.Contains(string(cfg), "WIKI_KEY") {
		t.Errorf("settings = %s, want the server without its headers", cfg)
	}
	warned := false
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, "does not support headers")
	}
	if !warned {
		t.Errorf("Warnings = %v, want the headers warning", res.Warnings)
	}
}
