package provisioning

// Delivery to the Hermes inbox (D141): Cartographer proposes, the agent
// adopts. The invariant these tests guard is that nothing is ever written
// under HERMES_HOME/skills/ — a regression there destroys the agent's own
// curated learning, which no sync can restore.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// hermesKB writes a one-skill KB and returns its root.
func hermesKB(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "runbooks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: runbooks\ndescription: d\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func hermesApplyOptions(kbRoot, baseDir string, lock Lock) ApplyOptions {
	return ApplyOptions{
		AutoTrust: true,
		KBRoots:   map[string]string{"kb": kbRoot},
		Provider:  configurator.ProviderHermes,
		BaseDir:   baseDir,
		Lock:      lock,
	}
}

func TestApply_HermesDeliversToInbox(t *testing.T) {
	kbRoot := hermesKB(t, "The runbooks.\n")
	// An agent artifact too: hermes has no subagent directory, so it must land
	// in Unsupported rather than anywhere on disk.
	if err := os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kbRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	res, err := Apply(m, hermesApplyOptions(kbRoot, baseDir, Lock{}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	inbox := filepath.Join(baseDir, "skill-inbox", "runbooks", "cartographer")
	skillMD, err := os.ReadFile(filepath.Join(inbox, "SKILL.md"))
	if err != nil {
		t.Fatalf("delivered SKILL.md: %v", err)
	}
	if !strings.Contains(string(skillMD), provenanceBlockBeginPrefix) {
		t.Error("delivered SKILL.md carries no provenance block")
	}

	source, err := os.ReadFile(filepath.Join(inbox, hermesSourceFile))
	if err != nil {
		t.Fatalf("delivered SOURCE.md: %v", err)
	}
	var skillHash string
	for _, a := range m.Artifacts {
		if a.Kind == "skill" && a.Name == "runbooks" {
			skillHash = a.ContentHash
		}
	}
	for _, want := range []string{"proposal", `KB "kb"`, "skills/runbooks/SKILL.md", skillHash, "skill_manage", "artifact_write"} {
		if !strings.Contains(string(source), want) {
			t.Errorf("SOURCE.md does not mention %q:\n%s", want, source)
		}
	}

	// The invariant: the agent's own skills directory is never created.
	if _, err := os.Stat(filepath.Join(baseDir, "skills")); !os.IsNotExist(err) {
		t.Fatalf("HERMES_HOME/skills/ must never be written: stat = %v", err)
	}

	for _, a := range res.Unsupported {
		if a.Kind == "skill" {
			t.Errorf("skill %q reported unsupported for hermes", a.Name)
		}
	}
	unsupported := map[string]bool{}
	for _, a := range res.Unsupported {
		unsupported[a.Kind] = true
	}
	for _, kind := range []string{"agent", "instructions"} {
		if !unsupported[kind] {
			t.Errorf("kind %q should be unsupported for hermes, got %+v", kind, res.Unsupported)
		}
	}

	// Both delivered files are managed, so D139 verification covers them —
	// including the generated SOURCE.md.
	managed := map[string]bool{}
	for _, mf := range res.NewLock.Managed {
		managed[filepath.Base(mf.Path)] = true
	}
	if !managed["SKILL.md"] || !managed[hermesSourceFile] {
		t.Errorf("delivered files not recorded as managed: %+v", res.NewLock.Managed)
	}
	if f := VerifyManaged(res.NewLock, configurator.ProviderHermes, baseDir); len(f) != 0 {
		t.Errorf("fresh delivery already reports drift: %+v", f)
	}
}

// A second sync of an unchanged skill rewrites nothing: the delivery path
// carries no timestamp precisely so it stays a fixed point (D141).
func TestApply_HermesDeliveryIsIdempotent(t *testing.T) {
	kbRoot := hermesKB(t, "The runbooks.\n")
	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	first, err := Apply(m, hermesApplyOptions(kbRoot, baseDir, Lock{}))
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	inbox := filepath.Join(baseDir, "skill-inbox", "runbooks", "cartographer")
	before, err := os.ReadFile(filepath.Join(inbox, hermesSourceFile))
	if err != nil {
		t.Fatal(err)
	}

	// Compare only what hermes can materialize: an unsupported kind is not
	// drift, it simply does not concern the provider.
	if d := ComputeDiff(FilterForProvider(m, configurator.ProviderHermes), first.NewLock); !d.InSync {
		t.Errorf("re-sync of an unchanged manifest is not in sync: %+v", d)
	}
	second, err := Apply(m, hermesApplyOptions(kbRoot, baseDir, first.NewLock))
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(inbox, hermesSourceFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("SOURCE.md changed on an unchanged re-delivery:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if len(second.Written) != 0 {
		t.Errorf("unchanged re-sync rewrote %d files: %+v", len(second.Written), second.Written)
	}

	// A changed skill re-delivers with the new hash: that is what tells the
	// curator a new proposal from a re-delivery.
	if err := os.WriteFile(filepath.Join(kbRoot, "skills", "runbooks", "SKILL.md"),
		[]byte("---\nname: runbooks\ndescription: d\n---\nRewritten.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	if _, err := Apply(m2, hermesApplyOptions(kbRoot, baseDir, first.NewLock)); err != nil {
		t.Fatalf("Apply 3: %v", err)
	}
	updated, err := os.ReadFile(filepath.Join(inbox, hermesSourceFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) == string(before) {
		t.Error("SOURCE.md unchanged after the skill changed: the content hash must follow it")
	}
	body, err := os.ReadFile(filepath.Join(inbox, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Rewritten.") {
		t.Error("changed skill not re-delivered")
	}
}

// Removing the skill from the KB prunes exactly its delivery: the inbox root
// is shared with other proposers and never removed.
func TestApply_HermesPruneStopsAtInboxRoot(t *testing.T) {
	kbRoot := hermesKB(t, "The runbooks.\n")
	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	first, err := Apply(m, hermesApplyOptions(kbRoot, baseDir, Lock{}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Another proposer's delivery, which the prune must not touch.
	other := filepath.Join(baseDir, "skill-inbox", "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(kbRoot, "skills", "runbooks")); err != nil {
		t.Fatal(err)
	}
	empty, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	if _, err := Apply(empty, hermesApplyOptions(kbRoot, baseDir, first.NewLock)); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "skill-inbox", "runbooks")); !os.IsNotExist(err) {
		t.Errorf("the delivered skill's inbox directory survived the prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "skill-inbox")); err != nil {
		t.Errorf("the inbox root must never be removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another proposer's delivery was removed: %v", err)
	}
}

// A KB skill that ships its own SOURCE.md would be silently overwritten by the
// generated one: that single artifact fails with a warning naming it, and the
// rest of the sync completes.
func TestApply_HermesSourceFileCollision(t *testing.T) {
	baseDir := t.TempDir()
	m := Manifest{
		Revision: "r1",
		Artifacts: []Artifact{
			{
				Kind: "skill", Name: "runbooks", Source: "kb:kb", ContentHash: "h1",
				Files: []ArtifactFile{
					{Path: "SKILL.md", Content: []byte("---\nname: runbooks\n---\nbody\n")},
					{Path: hermesSourceFile, Content: []byte("mine\n")},
				},
			},
			{
				Kind: "skill", Name: "other", Source: "kb:kb", ContentHash: "h2",
				Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: other\n---\nbody\n")}},
			},
		},
	}
	res, err := Apply(m, hermesApplyOptions(t.TempDir(), baseDir, Lock{}))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "runbooks") && strings.Contains(w, hermesSourceFile) {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning naming the colliding skill: %+v", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "skill-inbox", "runbooks")); !os.IsNotExist(err) {
		t.Errorf("the colliding skill was delivered anyway: %v", err)
	}
	for _, mf := range res.NewLock.Managed {
		if mf.Name == "runbooks" {
			t.Errorf("the colliding skill was recorded as managed: %+v", mf)
		}
	}
	// The other skill in the same sync is delivered normally.
	if _, err := os.Stat(filepath.Join(baseDir, "skill-inbox", "other", "cartographer", "SKILL.md")); err != nil {
		t.Errorf("the rest of the sync did not complete: %v", err)
	}
}

// LockBaseDir is what every prune/verify call site must go through: a Lock
// that records its own base dir wins over the lockfile's directory (D141).
func TestLockBaseDir(t *testing.T) {
	if got := LockBaseDir(Lock{}, "/home/user"); got != "/home/user" {
		t.Errorf("LockBaseDir(no base dir) = %q, want the default", got)
	}
	if got := LockBaseDir(Lock{BaseDir: "/opt/data"}, "/home/user"); got != "/opt/data" {
		t.Errorf("LockBaseDir(recorded) = %q, want /opt/data", got)
	}
}

func TestBaseDirFor(t *testing.T) {
	if got, err := BaseDirFor(configurator.ProviderClaudeCode, "/home/user"); err != nil || got != "/home/user" {
		t.Errorf("BaseDirFor(claude) = %q, %v", got, err)
	}
	t.Setenv("HERMES_HOME", "/opt/data")
	if got, err := BaseDirFor(configurator.ProviderHermes, "/home/user"); err != nil || got != "/opt/data" {
		t.Errorf("BaseDirFor(hermes) = %q, %v", got, err)
	}
}
