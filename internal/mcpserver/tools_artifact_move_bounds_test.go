package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillFiles puts n files of size bytes each, next to a valid SKILL.md.
func writeSkillFiles(t *testing.T, f *transferFixture, n, size int) {
	t.Helper()
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	blob := make([]byte, size)
	for i := 0; i < n-1; i++ {
		amWriteBytes(t, f.a, fmt.Sprintf("skills/s/res/f%04d.bin", i), blob, 0o644)
	}
	xferCommit(t, f.a)
}

func assertRefusedUntouched(t *testing.T, f *transferFixture, res ToolResult, wants ...string) {
	t.Helper()
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)
	if !res.IsError {
		t.Fatalf("want a refusal, got %s", xferText(res))
	}
	for _, w := range wants {
		if !strings.Contains(xferText(res), w) {
			t.Errorf("the refusal %q does not say %q", xferText(res), w)
		}
	}
	if !amExists(f.a, "skills/s/SKILL.md") || amExists(f.b, "skills/s") {
		t.Error("a refused move changed a KB")
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Error("a refused move committed")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_RefusesAnArtifactWithTooManyFiles(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	writeSkillFiles(t, f, artifactMoveMaxFiles+1, 1)
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`)
	assertRefusedUntouched(t, f, res, "skills/s", fmt.Sprintf("more than %d files", artifactMoveMaxFiles))
}

func TestArtifactMove_RefusesAnArtifactOverTheByteBound(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	writeSkillFiles(t, f, artifactMoveMaxBytes/artifactMaxFileSize+2, artifactMaxFileSize)
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`)
	assertRefusedUntouched(t, f, res, "skills/s", fmt.Sprintf("more than %d bytes", artifactMoveMaxBytes))
}

// The walk stops at the bound: the numbers in the refusal are the limits, and a
// directory with a million files is not read to the end under both locks.
func TestCollectTransferFilesBounded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), make([]byte, 10), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if files, err := collectTransferFilesBounded(dir, 5, 50); err != nil || len(files) != 5 {
		t.Errorf("exactly at both bounds: %d files, %v", len(files), err)
	}
	if _, err := collectTransferFilesBounded(dir, 4, 1000); err == nil || !strings.Contains(err.Error(), "more than 4 files") {
		t.Errorf("one file over: %v", err)
	}
	if _, err := collectTransferFilesBounded(dir, 100, 49); err == nil || !strings.Contains(err.Error(), "more than 49 bytes") {
		t.Errorf("one byte over: %v", err)
	}
	if files, err := collectTransferFiles(dir); err != nil || len(files) != 5 {
		t.Errorf("concept_transfer's walk must stay unbounded here: %d files, %v", len(files), err)
	}
}

func refsOf(t *testing.T, res ToolResult) (refs []string, truncated bool) {
	t.Helper()
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	var out struct {
		Refs      []string `json:"references_in_source"`
		Truncated bool     `json:"references_truncated"`
	}
	if err := json.Unmarshal([]byte(xferText(res)), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, xferText(res))
	}
	return out.Refs, out.Truncated
}

func TestArtifactMove_ReferencesStopAtTheBound(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	for i := 0; i < artifactRefsMax+5; i++ {
		amWriteBytes(t, f.a, fmt.Sprintf("data/notes/n%03d.md", i), []byte("---\ntype: Note\ntitle: N\n---\nSee skills/s.\n"), 0o644)
	}
	xferCommit(t, f.a)
	refs, truncated := refsOf(t, f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`))
	if !truncated || len(refs) != artifactRefsMax {
		t.Fatalf("%d references, truncated=%v: want %d and true", len(refs), truncated, artifactRefsMax)
	}
	for i, r := range refs {
		if want := fmt.Sprintf("data/notes/n%03d.md → skills/s", i); r != want {
			t.Fatalf("references[%d] = %q, want %q (the first hits in walk order, no free-text tail)", i, r, want)
		}
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_ReferencesBelowTheBoundAreNotTruncated(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	amWriteBytes(t, f.a, "data/notes/n.md", []byte("---\ntype: Note\ntitle: N\n---\nSee skills/s.\n"), 0o644)
	xferCommit(t, f.a)
	refs, truncated := refsOf(t, f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`))
	if truncated || len(refs) != 1 {
		t.Errorf("%d references, truncated=%v: want 1 and false", len(refs), truncated)
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// A file above the per-file scan cap is not read, and the list says it may be
// incomplete rather than silently omitting what the file holds.
func TestArtifactMove_ReferencesSkipAFileAboveTheScanCap(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	big := append([]byte("---\ntype: Note\ntitle: Big\n---\nSee skills/s.\n"), bytes.Repeat([]byte("x"), transferScanMaxBytes)...)
	amWriteBytes(t, f.a, "data/notes/big.md", big, 0o644)
	xferCommit(t, f.a)
	refs, truncated := refsOf(t, f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`))
	if len(refs) != 0 || !truncated {
		t.Errorf("references %v truncated=%v: the oversized file must be skipped and flagged", refs, truncated)
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// The lint run under the locks reports what it did before: a junk file inside a
// moved unit is still a finding of the target.
func TestArtifactMove_LintStillReportsJunkInAMovedUnit(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/s/SKILL.md", []byte("---\nname: s\ndescription: A skill\n---\nbody\n"), 0o644)
	amWriteBytes(t, f.a, "skills/s/.DS_Store", []byte("x"), 0o644)
	xferCommit(t, f.a)
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/s"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	var out struct {
		Findings map[string][]findingOut `json:"findings_by_kb"`
	}
	if err := json.Unmarshal([]byte(xferText(res)), &out); err != nil {
		t.Fatal(err)
	}
	var junk bool
	for _, fd := range out.Findings["kb-b"] {
		junk = junk || (fd.Check == "junk_file" && fd.Path == "skills/s/.DS_Store")
	}
	if !junk {
		t.Errorf("findings_by_kb lacks junk_file on the moved .DS_Store: %+v", out.Findings)
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}
