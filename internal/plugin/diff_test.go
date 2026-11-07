package plugin

import (
	"bytes"
	"strings"
	"testing"
)

func TestDiffReportsPermissionsFilesAndRanges(t *testing.T) {
	old, probs := ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	next := old
	next.Manifest.Cartographer = ">=0.30.0"
	next.Manifest.Permissions = &Permissions{Net: []string{"evil.example.com"}}
	next.Manifest.Depends = map[string]string{"other-plugin": ">=1.0.0"}
	next.Files = append([]TreeFile{}, old.Files...)
	next.Files[0].Content = append([]byte("x"), next.Files[0].Content...)
	next.Files = append(next.Files, TreeFile{Path: "skills/new/SKILL.md", Content: []byte("abc")})
	d := Diff(old, next, ProviderEnv{GOOS: "linux", Home: "/home/user", Getenv: func(string) string { return "" }})
	var buf bytes.Buffer
	DiffPrint(&buf, "p\x1b", d)
	out := buf.String()
	for _, want := range []string{"!! PERMISSION ADDED: net: evil.example.com", "needs Cartographer:", "depends added: other-plugin >=1.0.0", "file added: skills/new/SKILL.md (3 bytes)", "file changed: " + next.Files[0].Path} {
		if !strings.Contains(out, want) {
			t.Errorf("diff lacks %q:\n%s", want, out)
		}
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("raw control character in the diff:\n%q", out)
	}
	if !Diff(old, old, ProviderEnv{}).Empty() {
		t.Error("identical trees must give an empty delta")
	}
}

func TestDiffPrintShowsDigestsWhenVersionsEqual(t *testing.T) {
	old, probs := ReadTree(goodTree(), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	next := old
	next.Digest = strings.Repeat("a", 64)
	next.Files = append([]TreeFile{}, old.Files...)
	next.Files[0].Content = append([]byte("x"), next.Files[0].Content...)
	d := Diff(old, next, ProviderEnv{})
	if d.OldDigest != old.Digest || d.NewDigest != next.Digest {
		t.Fatalf("digests not carried: %+v", d)
	}
	var buf bytes.Buffer
	DiffPrint(&buf, "p", d)
	want := "approved " + old.Manifest.Version + " (" + old.Digest[:12] + ") -> new " + next.Manifest.Version + " (" + strings.Repeat("a", 12) + "):"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("want %q in:\n%s", want, buf.String())
	}

	next.Manifest.Version = "9.9.9"
	buf.Reset()
	DiffPrint(&buf, "p", Diff(old, next, ProviderEnv{}))
	if !strings.Contains(buf.String(), "approved "+old.Manifest.Version+" -> new 9.9.9:") {
		t.Errorf("different versions must print versions only:\n%s", buf.String())
	}
}
