package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// moveRig is kb-a (the origin, holding ops/zzold and an alias old-a), kb-b and
// kb-c (each holding a page that links it) and, when withD, kb-d (the same),
// behind a real MultiKBServer.
type moveRig struct {
	multi *MultiKBServer
	kbs   map[string]*kb.KB
}

type moveRigOpts struct {
	withC, withD bool
	callKB       func(MultiCall) MultiCall
}

// MultiCall is the CallKB signature, named for the hook of newMoveRig.
type MultiCall = func(ctx context.Context, name, tool string, args json.RawMessage) ToolResult

func newMoveRig(t *testing.T, o moveRigOpts) moveRig {
	t.Helper()
	r := moveRig{multi: NewMultiKBServer("test"), kbs: map[string]*kb.KB{}}
	names := []string{"kb-a", "kb-b"}
	if o.withC {
		names = append(names, "kb-c")
	}
	if o.withD {
		names = append(names, "kb-d")
	}
	byName := map[string]*kb.KB{}
	for _, n := range names {
		k := kbtest.New(t)
		k.AuthName = n
		byName[n] = k
		r.kbs[n] = k
	}
	m := kb.NewMounts(byName, names, map[string]string{"old-a": "kb-a"})
	for _, k := range byName {
		k.SetMounts(m)
	}
	call := MultiCall(r.multi.CallKB)
	if o.callKB != nil {
		call = o.callKB(call)
	}
	helper := newCrossRewriteRig(t) // only for its put helper
	helper.put(t, byName["kb-a"], "ops/zzold", notePage+"the page\n")
	helper.put(t, byName["kb-a"], "friend", notePage+"unrelated\n")
	for _, n := range names[1:] {
		helper.put(t, byName[n], "page", notePage+"see [[kb:kb-a/ops/zzold#s|l]]\n")
		helper.put(t, byName[n], "viaalias", notePage+"see [[kb:old-a/ops/zzold]]\n")
	}
	for _, n := range names {
		k := byName[n]
		r.multi.MountKB(n, func(s *Server) {
			RegisterKBTools(s, k, Deps{CallKB: call, KBByName: func(n string) (*kb.KB, bool) { k, ok := byName[n]; return k, ok }})
		})
	}
	t.Cleanup(func() {
		for _, k := range byName {
			waitReadRefresh(k)
		}
	})
	return r
}

func moveArgs(extra string) string {
	return `{"source_id":"ops/zzold","target_id":"ops/zznew"` + extra + `}`
}

func (r moveRig) move(ctx context.Context, extra string) ToolResult {
	return r.multi.CallKB(ctx, "kb-a", "concept_move", json.RawMessage(moveArgs(extra)))
}

// writer writes kb-a, kb-b and kb-c; reader writes a and b and only reads c.
func perms(spec map[string]bool) context.Context {
	var ps []auth.Permission
	for n, w := range spec {
		ps = append(ps, auth.Permission{KB: n, Write: w})
	}
	return principal(ps...)
}

func crossKB(t *testing.T, res ToolResult) map[string]json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(res)), &obj); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, resultText(res))
	}
	if raw, ok := obj["cross_kb"]; ok {
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return nil
}

func lastMessage(t *testing.T, k *kb.KB) string { return gitOut(t, k, "log", "-1", "--format=%B") }

// AC-cross-kb-rewrite-5
func TestMoveRewritesCrossLinks(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{withC: true})
	a, b := r.kbs["kb-a"], r.kbs["kb-b"]
	beforeA, beforeB := commitCount(t, a), commitCount(t, b)
	res := r.move(perms(map[string]bool{"kb-a": true, "kb-b": true, "kb-c": false}), "")
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if commitCount(t, a)-beforeA != 1 || commitCount(t, b)-beforeB != 1 {
		t.Errorf("commits: kb-a +%d, kb-b +%d, want +1 each", commitCount(t, a)-beforeA, commitCount(t, b)-beforeB)
	}
	if got := pageBody(t, b, "page"); !strings.HasSuffix(got, "---\nsee [[kb:kb-a/ops/zznew#s|l]]\n") {
		t.Errorf("kb-b page = %q", got)
	}
	if got := pageBody(t, r.kbs["kb-c"], "page"); !strings.Contains(got, "ops/zzold") {
		t.Errorf("kb-c is not writable by the caller and must stay: %q", got)
	}
	ck := crossKB(t, res)
	if string(ck["rewritten"]) != `[{"kb":"kb-b","pages":2,"links":2}]` {
		t.Errorf("rewritten = %s", ck["rewritten"])
	}
	if string(ck["not_rewritten"]) != `[{"kb":"kb-c","pages":2}]` {
		t.Errorf("not_rewritten = %s", ck["not_rewritten"])
	}
	if _, ok := ck["failed"]; ok {
		t.Errorf("unexpected failure: %s", ck["failed"])
	}
	// rewrite_links=false leaves the other KBs alone and reports nothing.
	r2 := newMoveRig(t, moveRigOpts{})
	res = r2.move(perms(map[string]bool{"kb-a": true, "kb-b": true}), `,"rewrite_links":false`)
	if res.IsError && !strings.Contains(resultText(res), "linker") {
		t.Fatal(resultText(res))
	}
	if crossKB(t, res) != nil || !strings.Contains(pageBody(t, r2.kbs["kb-b"], "page"), "ops/zzold") {
		t.Error("rewrite_links=false must skip the cross-KB rewrite")
	}
}

// AC-cross-kb-rewrite-16
func TestRewriteCommitCarriesFixedReason(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	ctx := perms(map[string]bool{"kb-a": true, "kb-b": true})
	if res := r.move(ctx, `,"reason":"secret text zzold"`); res.IsError {
		t.Fatal(resultText(res))
	}
	msg := lastMessage(t, r.kbs["kb-b"])
	if !strings.Contains(msg, "Reason: "+crossRewriteReason) {
		t.Errorf("kb-b commit lacks the fixed reason: %q", msg)
	}
	for _, leak := range []string{"secret", "zzold", "zznew", "kb-a", "old-a"} {
		if strings.Contains(msg, leak) {
			t.Errorf("kb-b commit message leaks %q: %q", leak, msg)
		}
	}
	if strings.Contains(crossRewriteReason, "%") {
		t.Error("the constant must have no format verb")
	}
	if !strings.Contains(lastMessage(t, r.kbs["kb-a"]), "secret text zzold") {
		t.Error("the origin keeps its own reason")
	}
}

// AC-cross-kb-rewrite-6
func TestMoveNonDisclosure(t *testing.T) {
	t.Parallel()
	ctx := func() context.Context { return perms(map[string]bool{"kb-a": true, "kb-b": true}) }
	with := newMoveRig(t, moveRigOpts{withC: true, withD: true})
	without := newMoveRig(t, moveRigOpts{})
	got, want := with.move(ctx(), ""), without.move(ctx(), "")
	if got.IsError || want.IsError {
		t.Fatal(resultText(got), resultText(want))
	}
	if resultText(got) != resultText(want) {
		t.Errorf("an unreadable KB changes the answer:\n with: %s\n without: %s", resultText(got), resultText(want))
	}
	for _, n := range []string{"kb-c", "kb-d"} {
		if !strings.Contains(pageBody(t, with.kbs[n], "page"), "ops/zzold") {
			t.Errorf("%s was written by a caller with no grant on it", n)
		}
	}
	// A scoped writer of kb-b cannot be allowed to touch it: its link dangles, unnamed.
	scoped := newMoveRig(t, moveRigOpts{})
	res := scoped.move(principal(auth.Permission{KB: "kb-a", Write: true}, auth.Permission{KB: "kb-b", Write: true, Maps: []string{"ops"}}), "")
	if res.IsError || strings.Contains(resultText(res), "cross_kb") && !strings.Contains(resultText(res), "not_rewritten") {
		t.Errorf("scoped writer: %s", resultText(res))
	}
	if !strings.Contains(pageBody(t, scoped.kbs["kb-b"], "page"), "ops/zzold") {
		t.Error("a scoped writer's call must not rewrite kb-b")
	}
}

// AC-cross-kb-rewrite-7
func TestMovePartialFailure(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{withC: true})
	helper := newCrossRewriteRig(t)
	helper.put(t, r.kbs["kb-b"], "zbad", "---\ntype: [unclosed\n---\n[[kb:kb-a/ops/zzold]]\n")
	ctx := perms(map[string]bool{"kb-a": true, "kb-b": true, "kb-c": true})
	beforeA := commitCount(t, r.kbs["kb-a"])
	res := r.move(ctx, "")
	if res.IsError {
		t.Fatalf("the move is committed, the result is not an error: %s", resultText(res))
	}
	if commitCount(t, r.kbs["kb-a"])-beforeA != 1 {
		t.Error("kb-a's move must stay committed")
	}
	ck := crossKB(t, res)
	if !strings.Contains(string(ck["failed"]), `"kb":"kb-b"`) || !strings.Contains(string(ck["failed"]), "zbad") {
		t.Errorf("failed = %s", ck["failed"])
	}
	if string(ck["rewritten"]) != `[{"kb":"kb-c","pages":2,"links":2}]` {
		t.Errorf("kb-c must still be rewritten: %s", ck["rewritten"])
	}
	var note string
	json.Unmarshal(ck["note"], &note)
	if note == "" || ck["retry_retargets"] == nil {
		t.Errorf("the failure must say how to retry: %v", ck)
	}
	// Repair the page, then retry with the reported retargets.
	helper.put(t, r.kbs["kb-b"], "zbad", notePage+"[[kb:kb-a/ops/zzold]]\n")
	retry := `{"retargets":` + string(ck["retry_retargets"]) + `}`
	if out := r.multi.CallKB(ctx, "kb-b", "cross_link_rewrite", json.RawMessage(retry)); out.IsError {
		t.Fatal(resultText(out))
	}
	if strings.Contains(pageBody(t, r.kbs["kb-b"], "page"), "zzold") || strings.Contains(pageBody(t, r.kbs["kb-b"], "zbad"), "zzold") {
		t.Error("cross_link_rewrite with the reported retargets must repair kb-b")
	}
}

// AC-cross-kb-rewrite-8
func TestNoSelfNamedForm(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	helper := newCrossRewriteRig(t)
	self := notePage + "pre-existing [[kb:kb-a/ops/zzold]] and [[kb:old-a/ops/zzold]]\n"
	helper.put(t, r.kbs["kb-a"], "selfp", self)
	res := r.move(perms(map[string]bool{"kb-a": true, "kb-b": true}), "")
	if res.IsError {
		t.Fatalf("a pre-existing self-named link must not block the move: %s", resultText(res))
	}
	if got := pageBody(t, r.kbs["kb-a"], "selfp"); !strings.Contains(got, "[[kb:kb-a/ops/zzold]]") || !strings.Contains(got, "[[kb:old-a/ops/zzold]]") {
		t.Errorf("the move must not read or rewrite a self-named link: %q", got)
	}
	for n, k := range r.kbs {
		for _, id := range []string{"page", "viaalias"} {
			if n == "kb-a" {
				continue
			}
			if body := pageBody(t, k, id); strings.Contains(body, "[[kb:"+n+"/") {
				t.Errorf("%s/%s holds a self-named link: %q", n, id, body)
			}
		}
	}
}

func TestMoveSkipsWhatTheConnectionDoesNotServe(t *testing.T) {
	t.Parallel()
	r := newMoveRig(t, moveRigOpts{})
	ctx := withRoutedSet(perms(map[string]bool{"kb-a": true, "kb-b": true}), []string{"kb-a"})
	res := r.move(ctx, "")
	if res.IsError || crossKB(t, res) != nil || strings.Contains(resultText(res), "kb-b") {
		t.Errorf("a KB outside the connection must be skipped silently: %s", resultText(res))
	}
	if !strings.Contains(pageBody(t, r.kbs["kb-b"], "page"), "zzold") {
		t.Error("kb-b is outside the routed set and must stay")
	}
	// Read-only on the connection: not written, but the caller reads it, so it is counted.
	r2 := newMoveRig(t, moveRigOpts{})
	ctx = withRoutedReadOnly(perms(map[string]bool{"kb-a": true, "kb-b": true}), []string{"kb-b"})
	res = r2.move(ctx, "")
	if string(crossKB(t, res)["not_rewritten"]) != `[{"kb":"kb-b","pages":2}]` {
		t.Errorf("read-only on the connection: %s", resultText(res))
	}
}

func TestSingleKBHasNoCrossKB(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	helper := newCrossRewriteRig(t)
	helper.put(t, k, "ops/zzold", notePage+"x\n")
	res := callTool(t, s, "concept_move", moveArgs(""))
	if res.IsError || crossKB(t, res) != nil {
		t.Errorf("single-KB: %s", resultText(res))
	}
}

func TestCrossMovesNeverReachTheWire(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(ToolResult{Content: []ContentBlock{{Type: "text", Text: "x"}}, crossMoves: []kb.CrossRetarget{{FromKB: "kb-a", FromID: "secretid"}}})
	if err != nil || strings.Contains(string(b), "secretid") || strings.Contains(string(b), "crossMoves") {
		t.Errorf("crossMoves on the wire: %s %v", b, err)
	}
}

// AC-cross-kb-rewrite-9
func TestOneLockAtATime(t *testing.T) {
	t.Parallel()
	var rig moveRig
	probes := 0
	var mu sync.Mutex
	rig = newMoveRig(t, moveRigOpts{callKB: func(next MultiCall) MultiCall {
		return func(ctx context.Context, name, tool string, args json.RawMessage) ToolResult {
			// The origin has released its lock before a follow-up starts: taking it
			// here, from the goroutine that made the move, must not wait forever.
			for _, n := range []string{"kb-a", "kb-b"} {
				if kbLockHeld(rig.kbs[n], 5*time.Second) {
					t.Errorf("%s's lock is held while the follow-up on %s starts", n, name)
				}
			}
			mu.Lock()
			probes++
			mu.Unlock()
			return next(ctx, name, tool, args)
		}
	}})
	helper := newCrossRewriteRig(t)
	helper.put(t, rig.kbs["kb-b"], "ops/other", notePage+"o\n")
	helper.put(t, rig.kbs["kb-a"], "xlink", notePage+"[[kb:kb-b/ops/other]]\n")
	helper.put(t, rig.kbs["kb-a"], "tr", notePage+"t\n")
	ctx := perms(map[string]bool{"kb-a": true, "kb-b": true})
	done := make(chan string, 3)
	go func() { done <- "move-a" + fmt.Sprint(rig.move(ctx, "").IsError) }()
	go func() {
		done <- "move-b" + fmt.Sprint(rig.multi.CallKB(ctx, "kb-b", "concept_move", json.RawMessage(`{"source_id":"ops/other","target_id":"ops/other2"}`)).IsError)
	}()
	go func() {
		done <- "transfer" + fmt.Sprint(rig.multi.CallKB(ctx, "kb-a", "concept_transfer", json.RawMessage(`{"target_kb":"kb-b","moves":[{"source_id":"tr","target_id":"tr"}]}`)).IsError)
	}()
	deadline := time.After(10 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case got := <-done:
			if strings.HasSuffix(got, "true") {
				t.Errorf("%s failed", got)
			}
		case <-deadline:
			t.Fatal("concurrent moves and a transfer between two KBs did not finish: deadlock")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if probes == 0 {
		t.Error("the hook never ran: no follow-up was made")
	}
}

// kbLockHeld says whether k's git lock could not be taken within wait, by a
// goroutine of its own: a lock held by the goroutine that asks (a path that
// calls another KB with its own lock still held) is never released meanwhile.
func kbLockHeld(k *kb.KB, wait time.Duration) bool {
	done := make(chan struct{})
	go func() { k.WithGitLock(func() error { return nil }); close(done) }()
	select {
	case <-done:
		return false
	case <-time.After(wait):
		return true
	}
}

// The probe of AC-9 sees a held lock (a path holding X's lock while it calls Y
// would fail the test) and sees a free one.
func TestLockProbeSeesAHeldLock(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	if kbLockHeld(k, time.Second) {
		t.Error("a free lock reads as held")
	}
	k.WithGitLock(func() error {
		if !kbLockHeld(k, 200*time.Millisecond) {
			t.Error("a held lock reads as free")
		}
		return nil
	})
}
