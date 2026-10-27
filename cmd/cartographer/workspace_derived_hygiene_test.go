package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// trackedRepo is a git repository tracking the given files (path -> content).
func trackedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws := t.TempDir()
	if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	for rel, content := range files {
		abs := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", ws, "add", rel).CombinedOutput(); err != nil {
			t.Skipf("git add: %v %s", err, out)
		}
	}
	return ws
}

func kiroProjection(ws string, derived bool) syncProjection {
	p := syncProjection{Provider: "kiro", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject, Derived: derived}
	if derived {
		p.SharedSkipped, p.SkipKinds = derivedSharedSkips(p.Provider, ws)
	}
	return p
}

func skillAndMCPManifest() provisioning.Manifest {
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{
		{Kind: "skill", Name: "example", Source: "bundle", BuiltIn: true, ContentHash: "h-skill",
			Files: []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("# example\n")}}},
		{Kind: "mcp", Name: "tools", Source: "bundle", BuiltIn: true, ContentHash: "h-mcp",
			Files: []provisioning.ArtifactFile{{Path: "tools.json", Content: []byte(`{"type":"http","url":"https://tools.example.com/mcp"}`)}}},
	}}
	m.Revision = "r"
	return m
}

func sync1181(t *testing.T, projections []syncProjection) error {
	t.Helper()
	manifests := map[string]provisioning.Manifest{}
	for _, p := range projections {
		manifests[projectionKey(p)] = skillAndMCPManifest()
	}
	dir := t.TempDir()
	_, err := materializeForProviders(manifests, projections, dir, "", false, false, portabilityOptions{}, nil)
	return err
}

// #1181: a derived workspace that tracks the MCP file Kiro's projection writes
// whole syncs, and the file stays byte-identical.
func TestDerivedWorkspace_TrackedMCPFileIsLeftAlone(t *testing.T) {
	const original = "{\"mcpServers\": {}}\n"
	ws := trackedRepo(t, map[string]string{".kiro/settings/mcp.json": original})
	p := kiroProjection(ws, true)
	if len(p.SharedSkipped) != 1 || p.SharedSkipped[0] != ".kiro/settings/mcp.json" {
		t.Fatalf("SharedSkipped = %v, want the tracked mcp file", p.SharedSkipped)
	}
	if err := sync1181(t, []syncProjection{p}); err != nil {
		t.Fatalf("sync = %v, want no error", err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, ".kiro", "settings", "mcp.json"))
	if !bytes.Equal(got, []byte(original)) {
		t.Errorf("the tracked file changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".kiro", "skills", "example", "SKILL.md")); err != nil {
		t.Errorf("the skill was not projected: %v", err)
	}
}

// #1181: a real name clash in one derived workspace skips that projection with
// a warning and the others in the run are projected; an explicit binding with
// the same clash is still refused.
func TestDerivedWorkspace_ClashSkipsOnlyThatProjection(t *testing.T) {
	clash := trackedRepo(t, map[string]string{".kiro/skills/example/SKILL.md": "mine\n"})
	clean := trackedRepo(t, map[string]string{"README.md": "x\n"})

	err := sync1181(t, []syncProjection{kiroProjection(clash, true), kiroProjection(clean, true)})
	if err != nil {
		t.Fatalf("sync = %v, want the clash skipped", err)
	}
	if b, _ := os.ReadFile(filepath.Join(clash, ".kiro", "skills", "example", "SKILL.md")); string(b) != "mine\n" {
		t.Errorf("the tracked skill changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(clean, ".kiro", "skills", "example", "SKILL.md")); err != nil {
		t.Errorf("the other workspace was not projected: %v", err)
	}

	err = sync1181(t, []syncProjection{kiroProjection(clash, false)})
	var collision *provisioning.TrackedCollisionError
	if !errors.As(err, &collision) {
		t.Errorf("explicit binding = %v, want the refusal", err)
	}
}

// A lock that already records an MCP entry in a file git now tracks: the
// derived sync neither prunes it from the file nor drops it from the lock.
func TestDerivedWorkspace_TrackedMCPFileKeepsItsLockEntries(t *testing.T) {
	const original = "{\n  \"mcpServers\": {\"tools\": {\"url\": \"https://tools.example.com/mcp\"}}\n}\n"
	ws := trackedRepo(t, map[string]string{".kiro/settings/mcp.json": original})
	p := kiroProjection(ws, true)

	dir := t.TempDir()
	var lf provisioning.LockFile
	lf.SetProjection(p.Key(), provisioning.Lock{Provider: "kiro", BaseDir: ws, ProjectScope: true, Managed: []provisioning.ManagedFile{
		{Kind: "mcp", Name: "tools", Path: ".kiro/settings/mcp.json", ContentHash: "h-mcp"},
		{Kind: "mcp", Name: "bridge", Path: ".kiro/settings/mcp.json", Source: provisioning.SourceClient},
	}}, "")
	if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
		t.Fatal(err)
	}
	m := skillAndMCPManifest()
	m.Artifacts = m.Artifacts[:1]
	if _, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, dir, "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, ".kiro", "settings", "mcp.json")); string(got) != original {
		t.Errorf("the tracked file changed:\n%s", got)
	}
	after, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, mf := range after.ForProjection(p.Key()).Managed {
		if mf.Kind == "mcp" {
			names[mf.Name] = true
		}
	}
	if !names["tools"] || !names["bridge"] {
		t.Errorf("lock entries lost: %v", names)
	}
	// The projection ran: a skipped one leaves the file and the lock alone
	// too, and would pass the checks above without the tracked-file rule.
	if _, err := os.Stat(filepath.Join(ws, ".kiro", "skills", "example", "SKILL.md")); err != nil {
		t.Errorf("the skill was not projected: %v", err)
	}
}

// #1189: the hook case of the test above. A hook registration file that git
// tracks once an earlier sync wrote it is left byte-identical by a derived
// sync, which does not apply the hook, and whose lock keeps the hook's
// entries, while the rest is projected.
func TestDerivedWorkspace_TrackedHookFileKeepsItsLockEntries(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	hook := provisioning.Artifact{Kind: "hook", Name: "guard", Source: "bundle", BuiltIn: true, ContentHash: "h-hook", Files: []provisioning.ArtifactFile{
		{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)},
		{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
	}}
	skill := skillAndMCPManifest().Artifacts[0]
	dir := t.TempDir()
	apply := func(p syncProjection, artifacts ...provisioning.Artifact) {
		t.Helper()
		m := provisioning.Manifest{Revision: "r", Artifacts: artifacts}
		if _, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, dir, "", false, false, portabilityOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	// An earlier sync registered the hook, then the repository started
	// tracking the registration file (forced: that sync excluded it).
	explicit := syncProjection{Provider: "antigravity", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject}
	apply(explicit, hook)
	const reg = ".agents/hooks.json"
	original, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(reg)))
	if err != nil {
		t.Fatalf("the first sync did not register the hook: %v", err)
	}
	if out, err := exec.Command("git", "-C", ws, "add", "-f", reg).CombinedOutput(); err != nil {
		t.Skipf("git add: %v %s", err, out)
	}

	p := explicit
	p.Derived = true
	p.SharedSkipped, p.SkipKinds = derivedSharedSkips(p.Provider, ws)
	// The KB still serves the hook, changed: a derived sync must not apply it.
	changed := hook
	changed.ContentHash = "h-hook-2"
	changed.Files = []provisioning.ArtifactFile{hook.Files[0], {Path: "run.sh", Content: []byte("#!/bin/sh\necho changed\n"), Executable: true}}
	apply(p, skill, changed)

	if got, _ := os.ReadFile(filepath.Join(ws, filepath.FromSlash(reg))); !bytes.Equal(got, original) {
		t.Errorf("the tracked registration file changed:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, ".agents", "hooks", "guard", "run.sh")); string(got) != "#!/bin/sh\n" {
		t.Errorf("the hook was applied although its registration file is tracked: %q", got)
	}
	after, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	hookEntries := 0
	for _, mf := range after.ForProjection(p.Key()).Managed {
		if mf.Kind == "hook" && mf.Name == "guard" {
			hookEntries++
		}
	}
	if hookEntries == 0 {
		t.Errorf("the hook's lock entries were dropped: %+v", after.ForProjection(p.Key()).Managed)
	}
	if _, err := os.Stat(filepath.Join(ws, ".agents", "skills", "example", "SKILL.md")); err != nil {
		t.Errorf("the skill was not projected: %v", err)
	}
}

// An artifact name that cannot be projected skips a derived projection with a
// warning; an explicit binding still fails.
func TestDerivedWorkspace_InvalidNameSkipsOnlyDerived(t *testing.T) {
	ws := trackedRepo(t, map[string]string{"README.md": "x\n"})
	bad := func(p syncProjection) error {
		m := provisioning.Manifest{Revision: "r", Artifacts: []provisioning.Artifact{{Kind: "skill", Name: "x\n!/src", Source: "bundle", BuiltIn: true, ContentHash: "h"}}}
		_, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, t.TempDir(), "", false, false, portabilityOptions{}, nil)
		return err
	}
	if err := bad(kiroProjection(ws, true)); err != nil {
		t.Errorf("derived = %v, want skipped", err)
	}
	var badName *provisioning.InvalidArtifactNameError
	if err := bad(kiroProjection(ws, false)); !errors.As(err, &badName) {
		t.Errorf("explicit = %v, want the refusal", err)
	}
}

func claudeDerivedProjection(ws string) syncProjection {
	p := syncProjection{Provider: "claude", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject, Derived: true}
	p.InstructionsRel, p.InstructionsSkipped = derivedInstructions(p.Provider, ws)
	p.SharedSkipped, p.SkipKinds = derivedSharedSkips(p.Provider, ws)
	p.HookSettingsRel = derivedHookSettings(p.Provider, p.SkipKinds)
	return p
}

func bundleHook(name string) provisioning.Artifact {
	return provisioning.Artifact{Kind: "hook", Name: name, Source: "bundle", BuiltIn: true, ContentHash: "h-" + name, Files: []provisioning.ArtifactFile{
		{Path: "hook.json", Content: []byte(`{"event":"PreToolUse","matcher":"Bash","command":"./run.sh"}`)},
		{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
	}}
}

const trackedClaudeSettings = `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit",
        "hooks": [
          {
            "type": "command",
            "command": "make fmt",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
`

// #1198: a derived Claude Code projection registers its hooks in the untracked
// settings.local.json, never in a tracked settings.json, and leaves the working
// tree clean.
func TestDerivedWorkspace_ClaudeHooksGoToSettingsLocal(t *testing.T) {
	ws := trackedRepo(t, map[string]string{".claude/settings.json": trackedClaudeSettings})
	p := claudeDerivedProjection(ws)
	if p.HookSettingsRel != provisioning.ClaudeLocalSettings {
		t.Fatalf("HookSettingsRel = %q", p.HookSettingsRel)
	}
	m := provisioning.Manifest{Revision: "r", Artifacts: []provisioning.Artifact{bundleHook("guard"), bundleHook("audit")}}
	if _, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, t.TempDir(), "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, ".claude", "settings.json")); string(got) != trackedClaudeSettings {
		t.Errorf("the tracked settings.json changed:\n%s", got)
	}
	local, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.local.json"))
	if err != nil || !bytes.Contains(local, []byte(".claude/hooks/guard/")) || !bytes.Contains(local, []byte(".claude/hooks/audit/")) {
		t.Errorf("hooks not registered in settings.local.json: %v\n%s", err, local)
	}
	out, err := exec.Command("git", "-C", ws, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	// trackedRepo stages its files without a commit: that line is the only one.
	if got := strings.TrimSpace(string(out)); got != "A  .claude/settings.json" {
		t.Errorf("the working tree is dirty:\n%s", out)
	}
}

// A repository that tracks settings.local.json too gets no hook at all from a
// derived binding, and status says why.
func TestDerivedWorkspace_TrackedClaudeSettingsLocalSkipsHooks(t *testing.T) {
	const own = "{\"permissions\": {}}\n"
	ws := trackedRepo(t, map[string]string{".claude/settings.local.json": own})
	p := claudeDerivedProjection(ws)
	if p.HookSettingsRel != "" || !slices.Contains(p.SkipKinds, "hook") || !slices.Contains(p.SharedSkipped, provisioning.ClaudeLocalSettings) {
		t.Fatalf("projection = %+v, want hooks skipped", p)
	}
	m := provisioning.Manifest{Revision: "r", Artifacts: []provisioning.Artifact{bundleHook("guard")}}
	if _, err := materializeForProviders(map[string]provisioning.Manifest{projectionKey(p): m}, []syncProjection{p}, t.TempDir(), "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, ".claude", "settings.local.json")); string(got) != own {
		t.Errorf("the tracked settings.local.json changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("a settings.json was written: %v", err)
	}
}

// #1198: doctor reports a tracked settings.json holding Cartographer hook
// entries in a derived Claude Code workspace.
func TestTrackedClaudeHookSettings(t *testing.T) {
	withEntry := strings.Replace(trackedClaudeSettings, `"hooks": {`, `"hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "'/repo/.claude/hooks/guard/run.sh' # cartographer-hook: .claude/hooks/guard/"}]}],`, 1)
	for name, tc := range map[string]struct {
		content string
		want    bool
	}{
		"cartographer entry": {withEntry, true},
		"team hooks only":    {trackedClaudeSettings, false},
	} {
		t.Run(name, func(t *testing.T) {
			ws := trackedRepo(t, map[string]string{".claude/settings.json": tc.content})
			got := trackedClaudeHookSettings("claude", ws, provisioning.LockFile{})
			if (len(got) == 1) != tc.want {
				t.Errorf("findings = %+v, want finding %v", got, tc.want)
			}
			if len(got) == 1 && !strings.Contains(got[0].Message, provisioning.ClaudeLocalSettings) {
				t.Errorf("message does not name settings.local.json: %s", got[0].Message)
			}
		})
	}
	if got := trackedClaudeHookSettings("codex", t.TempDir(), provisioning.LockFile{}); len(got) != 0 {
		t.Errorf("codex: %+v", got)
	}
}
