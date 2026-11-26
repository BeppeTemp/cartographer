package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func (r moveRig) transfer(ctx context.Context, moves string) ToolResult {
	return r.multi.CallKB(ctx, "kb-a", "concept_transfer", json.RawMessage(`{"target_kb":"kb-b","moves":`+moves+`}`))
}

const transferOld = `[{"source_id":"ops/zzold","target_id":"ops/zznew"}]`

func transferCtx() context.Context {
	return perms(map[string]bool{"kb-a": true, "kb-b": true, "kb-c": true})
}

// AC-cross-kb-rewrite-10
func TestTransferRetargetsCrossLinks(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{withC: true, withD: true})
	a, b, c := r.kbs["kb-a"], r.kbs["kb-b"], r.kbs["kb-c"]
	ba, bb, bc := commitCount(t, a), commitCount(t, b), commitCount(t, c)
	res := r.transfer(transferCtx(), transferOld)
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if commitCount(t, a)-ba != 1 || commitCount(t, b)-bb != 2 || commitCount(t, c)-bc != 1 {
		t.Errorf("commits: kb-a +%d (want 1), kb-b +%d (want 2: the transfer and the rewrite), kb-c +%d (want 1)",
			commitCount(t, a)-ba, commitCount(t, b)-bb, commitCount(t, c)-bc)
	}
	if got := pageBody(t, c, "page"); !strings.HasSuffix(got, "---\nsee [[kb:kb-b/ops/zznew#s|l]]\n") {
		t.Errorf("kb-c page = %q", got)
	}
	if got := pageBody(t, c, "viaalias"); !strings.HasSuffix(got, "---\nsee [[kb:kb-b/ops/zznew]]\n") {
		t.Errorf("kb-c viaalias = %q", got)
	}
	if got := pageBody(t, b, "page"); !strings.HasSuffix(got, "---\nsee [[ops/zznew#s|l]]\n") {
		t.Errorf("kb-b page must hold the internal form: %q", got)
	}
	if !strings.Contains(pageBody(t, r.kbs["kb-d"], "page"), "kb:kb-a/ops/zzold") {
		t.Error("kb-d is not writable by the caller and must stay")
	}
	if strings.Contains(resultText(res), "kb-d") {
		t.Errorf("kb-d must not be named: %s", resultText(res))
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"source", "target", "moved"} {
		if out[key] == nil {
			t.Errorf("transfer result lost %q", key)
		}
	}
	ck := crossKB(t, res)
	if string(ck["rewritten"]) != `[{"kb":"kb-b","pages":2,"links":2},{"kb":"kb-c","pages":2,"links":2}]` {
		t.Errorf("rewritten = %s", ck["rewritten"])
	}
	if !strings.Contains(lastMessage(t, c), "Reason: "+crossRewriteReason) {
		t.Errorf("kb-c commit: %q", lastMessage(t, c))
	}
}

// AC-cross-kb-rewrite-11
func TestTransferPartialFailure(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{withC: true})
	newCrossRewriteRig(t).put(t, r.kbs["kb-c"], "zbad", "---\ntype: [unclosed\n---\n[[kb:kb-a/ops/zzold]]\n")
	ba, bb := commitCount(t, r.kbs["kb-a"]), commitCount(t, r.kbs["kb-b"])
	res := r.transfer(transferCtx(), transferOld)
	if res.IsError {
		t.Fatalf("the transfer is committed, the result is not an error: %s", resultText(res))
	}
	if commitCount(t, r.kbs["kb-a"])-ba != 1 || commitCount(t, r.kbs["kb-b"])-bb < 1 {
		t.Error("both transfer commits must stay")
	}
	ck := crossKB(t, res)
	if !strings.Contains(string(ck["failed"]), `"kb":"kb-c"`) || ck["retry_retargets"] == nil {
		t.Errorf("cross_kb = %v", ck)
	}
	var out map[string]json.RawMessage
	json.Unmarshal([]byte(resultText(res)), &out)
	for _, key := range []string{"source", "target", "moved", "cross_kb"} {
		if out[key] == nil {
			t.Errorf("missing %q in %s", key, resultText(res))
		}
	}
}

// AC-cross-kb-rewrite-17
func TestTransferInternalizesSelfNamed(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	h := newCrossRewriteRig(t)
	h.put(t, r.kbs["kb-b"], "z", notePage+"z\n")
	h.put(t, r.kbs["kb-a"], "stay", notePage+"stays\n")
	h.put(t, r.kbs["kb-a"], "mv1", notePage+"[[kb:kb-b/z]] [[kb:kb-a/mv2]] [[kb:kb-a/stay]] [[kb:kb-b@host/q]]\n")
	h.put(t, r.kbs["kb-a"], "mv2", notePage+"two\n")
	res := r.transfer(transferCtx(), `[{"source_id":"mv1","target_id":"mv1"},{"source_id":"mv2","target_id":"mv2b"}]`)
	if res.IsError {
		t.Fatal(resultText(res))
	}
	got := pageBody(t, r.kbs["kb-b"], "mv1")
	if !strings.HasSuffix(got, "---\n[[z]] [[mv2b]] [[kb:kb-a/stay]] [[kb:kb-b@host/q]]\n") {
		t.Errorf("moved page = %q", got)
	}
	if strings.Contains(got, "[[kb:kb-b/") {
		t.Error("a self-named link landed in kb-b")
	}
	// Inside the transfer's own commit: the page was already right there.
	k := r.kbs["kb-b"]
	sha := gitOut(t, k, "log", "--format=%H", "--grep=^concept_transfer", "-n", "1")
	rel, err := filepath.Rel(k.Root, filepath.Join(k.DataRoot(), "mv1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if at := gitOut(t, k, "show", sha+":"+filepath.ToSlash(rel)); !strings.Contains(at, "[[z]] [[mv2b]]") {
		t.Errorf("the transfer's own commit holds %q", at)
	}
}

func TestTransferErrorDoesNotFollowUp(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	bb := commitCount(t, r.kbs["kb-b"])
	res := r.transfer(transferCtx(), `[{"source_id":"missing","target_id":"missing"}]`)
	if !res.IsError || commitCount(t, r.kbs["kb-b"]) != bb {
		t.Errorf("a refused transfer must write nothing: %s", resultText(res))
	}
}

// A relation of the target that pins an old name of the source to a remote the
// source does not have leaves that alias unresolved: a moved page's link by it
// is copied as written, the link by the current name becomes internal. (A
// regression to every alias of the source would rewrite both.)
func TestTransferLeavesGuardedAliasLink(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	b := r.kbs["kb-b"]
	if err := os.WriteFile(filepath.Join(b.Root, kb.SettingsFile), []byte("depends_on:\n  - {name: old-a, remote: git.example.com/org/a}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, b.Root, "add", kb.SettingsFile)
	gitIn(t, b.Root, "commit", "-q", "-m", "deps")
	h := newCrossRewriteRig(t)
	h.put(t, r.kbs["kb-a"], "mv1", notePage+"[[kb:old-a/mv2]] [[kb:kb-a/mv2]]\n")
	h.put(t, r.kbs["kb-a"], "mv2", notePage+"two\n")
	res := r.transfer(transferCtx(), `[{"source_id":"mv1","target_id":"mv1"},{"source_id":"mv2","target_id":"mv2b"}]`)
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if got := pageBody(t, b, "mv1"); !strings.HasSuffix(got, "---\n[[kb:old-a/mv2]] [[mv2b]]\n") {
		t.Errorf("moved page = %q", got)
	}
}
