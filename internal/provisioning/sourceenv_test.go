package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// A KB skill that ships its own SOURCE.env would be silently overwritten by the
// generated one (D316): that single artifact fails with a warning naming it,
// and the rest of the sync completes.
func TestApply_SourceEnvCollision(t *testing.T) {
	baseDir := t.TempDir()
	m := Manifest{
		Revision: "r1",
		Artifacts: []Artifact{
			{
				Kind: "skill", Name: "runbooks", Source: "kb:kb", ContentHash: "h1",
				Files: []ArtifactFile{
					{Path: "SKILL.md", Content: []byte("---\nname: runbooks\n---\nbody\n")},
					{Path: skillSourceEnvFile, Content: []byte("mine\n")},
				},
			},
			{
				Kind: "skill", Name: "other", Source: "kb:kb", ContentHash: "h2",
				Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: other\n---\nbody\n")}},
			},
		},
	}
	res, err := Apply(m, ApplyOptions{AutoTrust: true, KBRoots: map[string]string{"kb": t.TempDir()}, Provider: configurator.ProviderClaudeCode, BaseDir: baseDir})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "runbooks") && strings.Contains(w, skillSourceEnvFile) {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning naming the colliding skill: %+v", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "skills", "runbooks")); !os.IsNotExist(err) {
		t.Errorf("the colliding skill was delivered anyway: %v", err)
	}
	for _, mf := range res.NewLock.Managed {
		if mf.Name == "runbooks" {
			t.Errorf("the colliding skill was recorded as managed: %+v", mf)
		}
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "skills", "other", "SKILL.md")); err != nil {
		t.Errorf("the rest of the sync did not complete: %v", err)
	}
}

// LockBaseDir is what every prune/verify call site goes through: a Lock that
// records its own base dir (a workspace projection, or a lockfile written when
// Hermes was supported, D898) wins over the lockfile's directory.
func TestLockBaseDir(t *testing.T) {
	if got := LockBaseDir(Lock{}, "/home/user"); got != "/home/user" {
		t.Errorf("LockBaseDir(no base dir) = %q, want the default", got)
	}
	if got := LockBaseDir(Lock{BaseDir: "/opt/data"}, "/home/user"); got != "/opt/data" {
		t.Errorf("LockBaseDir(recorded) = %q, want /opt/data", got)
	}
}

// A lockfile written before D898 — a global section naming a provider that no
// longer exists, with a base_dir — still loads: the section survives as data
// until a sync drops it.
func TestReadLockFile_KeepsASectionOfARemovedProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)
	old := `{"providers":{"hermes":{"applied_revision":"r","provider":"hermes","base_dir":"/opt/data","managed":[{"kind":"skill","name":"demo","path":"skill-inbox/demo/cartographer/SKILL.md","content_hash":"h"}]}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	lf, err := ReadLockFile(path)
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	if got := lf.ForProvider("hermes"); got.BaseDir != "/opt/data" || len(got.Managed) != 1 {
		t.Errorf("section = %+v", got)
	}
}
