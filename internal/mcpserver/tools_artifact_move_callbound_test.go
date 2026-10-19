package mcpserver

import (
	"fmt"
	"testing"
)

// The bound is the call's: two artifacts each within it are refused together
// when their sum is not, because the copy of both runs under both git locks.
func TestArtifactMove_BoundIsPerCall(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	half := artifactMoveMaxFiles/2 + 1
	for _, slug := range []string{"s", "t"} {
		amWriteBytes(t, f.a, "skills/"+slug+"/SKILL.md", []byte("---\nname: "+slug+"\ndescription: A skill\n---\nbody\n"), 0o644)
		for i := 0; i < half-1; i++ {
			amWriteBytes(t, f.a, fmt.Sprintf("skills/%s/res/f%04d.txt", slug, i), []byte("x"), 0o644)
		}
	}
	xferCommit(t, f.a)
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/s","skills/t"]}`)
	assertRefusedUntouched(t, f, res, fmt.Sprintf("%d files", 2*half), "in one call", "split it")
	if !amExists(f.a, "skills/t/SKILL.md") || amExists(f.b, "skills/t") {
		t.Error("the second artifact of a refused call moved")
	}

	one := f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`)
	if one.IsError {
		t.Fatalf("one artifact within the bound must move: %s", xferText(one))
	}
}

// A scan that finds nothing still stops at its budget and says the list may be
// incomplete: artifactRefsMax alone bounds only a scan that keeps matching.
func TestArtifactMove_ReferenceScanStopsAtItsBudget(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	for i := 0; i < 5; i++ {
		amWriteBytes(t, f.a, fmt.Sprintf("data/notes/n%d.md", i), []byte("---\ntype: Note\ntitle: N\n---\nNothing here.\n"), 0o644)
	}
	amWriteBytes(t, f.a, "data/notes/z.md", []byte("---\ntype: Note\ntitle: Z\n---\nSee skills/s.\n"), 0o644)
	xferCommit(t, f.a)
	pl := &artifactMovePlan{units: []artifactUnit{{rel: "skills/s"}}}

	if refs, cut := pl.referencesInSourceBounded(f.a, 1000, 1<<20); cut || len(refs) != 1 {
		t.Fatalf("within the budget: %v cut=%v, want the one reference and no cut", refs, cut)
	}
	if refs, cut := pl.referencesInSourceBounded(f.a, 2, 1<<20); !cut {
		t.Errorf("over the file budget: %v cut=%v, want the list flagged incomplete", refs, cut)
	}
	if refs, cut := pl.referencesInSourceBounded(f.a, 1000, 10); !cut {
		t.Errorf("over the byte budget: %v cut=%v, want the list flagged incomplete", refs, cut)
	}
}
