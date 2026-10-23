package provisioning_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// A writer that reports no artifact of its own — the MCP entry, the instructions
// block, a hook's registration — refuses a symlinked destination (D148) as a
// per-artifact refusal too: reported, not recorded as written or managed, the
// rest of the pass and the lockfile unaffected, retried by the next sync.
func TestApply_RefusedMCPAndInstructionsAreReportedPerArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	skill := filepath.Join(kbRoot, "skills", "demo")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := signedMCPManifest(t, kbRoot, "wiki-tools")
	for i := range m.Artifacts {
		m.Artifacts[i].Signed = true
	}
	kinds := map[string]bool{}
	for _, a := range m.Artifacts {
		kinds[a.Kind] = true
	}
	for _, k := range []string{"skill", "instructions", "mcp"} {
		if !kinds[k] {
			t.Fatalf("fixture has no %s artifact: %+v", k, m.Artifacts)
		}
	}

	apply := func(base string, lock provisioning.Lock) provisioning.AppliedResult {
		t.Helper()
		res, err := provisioning.Apply(m, provisioning.ApplyOptions{
			ApprovedMCP: approveMCP(m), KBRoots: map[string]string{"kb": kbRoot},
			Provider: configurator.ProviderKiro, BaseDir: base, Lock: lock,
		})
		if err != nil {
			t.Fatalf("Apply = %v, want the refusals reported, not fatal", err)
		}
		return res
	}
	refusedKinds := func(res provisioning.AppliedResult) map[string]bool {
		out := map[string]bool{}
		for _, a := range res.Refused {
			out[a.Kind] = true
		}
		return out
	}

	t.Run("every destination behind a link", func(t *testing.T) {
		base, elsewhere := t.TempDir(), t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(base, ".kiro")); err != nil {
			t.Fatal(err)
		}
		res := apply(base, provisioning.Lock{})
		got := refusedKinds(res)
		if !got["skill"] || !got["instructions"] || !got["mcp"] {
			t.Errorf("Refused = %+v, want the skill, the instructions and the mcp entry", res.Refused)
		}
		if len(res.Written) != 0 || len(res.NewLock.Managed) != 0 {
			t.Errorf("Written = %+v, NewLock.Managed = %+v; want nothing recorded", res.Written, res.NewLock.Managed)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("the link target gained %d entries", len(entries))
		}
		if _, err := os.Stat(filepath.Join(base, provisioning.LockFileName)); err != nil {
			t.Errorf("the lockfile was not written: %v", err)
		}
	})

	t.Run("only the MCP config behind a link", func(t *testing.T) {
		base, elsewhere := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, ".kiro"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(base, ".kiro", "settings")); err != nil {
			t.Fatal(err)
		}
		res := apply(base, provisioning.Lock{})
		got := refusedKinds(res)
		if !got["mcp"] || got["skill"] || got["instructions"] {
			t.Errorf("Refused = %+v, want only the mcp entry", res.Refused)
		}
		recorded := map[string]bool{}
		for _, mf := range res.NewLock.Managed {
			recorded[mf.Kind] = true
		}
		if !recorded["skill"] || !recorded["instructions"] || recorded["mcp"] {
			t.Errorf("NewLock.Managed = %+v, want the skill and the instructions, not the refused mcp entry", res.NewLock.Managed)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("the link target gained %d entries", len(entries))
		}
		// The operator replaces the link; the next sync installs it.
		if err := os.Remove(filepath.Join(base, ".kiro", "settings")); err != nil {
			t.Fatal(err)
		}
		res = apply(base, res.NewLock)
		var mcp bool
		for _, mf := range res.NewLock.Managed {
			mcp = mcp || mf.Kind == "mcp"
		}
		if len(res.Refused) != 0 || !mcp {
			t.Errorf("retry: Refused = %+v, NewLock.Managed = %+v; want the mcp entry installed", res.Refused, res.NewLock.Managed)
		}
	})
}

// A hook's script must not be deleted while its registration cannot be removed:
// the registration would then point at nothing.
func TestPruneManaged_AHookKeepsItsScriptWhenItsRegistrationIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base, elsewhere := t.TempDir(), t.TempDir()
	// Claude Code: the script is on a real path, settings.json is a link (a
	// dotfile manager) holding the hook's registration.
	scriptDir := filepath.Join(base, ".claude", "hooks", "h")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scriptDir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const settings = `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"sh .claude/hooks/h/run.sh"}]}]}}`
	target := filepath.Join(elsewhere, "settings.json")
	if err := os.WriteFile(target, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}

	managed := []provisioning.ManagedFile{{Kind: "hook", Name: "h", Path: filepath.Join(".claude", "hooks", "h", "run.sh")}}
	pruned, err := provisioning.PruneManaged(managed, base, false)
	if !errors.Is(err, provisioning.ErrSymlinkDestination) || len(pruned) != 0 {
		t.Fatalf("PruneManaged = %v, %v; want the hook refused", pruned, err)
	}
	if _, statErr := os.Stat(script); statErr != nil {
		t.Errorf("the script was deleted although its registration was refused: %v", statErr)
	}
	if b, _ := os.ReadFile(target); string(b) != settings {
		t.Errorf("registration changed: %s", b)
	}
}
