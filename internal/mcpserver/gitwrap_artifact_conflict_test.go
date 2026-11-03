package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// A rebase conflict on a file that is neither a concept nor reserved (a skill)
// used to be registered nowhere: every write failed with "0 concept(s)" and no
// tool listed or resolved it (D1565).
func TestGitWrap_ConflictOnASkillFileIsRegisteredAndResolvable(t *testing.T) {
	t.Parallel()
	for _, strategy := range []string{"theirs", "ours"} {
		k, bare := setupGitKBWithRemote(t)
		k.SyncInWindow = 0
		if err := k.SyncOut(); err != nil {
			t.Fatalf("seed SyncOut: %v", err)
		}
		branch, _ := gitx.Branch(k.Root)
		const skill = "skills/x/SKILL.md"
		pushRemoteFile(t, bare, branch, skill, "---\nname: x\n---\nremote\n", "remote skill")
		abs := filepath.Join(k.Root, skill)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("---\nname: x\n---\nlocal\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := k.CommitOp("local skill"); err != nil {
			t.Fatal(err)
		}

		res := writeWrappedTool(t, k, "test_write", nil)
		msg := res.Content[0].Text
		if !res.IsError || !strings.Contains(msg, skill) || !strings.Contains(msg, "git_conflict_resolve") || strings.Contains(msg, "0 concept(s)") {
			t.Fatalf("write = %+v, want a refusal naming %s", res, skill)
		}
		cs, _ := k.ListConflicts()
		if len(cs) != 1 || cs[0].Kind != kb.ConflictKindArtifact || cs[0].ConceptID != skill {
			t.Fatalf("registry = %+v", cs)
		}
		list, _ := toolConflictsList(k).Handler(authLocalContext(), nil)
		if !strings.Contains(list.Content[0].Text, skill) {
			t.Fatalf("conflicts_list = %s", list.Content[0].Text)
		}
		resolve := toolGitConflictResolve(k).Handler
		if r, _ := resolve(authLocalContext(), json.RawMessage(`{"concept_id":"`+skill+`","strategy":"edit","body":"x"}`)); !r.IsError {
			t.Fatalf("edit on an artifact accepted: %+v", r)
		}
		if r, _ := resolve(authLocalContext(), json.RawMessage(`{"concept_id":"`+skill+`","strategy":"`+strategy+`"}`)); r.IsError {
			t.Fatalf("resolve %s: %+v", strategy, r)
		}
		if cs, _ := k.ListConflicts(); len(cs) != 0 {
			t.Fatalf("registry after resolve = %+v", cs)
		}
		if res := writeWrappedTool(t, k, "test_write_again", nil); res.IsError {
			t.Fatalf("write after resolve (%s): %+v", strategy, res)
		}
	}
}

func TestConflictMessage_NeverPromisesAToolForNothing(t *testing.T) {
	t.Parallel()
	if m := conflictMessage(0, nil, nil); strings.Contains(m, "git_conflict_resolve") || strings.Contains(m, "conflicts_list") {
		t.Fatalf("message = %q", m)
	}
	m := conflictMessage(0, nil, []string{"skills/x/SKILL.md"})
	if !strings.Contains(m, "skills/x/SKILL.md") || !strings.Contains(m, "ours|theirs") {
		t.Fatalf("message = %q", m)
	}
}
