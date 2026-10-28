package provisioning_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
