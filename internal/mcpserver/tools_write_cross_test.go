package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func (r moveRig) del(ctx context.Context, extra string) ToolResult {
	return r.multi.CallKB(ctx, "kb-a", "concept_delete", json.RawMessage(`{"id":"ops/zzold"`+extra+`}`))
}

func (r moveRig) batch(ctx context.Context, ops string) ToolResult {
	return r.multi.CallKB(ctx, "kb-a", "concept_batch", json.RawMessage(`{"operations":`+ops+`}`))
}

func writesAB() context.Context {
	return perms(map[string]bool{"kb-a": true, "kb-b": true, "kb-c": false})
}

// AC-cross-kb-rewrite-12
func TestDeleteRefusesWritableCrossLinks(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{withC: true, withD: true})
	res := r.del(writesAB(), "")
	if !res.IsError || !strings.Contains(resultText(res), "inbound_links") || !strings.Contains(resultText(res), "kb-b:page") {
		t.Fatalf("want inbound_links naming kb-b:page: %s", resultText(res))
	}
	for _, leak := range []string{"kb-c", "kb-d"} {
		if strings.Contains(resultText(res), leak) {
			t.Errorf("the refusal names %s: %s", leak, resultText(res))
		}
	}
	if !strings.Contains(pageBody(t, r.kbs["kb-a"], "ops/zzold"), "the page") {
		t.Error("a refused delete must keep the page")
	}
	res = r.del(writesAB(), `,"force":true`)
	if res.IsError || !strings.Contains(resultText(res), "cross_kb: links in kb-b now dangle") {
		t.Fatalf("force: %s", resultText(res))
	}
	if strings.Contains(resultText(res), "kb-c") || strings.Contains(resultText(res), "kb-d") {
		t.Errorf("force names a KB the caller cannot write: %s", resultText(res))
	}

	// Unreadable, read-only and absent: the same success text, no refusal.
	readOnlyB := perms(map[string]bool{"kb-a": true, "kb-b": false})
	noB := perms(map[string]bool{"kb-a": true})
	var texts []string
	for _, c := range []struct {
		name string
		ctx  context.Context
		opts moveRigOpts
	}{
		{"read-only", readOnlyB, moveRigOpts{}},
		{"unreadable", noB, moveRigOpts{}},
		{"absent", noB, moveRigOpts{noLinks: true}},
	} {
		rig := newMoveRig(t, c.opts)
		got := rig.del(c.ctx, "")
		if got.IsError {
			t.Fatalf("%s: refused: %s", c.name, resultText(got))
		}
		texts = append(texts, resultText(got))
	}
	if texts[0] != texts[1] || texts[1] != texts[2] {
		t.Errorf("responses differ:\n%q\n%q\n%q", texts[0], texts[1], texts[2])
	}
}

// AC-cross-kb-rewrite-18
func TestBatchDeleteCrossForce(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	del := `[{"op":"delete","id":"ops/zzold"}]`
	res := r.batch(writesAB(), del)
	if !res.IsError || !strings.Contains(resultText(res), "inbound_links") || !strings.Contains(resultText(res), "kb-b:page") {
		t.Fatalf("want inbound_links naming kb-b:page: %s", resultText(res))
	}
	if !strings.Contains(pageBody(t, r.kbs["kb-a"], "ops/zzold"), "the page") {
		t.Error("nothing may be deleted by a refused batch")
	}
	res = r.batch(writesAB(), `[{"op":"delete","id":"ops/zzold","force":true}]`)
	if res.IsError {
		t.Fatalf("force on the delete: %s", resultText(res))
	}
	var out struct {
		CrossKB struct {
			Dangling []string `json:"dangling"`
		} `json:"cross_kb"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil || len(out.CrossKB.Dangling) != 1 || out.CrossKB.Dangling[0] != "kb-b" {
		t.Errorf("dangling KBs: %s (%v)", resultText(res), err)
	}

	// force never lifts an internal linker outside the batch, and is a delete-only field.
	r2 := newMoveRig(t, moveRigOpts{noLinks: true})
	newCrossRewriteRig(t).put(t, r2.kbs["kb-a"], "inner", notePage+"see [[ops/zzold]]\n")
	res = r2.batch(writesAB(), `[{"op":"delete","id":"ops/zzold","force":true}]`)
	if !res.IsError || !strings.Contains(resultText(res), "inbound_links") {
		t.Errorf("an internal linker must still refuse: %s", resultText(res))
	}
	res = r2.batch(writesAB(), `[{"op":"write","id":"newpage","frontmatter":{"type":"Note"},"body":"x","force":true}]`)
	if !res.IsError || !strings.Contains(resultText(res), "force applies to delete operations only") {
		t.Errorf("force on a write: %s", resultText(res))
	}
}

// A plain page is not blocked by a link to a page under its name: only an
// expanded concept owns what lies under id+"/".
func TestDeletePlainPageIgnoresLinksUnderItsName(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{noLinks: true})
	h := newCrossRewriteRig(t)
	h.put(t, r.kbs["kb-a"], "solo", notePage+"plain\n")
	h.put(t, r.kbs["kb-a"], "solo/sub", notePage+"sub\n")
	h.put(t, r.kbs["kb-b"], "linker", notePage+"[[kb:kb-a/solo/sub]]\n")
	// A page written by hand is seen within 3 s (the other KBs' graph snapshot
	// age); taking the lock, as any write does, makes the next read look again.
	r.kbs["kb-b"].WithGitLock(func() error { return nil })
	// The page itself is protected by the link...
	res := r.multi.CallKB(writesAB(), "kb-a", "concept_delete", json.RawMessage(`{"id":"solo/sub"}`))
	if !res.IsError || !strings.Contains(resultText(res), "kb-b:linker") {
		t.Fatalf("the page itself is protected: %s", resultText(res))
	}
	// ...and the plain page above it is not blocked by that link.
	res = r.multi.CallKB(writesAB(), "kb-a", "concept_delete", json.RawMessage(`{"id":"solo"}`))
	if res.IsError {
		t.Fatalf("a link to solo/sub must not block deleting solo: %s", resultText(res))
	}
}
