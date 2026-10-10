package provisioning_test

// GitHub Copilot CLI hooks, agents and bootstrap (D676, probed on 1.0.94).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func applyCopilotHook(t *testing.T, event, matcher string) (provisioning.AppliedResult, string) {
	t.Helper()
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", event, matcher, "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderCopilot, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, baseDir
}

func TestApplyAndPrune_CopilotHookIsADedicatedFile(t *testing.T) {
	res, baseDir := applyCopilotHook(t, "PreToolUse", "")

	// settings.json is the user's: it is never created or patched.
	if _, err := os.Stat(filepath.Join(baseDir, ".copilot", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json was touched: %v", err)
	}
	reg := filepath.Join(baseDir, ".copilot", "hooks", "cartographer-guard.json")
	root := readJSONFile(t, reg)
	if root["version"] != float64(1) {
		t.Errorf("version = %v", root["version"])
	}
	list := root["hooks"].(map[string]any)["preToolUse"].([]any)
	if len(list) != 1 {
		t.Fatalf("hooks = %v", root["hooks"])
	}
	h := list[0].(map[string]any)
	want := filepath.Join(baseDir, ".copilot", "cartographer-hooks", "guard", "notify.sh")
	if h["type"] != "command" || h["bash"] != filepath.ToSlash(want) && h["bash"] != want || h["powershell"] != want {
		t.Errorf("entry = %v, want a command running %s", h, want)
	}

	managed := crushHookManaged(res, "guard")
	// Copilot parses every *.json below ~/.copilot/hooks/, subdirectories
	// included (probed on 1.0.94): only the registration may live there.
	for _, mf := range managed {
		if strings.HasPrefix(mf.Path, ".copilot/hooks/") && mf.Path != ".copilot/hooks/cartographer-guard.json" {
			t.Errorf("%s would be parsed by Copilot as a hook configuration", mf.Path)
		}
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".copilot", "cartographer-hooks", "guard", "hook.json")); err != nil {
		t.Errorf("hook files not under .copilot/cartographer-hooks: %v", err)
	}
	if err := filepath.WalkDir(filepath.Join(baseDir, ".copilot", "hooks"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), "cartographer-") {
			t.Errorf("stray file under .copilot/hooks: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var sawReg bool
	for _, mf := range managed {
		if mf.Path == ".copilot/hooks/cartographer-guard.json" {
			sawReg = true
		}
	}
	if !sawReg {
		t.Fatalf("registration file is not a managed file: %+v", managed)
	}
	if _, err := provisioning.PruneManaged(managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reg); !os.IsNotExist(err) {
		t.Errorf("registration file survived the prune: %v", err)
	}
}

func TestApply_CopilotHookWithMatcherIsNotRegistered(t *testing.T) {
	res, baseDir := applyCopilotHook(t, "PostToolUse", "concept_write")
	if _, err := os.Stat(filepath.Join(baseDir, ".copilot", "hooks", "cartographer-guard.json")); !os.IsNotExist(err) {
		t.Errorf("a hook with a matcher was registered on a client that has none: %v", err)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "no matcher") {
		t.Errorf("no warning: %v", res.Warnings)
	}
}

func TestApply_CopilotHookEventNotFired(t *testing.T) {
	res, baseDir := applyCopilotHook(t, "Stop", "")
	if _, err := os.Stat(filepath.Join(baseDir, ".copilot", "hooks", "cartographer-guard.json")); !os.IsNotExist(err) {
		t.Errorf("an event Copilot does not fire was registered: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("no warning for an unregistered event")
	}
}

func TestEnsureBootstrapHook_Copilot(t *testing.T) {
	if !provisioning.SupportsSessionHook(configurator.ProviderCopilot) {
		t.Fatal("copilot fires sessionStart")
	}
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderCopilot, provisioning.Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	reg := filepath.Join(baseDir, ".copilot", "hooks", "cartographer-"+provisioning.BootstrapHookName+".json")
	if _, ok := readJSONFile(t, reg)["hooks"].(map[string]any)["sessionStart"]; !ok {
		t.Errorf("no sessionStart registration in %s", reg)
	}
	if _, err := provisioning.PruneManaged(lock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reg); !os.IsNotExist(err) {
		t.Errorf("registration file survived: %v", err)
	}
}

// Copilot has no matcher, so the write-findings hook registers with none and
// reads the tool name in its command; the channel is `copilot`.
func TestEnsureWriteFindingsHook_Copilot(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderCopilot, provisioning.Lock{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(filepath.Join(baseDir, ".copilot", "cartographer-hooks", provisioning.WriteFindingsHookName, "hook.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spec), "--channel copilot") || strings.Contains(string(spec), "matcher") {
		t.Errorf("hook.json = %s, want the copilot channel and no matcher", spec)
	}
	reg := filepath.Join(baseDir, ".copilot", "hooks", "cartographer-"+provisioning.WriteFindingsHookName+".json")
	list := readJSONFile(t, reg)["hooks"].(map[string]any)["postToolUse"].([]any)
	if cmd, _ := list[0].(map[string]any)["bash"].(string); !strings.HasSuffix(cmd, "--channel copilot") {
		t.Errorf("bash = %q, want the shim with --channel copilot", cmd)
	}
	for i := 0; i < 2; i++ {
		if lock, err = provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderCopilot, lock, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(reg); !os.IsNotExist(err) || len(lock.Managed) != 0 {
		t.Errorf("opt-out left %v / %+v", err, lock.Managed)
	}
}

func TestApply_Copilot_MaterializzaAgent(t *testing.T) {
	baseDir := t.TempDir()
	src := "---\nname: reviewer\ndescription: \"Reviews: code safely\"\ntools: Read, Grep\nmodel: sonnet\n---\nReviewer system prompt.\n"
	a := provisioning.Artifact{
		Kind: "agent", Name: "reviewer", Source: "kb:x", ContentHash: "h1", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "reviewer.md", Content: []byte(src)}},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, Provider: configurator.ProviderCopilot, BaseDir: baseDir, Lock: provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(baseDir, ".copilot", "agents", "reviewer.agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	fmRaw, body, has := okf.SplitFrontmatter(string(data))
	if !has {
		t.Fatalf("output has no frontmatter: %s", data)
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]interface{}{"name": "reviewer", "description": "Reviews: code safely"} {
		if got, _ := fm.Get(key); got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
	for _, unwanted := range []string{"tools:", "model:"} {
		if strings.Contains(fmRaw, unwanted) {
			t.Errorf("translated frontmatter contains %q: %s", unwanted, fmRaw)
		}
	}
	assertStampedOnce(t, body, "Reviewer system prompt.\n")
}
