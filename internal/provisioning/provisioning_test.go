package provisioning_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// test bundleFS with a single bundled skill.
func makeBundleFS(skillContent string) fstest.MapFS {
	return fstest.MapFS{
		"bundled/kb-create/SKILL.md": &fstest.MapFile{
			Data: []byte("---\nname: kb-create\ndescription: Guide KB creation\nversion: \"1.0\"\n---\n" + skillContent),
		},
	}
}

// --- ContentHashDir ---

func TestContentHashDir_Determinismo(t *testing.T) {
	fsys := fstest.MapFS{
		"skill/SKILL.md": &fstest.MapFile{Data: []byte("content")},
		"skill/extra.md": &fstest.MapFile{Data: []byte("extra")},
	}

	h1, err := provisioning.ContentHashDir(fsys, "skill")
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	h2, err := provisioning.ContentHashDir(fsys, "skill")
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("ContentHashDir not deterministic: %q != %q", h1, h2)
	}
}

func TestContentHashDir_CambioContenuto(t *testing.T) {
	fsys1 := fstest.MapFS{
		"skill/SKILL.md": &fstest.MapFile{Data: []byte("version-one")},
	}
	fsys2 := fstest.MapFS{
		"skill/SKILL.md": &fstest.MapFile{Data: []byte("version-two")},
	}

	h1, _ := provisioning.ContentHashDir(fsys1, "skill")
	h2, _ := provisioning.ContentHashDir(fsys2, "skill")
	if h1 == h2 {
		t.Error("ContentHashDir: different content must produce a different hash")
	}
}

func TestContentHashDir_OrdineFile(t *testing.T) {
	// File order must not affect the hash: same content → same hash.
	fsys1 := fstest.MapFS{
		"skill/a.md": &fstest.MapFile{Data: []byte("aaa")},
		"skill/b.md": &fstest.MapFile{Data: []byte("bbb")},
	}
	fsys2 := fstest.MapFS{
		"skill/b.md": &fstest.MapFile{Data: []byte("bbb")},
		"skill/a.md": &fstest.MapFile{Data: []byte("aaa")},
	}

	h1, _ := provisioning.ContentHashDir(fsys1, "skill")
	h2, _ := provisioning.ContentHashDir(fsys2, "skill")
	if h1 != h2 {
		t.Errorf("ContentHashDir: iteration order must not change the hash: %q != %q", h1, h2)
	}
}

// --- BuildManifest ---

func TestBuildManifest_BundledSkillInventory(t *testing.T) {
	m, err := provisioning.BuildManifest(skillbundle.FS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest bundled skills: %v", err)
	}

	found := make(map[string]bool)
	for _, artifact := range m.Artifacts {
		if artifact.Kind == "skill" && artifact.Source == "bundle" {
			found[artifact.Name] = true
		}
	}
	for _, name := range []string{"cartographer-ops", "kb-conflict-resolve", "kb-create", "kb-import"} {
		if !found[name] {
			t.Errorf("bundled skill %q missing from manifest", name)
		}
	}
}

func TestBuildManifest_RevisioneDeterministica(t *testing.T) {
	bundleFS := makeBundleFS("Body.")

	m1, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	m2, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	if m1.Revision == "" {
		t.Fatal("BuildManifest: empty revision")
	}
	if m1.Revision != m2.Revision {
		t.Errorf("BuildManifest: non-deterministic revision: %q != %q", m1.Revision, m2.Revision)
	}
}

func TestBuildManifest_CambioContenuto(t *testing.T) {
	bundleFS1 := makeBundleFS("Original body.")
	bundleFS2 := makeBundleFS("Modified body.")

	m1, _ := provisioning.BuildManifest(bundleFS1, nil, false)
	m2, _ := provisioning.BuildManifest(bundleFS2, nil, false)

	if m1.Revision == m2.Revision {
		t.Error("BuildManifest: different content must produce a different revision")
	}
}

func TestBuildManifest_SkillBundleSigned(t *testing.T) {
	bundleFS := makeBundleFS("Body.")
	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if len(m.Artifacts) == 0 {
		t.Fatal("BuildManifest: no artifacts")
	}
	for _, a := range m.Artifacts {
		if a.Source == "bundle" && !a.Signed {
			t.Errorf("bundle skill %q must have Signed:true", a.Name)
		}
	}
}

func TestBuildManifest_KBSkillAutoTrust(t *testing.T) {
	// Create a test KB with a skill.
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "my-skill")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: my-skill\ndescription: Test\n---\nBody.\n"), 0o644)

	// autoTrust=false → Signed:false
	m1, err := provisioning.BuildManifest(nil, map[string]string{"test-kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest autoTrust=false: %v", err)
	}
	for _, a := range m1.Artifacts {
		if a.Source == "kb:test-kb" && a.Signed {
			t.Errorf("KB skill %q with autoTrust=false must have Signed:false", a.Name)
		}
	}

	// autoTrust=true → Signed:true
	m2, err := provisioning.BuildManifest(nil, map[string]string{"test-kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest autoTrust=true: %v", err)
	}
	for _, a := range m2.Artifacts {
		if a.Source == "kb:test-kb" && !a.Signed {
			t.Errorf("KB skill %q with autoTrust=true must have Signed:true", a.Name)
		}
	}
}

func TestBuildManifest_DedupBundleVsKB(t *testing.T) {
	// skill_install case: the same "kb-create" skill exists both in the bundle and the KB.
	// The manifest must contain it ONLY once, with the KB taking precedence.
	bundleFS := makeBundleFS("Bundle body.")

	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "kb-create")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: kb-create\ndescription: KB version\n---\nKB body.\n"), 0o644)

	m, err := provisioning.BuildManifest(bundleFS, map[string]string{"mia-kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	var count int
	var chosen provisioning.Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "skill" && a.Name == "kb-create" {
			count++
			chosen = a
		}
	}
	if count != 1 {
		t.Fatalf("dedup: expected 1 kb-create artifact, got %d", count)
	}
	if chosen.Source != "kb:mia-kb" {
		t.Errorf("dedup: expected precedence for the KB, got source %q", chosen.Source)
	}

	// The revision must remain deterministic even with the collision.
	m2, _ := provisioning.BuildManifest(bundleFS, map[string]string{"mia-kb": kbRoot}, true)
	if m.Revision != m2.Revision {
		t.Errorf("dedup: non-deterministic revision: %q != %q", m.Revision, m2.Revision)
	}
}

// --- ReadLock / WriteLock ---

func TestReadWriteLock_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock.json")

	lock := provisioning.Lock{
		AppliedRevision: "abc123",
		Provider:        "claude",
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "kb-create", Path: ".claude/skills/kb-create/SKILL.md", ContentHash: "deadbeef"},
		},
	}

	if err := provisioning.WriteLock(lockPath, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	got, err := provisioning.ReadLock(lockPath)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if got.AppliedRevision != lock.AppliedRevision {
		t.Errorf("AppliedRevision: expected %q, got %q", lock.AppliedRevision, got.AppliedRevision)
	}
	if len(got.Managed) != 1 || got.Managed[0].Name != "kb-create" {
		t.Errorf("Managed: expected [kb-create], got %v", got.Managed)
	}
}

func TestReadLock_FileNonEsistente(t *testing.T) {
	lock, err := provisioning.ReadLock("/tmp/nonexistent-cartographer-test.lock.json")
	if err != nil {
		t.Fatalf("ReadLock on a non-existent file must return Lock{} without error: %v", err)
	}
	if lock.AppliedRevision != "" || len(lock.Managed) != 0 {
		t.Errorf("ReadLock on a non-existent file must return Lock{}: %+v", lock)
	}
}

// --- ComputeDiff ---

func TestComputeDiff_InSync(t *testing.T) {
	m := provisioning.Manifest{
		Revision: "rev1",
		Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "kb-create", Source: "bundle", ContentHash: "hash1", Signed: true},
		},
	}
	lock := provisioning.Lock{
		AppliedRevision: "rev1",
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "kb-create", Path: ".claude/skills/kb-create/SKILL.md", ContentHash: "hash1"},
		},
	}
	d := provisioning.ComputeDiff(m, lock)
	if !d.InSync {
		t.Error("ComputeDiff: expected InSync=true with identical revisions")
	}
	if len(d.Added) != 0 || len(d.Updated) != 0 || len(d.Removed) != 0 {
		t.Errorf("ComputeDiff InSync: expected no diff, got: %+v", d)
	}
}

func TestComputeDiff_Added(t *testing.T) {
	m := provisioning.Manifest{
		Revision: "rev2",
		Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "nuova-skill", Source: "bundle", ContentHash: "hashN", Signed: true},
		},
	}
	lock := provisioning.Lock{
		AppliedRevision: "rev1",
		Managed:         []provisioning.ManagedFile{},
	}
	d := provisioning.ComputeDiff(m, lock)
	if len(d.Added) != 1 || d.Added[0].Name != "nuova-skill" {
		t.Errorf("ComputeDiff Added: expected [nuova-skill], got: %v", d.Added)
	}
}

func TestComputeDiff_Updated(t *testing.T) {
	m := provisioning.Manifest{
		Revision: "rev2",
		Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "kb-create", Source: "bundle", ContentHash: "hash-nuovo", Signed: true},
		},
	}
	lock := provisioning.Lock{
		AppliedRevision: "rev1",
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "kb-create", Path: ".claude/skills/kb-create/SKILL.md", ContentHash: "hash-vecchio"},
		},
	}
	d := provisioning.ComputeDiff(m, lock)
	if len(d.Updated) != 1 || d.Updated[0].Name != "kb-create" {
		t.Errorf("ComputeDiff Updated: expected [kb-create], got: %v", d.Updated)
	}
}

func TestComputeDiff_Removed(t *testing.T) {
	m := provisioning.Manifest{
		Revision:  "rev2",
		Artifacts: []provisioning.Artifact{},
	}
	lock := provisioning.Lock{
		AppliedRevision: "rev1",
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "skill-vecchia", Path: ".claude/skills/skill-vecchia/SKILL.md", ContentHash: "hashV"},
		},
	}
	d := provisioning.ComputeDiff(m, lock)
	if len(d.Removed) != 1 || d.Removed[0].Name != "skill-vecchia" {
		t.Errorf("ComputeDiff Removed: expected [skill-vecchia], got: %v", d.Removed)
	}
}

// --- Apply ---

func TestApply_ScriveTrusted(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Body skill bundled.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		KBRoots:  nil,
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		DryRun:   false,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify SKILL.md was written.
	skillPath := filepath.Join(baseDir, ".claude", "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("Apply: SKILL.md not found at %s: %v", skillPath, err)
	}

	// Verify the lock was written.
	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("Apply: lockfile not found at %s: %v", lockPath, err)
	}

	// Verify Written.
	if len(res.Written) == 0 {
		t.Error("Apply: Written empty, expected at least one file")
	}
	// Verify applied revision.
	if res.NewLock.AppliedRevision != m.Revision {
		t.Errorf("Apply: AppliedRevision %q != manifest %q", res.NewLock.AppliedRevision, m.Revision)
	}
}

func TestApply_SaltaNonSigned(t *testing.T) {
	baseDir := t.TempDir()

	// KB skill with autoTrust=false → Signed:false.
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "kb-skill")
	os.MkdirAll(skillDir, 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: kb-skill\ndescription: Test\n---\nBody.\n"), 0o644)

	m, err := provisioning.BuildManifest(nil, map[string]string{"mia-kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		KBRoots:  map[string]string{"mia-kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		DryRun:   false,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Unsigned skill: no file written, goes to NeedsApproval.
	if len(res.Written) != 0 {
		t.Errorf("Apply: expected no file written for an unsigned skill, written: %v", res.Written)
	}
	if len(res.NeedsApproval) == 0 {
		t.Error("Apply: expected at least one artifact in NeedsApproval")
	}
}

func TestApply_PruneObsoleto(t *testing.T) {
	baseDir := t.TempDir()

	// Create the "obsolete" file that was managed by the previous lock.
	obsoleteDir := filepath.Join(baseDir, ".claude", "skills", "skill-obsoleta")
	os.MkdirAll(obsoleteDir, 0o755)
	obsoleteFile := filepath.Join(obsoleteDir, "SKILL.md")
	os.WriteFile(obsoleteFile, []byte("obsolete"), 0o644)

	// Empty manifest (no skills) but a lock with the obsolete skill.
	m := provisioning.Manifest{
		Revision:  "rev-nuovo",
		Artifacts: []provisioning.Artifact{},
	}

	lock := provisioning.Lock{
		AppliedRevision: "rev-vecchio",
		Provider:        "claude",
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "skill-obsoleta", Path: ".claude/skills/skill-obsoleta/SKILL.md", ContentHash: "old"},
		},
	}

	opts := provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		DryRun:   false,
		Lock:     lock,
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply prune: %v", err)
	}

	// The obsolete file must have been removed.
	if _, err := os.Stat(obsoleteFile); !os.IsNotExist(err) {
		t.Errorf("Apply prune: obsolete file not removed: %v", err)
	}
	if len(res.Pruned) == 0 {
		t.Error("Apply prune: Pruned empty, expected the obsolete file")
	}
}

func TestApply_DryRun(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Body dry run.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		DryRun:   true,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply DryRun: %v", err)
	}

	// DryRun: no file written to disk.
	skillPath := filepath.Join(baseDir, ".claude", "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		t.Error("Apply DryRun: SKILL.md must not be written in DryRun")
	}
	// DryRun: lockfile not written.
	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	if _, err := os.Stat(lockPath); err == nil {
		t.Error("Apply DryRun: lockfile must not be written in DryRun")
	}
	// But Written must contain the paths that would have been written.
	if len(res.Written) == 0 {
		t.Error("Apply DryRun: Written must contain the expected paths")
	}
}

func TestApply_Idempotente(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Idempotent body.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}

	res1, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Second Apply with the lock resulting from the first.
	opts.Lock = res1.NewLock
	res2, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}

	// The second Apply must write nothing (already in sync).
	if len(res2.Written) != 0 {
		t.Errorf("second idempotent Apply: Written not empty: %v", res2.Written)
	}
	if len(res2.Pruned) != 0 {
		t.Errorf("second idempotent Apply: Pruned not empty: %v", res2.Pruned)
	}
}

func TestApply_OpenCode_Materializza(t *testing.T) {
	// Bundled skill (Signed:true) with Provider=opencode:
	// must be materialized in .opencode/skills/<name>/ and NOT in NeedsApproval.
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("OpenCode test body.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		KBRoots:  nil,
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		DryRun:   false,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// No artifact must end up in NeedsApproval (the skill is signed=true).
	if len(res.NeedsApproval) != 0 {
		t.Errorf("Apply opencode: expected empty NeedsApproval, got: %v", res.NeedsApproval)
	}

	// SKILL.md must be written under .opencode/skills/<name>/.
	skillPath := filepath.Join(baseDir, ".opencode", "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("Apply opencode: SKILL.md not found at %s: %v", skillPath, err)
	}

	// Verify Written not empty and lockfile written.
	if len(res.Written) == 0 {
		t.Error("Apply opencode: Written empty, expected at least one file")
	}
	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("Apply opencode: lockfile not found: %v", err)
	}
}

func TestApply_Codex_Materializza(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Codex test body.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		KBRoots:  nil,
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		DryRun:   false,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(res.NeedsApproval) != 0 {
		t.Errorf("Apply codex: expected empty NeedsApproval, got: %v", res.NeedsApproval)
	}

	skillPath := filepath.Join(baseDir, ".codex", "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("Apply codex: SKILL.md not found at %s: %v", skillPath, err)
	}

	if len(res.Written) == 0 {
		t.Error("Apply codex: Written empty, expected at least one file")
	}
	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("Apply codex: lockfile not found: %v", err)
	}
}

func TestApply_Kiro_Materializza(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Kiro test body.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		KBRoots:  nil,
		Provider: configurator.ProviderKiro,
		BaseDir:  baseDir,
		DryRun:   false,
		Lock:     provisioning.Lock{},
	}

	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(res.NeedsApproval) != 0 {
		t.Errorf("Apply kiro: expected empty NeedsApproval, got: %v", res.NeedsApproval)
	}

	skillPath := filepath.Join(baseDir, ".kiro", "skills", "kb-create", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("Apply kiro: SKILL.md not found at %s: %v", skillPath, err)
	}

	if len(res.Written) == 0 {
		t.Error("Apply kiro: Written empty, expected at least one file")
	}
	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("Apply kiro: lockfile not found: %v", err)
	}
}

func TestApply_ScriveLockfile(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Lock test body.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS: bundleFS,
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}

	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	var lock provisioning.Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("parse lockfile: %v", err)
	}
	if lock.AppliedRevision != m.Revision {
		t.Errorf("lockfile: AppliedRevision %q != manifest %q", lock.AppliedRevision, m.Revision)
	}
	if lock.Provider != "claude" {
		t.Errorf("lockfile: Provider %q != claude", lock.Provider)
	}
}

// --- MergeArtifacts ---

func TestMergeArtifacts_DedupPrecedenzaKB(t *testing.T) {
	artifacts := []provisioning.Artifact{
		{Kind: "skill", Name: "kb-create", Source: "bundle", ContentHash: "hash-bundle", Signed: true},
		{Kind: "skill", Name: "kb-create", Source: "kb:homelab", ContentHash: "hash-kb", Signed: false},
	}
	m := provisioning.MergeArtifacts(artifacts)
	if len(m.Artifacts) != 1 {
		t.Fatalf("MergeArtifacts: expected 1 artifact after dedup, got %d", len(m.Artifacts))
	}
	if m.Artifacts[0].Source != "kb:homelab" {
		t.Errorf("MergeArtifacts: expected the KB to take precedence over the bundle, got source=%q", m.Artifacts[0].Source)
	}
}

func TestMergeArtifacts_RevisioneDeterministica(t *testing.T) {
	a := []provisioning.Artifact{
		{Kind: "skill", Name: "b", Source: "bundle", ContentHash: "hb"},
		{Kind: "skill", Name: "a", Source: "bundle", ContentHash: "ha"},
	}
	m1 := provisioning.MergeArtifacts(a)
	// Reversed input order → same revision (deterministic internal sorting).
	m2 := provisioning.MergeArtifacts([]provisioning.Artifact{a[1], a[0]})
	if m1.Revision != m2.Revision {
		t.Errorf("MergeArtifacts: revision not deterministic with respect to input order: %q != %q", m1.Revision, m2.Revision)
	}
}

// --- ReadArtifactFiles ---

func TestReadArtifactFiles_Bundle(t *testing.T) {
	bundleFS := makeBundleFS("Body for ReadArtifactFiles.")
	a := provisioning.Artifact{Kind: "skill", Name: "kb-create", Source: "bundle"}

	files, err := provisioning.ReadArtifactFiles(a, bundleFS, nil)
	if err != nil {
		t.Fatalf("ReadArtifactFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("ReadArtifactFiles: expected 1 SKILL.md file, got %+v", files)
	}
	if !strings.Contains(string(files[0].Content), "Body for ReadArtifactFiles.") {
		t.Errorf("ReadArtifactFiles: unexpected content: %s", files[0].Content)
	}
}

func TestReadArtifactFiles_KB(t *testing.T) {
	kbRoot := t.TempDir()
	skillDir := filepath.Join(kbRoot, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: my-skill\ndescription: d\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := provisioning.Artifact{Kind: "skill", Name: "my-skill", Source: "kb:homelab"}
	files, err := provisioning.ReadArtifactFiles(a, nil, map[string]string{"homelab": kbRoot})
	if err != nil {
		t.Fatalf("ReadArtifactFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("ReadArtifactFiles: expected 1 SKILL.md file, got %+v", files)
	}
}

// --- Apply with Artifact.Files (in-memory materialization, remote client) ---

func TestApply_ArtifactFiles_InMemory(t *testing.T) {
	baseDir := t.TempDir()
	a := provisioning.Artifact{
		Kind: "skill", Name: "remote-skill", Source: "kb:homelab",
		ContentHash: "hash1", Signed: true,
		Files: []provisioning.ArtifactFile{
			{Path: "SKILL.md", Content: []byte("---\nname: remote-skill\n---\nBody.\n")},
		},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	opts := provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) != 1 {
		t.Fatalf("Apply: expected 1 file written, got %d", len(res.Written))
	}
	skillPath := filepath.Join(baseDir, ".claude", "skills", "remote-skill", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("Apply: SKILL.md not found at %s: %v", skillPath, err)
	}
}

func TestApply_RifiutaPathTraversal(t *testing.T) {
	baseDir := t.TempDir()

	cases := []provisioning.Artifact{
		{Kind: "skill", Name: "evil", Source: "kb:x", ContentHash: "h1", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: "../../escape.md", Content: []byte("x")}}},
		{Kind: "skill", Name: "evil", Source: "kb:x", ContentHash: "h2", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: "/etc/escape.md", Content: []byte("x")}}},
		{Kind: "skill", Name: "../evil", Source: "kb:x", ContentHash: "h3", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("x")}}},
	}
	for i, a := range cases {
		m := provisioning.MergeArtifacts([]provisioning.Artifact{a})
		opts := provisioning.ApplyOptions{
			Provider: configurator.ProviderClaudeCode,
			BaseDir:  baseDir,
			Lock:     provisioning.Lock{},
		}
		if _, err := provisioning.Apply(m, opts); err == nil {
			t.Errorf("case %d: Apply should have rejected the artifact with the malicious path", i)
		}
	}
	if _, err := os.Stat(filepath.Join(baseDir, "..", "escape.md")); err == nil {
		t.Fatal("file written outside baseDir")
	}
}

// --- SkipLockWrite ---

func TestApply_SkipLockWrite(t *testing.T) {
	baseDir := t.TempDir()
	bundleFS := makeBundleFS("Skip lock body.")

	m, err := provisioning.BuildManifest(bundleFS, nil, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	opts := provisioning.ApplyOptions{
		BundleFS:      bundleFS,
		Provider:      configurator.ProviderClaudeCode,
		BaseDir:       baseDir,
		Lock:          provisioning.Lock{},
		SkipLockWrite: true,
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.NewLock.AppliedRevision != m.Revision {
		t.Errorf("Apply: NewLock must still be computed even with SkipLockWrite")
	}

	lockPath := filepath.Join(baseDir, provisioning.LockFileName)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("Apply: with SkipLockWrite the lockfile must not be written to disk, stat err=%v", err)
	}
}

// --- LockFile v2 (multi-provider) ---

func TestLockFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, provisioning.LockFileName)

	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{}}
	lf.SetProvider("claude", provisioning.Lock{
		AppliedRevision: "rev1",
		Managed:         []provisioning.ManagedFile{{Kind: "skill", Name: "kb-create", Path: ".claude/skills/kb-create/SKILL.md", ContentHash: "h1"}},
	})
	lf.SetProvider("opencode", provisioning.Lock{AppliedRevision: "rev1"})

	if err := provisioning.WriteLockFile(path, lf); err != nil {
		t.Fatalf("WriteLockFile: %v", err)
	}

	loaded, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if len(loaded.Providers) != 2 {
		t.Fatalf("ReadLockFile: expected 2 providers, got %d", len(loaded.Providers))
	}
	claude := loaded.ForProvider("claude")
	if claude.AppliedRevision != "rev1" || len(claude.Managed) != 1 {
		t.Errorf("ReadLockFile: unexpected claude lock: %+v", claude)
	}
}

func TestReadLockFile_NotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), provisioning.LockFileName)
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatalf("ReadLockFile on a non-existent file must not error: %v", err)
	}
	if len(lf.Providers) != 0 {
		t.Errorf("ReadLockFile on a non-existent file: expected 0 providers, got %d", len(lf.Providers))
	}
}

func TestReadLockFile_MigrazioneV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, provisioning.LockFileName)

	// Write a v1 lockfile (legacy format, top-level "provider" field).
	v1 := provisioning.Lock{
		AppliedRevision: "rev-legacy",
		Provider:        "claude",
		Managed:         []provisioning.ManagedFile{{Kind: "skill", Name: "kb-create", Path: ".claude/skills/kb-create/SKILL.md", ContentHash: "h1"}},
	}
	if err := provisioning.WriteLock(path, v1); err != nil {
		t.Fatalf("WriteLock (v1): %v", err)
	}

	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		t.Fatalf("ReadLockFile: v1 migration failed: %v", err)
	}
	claude := lf.ForProvider("claude")
	if claude.AppliedRevision != "rev-legacy" {
		t.Errorf("ReadLockFile: v1 migration missing, AppliedRevision=%q", claude.AppliedRevision)
	}
	if len(claude.Managed) != 1 {
		t.Errorf("ReadLockFile: v1 migration did not preserve Managed: %+v", claude.Managed)
	}
}
