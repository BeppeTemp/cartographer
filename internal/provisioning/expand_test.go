package provisioning

// Tests for the client-side expansion of {{repo:<key>}}/{{path:<name>}}
// placeholders at materialization time (D75 WP3). White-box package
// (provisioning, not provisioning_test) to exercise
// expandPlaceholders/hashArtifactFiles/expandHomePath directly besides Apply.

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// --- expandPlaceholders (unit) ---

func TestExpandPlaceholders_NoopWhenDisabled(t *testing.T) {
	content := []byte("See {{path:design}} for the assets.")
	opts := ApplyOptions{Paths: map[string]string{"design": "/mnt/design"}} // ExpandPlaceholders: false
	tracker := newExpansionTracker()

	got := expandPlaceholders(content, opts, tracker)
	if string(got) != string(content) {
		t.Errorf("content changed without ExpandPlaceholders: %s", got)
	}
	if len(tracker.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tracker.warnings)
	}
}

func TestExpandPlaceholders_PathResolved(t *testing.T) {
	content := []byte("See {{path:design}} for the assets.")
	opts := ApplyOptions{ExpandPlaceholders: true, Paths: map[string]string{"design": "/mnt/design"}}
	tracker := newExpansionTracker()

	got := expandPlaceholders(content, opts, tracker)
	if string(got) != "See /mnt/design for the assets." {
		t.Errorf("path not expanded correctly: %s", got)
	}
	if len(tracker.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tracker.warnings)
	}
	if tracker.resolved["path:design"] != "/mnt/design" {
		t.Errorf("tracker.resolved missing or wrong: %+v", tracker.resolved)
	}
}

func TestExpandPlaceholders_RepoResolvedViaManualPathsOverride(t *testing.T) {
	// repoindex.Resolve checks manualPaths BEFORE touching cache/filesystem:
	// so this test neither scans nor writes anything on the real filesystem.
	content := []byte("The repo is in {{repo:cartographer}}.")
	opts := ApplyOptions{ExpandPlaceholders: true, Paths: map[string]string{"cartographer": "/home/x/repos/cartographer"}}
	tracker := newExpansionTracker()

	got := expandPlaceholders(content, opts, tracker)
	if string(got) != "The repo is in /home/x/repos/cartographer." {
		t.Errorf("repo not expanded correctly: %s", got)
	}
	if tracker.resolved["repo:cartographer"] != "/home/x/repos/cartographer" {
		t.Errorf("tracker.resolved missing or wrong: %+v", tracker.resolved)
	}
}

func TestExpandPlaceholders_UnresolvedLeftAsIsWithWarning(t *testing.T) {
	content := []byte("See {{path:missing}} for the assets.")
	opts := ApplyOptions{ExpandPlaceholders: true, Paths: map[string]string{}}
	tracker := newExpansionTracker()

	got := expandPlaceholders(content, opts, tracker)
	if string(got) != string(content) {
		t.Errorf("unresolved placeholder must stay literal: %s", got)
	}
	if len(tracker.warnings) != 1 {
		t.Errorf("expected 1 warning, got: %v", tracker.warnings)
	}
}

func TestExpandPlaceholders_NoPlaceholdersUntouched(t *testing.T) {
	content := []byte("No placeholder here.")
	opts := ApplyOptions{ExpandPlaceholders: true, Paths: map[string]string{}}
	tracker := newExpansionTracker()

	got := expandPlaceholders(content, opts, tracker)
	if string(got) != string(content) {
		t.Errorf("placeholder-free content altered: %s", got)
	}
	if len(tracker.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", tracker.warnings)
	}
}

// --- expandHomePath (unit) ---

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home dir not available in this environment")
	}
	if got := expandHomePath("~/design"); got != filepath.Join(home, "design") {
		t.Errorf("expandHomePath(~/design) = %q, want %q", got, filepath.Join(home, "design"))
	}
	if got := expandHomePath("/mnt/design"); got != "/mnt/design" {
		t.Errorf("expandHomePath(/mnt/design) = %q, want unchanged", got)
	}
}

// --- hashArtifactFiles (unit) ---

func TestHashArtifactFiles_MatchesContentHashFileFormula(t *testing.T) {
	// Same formula as contentHashFile: sha256(basename + NUL + content + '\n').
	content := []byte("Body.\n")
	got := hashArtifactFiles([]ArtifactFile{{Path: "reviewer.md", Content: content}})

	h := sha256.New()
	fmt.Fprintf(h, "%s\x00", "reviewer.md")
	h.Write(content)
	h.Write([]byte{'\n'})
	want := fmt.Sprintf("%x", h.Sum(nil))

	if got != want {
		t.Errorf("hashArtifactFiles = %q, want %q", got, want)
	}
}

func TestHashArtifactFiles_OrderIndependent(t *testing.T) {
	files1 := []ArtifactFile{{Path: "a.md", Content: []byte("aaa")}, {Path: "b.md", Content: []byte("bbb")}}
	files2 := []ArtifactFile{{Path: "b.md", Content: []byte("bbb")}, {Path: "a.md", Content: []byte("aaa")}}
	if hashArtifactFiles(files1) != hashArtifactFiles(files2) {
		t.Error("hashArtifactFiles must be independent of file order")
	}
}

// --- Apply: agent (single file) ---

func TestApply_ExpandPlaceholders_ZeroDriftAgent(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nNo placeholder here.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var agentArtifact Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "agent" {
			agentArtifact = a
		}
	}
	if agentArtifact.Name == "" {
		t.Fatal("agent artifact not found in the manifest")
	}

	baseDir := t.TempDir()
	res, err := Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var agentWritten ManagedFile
	for _, w := range res.Written {
		if w.Kind == "agent" {
			agentWritten = w
		}
	}
	if agentWritten.ContentHash != agentArtifact.ContentHash {
		t.Errorf("zero-drift violated: ManagedFile.ContentHash %q != Artifact.ContentHash %q", agentWritten.ContentHash, agentArtifact.ContentHash)
	}
}

func TestApply_ExpandPlaceholders_AgentPathPlaceholder(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nThe assets are in {{path:design}}.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var agentArtifact Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "agent" {
			agentArtifact = a
		}
	}

	baseDir := t.TempDir()
	res, err := Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
		Paths:              map[string]string{"design": "/mnt/design"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "agents", "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/mnt/design") || strings.Contains(string(data), "{{path:design}}") {
		t.Errorf("placeholder not expanded on disk: %s", data)
	}

	var agentWritten ManagedFile
	for _, w := range res.Written {
		if w.Kind == "agent" {
			agentWritten = w
		}
	}
	if agentWritten.ContentHash == agentArtifact.ContentHash {
		t.Error("expected a hash different from the manifest's (expanded content), got the same")
	}
}

func TestApply_ExpandPlaceholders_UnresolvedWarnsAndLeavesLiteral(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nThe assets are in {{path:missing}}.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "agents", "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "{{path:missing}}") {
		t.Errorf("unresolved placeholder should have stayed literal: %s", data)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected at least one warning for the unresolved placeholder")
	}
}

func TestApply_ExpandPlaceholders_ServerNeverExpands(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nThe assets are in {{path:design}}.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var agentArtifact Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "agent" {
			agentArtifact = a
		}
	}

	baseDir := t.TempDir()
	// Like internal/mcpserver: ExpandPlaceholders never set (default false).
	res, err := Apply(m, ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "agents", "reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "{{path:design}}") {
		t.Errorf("the server must never expand: %s", data)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("no warning expected server-side: %v", res.Warnings)
	}

	var agentWritten ManagedFile
	for _, w := range res.Written {
		if w.Kind == "agent" {
			agentWritten = w
		}
	}
	if agentWritten.ContentHash != agentArtifact.ContentHash {
		t.Errorf("server-side hash must stay the manifest's: %q != %q", agentWritten.ContentHash, agentArtifact.ContentHash)
	}
}

// --- Apply: skill (multi-file) ---

func TestApply_ExpandPlaceholders_ZeroDriftSkill(t *testing.T) {
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "kb-skill")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: kb-skill\ndescription: Test\n---\nNo placeholder.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var skillArtifact Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "skill" {
			skillArtifact = a
		}
	}

	baseDir := t.TempDir()
	res, err := Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var skillWritten ManagedFile
	for _, w := range res.Written {
		if w.Kind == "skill" {
			skillWritten = w
		}
	}
	if skillWritten.ContentHash != skillArtifact.ContentHash {
		t.Errorf("zero-drift violated: ManagedFile.ContentHash %q != Artifact.ContentHash %q", skillWritten.ContentHash, skillArtifact.ContentHash)
	}
}

func TestApply_ExpandPlaceholders_SkillMultiFilePlaceholder(t *testing.T) {
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "kb-skill")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: kb-skill\ndescription: Test\n---\nSee {{repo:tools}}.\n"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "notes.md"), []byte("No placeholder here.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	_, err = Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
		Paths:              map[string]string{"tools": "/opt/tools"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	skillMD, err := os.ReadFile(filepath.Join(baseDir, ".claude", "skills", "kb-skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skillMD), "/opt/tools") {
		t.Errorf("placeholder not expanded in SKILL.md: %s", skillMD)
	}
	notes, err := os.ReadFile(filepath.Join(baseDir, ".claude", "skills", "kb-skill", "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(notes) != "No placeholder here.\n" {
		t.Errorf("notes.md should not have changed: %s", notes)
	}
}

// --- Apply: instructions (group) ---

func instructionsArtifactForExpandTest(name, content string) Artifact {
	h := sha256.Sum256([]byte(content))
	return Artifact{
		Kind:        "instructions",
		Name:        name,
		Source:      "kb:" + name,
		ContentHash: fmt.Sprintf("%x", h),
		Signed:      true,
		Files:       []ArtifactFile{{Path: "instructions.md", Content: []byte(content)}},
	}
}

func TestApply_ExpandPlaceholders_Instructions(t *testing.T) {
	baseDir := t.TempDir()
	a := instructionsArtifactForExpandTest("homelab", "Working repo: {{repo:homelab}}.\n")
	m := MergeArtifacts([]Artifact{a})

	res, err := Apply(m, ApplyOptions{
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
		Paths:              map[string]string{"homelab": "/srv/homelab"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/srv/homelab") {
		t.Errorf("placeholder not expanded in the instructions block: %s", data)
	}
	if len(res.Written) != 1 || res.Written[0].ContentHash == a.ContentHash {
		t.Errorf("expected a hash different from the manifest's (expanded content): %+v", res.Written)
	}
	if len(res.NewLock.Managed) != 1 || res.NewLock.Managed[0].ContentHash == a.ContentHash {
		t.Errorf("Lock.Managed must reflect the expanded hash: %+v", res.NewLock.Managed)
	}
}

func TestApply_ExpandPlaceholders_ZeroDriftInstructions(t *testing.T) {
	baseDir := t.TempDir()
	a := instructionsArtifactForExpandTest("homelab", "No placeholder here.\n")
	m := MergeArtifacts([]Artifact{a})

	res, err := Apply(m, ApplyOptions{
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) != 1 || res.Written[0].ContentHash != a.ContentHash {
		t.Errorf("zero-drift violated for instructions: %+v (want hash %q)", res.Written, a.ContentHash)
	}
	if len(res.NewLock.Managed) != 1 || res.NewLock.Managed[0].ContentHash != a.ContentHash {
		t.Errorf("Lock.Managed zero-drift violated: %+v", res.NewLock.Managed)
	}
}

// --- buildPathsTable (unit, D75 WP4) ---

func TestBuildPathsTable_Empty(t *testing.T) {
	if got := buildPathsTable(map[string]string{}); got != "" {
		t.Errorf("buildPathsTable(empty) = %q, want \"\"", got)
	}
}

func TestBuildPathsTable_SortedRows(t *testing.T) {
	table := buildPathsTable(map[string]string{
		"repo:zeta":  "/z",
		"path:alpha": "/a",
	})
	zIdx := strings.Index(table, "{{repo:zeta}}")
	aIdx := strings.Index(table, "{{path:alpha}}")
	if zIdx == -1 || aIdx == -1 || aIdx > zIdx {
		t.Errorf("rows not sorted by key:\n%s", table)
	}
	if !strings.Contains(table, "`cartographer resolve") {
		t.Errorf("fallback instruction missing:\n%s", table)
	}
}

// --- Apply: "Local paths" table in the instructions block (D75 WP4) ---

func TestApply_ExpandPlaceholders_PathsTableInInstructions(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	// The placeholder lives in the agent, not in the instructions: the table
	// must still collect it, being shared across all artifacts materialized
	// in this same Apply (tracker, not per-kind).
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nThe assets are in {{path:design}}.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	_, err = Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
		Paths:              map[string]string{"design": "/mnt/design"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Local paths") {
		t.Errorf("Local paths table missing from the instructions block:\n%s", content)
	}
	if !strings.Contains(content, "{{path:design}}") || !strings.Contains(content, "/mnt/design") {
		t.Errorf("row for path:design missing from the table:\n%s", content)
	}
	if !strings.Contains(content, "cartographer resolve") {
		t.Errorf("fallback instruction missing from the instructions block:\n%s", content)
	}
}

func TestApply_ExpandPlaceholders_NoPathsTableWhenNothingResolved(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nNo placeholder here.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	_, err = Apply(m, ApplyOptions{
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Local paths") {
		t.Errorf("Local paths table should not appear without resolved placeholders:\n%s", data)
	}
}

func TestApply_ExpandPlaceholders_NoPathsTableServerSide(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nThe assets are in {{path:design}}.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	// ExpandPlaceholders never set, like internal/mcpserver.
	_, err = Apply(m, ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Local paths") {
		t.Errorf("the server must never add the Local paths table:\n%s", data)
	}
}
