package provisioning_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func trackedKB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "*.log\n")
	write("skills/demo/SKILL.md", "---\nname: demo\ndescription: Use when testing.\n---\nBody.\n")
	write("skills/demo/run.py", "print(1)\n")
	write("agents/rev.md", "---\nname: rev\n---\nBody.\n")
	write("hooks/h/hook.json", "{}\n")
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	return root
}

// #1325: an untracked or git-ignored file in an artifact directory (a
// __pycache__ left by running a skill's script) is not part of the artifact,
// and its presence does not change the revision.
func TestKBArtifacts_OnlyTrackedFiles(t *testing.T) {
	root := trackedKB(t)
	roots := map[string]string{"kb-a": root}
	before, err := provisioning.BuildManifest(nil, roots, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for rel, body := range map[string]string{
		"skills/demo/__pycache__/x.pyc": "junk",
		"skills/demo/debug.log":         "ignored",
		"hooks/h/stray.sh":              "untracked",
		"agents/draft.md":               "---\nname: draft\n---\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	after, err := provisioning.BuildManifest(nil, roots, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if before.Revision != after.Revision {
		t.Errorf("revision changed by untracked files: %s -> %s", before.Revision, after.Revision)
	}
	for _, a := range after.Artifacts {
		if a.Name == "draft" {
			t.Errorf("untracked agent was provisioned: %+v", a)
		}
		if a.Kind != "skill" && a.Kind != "hook" {
			continue
		}
		files, err := provisioning.ReadArtifactFiles(a, nil, roots)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			switch f.Path {
			case "SKILL.md", "run.py", "hook.json":
			default:
				t.Errorf("%s/%s carries untracked file %q", a.Kind, a.Name, f.Path)
			}
		}
	}
}

// D1325: a KB whose git cannot answer withholds its own artifacts and reports
// why; another KB still builds. A skill whose SKILL.md is untracked is no
// artifact, and the tracked listing follows a new commit despite the cache.
func TestBuildManifest_BrokenGitWithholdsOnlyThatKB(t *testing.T) {
	good := trackedKB(t)
	bad := trackedKB(t)
	if err := os.WriteFile(filepath.Join(bad, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bad, ".git", "index")); err != nil {
		t.Fatal(err)
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": good, "kb-b": bad}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var fromGood, fromBad int
	for _, a := range m.Artifacts {
		switch a.Source {
		case "kb:kb-a":
			fromGood++
		case "kb:kb-b":
			fromBad++
		}
	}
	if fromGood == 0 || fromBad != 0 {
		t.Errorf("artifacts: good=%d bad=%d, want good>0 bad=0", fromGood, fromBad)
	}
	found := false
	for _, i := range m.Issues {
		if strings.Contains(i, `"kb-b"`) && strings.Contains(i, "withheld") {
			found = true
		}
	}
	if !found {
		t.Errorf("no issue names kb-b: %v", m.Issues)
	}
}

func TestKBArtifacts_SkillNeedsTrackedSkillMDAndCacheFollowsCommits(t *testing.T) {
	root := trackedKB(t)
	roots := map[string]string{"kb-a": root}
	p := filepath.Join(root, "skills", "late")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, b := range map[string]string{"SKILL.md": "---\nname: late\ndescription: Use when testing.\n---\nB.\n", "x.py": "1\n"} {
		if err := os.WriteFile(filepath.Join(p, f), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	has := func() bool {
		m, err := provisioning.BuildManifest(nil, roots, provisioning.BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range m.Artifacts {
			if a.Name == "late" {
				return true
			}
		}
		return false
	}
	if has() {
		t.Fatal("untracked skill was provisioned")
	}
	git("add", "skills/late/x.py") // an auxiliary file alone is not enough
	git("commit", "-q", "-m", "aux")
	if has() {
		t.Fatal("skill with an untracked SKILL.md was provisioned")
	}
	git("add", "skills/late/SKILL.md")
	git("commit", "-q", "-m", "skill")
	if !has() {
		t.Fatal("committed skill is not provisioned (stale tracked cache?)")
	}
}
