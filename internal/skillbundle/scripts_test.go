package skillbundle

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBundledScriptsAreEmbeddedAndRouted is the scripts/ twin of the two
// references/ tests above: a script must reach the embedded FS, carry a body
// and a POSIX shebang, and be named by its skill's SKILL.md, or the agent is
// pointed at a file it cannot run, or ships one nothing tells it to run.
func TestBundledScriptsAreEmbeddedAndRouted(t *testing.T) {
	skills, err := fs.ReadDir(FS, "bundled")
	if err != nil {
		t.Fatalf("read bundled: %v", err)
	}
	seen := 0
	for _, s := range skills {
		if !s.IsDir() {
			continue
		}
		scripts, err := fs.ReadDir(FS, path.Join("bundled", s.Name(), "scripts"))
		if err != nil {
			continue // a skill with no scripts is fine
		}
		body, err := fs.ReadFile(FS, path.Join("bundled", s.Name(), "SKILL.md"))
		if err != nil {
			t.Fatalf("%s: read SKILL.md: %v", s.Name(), err)
		}
		for _, sc := range scripts {
			if sc.IsDir() {
				continue
			}
			seen++
			p := path.Join("bundled", s.Name(), "scripts", sc.Name())
			data, err := fs.ReadFile(FS, p)
			if err != nil {
				t.Errorf("%s: not readable from the embedded FS: %v", p, err)
				continue
			}
			if !strings.HasPrefix(string(data), "#!/bin/sh\n") {
				t.Errorf("%s: want a #!/bin/sh shebang (the skill runs it with sh)", p)
			}
			if want := "scripts/" + sc.Name(); !strings.Contains(string(body), want) {
				t.Errorf("%s/SKILL.md does not route to %s", s.Name(), want)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no scripts/ file found in the embedded bundle: the embed no longer carries them")
	}
}

// TestKBDoctorScriptsReadAKBClone runs the kb-doctor scripts on a fixture
// clone. Each fixture file carries one trap the first version fell into: an
// unclosed fence leaking into the next page, an expanded concept's index.md
// (the page itself, D310) skipped as if it were the map's index, a body line
// read as frontmatter, a block-form lint_ignore cut to its first line, and a
// lint_accept that is the last frontmatter key printing the body after it.
func TestKBDoctorScriptsReadAKBClone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the scripts need a POSIX shell")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	scripts := t.TempDir()
	for _, name := range []string{"section_freq.sh", "suppression_inventory.sh"} {
		data, err := fs.ReadFile(FS, path.Join("bundled", "kb-doctor", "scripts", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(scripts, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	clone := t.TempDir()
	for rel, content := range map[string]string{
		"data/notes/_map.md":        "---\ntype: Map\nlint_ignore:\n  - bare_link_list\n  - orphan\n---\n# Notes\n",
		"data/notes/index.md":       "# Notes\n\n## Not a page\n",
		"data/notes/log.md":         "# Log\n\n## Not a page either\n",
		"data/notes/a.md":           "---\ntype: Topic\nlint_ignore: [stale_claim]\n---\n# A\n\n## Context\n\n```markdown\n## Inside a fence\n",
		"data/notes/b.md":           "---\ntype: Topic\n---\n# B\n\n## Context\n\n## Context\n\n## Decisions\n",
		"data/notes/c.md":           "---\ntype: Topic\nlint_ignore: []\n---\n# C\n\nlint_ignore: [written-in-the-body]\n",
		"data/notes/topic/index.md": "---\ntype: Topic\n---\n# Topic\n\n## Context\n\n~~~\n## Fenced\n~~~\n\n## Decisions ##\n",
		"instructions.md":           "---\ntitle: Instructions\nlint_accept:\n  \"skills/deploy/SKILL.md\": [skill_git_command]   # a comment\n---\n  indented body line\n",
	} {
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(shell, append([]string{filepath.Join(scripts, name)}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		return string(out)
	}

	if got, want := run("section_freq.sh", clone, "notes", "1"), "   3  Context\n   2  Decisions\n"; got != want {
		t.Errorf("section_freq.sh:\n got %q\nwant %q", got, want)
	}

	want := strings.Join([]string{
		"== concepts with lint_ignore",
		"notes/a.md\tstale_claim",
		"(nothing printed = none)",
		"== maps with lint_ignore",
		"notes/_map.md\tbare_link_list, orphan",
		"(nothing printed = none)",
		"== instructions.md lint_accept",
		`"skills/deploy/SKILL.md": [skill_git_command]   # a comment`,
		"(nothing printed = none)",
		"",
	}, "\n")
	if got := run("suppression_inventory.sh", clone); got != want {
		t.Errorf("suppression_inventory.sh:\n got %q\nwant %q", got, want)
	}
}
