package provisioning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// sectionOf is the text of one KB's region of a block, markers included.
func sectionOf(t *testing.T, content, kb string) string {
	t.Helper()
	begin := "<!-- cartographer:kb:" + kb + ":begin -->"
	end := "<!-- cartographer:kb:" + kb + ":end -->"
	i, j := strings.Index(content, begin), strings.Index(content, end)
	if i < 0 || j < i {
		t.Fatalf("no section of %s in:\n%s", kb, content)
	}
	return content[i : j+len(end)]
}

func heldApply(t *testing.T, base string, lock provisioning.Lock, held []string, arts ...contract.Artifact) (provisioning.AppliedResult, string) {
	t.Helper()
	res, err := provisioning.Apply(contract.Manifest{Revision: "r", Artifacts: arts}, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: lock, ExpandPlaceholders: true, HeldKBs: held,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(base, ".claude", "CLAUDE.md"))
	return res, string(data)
}

// withoutKB is the lock a sync hands Apply: the entries of a held KB are carried
// by the client, not diffed.
func withoutKB(l provisioning.Lock, kb string) provisioning.Lock {
	var kept []provisioning.ManagedFile
	for _, mf := range l.Managed {
		if mf.Source != "kb:"+kb {
			kept = append(kept, mf)
		}
	}
	l.Managed = kept
	return l
}

// AC-cad-21: with HeldKBs naming kb-b, a rewrite of the block for another KB's
// change keeps kb-b's section byte-identical and in its order, writes nothing on
// a second apply, and drops a held name whose section is not on disk.
func TestHeldKBSectionIsCarried(t *testing.T) {
	base := t.TempDir()
	a1 := instructionsArtifact("kb-a", "Rule A one.\n")
	b1 := instructionsArtifact("kb-b", "Rule B <one>  \n\n- keep   spacing\n")
	c1 := instructionsArtifact("kb-c", "Rule C.\n")
	first, content1 := heldApply(t, base, provisioning.Lock{}, nil, a1, b1, c1)
	wantB := sectionOf(t, content1, "kb-b")

	// kb-b is held and kb-a changes: the block is rewritten.
	a2 := instructionsArtifact("kb-a", "Rule A two.\n")
	lock := withoutKB(first.NewLock, "kb-b")
	r2, content2 := heldApply(t, base, lock, []string{"kb-b"}, a2, c1)
	if len(r2.Written) == 0 || !strings.Contains(content2, "Rule A two.") {
		t.Fatalf("the block was not rewritten for kb-a:\n%s", content2)
	}
	if got := sectionOf(t, content2, "kb-b"); got != wantB {
		t.Errorf("kb-b's section changed:\n%q\nwas:\n%q", got, wantB)
	}
	ia, ib, ic := strings.Index(content2, "cartographer:kb:kb-a:begin"), strings.Index(content2, "cartographer:kb:kb-b:begin"), strings.Index(content2, "cartographer:kb:kb-c:begin")
	if !(ia < ib && ib < ic) {
		t.Errorf("kb-b left its place (%d %d %d)", ia, ib, ic)
	}
	if len(r2.HeldKBsWithoutSection) != 0 {
		t.Errorf("a section on disk reported missing: %v", r2.HeldKBsWithoutSection)
	}
	for _, mf := range r2.NewLock.Managed {
		if mf.Source == "kb:kb-b" {
			t.Errorf("Apply recorded an entry of the held KB (the client carries them): %+v", mf)
		}
	}

	// Second apply: converged, nothing written, the file untouched.
	r3, content3 := heldApply(t, base, r2.NewLock, []string{"kb-b"}, a2, c1)
	if len(r3.Written) != 0 || content3 != content2 {
		t.Errorf("a second apply wrote: %+v", r3.Written)
	}

	// A held name whose section is not on disk carries nothing.
	r4, content4 := heldApply(t, base, r3.NewLock, []string{"kb-b", "kb-gone"}, a2, c1)
	if strings.Contains(content4, "kb-gone") || len(r4.HeldKBsWithoutSection) != 1 || r4.HeldKBsWithoutSection[0] != "kb-gone" {
		t.Errorf("missing = %v:\n%s", r4.HeldKBsWithoutSection, content4)
	}
	// And a block that holds only held regions stays, instead of being removed.
	_, content5 := heldApply(t, base, withoutKB(withoutKB(r4.NewLock, "kb-b"), "kb-a"), []string{"kb-b"})
	if !strings.Contains(content5, wantB) {
		t.Errorf("the only region left was dropped:\n%s", content5)
	}
}

// A block that is not exactly one begin and one end stays refused: nothing is
// carried out of it and nothing is edited.
func TestHeldKBSectionCorruptedBlockStaysRefused(t *testing.T) {
	base := t.TempDir()
	a := instructionsArtifact("kb-a", "Rule A.\n")
	b := instructionsArtifact("kb-b", "Rule B.\n")
	first, content := heldApply(t, base, provisioning.Lock{}, nil, a, b)
	corrupt := content + "\n<!-- cartographer:instructions:end -->\n"
	path := filepath.Join(base, ".claude", "CLAUDE.md")
	if err := os.WriteFile(path, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	a2 := instructionsArtifact("kb-a", "Rule A two.\n")
	res, _ := heldApply(t, base, withoutKB(first.NewLock, "kb-b"), []string{"kb-b"}, a2)
	if got, _ := os.ReadFile(path); string(got) != corrupt {
		t.Errorf("a corrupted block was edited:\n%s", got)
	}
	if len(res.Refused) == 0 {
		t.Errorf("the corrupted block was not refused: %+v", res)
	}
	if len(res.HeldKBsWithoutSection) != 1 {
		t.Errorf("nothing may be carried out of a corrupted block: %v", res.HeldKBsWithoutSection)
	}
}

// The regions are found by the block's own marker rules, not by comparing the
// file with itself: a held section directly followed by another section marker
// or by the end marker, and a block followed by text that starts the way the
// block does, each keep their bytes.
func TestHeldKBSectionsAreFoundExactly(t *testing.T) {
	base := t.TempDir()
	a := instructionsArtifact("kb-a", "Rule A.\n")
	b := instructionsArtifact("kb-b", "Rule B.\n")
	c := instructionsArtifact("kb-c", "Rule C.\n")
	first, content := heldApply(t, base, provisioning.Lock{}, nil, a, b, c)
	wantA, wantB := sectionOf(t, content, "kb-a"), sectionOf(t, content, "kb-b")

	path := filepath.Join(base, ".claude", "CLAUDE.md")
	// kb-a and kb-b with no blank line between them, kb-b right before the end
	// marker, and a note after the block that begins with "<!--" like the block.
	tight := strings.Replace(content, wantA+"\n\n"+wantB, wantA+"\n"+wantB, 1)
	i := strings.Index(tight, "<!-- cartographer:kb:kb-c:begin -->")
	j := strings.Index(tight, "<!-- cartographer:instructions:end -->")
	if i < 0 || j < i {
		t.Fatalf("unexpected block:\n%s", tight)
	}
	tight = tight[:i] + tight[j:] // the end marker follows kb-b directly
	tight = strings.TrimRight(tight, "\n") + "\n<!-- a note of the user -->\nmore\n"
	if err := os.WriteFile(path, []byte(tight), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, held := range [][]string{{"kb-a"}, {"kb-b"}, {"kb-a", "kb-b"}} {
		lock := first.NewLock
		for _, kb := range held {
			lock = withoutKB(lock, kb)
		}
		if err := os.WriteFile(path, []byte(tight), 0o644); err != nil {
			t.Fatal(err)
		}
		// kb-c changes, so the block is rewritten around the held regions.
		c2 := instructionsArtifact("kb-c", "Rule C two.\n")
		arts := []contract.Artifact{c2}
		for _, kb := range []string{"kb-a", "kb-b"} {
			if !contains(held, kb) {
				arts = append(arts, instructionsArtifact(kb, map[string]string{"kb-a": "Rule A.\n", "kb-b": "Rule B.\n"}[kb]))
			}
		}
		res, out := heldApply(t, base, lock, held, arts...)
		if len(res.HeldKBsWithoutSection) != 0 {
			t.Errorf("held %v: sections not found: %v", held, res.HeldKBsWithoutSection)
		}
		for _, kb := range held {
			want := map[string]string{"kb-a": wantA, "kb-b": wantB}[kb]
			if got := sectionOf(t, out, kb); got != want {
				t.Errorf("held %v: section of %s = %q, want %q", held, kb, got, want)
			}
		}
		if !strings.Contains(out, "<!-- a note of the user -->\nmore") || !strings.Contains(out, "Rule C two.") {
			t.Errorf("held %v: the text after the block or the rewrite is wrong:\n%s", held, out)
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
