package provisioning

// Tests for the provenance stamp and the source/materialized hash split
// (D138). White-box: the stamping helpers are unexported.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func countBlocks(s string) int { return strings.Count(s, provenanceBlockBeginPrefix) }

func TestStampArtifact_OnlyStampedKinds(t *testing.T) {
	content := []byte("# Body\n")
	for kind, want := range map[string]bool{
		"skill": true, "agent": true,
		"hook": false, "mcp": false, "instructions": false,
	} {
		got := stampArtifact(content, Artifact{Kind: kind, Name: "x", Source: "kb:wiki", ContentHash: "h"})
		stamped := countBlocks(string(got)) == 1
		if stamped != want {
			t.Errorf("kind %q: stamped=%v, want %v (%s)", kind, stamped, want, got)
		}
		if !want && string(got) != string(content) {
			t.Errorf("kind %q: content must be byte-identical, got %q", kind, got)
		}
	}
}

func TestStampArtifact_IsAFixedPoint(t *testing.T) {
	a := Artifact{Kind: "skill", Name: "runbooks", Source: "kb:wiki", ContentHash: "abc123"}
	once := stampArtifact([]byte("# Runbooks\n\nBody.\n"), a)
	twice := stampArtifact(once, a)
	if string(once) != string(twice) {
		t.Errorf("stamping twice is not a fixed point:\n%q\nvs\n%q", once, twice)
	}
	if countBlocks(string(twice)) != 1 {
		t.Errorf("expected exactly one block after restamping:\n%s", twice)
	}

	// A block written by another version — different display text after the
	// marker prefix — is replaced, not nested.
	older := "# Runbooks\n\n<!-- cartographer:provenance:begin — blocco gestito -->\nstale\n<!-- cartographer:provenance:end -->\n"
	restamped := stampArtifact([]byte(older), a)
	if countBlocks(string(restamped)) != 1 || strings.Contains(string(restamped), "stale") {
		t.Errorf("an older block was not replaced:\n%s", restamped)
	}
}

func TestStampArtifact_Content(t *testing.T) {
	kb := stampArtifact([]byte("# Body\n"), Artifact{Kind: "skill", Name: "runbooks", Source: "kb:wiki", ContentHash: "abc123"})
	for _, want := range []string{`KB "wiki"`, "skills/runbooks/SKILL.md", "abc123", "artifact_write"} {
		if !strings.Contains(string(kb), want) {
			t.Errorf("KB stamp does not mention %q:\n%s", want, kb)
		}
	}
	// No timestamp, no manifest revision: both would rewrite every file on
	// every sync and defeat hash comparison.
	if strings.Contains(string(kb), "revision") {
		t.Errorf("stamp must not carry the manifest revision:\n%s", kb)
	}

	bundled := stampArtifact([]byte("# Body\n"), Artifact{Kind: "skill", Name: "kb-create", Source: "bundle", BuiltIn: true, ContentHash: "def456"})
	if strings.Contains(string(bundled), "artifact_write") {
		t.Errorf("a bundled artifact has no KB to write back to:\n%s", bundled)
	}
	if !strings.Contains(string(bundled), "bundled with the Cartographer server") {
		t.Errorf("bundled stamp does not state its source:\n%s", bundled)
	}

	agent := stampArtifact([]byte("Body.\n"), Artifact{Kind: "agent", Name: "reviewer", Source: "kb:wiki", ContentHash: "h"})
	if !strings.Contains(string(agent), "agents/reviewer.md") {
		t.Errorf("agent stamp does not name its KB path:\n%s", agent)
	}

	// Empty content: block only, no leading blank lines.
	empty := stampArtifact(nil, Artifact{Kind: "skill", Name: "x", Source: "kb:wiki", ContentHash: "h"})
	if !strings.HasPrefix(string(empty), provenanceBlockBeginPrefix) {
		t.Errorf("empty content should yield the block alone, got %q", empty)
	}
}

// A skill with no SKILL.md — reachable from a remote server via sync_pull,
// which ships the artifact's files in memory — is materialized anyway, with a
// warning: failing the whole sync over one malformed KB artifact would be
// disproportionate.
func TestApply_SkillWithoutPrincipalFile_WarnsAndMaterializes(t *testing.T) {
	m := Manifest{Revision: "r1", Artifacts: []Artifact{{
		Kind:        "skill",
		Name:        "broken",
		Source:      "kb:wiki",
		ContentHash: "h",
		Files:       []ArtifactFile{{Path: "notes.md", Content: []byte("no SKILL.md here\n")}},
	}}}

	baseDir := t.TempDir()
	res, err := Apply(m, ApplyOptions{AutoTrust: true, Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: Lock{}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "skills", "broken", "notes.md"))
	if err != nil {
		t.Fatalf("skill not materialized: %v", err)
	}
	if string(data) != "no SKILL.md here\n" {
		t.Errorf("a non-principal file must be copied untouched, got %q", data)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "SKILL.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning about the missing SKILL.md, got %v", res.Warnings)
	}
}

// Hooks and MCP descriptors are never stamped: a comment would change program
// semantics, and JSON has no comment syntax.
func TestApply_HookFilesAreByteIdentical(t *testing.T) {
	script := "#!/bin/sh\necho hi\n"
	hookJSON := "{\"event\":\"SessionStart\",\"command\":\"./run.sh\"}\n"
	m := Manifest{Revision: "r1", Artifacts: []Artifact{{
		Kind:        "hook",
		Name:        "greet",
		Source:      "kb:wiki",
		ContentHash: "h",
		Files: []ArtifactFile{
			{Path: "run.sh", Content: []byte(script), Executable: true},
			{Path: "hook.json", Content: []byte(hookJSON)},
		},
	}}}

	baseDir := t.TempDir()
	if _, err := Apply(m, ApplyOptions{AutoTrust: true, Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: Lock{}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for name, want := range map[string]string{"run.sh": script, "hook.json": hookJSON} {
		data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "hooks", "greet", name))
		if err != nil {
			t.Fatalf("hook file %s: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("%s is not byte-identical to the source:\n%q", name, data)
		}
	}
}

// The regression guard for the latent defect: an artifact carrying a
// placeholder used to be reported Updated on every sync, because the expanded
// hash was compared against the manifest's source hash.
func TestApply_PlaceholderArtifact_NoDriftOnResync(t *testing.T) {
	kbRoot := t.TempDir()
	os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755)
	os.WriteFile(filepath.Join(kbRoot, "agents", "reviewer.md"),
		[]byte("---\nname: reviewer\n---\nThe assets are in {{path:design}}.\n"), 0o644)
	skillDir := filepath.Join(kbRoot, "skills", "runbooks")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: runbooks\ndescription: d\n---\nSee {{path:design}}.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	opts := ApplyOptions{
		AutoTrust:          true,
		KBRoots:            map[string]string{"kb": kbRoot},
		Provider:           configurator.ProviderClaudeCode,
		BaseDir:            baseDir,
		Lock:               Lock{},
		ExpandPlaceholders: true,
		Paths:              map[string]string{"design": "/mnt/design"},
	}
	first, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	skillPath := filepath.Join(baseDir, ".claude", "skills", "runbooks", "SKILL.md")
	before, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read materialized skill: %v", err)
	}

	// Same manifest, lock from the first Apply: nothing has changed.
	if d := ComputeDiff(m, first.NewLock); len(d.Updated) != 0 {
		t.Errorf("re-sync of an unchanged manifest reports %d Updated: %+v", len(d.Updated), d.Updated)
	}
	opts.Lock = first.NewLock
	if _, err := Apply(m, opts); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	after, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read materialized skill again: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("second Apply rewrote the file:\n%q\nvs\n%q", before, after)
	}
	if countBlocks(string(after)) != 1 {
		t.Errorf("expected exactly one provenance block after two applies:\n%s", after)
	}
	if !strings.Contains(string(after), "/mnt/design") {
		t.Errorf("placeholder not expanded:\n%s", after)
	}
}
