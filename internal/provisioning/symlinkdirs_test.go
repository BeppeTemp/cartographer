package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// A client directory that is a symlink (a dotfile manager, a shared checkout)
// diverts every write, mkdir and remove below it to the link's target, so each
// of them refuses it — the directory chain under the base dir, not only the
// final file — and leaves the target byte for byte as it was (D148).

func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
}

func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("%s: the link target changed shape: %v -> %v", what, before, after)
		return
	}
	for k, v := range before {
		if a, ok := after[k]; !ok || a != v {
			t.Errorf("%s: %q changed through the link: %q -> %q", what, k, v, a)
		}
	}
}

func linkFirstComponent(t *testing.T, base, rel, target string) {
	t.Helper()
	first := strings.Split(filepath.ToSlash(rel), "/")[0]
	if err := os.Symlink(target, filepath.Join(base, first)); err != nil {
		t.Fatal(err)
	}
}

func TestMCPRegisterAndRemove_RefuseALinkedDirectoryAndNoOpIsNotARefusal(t *testing.T) {
	skipWithoutSymlinks(t)
	spec := configurator.ServerSpec{Type: "http", URL: "https://mcp.example.test/mcp"}
	for _, provider := range configurator.ProviderList() {
		t.Run(string(provider), func(t *testing.T) {
			// A scratch home holding our entry is the link target.
			target := t.TempDir()
			rel, _, err := registerMCPServer(target, "wiki", spec, provider, ScopeGlobal)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.ContainsAny(filepath.ToSlash(rel), "/") {
				t.Skipf("%s keeps its MCP config at the base dir, with no directory above it", provider)
			}
			// The link target is the first directory of the path, so move the
			// scratch home's copy of it out of the way and link to it.
			first := strings.Split(filepath.ToSlash(rel), "/")[0]
			elsewhere := t.TempDir()
			if err := os.Rename(filepath.Join(target, first), filepath.Join(elsewhere, first)); err != nil {
				t.Fatal(err)
			}
			base := t.TempDir()
			linkFirstComponent(t, base, rel, filepath.Join(elsewhere, first))
			before := treeOf(t, elsewhere)

			if _, _, err := registerMCPServer(base, "other", spec, provider, ScopeGlobal); !errors.Is(err, ErrSymlinkDestination) {
				t.Errorf("registerMCPServer = %v, want ErrSymlinkDestination", err)
			}
			if err := removeMCPServer(base, "wiki", provider, filepath.ToSlash(rel)); !errors.Is(err, ErrSymlinkDestination) {
				t.Errorf("removeMCPServer of a present entry = %v, want ErrSymlinkDestination", err)
			}
			// Nothing of ours in it: a no-op, whatever the directory is.
			if err := removeMCPServer(base, "absent", provider, filepath.ToSlash(rel)); err != nil {
				t.Errorf("removeMCPServer of an absent entry = %v, want nil", err)
			}
			sameTree(t, "mcp config", before, treeOf(t, elsewhere))
		})
	}
}

func TestInstructionsBlock_RefusesALinkedDirectoryAndNoOpIsNotARefusal(t *testing.T) {
	skipWithoutSymlinks(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "steering"), 0o755); err != nil {
		t.Fatal(err)
	}
	withBlock := filepath.Join(elsewhere, "steering", "with.md")
	if err := writeInstructionsBlock(filepath.Dir(withBlock), withBlock, "body"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "steering", "plain.md"), []byte("user text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(base, ".kiro")); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, elsewhere)
	with := filepath.Join(base, ".kiro", "steering", "with.md")
	plain := filepath.Join(base, ".kiro", "steering", "plain.md")

	for _, p := range []string{with, plain, filepath.Join(base, ".kiro", "steering", "new.md"), filepath.Join(base, ".kiro", "new", "new.md")} {
		if err := writeInstructionsBlock(base, p, "new body"); !errors.Is(err, ErrSymlinkDestination) {
			t.Errorf("writeInstructionsBlock(%s) = %v, want ErrSymlinkDestination", p, err)
		}
	}
	if err := removeInstructionsBlock(base, with); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("removeInstructionsBlock with a block = %v, want ErrSymlinkDestination", err)
	}
	for _, p := range []string{plain, filepath.Join(base, ".kiro", "steering", "absent.md")} {
		if err := removeInstructionsBlock(base, p); err != nil {
			t.Errorf("removeInstructionsBlock(%s) with no block = %v, want nil", p, err)
		}
	}
	sameTree(t, "instructions", before, treeOf(t, elsewhere))
}

func TestHookRegistrationFiles_RefuseALinkedDirectory(t *testing.T) {
	skipWithoutSymlinks(t)
	base := t.TempDir()
	elsewhere := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(elsewhere, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// One link per client directory the hook files live under.
	write(".copilot/hooks/cartographer-h.json", "{}")
	write(".config/opencode/plugins/cartographer-h.js", "// plugin")
	write(".kiro/hooks/cartographer.json", `{"version":"v1","hooks":[{"name":"h"}]}`)
	write(".codex/hooks.json", "{}")
	for _, d := range []string{".copilot", ".config", ".kiro", ".codex"} {
		if err := os.Symlink(filepath.Join(elsewhere, d), filepath.Join(base, d)); err != nil {
			t.Fatal(err)
		}
	}
	before := treeOf(t, elsewhere)

	if err := removeCopilotHook(base, "h"); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("removeCopilotHook = %v, want ErrSymlinkDestination", err)
	}
	if err := removeOpenCodePlugin(base, "h"); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("removeOpenCodePlugin = %v, want ErrSymlinkDestination", err)
	}
	if err := removeKiroHook(base, "h"); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("removeKiroHook = %v, want ErrSymlinkDestination", err)
	}
	if err := saveJSONObject(base, filepath.Join(base, ".codex", "hooks.json"), map[string]interface{}{"a": 1}); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("saveJSONObject = %v, want ErrSymlinkDestination", err)
	}
	if err := saveJSONObject(base, filepath.Join(base, ".codex", "sub", "new.json"), map[string]interface{}{"a": 1}); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("saveJSONObject into a new subdirectory = %v, want ErrSymlinkDestination", err)
	}
	// Nothing registered: no-ops stay clean.
	if err := removeCopilotHook(base, "absent"); err != nil {
		t.Errorf("removeCopilotHook of an absent hook = %v, want nil", err)
	}
	if err := removeOpenCodePlugin(base, "absent"); err != nil {
		t.Errorf("removeOpenCodePlugin of an absent hook = %v, want nil", err)
	}
	if err := removeKiroHook(base, "absent"); err != nil {
		t.Errorf("removeKiroHook of an absent hook = %v, want nil", err)
	}
	if err := removeCodexHook(base, "absent"); err != nil {
		t.Errorf("removeCodexHook of an absent hook = %v, want nil", err)
	}
	sameTree(t, "hook files", before, treeOf(t, elsewhere))
}

func TestPruneManaged_RefusesPerFileAndKeepsGoing(t *testing.T) {
	skipWithoutSymlinks(t)
	base := t.TempDir()
	elsewhere := t.TempDir()
	linked := filepath.Join(elsewhere, "skills", "s")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "SKILL.md"), []byte("someone else's"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, ".claude")); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(base, ".kiro", "skills", "ok", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(realFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realFile, []byte("ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, elsewhere)

	managed := []ManagedFile{
		{Kind: "skill", Name: "s", Path: filepath.Join(".claude", "skills", "s", "SKILL.md")},
		{Kind: "instructions", Name: "kb", Path: filepath.Join(".claude", "CLAUDE.md")},
		{Kind: "skill", Name: "ok", Path: filepath.Join(".kiro", "skills", "ok", "SKILL.md")},
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "CLAUDE.md"), []byte(instructionsBlockBegin+"\nx\n"+instructionsBlockEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before = treeOf(t, elsewhere)

	pruned, err := PruneManaged(managed, base, false)
	var refused *PruneRefusedError
	if !errors.As(err, &refused) || !errors.Is(err, ErrSymlinkDestination) {
		t.Fatalf("PruneManaged = %v, want a PruneRefusedError matching ErrSymlinkDestination", err)
	}
	if len(refused.Refusals) != 2 {
		t.Errorf("refusals = %+v, want the skill and the instructions block", refused.Refusals)
	}
	if len(pruned) != 1 || pruned[0].Name != "ok" {
		t.Errorf("pruned = %+v, want only the file on a real path", pruned)
	}
	if _, err := os.Stat(realFile); !os.IsNotExist(err) {
		t.Errorf("the file on a real path was not pruned: %v", err)
	}
	sameTree(t, "prune", before, treeOf(t, elsewhere))

	// Nothing to remove behind the link: not a refusal, and the empty directory
	// it leaves in the target is not pruned through the link either.
	empty := filepath.Join(elsewhere, "skills", "gone")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	before = treeOf(t, elsewhere)
	if _, err := PruneManaged([]ManagedFile{{Kind: "skill", Name: "gone", Path: filepath.Join(".claude", "skills", "gone", "SKILL.md")}}, base, false); err != nil {
		t.Errorf("PruneManaged of an absent file behind a link = %v, want nil", err)
	}
	sameTree(t, "prune of an absent file", before, treeOf(t, elsewhere))
}

// A refused prune is per artifact: reported, left in the lockfile so the next
// sync retries, and the pass does not fail.
func TestApply_RefusedPruneIsReportedKeptInTheLockAndRetried(t *testing.T) {
	skipWithoutSymlinks(t)
	base := t.TempDir()
	elsewhere := t.TempDir()
	skillFile := filepath.Join(elsewhere, "skills", "s", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("someone else's"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, ".claude")); err != nil {
		t.Fatal(err)
	}
	stale := ManagedFile{Kind: "skill", Name: "s", Path: filepath.Join(".claude", "skills", "s", "SKILL.md"), Source: "kb:test"}
	before := treeOf(t, elsewhere)

	res, err := Apply(Manifest{Revision: "r"}, ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: Lock{Managed: []ManagedFile{stale}}})
	if err != nil {
		t.Fatalf("Apply = %v, want the refusal reported, not fatal", err)
	}
	if len(res.Refused) != 1 || res.Refused[0].Name != "s" || len(res.Pruned) != 0 {
		t.Errorf("Refused = %+v, Pruned = %+v; want the skill refused and nothing pruned", res.Refused, res.Pruned)
	}
	if len(res.NewLock.Managed) != 1 || res.NewLock.Managed[0].Name != "s" {
		t.Errorf("NewLock.Managed = %+v, want the refused file kept", res.NewLock.Managed)
	}
	var warned bool
	for _, w := range res.Warnings {
		warned = warned || (strings.Contains(w, "skill/s") && strings.Contains(w, "symlink"))
	}
	if !warned {
		t.Errorf("Warnings = %v, want one naming the refused artifact", res.Warnings)
	}
	sameTree(t, "apply prune", before, treeOf(t, elsewhere))

	// The operator replaces the link with a real directory; the retry prunes.
	if err := os.Remove(filepath.Join(base, ".claude")); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, ".claude", "skills", "s")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("ours"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Apply(Manifest{Revision: "r"}, ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: res.NewLock})
	if err != nil || len(res.Refused) != 0 || len(res.Pruned) != 1 {
		t.Fatalf("retry: err=%v refused=%+v pruned=%+v", err, res.Refused, res.Pruned)
	}
	if len(res.NewLock.Managed) != 0 {
		t.Errorf("retry left %+v in the lockfile", res.NewLock.Managed)
	}
}

func TestEnsureWorkspaceExcluded_RefusesALinkedGitDirOnlyWhenItWouldWrite(t *testing.T) {
	skipWithoutSymlinks(t)
	ws := t.TempDir()
	gitdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitdir, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "info", "exclude"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitdir, filepath.Join(ws, ".git")); err != nil {
		t.Fatal(err)
	}
	before := treeOf(t, gitdir)

	if err := EnsureWorkspaceExcluded(ws, []string{".claude/skills/x"}); !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("EnsureWorkspaceExcluded = %v, want ErrSymlinkDestination", err)
	}
	// No block of ours and none to write: unbinding changes nothing, so it
	// does not refuse either.
	if err := RemoveWorkspaceExclusions(ws); err != nil {
		t.Errorf("RemoveWorkspaceExclusions with no block = %v, want nil", err)
	}
	sameTree(t, "git exclude", before, treeOf(t, gitdir))
}
