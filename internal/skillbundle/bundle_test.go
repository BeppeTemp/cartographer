package skillbundle

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

// TestBundledReferencesAreEmbedded guards the routing model the onboarding
// skills use (D196): a SKILL.md routes to references/*.md loaded on demand, and
// those files ship inside the binary via //go:embed. A reference added to the
// source tree but not reaching the embedded FS — or shipped empty — would leave
// the skill pointing at a file the agent cannot read, which is exactly the
// silent gap the routing was introduced to close.
func TestBundledReferencesAreEmbedded(t *testing.T) {
	seen := 0
	err := fs.WalkDir(FS, "bundled", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Base(path.Dir(p)) != "references" {
			return nil
		}
		seen++
		data, readErr := fs.ReadFile(FS, p)
		if readErr != nil {
			t.Errorf("%s: not readable from the embedded FS: %v", p, readErr)
			return nil
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("%s: embedded but empty", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bundled: %v", err)
	}
	if seen == 0 {
		t.Fatal("no references/ file found in the embedded bundle: the embed no longer carries them")
	}
}

// TestBundledSkillReferencesAreRouted pairs with the test above from the other
// side: a reference file that no SKILL.md mentions is dead weight the agent will
// never load, and a SKILL.md routing to a file that is not there is worse.
func TestBundledSkillReferencesAreRouted(t *testing.T) {
	skills, err := fs.ReadDir(FS, "bundled")
	if err != nil {
		t.Fatalf("read bundled: %v", err)
	}
	for _, s := range skills {
		if !s.IsDir() {
			continue
		}
		refs, err := fs.ReadDir(FS, path.Join("bundled", s.Name(), "references"))
		if err != nil {
			continue // a skill with no references is fine
		}
		body, err := fs.ReadFile(FS, path.Join("bundled", s.Name(), "SKILL.md"))
		if err != nil {
			t.Fatalf("%s: read SKILL.md: %v", s.Name(), err)
		}
		for _, r := range refs {
			want := "references/" + r.Name()
			if !strings.Contains(string(body), want) {
				t.Errorf("%s/SKILL.md does not route to %s", s.Name(), want)
			}
		}
	}
}
