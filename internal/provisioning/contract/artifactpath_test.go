package contract

import "testing"

func TestValidArtifactFilePath(t *testing.T) {
	for _, p := range []string{"SKILL.md.", "SKILL.md ", "SKILL.md./x", "SKILL.md /x", "a/SKILL.md..", "SKILL.md:stream", "SKILL.md::$DATA", "con", "a/NUL.txt", "notes/com1/x.txt", "a/aux.txt", "a\x1bb", "a/b\x7f.md", "a/\u0085.md", "a/b\nc.md", "a/\u009b[2J", "", ".", "./a", "a/../b", "a//b", "a/", "/a"} {
		if ValidArtifactFilePath(p) {
			t.Errorf("%q must be refused", p)
		}
	}
	for _, p := range []string{"SKILL.md", "a/b.md", "notes/console.txt", ".hidden/x", "a.b/c"} {
		if !ValidArtifactFilePath(p) {
			t.Errorf("%q must stay valid", p)
		}
	}
}
