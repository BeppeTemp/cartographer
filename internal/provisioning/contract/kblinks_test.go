package contract_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// A KB comes from a remote, and git stores symlinks. An artifact is made of
// regular files of the KB: the manifest builder and the file reader refuse a
// symlink in any of them (the file, a directory above it, the kind's own
// directory), never read what it points at, and name the artifact in a
// manifest issue. The artifacts around it are unaffected.

const (
	linkSecret = "OUTSIDE-SECRET-CONTENT"
	skillMD    = "---\nname: %s\ndescription: Use when testing.\n---\nBody.\n"
	mcpJSON    = `{"type":"http","url":"https://example.com/mcp"}` + "\n"
)

func skipLinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
}

func linkWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func linkTo(t *testing.T, root, rel, target string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

func gitCommitAll(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// outsideTree is a directory outside the KB with a file of every shape an
// artifact link can point at.
func outsideTree(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	linkWrite(t, out, "secret.txt", linkSecret)
	linkWrite(t, out, "secret.md", linkSecret)
	linkWrite(t, out, "secret.json", linkSecret)
	linkWrite(t, out, "skill/SKILL.md", strings.Replace(skillMD, "%s", "outside", 1))
	linkWrite(t, out, "dir/x.md", linkSecret)
	return out
}

// goodArtifacts writes one regular artifact of each kind.
func goodArtifacts(t *testing.T, root string) {
	t.Helper()
	linkWrite(t, root, "skills/good/SKILL.md", strings.Replace(skillMD, "%s", "good", 1))
	linkWrite(t, root, "hooks/good/hook.json", "{}\n")
	linkWrite(t, root, "agents/good.md", "---\nname: good\n---\nBody.\n")
	linkWrite(t, root, "mcp/good.json", mcpJSON)
}

func hasArtifact(m contract.Manifest, kind, name string) bool {
	for _, a := range m.Artifacts {
		if a.Kind == kind && a.Name == name {
			return true
		}
	}
	return false
}

func issueNaming(m contract.Manifest, parts ...string) bool {
	for _, issue := range m.Issues {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(issue, p)
		}
		if ok {
			return true
		}
	}
	return false
}

func assertNoSecret(t *testing.T, what string, files []contract.ArtifactFile, err error) {
	t.Helper()
	for _, f := range files {
		if strings.Contains(string(f.Content), linkSecret) {
			t.Errorf("%s: served the content of a file behind a link: %s", what, f.Path)
		}
	}
	if err != nil && strings.Contains(err.Error(), linkSecret) {
		t.Errorf("%s: the error carries the content behind a link: %v", what, err)
	}
}

func TestKBArtifacts_ARegularFileIsRequired(t *testing.T) {
	skipLinks(t)
	for _, tracked := range []bool{true, false} {
		name := "plain folder"
		if tracked {
			name = "git clone"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			out := outsideTree(t)
			goodArtifacts(t, root)

			// A skill and a hook holding a link to a file, and a link to a directory.
			linkWrite(t, root, "skills/inner/SKILL.md", strings.Replace(skillMD, "%s", "inner", 1))
			linkTo(t, root, "skills/inner/notes.txt", filepath.Join(out, "secret.txt"))
			linkWrite(t, root, "skills/deep/SKILL.md", strings.Replace(skillMD, "%s", "deep", 1))
			linkTo(t, root, "skills/deep/refs", filepath.Join(out, "dir"))
			linkWrite(t, root, "hooks/inner/hook.json", "{}\n")
			linkTo(t, root, "hooks/inner/run.sh", filepath.Join(out, "secret.txt"))
			// A SKILL.md that is a link, and a skill directory that is one.
			linkTo(t, root, "skills/skilllink/SKILL.md", filepath.Join(out, "skill", "SKILL.md"))
			linkTo(t, root, "skills/dirlink", filepath.Join(out, "skill"))
			linkTo(t, root, "hooks/dirlink", filepath.Join(out, "dir"))
			// An agent and an mcp descriptor that are links, to a file and to a directory.
			linkTo(t, root, "agents/filelink.md", filepath.Join(out, "secret.md"))
			linkTo(t, root, "agents/directory.md", filepath.Join(out, "dir"))
			linkTo(t, root, "mcp/filelink.json", filepath.Join(out, "secret.json"))
			linkTo(t, root, "mcp/directory.json", filepath.Join(out, "dir"))
			if tracked {
				gitCommitAll(t, root)
			}

			roots := map[string]string{"kb-a": root}
			m, err := contract.BuildManifest(nil, roots, contract.BuildOptions{})
			if err != nil {
				t.Fatalf("one linked artifact fails the manifest of the others: %v", err)
			}
			for _, kn := range [][2]string{{"skill", "good"}, {"hook", "good"}, {"agent", "good"}, {"mcp", "good"}} {
				if !hasArtifact(m, kn[0], kn[1]) {
					t.Errorf("the regular %s %q is missing from the manifest", kn[0], kn[1])
				}
			}
			for _, kn := range [][3]string{
				{"skill", "inner", "notes.txt"}, {"skill", "deep", "refs"}, {"hook", "inner", "run.sh"},
				{"skill", "skilllink", "SKILL.md"}, {"agent", "filelink", "filelink.md"}, {"agent", "directory", "directory.md"},
				{"mcp", "filelink", "filelink.json"}, {"mcp", "directory", "directory.json"},
			} {
				if hasArtifact(m, kn[0], kn[1]) {
					t.Errorf("the %s %q holding a link is in the manifest", kn[0], kn[1])
				}
				if !issueNaming(m, kn[1], "symlink") && !issueNaming(m, kn[2], "symlink") {
					t.Errorf("no issue names the symlink of %s %q: %v", kn[0], kn[1], m.Issues)
				}
			}
			for _, kn := range [][2]string{{"skill", "dirlink"}, {"hook", "dirlink"}} {
				if hasArtifact(m, kn[0], kn[1]) {
					t.Errorf("the %s %q behind a directory link is in the manifest", kn[0], kn[1])
				}
			}
			if !issueNaming(m, "hooks/dirlink", "symlink") {
				t.Errorf("no issue names the hook directory that is a link: %v", m.Issues)
			}

			// What the manifest lists is read without a link in the way, and what
			// it does not list is refused by the reader too.
			for _, a := range m.Artifacts {
				files, err := contract.ReadArtifactFiles(a, nil, roots)
				assertNoSecret(t, a.Kind+"/"+a.Name, files, err)
			}
			for _, a := range []contract.Artifact{
				{Kind: "skill", Name: "inner"}, {Kind: "skill", Name: "deep"}, {Kind: "skill", Name: "skilllink"},
				{Kind: "skill", Name: "dirlink"}, {Kind: "hook", Name: "inner"}, {Kind: "hook", Name: "dirlink"},
				{Kind: "agent", Name: "filelink"}, {Kind: "agent", Name: "directory"},
				{Kind: "mcp", Name: "filelink"}, {Kind: "mcp", Name: "directory"},
			} {
				a.Source = "kb:kb-a"
				files, err := contract.ReadArtifactFiles(a, nil, roots)
				assertNoSecret(t, a.Kind+"/"+a.Name, files, err)
				if err == nil {
					t.Errorf("ReadArtifactFiles(%s/%s) read through a link: %d files", a.Kind, a.Name, len(files))
				}
			}
		})
	}
}

// The kind's own directory (skills/, hooks/, agents/, mcp/) may itself be a
// link; nothing below it is read, in a plain folder where no git index
// narrows the listing.
func TestKBArtifacts_AKindDirectoryThatIsALinkServesNothing(t *testing.T) {
	skipLinks(t)
	for _, dir := range []string{"skills", "hooks", "agents", "mcp"} {
		t.Run(dir, func(t *testing.T) {
			root := t.TempDir()
			out := t.TempDir()
			linkWrite(t, out, "x/SKILL.md", strings.Replace(skillMD, "%s", "x", 1))
			linkWrite(t, out, "x/hook.json", "{}\n")
			linkWrite(t, out, "x.md", "---\nname: x\n---\n"+linkSecret+"\n")
			linkWrite(t, out, "x.json", mcpJSON)
			linkTo(t, root, dir, out)
			roots := map[string]string{"kb-a": root}

			m, err := contract.BuildManifest(nil, roots, contract.BuildOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range m.Artifacts {
				if a.Kind != "instructions" {
					t.Errorf("an artifact came out of %s/, a link: %+v", dir, a)
				}
			}
			if !issueNaming(m, dir, "symlink") {
				t.Errorf("no issue names %s/ as a link: %v", dir, m.Issues)
			}
			for _, a := range []contract.Artifact{
				{Kind: "skill", Name: "x"}, {Kind: "hook", Name: "x"}, {Kind: "agent", Name: "x"}, {Kind: "mcp", Name: "x"},
			} {
				a.Source = "kb:kb-a"
				files, err := contract.ReadArtifactFiles(a, nil, roots)
				assertNoSecret(t, a.Kind+"/x", files, err)
				if err == nil && dir == kindDir(a.Kind) {
					t.Errorf("ReadArtifactFiles(%s/x) read through %s/, a link: %d files", a.Kind, dir, len(files))
				}
			}
		})
	}
}

func kindDir(kind string) string {
	switch kind {
	case "skill":
		return "skills"
	case "hook":
		return "hooks"
	case "agent":
		return "agents"
	}
	return "mcp"
}

// An untracked link beside the tracked files of a skill is not part of the
// artifact and does not stop it (a developer's local checkout, #1325).
func TestKBArtifacts_AnUntrackedLinkDoesNotStopTheArtifact(t *testing.T) {
	skipLinks(t)
	root := t.TempDir()
	out := outsideTree(t)
	goodArtifacts(t, root)
	gitCommitAll(t, root)
	linkTo(t, root, "skills/good/local-notes", filepath.Join(out, "secret.txt"))
	roots := map[string]string{"kb-a": root}

	m, err := contract.BuildManifest(nil, roots, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasArtifact(m, "skill", "good") {
		t.Fatalf("an untracked link stopped the skill: %v", m.Issues)
	}
	for _, a := range m.Artifacts {
		files, err := contract.ReadArtifactFiles(a, nil, roots)
		assertNoSecret(t, a.Kind+"/"+a.Name, files, err)
		if err != nil {
			t.Errorf("ReadArtifactFiles(%s/%s): %v", a.Kind, a.Name, err)
		}
	}
}
