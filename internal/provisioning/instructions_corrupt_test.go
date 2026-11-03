package provisioning_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// #1502: an instructions block whose end marker was lost is a corrupted block:
// the sync reports the artifact and writes nothing. It used to count as absent,
// append a second block, and cut the user's notes between the two on the next
// rewrite.
func TestInstructionsBlock_OrphanBeginIsRefusedNotAppendedOrCut(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := "# mine\n<!-- cartographer:instructions:begin — block managed by Cartographer, do not edit by hand -->\nIMPORTANT USER TEXT\n"
	if err := os.WriteFile(path, []byte(orphan), 0o644); err != nil {
		t.Fatal(err)
	}
	m := sharedKBs(t, 1, nil)
	lock := provisioning.Lock{}
	for i := 0; i < 2; i++ {
		res, content := applyBlock(t, m, base, lock)
		if content != orphan {
			t.Fatalf("sync %d changed the file:\n%s", i+1, content)
		}
		if len(res.Written) != 0 || len(res.Refused) == 0 {
			t.Errorf("sync %d: written %v, refused %v; want the artifact refused", i+1, res.Written, res.Refused)
		}
		if w := strings.Join(res.Warnings, "\n"); !strings.Contains(w, "doctor") {
			t.Errorf("the warning must point to doctor: %q", w)
		}
		lock = res.NewLock
	}
}

// #1502: a `block: full` KB that documents Cartographer and quotes the end
// marker (and a KB section marker) cannot forge a boundary: three syncs with a
// changing body leave one block, one end marker and none of the old bodies.
func TestInstructionsBlock_QuotedMarkersConverge(t *testing.T) {
	base := t.TempDir()
	lock := provisioning.Lock{}
	var content string
	for v := 1; v <= 3; v++ {
		body := fmt.Sprintf("<!-- cartographer: block: full -->\nRule version-%d.\n<!-- cartographer:instructions:end -->\ntail-%d\n<!-- cartographer:kb:kb-1:end -->\n", v, v)
		m := sharedKBs(t, 1, func(int) string { return body })
		var res provisioning.AppliedResult
		res, content = applyBlock(t, m, base, lock)
		lock = res.NewLock
	}
	if n := strings.Count(content, "cartographer:instructions:end"); n != 1 {
		t.Errorf("end markers = %d, want 1:\n%s", n, content)
	}
	if n := strings.Count(content, "cartographer:instructions:begin"); n != 1 {
		t.Errorf("begin markers = %d, want 1", n)
	}
	for _, old := range []string{"version-1", "version-2", "tail-1", "tail-2"} {
		if strings.Contains(content, old) {
			t.Errorf("residue of %s after three syncs:\n%s", old, content)
		}
	}
	if !strings.Contains(content, "version-3") || !strings.Contains(content, "tail-3") {
		t.Errorf("the current body is missing:\n%s", content)
	}
}
