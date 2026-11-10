package provisioning_test

// The Copilot workspace cells VS Code reads in a trusted workspace:
// .github/agents and .github/hooks (probed on VS Code 1.145.2, 2026-11-09).

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func writeCopilotProjectKB(t *testing.T, event, matcher string) (string, contract.Manifest) {
	t.Helper()
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", event, matcher, "./notify.sh")
	if err := os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kbRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\ndescription: Reviews\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := contract.BuildManifest(nil, map[string]string{"kb": kbRoot}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return kbRoot, m
}

func applyCopilotProject(t *testing.T, kbRoot string, m contract.Manifest, baseDir string, lock provisioning.Lock) provisioning.AppliedResult {
	t.Helper()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots: map[string]string{"kb": kbRoot}, ApprovedMCP: approveAll(m),
		Provider: configurator.ProviderCopilot, BaseDir: baseDir, Scope: contract.ScopeProject, Lock: lock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestApplyAndPrune_CopilotProjectScope(t *testing.T) {
	kbRoot, m := writeCopilotProjectKB(t, "PreToolUse", "")
	baseDir := t.TempDir()
	// Files that belong to the repository, beside the projected ones.
	mine := map[string]string{
		".github/hooks/mine.json":         `{"version":1,"hooks":{}}`,
		".github/agents/mine.agent.md":    "---\nname: mine\n---\n",
		".github/workflows/ci.yml":        "name: ci\n",
		".github/cartographer-hooks/keep": "x",
	}
	for rel, c := range mine {
		p := filepath.Join(baseDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := applyCopilotProject(t, kbRoot, m, baseDir, provisioning.Lock{})

	// Nothing reaches the global Copilot home.
	if _, err := os.Stat(filepath.Join(baseDir, ".copilot")); !os.IsNotExist(err) {
		t.Error("a project projection wrote into .copilot")
	}
	agent := filepath.Join(baseDir, ".github", "agents", "reviewer.agent.md")
	if b, err := os.ReadFile(agent); err != nil || !strings.Contains(string(b), "name: reviewer") {
		t.Errorf("agent file: %v %q", err, b)
	}
	reg := filepath.Join(baseDir, ".github", "hooks", "cartographer-guard.json")
	root := readJSONFile(t, reg)
	h := root["hooks"].(map[string]any)["preToolUse"].([]any)[0].(map[string]any)
	want := filepath.Join(baseDir, ".github", "cartographer-hooks", "guard", "notify.sh")
	if h["powershell"] != want {
		t.Errorf("entry = %v, want a command running %s", h, want)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".github", "cartographer-hooks", "guard", "hook.json")); err != nil {
		t.Error(err)
	}
	// Copilot parses every *.json below .github/hooks/: only registrations there.
	for _, mf := range res.NewLock.Managed {
		if strings.HasPrefix(mf.Path, ".github/hooks/") && mf.Path != ".github/hooks/cartographer-guard.json" {
			t.Errorf("%s would be parsed by Copilot as a hook configuration", mf.Path)
		}
	}

	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{reg, agent, filepath.Join(baseDir, ".github", "cartographer-hooks", "guard")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the prune: %v", gone, err)
		}
	}
	for rel := range mine {
		if _, err := os.Stat(filepath.Join(baseDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("the repository's %s was touched: %v", rel, err)
		}
	}
}

// .github is the repository's directory: emptying what Cartographer put there
// never removes it.
func TestPruneManaged_CopilotProjectKeepsAnEmptiedGithubDir(t *testing.T) {
	kbRoot, m := writeCopilotProjectKB(t, "PreToolUse", "")
	baseDir := t.TempDir()
	res := applyCopilotProject(t, kbRoot, m, baseDir, provisioning.Lock{})
	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(baseDir, ".github")); err != nil || !fi.IsDir() {
		t.Errorf(".github was pruned: %v", err)
	}
}

func TestApply_CopilotProjectHookWithMatcherOrUnfiredEventIsNotRegistered(t *testing.T) {
	for _, tc := range []struct{ event, matcher, warn string }{
		{"PostToolUse", "concept_write", "no matcher"},
		{"Stop", "", ""},
	} {
		kbRoot, m := writeCopilotProjectKB(t, tc.event, tc.matcher)
		baseDir := t.TempDir()
		res := applyCopilotProject(t, kbRoot, m, baseDir, provisioning.Lock{})
		if _, err := os.Stat(filepath.Join(baseDir, ".github", "hooks")); !os.IsNotExist(err) {
			t.Errorf("%s/%q: a registration was written: %v", tc.event, tc.matcher, err)
		}
		if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), tc.warn) {
			t.Errorf("%s/%q: warnings = %v", tc.event, tc.matcher, res.Warnings)
		}
	}
}

// The same KB hook in both scopes: two independent registrations, neither
// touching the other (VS Code fires both, so the hook runs twice; the other
// providers behave the same, docs/sync.md).
func TestApply_CopilotHookInBothScopesIsTwoIndependentRegistrations(t *testing.T) {
	kbRoot, m := writeCopilotProjectKB(t, "PreToolUse", "")
	home := t.TempDir()
	ws := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots: map[string]string{"kb": kbRoot}, ApprovedMCP: approveAll(m),
		Provider: configurator.ProviderCopilot, BaseDir: home, Lock: provisioning.Lock{},
	}); err != nil {
		t.Fatal(err)
	}
	res := applyCopilotProject(t, kbRoot, m, ws, provisioning.Lock{})
	globalReg := filepath.Join(home, ".copilot", "hooks", "cartographer-guard.json")
	before, err := os.ReadFile(globalReg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provisioning.PruneManaged(res.NewLock.Managed, ws, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(globalReg)
	if err != nil || string(after) != string(before) {
		t.Errorf("pruning the workspace changed the global registration: %v", err)
	}
}

func TestApply_CopilotProjectRefusesASymlinkedGithubDir(t *testing.T) {
	kbRoot, m := writeCopilotProjectKB(t, "PreToolUse", "")
	baseDir := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(baseDir, ".github")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	res := applyCopilotProject(t, kbRoot, m, baseDir, provisioning.Lock{})
	if len(res.Refused) == 0 {
		t.Error("nothing was refused")
	}
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Errorf("a write went through the link: %v", entries)
	}
}

func TestProjectOwnedPaths_CopilotRegistrationIsExcludedAndTrackedFilesAreRefused(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	_, m := writeCopilotProjectKB(t, "PreToolUse", "")
	owned, err := provisioning.ProjectOwnedArtifactPaths(configurator.ProviderCopilot, m, provisioning.Lock{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range owned {
		have[p] = true
	}
	for _, p := range []string{".github/hooks/cartographer-guard.json", ".github/cartographer-hooks/guard", ".github/agents/reviewer.agent.md"} {
		if !have[p] {
			t.Errorf("%s is not an owned path: %v", p, owned)
		}
	}

	ws := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", ws}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := provisioning.EnsureWorkspaceExcluded(ws, owned); err != nil {
		t.Fatal(err)
	}
	ex, err := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ex), ".github/hooks/cartographer-guard.json") {
		t.Errorf("the registration file is not in the exclude block:\n%s", ex)
	}
	// The repository's own hook file is not Cartographer's and not excluded.
	if strings.Contains(string(ex), "mine.json") {
		t.Errorf("the exclude block names a file Cartographer does not own:\n%s", ex)
	}

	tracked := filepath.Join(ws, ".github", "agents", "reviewer.agent.md")
	if err := os.MkdirAll(filepath.Dir(tracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("---\nname: reviewer\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-f", ".github/agents/reviewer.agent.md")
	err = provisioning.CheckWorkspaceHygiene("copilot", ws, owned, nil)
	var collision *provisioning.TrackedCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("a tracked agent file was not refused: %v", err)
	}

	reg := filepath.Join(ws, ".github", "hooks", "cartographer-guard.json")
	if err := os.MkdirAll(filepath.Dir(reg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("rm", "-q", "--cached", ".github/agents/reviewer.agent.md")
	git("add", "-f", ".github/hooks/cartographer-guard.json")
	collision = nil
	err = provisioning.CheckWorkspaceHygiene("copilot", ws, owned, nil)
	if !errors.As(err, &collision) || !strings.Contains(strings.Join(collision.Paths, ","), ".github/hooks/cartographer-guard.json") {
		t.Fatalf("a tracked registration file was not refused: %v", err)
	}
}
