package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// skillPath is where a provider materializes the skill the test KB ships.
func skillPath(t *testing.T, baseDir string, p configurator.Provider) string {
	t.Helper()
	rel := destDirScoped("skill", "tooling", p, ScopeGlobal)
	if rel == "" {
		t.Fatalf("no skill destination for %s", p)
	}
	return filepath.Join(baseDir, rel, "SKILL.md")
}

func rerenderFixture(t *testing.T) (Manifest, string) {
	t.Helper()
	kbRoot := t.TempDir()
	dir := filepath.Join(kbRoot, "skills", "tooling")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: tooling\ndescription: Use when testing.\n---\nTools live in {{path:tools}}.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifest(nil, map[string]string{"kb": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	// A remote client receives the files in the manifest (sync_pull).
	for i := range m.Artifacts {
		files, err := ReadArtifactFiles(m.Artifacts[i], nil, map[string]string{"kb": kbRoot})
		if err != nil {
			t.Fatal(err)
		}
		m.Artifacts[i].Files = files
	}
	return m, kbRoot
}

func wroteSkill(res AppliedResult) bool {
	for _, w := range res.Written {
		if w.Kind == "skill" {
			return true
		}
	}
	return false
}

// A changed resolution rewrites an already-materialized artifact in every
// client whose KB content did not change (#967); an unchanged one does not.
func TestApply_ChangedResolutionRewritesSkillInEveryClient(t *testing.T) {
	m, kbRoot := rerenderFixture(t)
	providers := []configurator.Provider{configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode}
	bases := map[configurator.Provider]string{}
	locks := map[configurator.Provider]Lock{}
	for _, p := range providers {
		bases[p] = t.TempDir()
		opts := clientApplyOptions(kbRoot, bases[p], Lock{})
		opts.Provider = p
		opts.Paths = map[string]string{"tools": "/srv/old"}
		res, err := Apply(m, opts)
		if err != nil {
			t.Fatalf("%s first Apply: %v", p, err)
		}
		locks[p] = res.NewLock
		if got, _ := os.ReadFile(skillPath(t, bases[p], p)); !strings.Contains(string(got), "/srv/old") {
			t.Fatalf("%s: first render lacks /srv/old:\n%s", p, got)
		}
	}

	// Unchanged resolution: nothing is rewritten.
	for _, p := range providers {
		opts := clientApplyOptions(kbRoot, bases[p], locks[p])
		opts.Provider = p
		opts.Paths = map[string]string{"tools": "/srv/old"}
		res, err := Apply(m, opts)
		if err != nil {
			t.Fatalf("%s second Apply: %v", p, err)
		}
		if wroteSkill(res) {
			t.Errorf("%s: skill rewritten although nothing changed: %+v", p, res.Written)
		}
	}

	// Changed resolution: every client gets the new path, then settles.
	for _, p := range providers {
		opts := clientApplyOptions(kbRoot, bases[p], locks[p])
		opts.Provider = p
		opts.Paths = map[string]string{"tools": "/srv/new"}
		res, err := Apply(m, opts)
		if err != nil {
			t.Fatalf("%s third Apply: %v", p, err)
		}
		if !wroteSkill(res) {
			t.Errorf("%s: skill not rewritten after the resolution changed", p)
		}
		got, _ := os.ReadFile(skillPath(t, bases[p], p))
		if !strings.Contains(string(got), "/srv/new") || strings.Contains(string(got), "/srv/old") {
			t.Errorf("%s: skill not re-rendered:\n%s", p, got)
		}
		again := clientApplyOptions(kbRoot, bases[p], res.NewLock)
		again.Provider = p
		again.Paths = opts.Paths
		res2, err := Apply(m, again)
		if err != nil {
			t.Fatalf("%s fourth Apply: %v", p, err)
		}
		if wroteSkill(res2) {
			t.Errorf("%s: skill rewritten again after settling", p)
		}
	}
}
