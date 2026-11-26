package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// AC-cross-kb-rewrite-13
func TestAliasLinkNeutralForNonReader(t *testing.T) {
	t.Parallel()
	r := newCrossRig(t) // old-b is the recorded alias of kb-b
	l := kb.CrossLink{KB: "old-b", ID: "ops/x"}
	if got := crossStateFor(principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-b"}), r.a, l); got != crossResolved {
		t.Errorf("a reader of the current KB: %q, want resolved", got)
	}
	// A caller who cannot read kb-b sees what an absent KB shows.
	absent := crossStateFor(principal(auth.Permission{KB: "kb-a"}), r.a, kb.CrossLink{KB: "nope", ID: "ops/x"})
	if got := crossStateFor(principal(auth.Permission{KB: "kb-a"}), r.a, l); got != crossUnresolved || got != absent {
		t.Errorf("a non-reader: %q, want %q (the value of an absent KB)", got, absent)
	}
}

// The relation guard seen through crossStateFor: an alias is followed to the
// remote the page's KB declares for that name, and to nothing else.
func TestAliasRelationGuardThroughState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	r := newCrossRig(t)
	gitIn(t, r.a.Root, "remote", "add", "origin", "https://git.example.com/org/a.git")
	gitIn(t, r.b.Root, "remote", "add", "origin", "https://git.example.com/org/b.git")
	settings := filepath.Join(r.a.Root, kb.SettingsFile)
	if err := os.WriteFile(settings, []byte("depends_on:\n  - {name: old-b, remote: git.example.com/org/other}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.a.Root, "add", kb.SettingsFile)
	gitIn(t, r.a.Root, "commit", "-q", "-m", "deps")
	gitIn(t, r.a.Root, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitIn(t, r.a.Root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	ctx := principal(auth.Permission{KB: "kb-a"}, auth.Permission{KB: "kb-b"})
	if got := crossStateFor(ctx, r.a, kb.CrossLink{KB: "old-b", ID: "ops/x"}); got != crossUnresolved {
		t.Errorf("alias to another remote: %q, want unresolved", got)
	}
	if got := crossStateFor(ctx, r.a, kb.CrossLink{KB: "kb-b", ID: "ops/x"}); got != crossResolved {
		t.Errorf("the current name has no guard: %q", got)
	}
}

// AC-cross-kb-rewrite-15
func TestMoveAfterRenameRewritesOldName(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{}) // kb-b/viaalias links [[kb:old-a/...]], old-a being the old name of kb-a
	if res := r.move(perms(map[string]bool{"kb-a": true, "kb-b": true}), ""); res.IsError {
		t.Fatal(resultText(res))
	}
	if got := pageBody(t, r.kbs["kb-b"], "viaalias"); !strings.HasSuffix(got, "---\nsee [[kb:kb-a/ops/zznew]]\n") {
		t.Errorf("a link written with the old name must follow, in the current name: %q", got)
	}
}
