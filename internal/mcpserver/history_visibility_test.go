package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func opsOnlyCtx() requestContext {
	return restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})
}

func (c *changesLinksKB) commitMsg(msg string) {
	c.t.Helper()
	if err := gitx.Commit(c.k.Root, msg, "Tester", "tester@example.test"); err != nil {
		c.t.Fatal(err)
	}
}

func (c *changesLinksKB) typed(id, typ, body string) {
	c.t.Helper()
	p := filepath.Join(c.k.DataRoot(), filepath.FromSlash(id)+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntype: "+typ+"\ntitle: T\n---\n"+body), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func rawResult(res ToolResult) string {
	b, _ := json.Marshal(res)
	return string(b)
}

// A page moved in from a map the caller cannot see has no past behind the
// move: history stops there, and a revision before it is not found.
func TestHistoryStopsAtTheCallersPerimeter(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("hidden/page", "classified one\n")
	c.commitMsg("concept_write: hidden/page")
	c.write("hidden/page", "classified two\n")
	c.commitMsg("concept_write: hidden/page")
	c.rename("hidden/page", "ops/page")
	c.commitMsg("concept_move: hidden/page -> ops/page")
	c.write("ops/page", "open text\n")
	c.commitMsg("concept_write: ops/page")

	full, _ := c.history(`{"id":"ops/page"}`)
	if len(full.Revisions) != 4 {
		t.Fatalf("whole-KB history = %d revisions, want 4: %+v", len(full.Revisions), full)
	}
	oldSHA := full.Revisions[3].SHA

	res := c.s.callTool(opsOnlyCtx(), "concept_history", json.RawMessage(`{"id":"ops/page"}`))
	text := rawResult(res)
	if res.IsError || strings.Contains(text, "hidden") || strings.Contains(text, oldSHA) {
		t.Fatalf("scoped history crossed the perimeter: %s", text)
	}
	var got historyResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Revisions) != 2 || got.Revisions[1].Subject != "concept_move" {
		t.Fatalf("scoped history = %+v, want the edit and the move (subject cut to the tool name)", got.Revisions)
	}

	for _, sha := range []string{oldSHA, full.Revisions[2].SHA} {
		res = c.s.callTool(opsOnlyCtx(), "concept_read", json.RawMessage(`{"id":"ops/page","rev":"`+sha+`"}`))
		if !res.IsError || strings.Contains(rawResult(res), "classified") {
			t.Fatalf("rev %s read from outside the perimeter: %s", sha, rawResult(res))
		}
	}
	// The revision inside the perimeter stays readable.
	res = c.s.callTool(opsOnlyCtx(), "concept_read", json.RawMessage(`{"id":"ops/page","rev":"`+full.Revisions[0].SHA+`"}`))
	if res.IsError || !strings.Contains(rawResult(res), "open text") {
		t.Fatalf("in-perimeter rev not readable: %s", rawResult(res))
	}
	// The whole-KB caller still reads the old revision.
	if got, res := c.read(`{"id":"ops/page","rev":"` + oldSHA + `"}`); res.IsError || !strings.Contains(got["body"].(string), "classified one") {
		t.Fatalf("whole-KB rev read changed: %+v", res)
	}
}

// A caller whose rule narrows by type sees a revision only when the page had
// that type then.
func TestHistoryStopsAtATypeTheCallerCannotSee(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.typed("ops/doc", "Secret", "restricted text\n")
	c.commitMsg("concept_write: ops/doc")
	c.typed("ops/doc", "Note", "public text\n")
	c.commitMsg("concept_write: ops/doc")
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}, Types: []string{"Note"}}}})

	full, _ := c.history(`{"id":"ops/doc"}`)
	if len(full.Revisions) != 2 {
		t.Fatalf("history = %+v", full)
	}
	res := c.s.callTool(ctx, "concept_history", json.RawMessage(`{"id":"ops/doc"}`))
	var got historyResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Revisions) != 1 || got.Revisions[0].SHA != full.Revisions[0].SHA {
		t.Fatalf("typed history = %+v, want only the revision of the visible type", got.Revisions)
	}
	res = c.s.callTool(ctx, "concept_read", json.RawMessage(`{"id":"ops/doc","rev":"`+full.Revisions[1].SHA+`"}`))
	if !res.IsError || strings.Contains(rawResult(res), "restricted") {
		t.Fatalf("a revision of a hidden type was read: %s", rawResult(res))
	}
}

// A commit that touched a page outside the perimeter too is shown by its tool
// name alone, with no reason, in concept_history, changes_since and the
// stale_write hint.
func TestCommitTextOutsideThePerimeterIsCutDown(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("hidden/old", "classified\n")
	c.write("ops/linker", "see [h](../hidden/old.md)\n")
	c.commitMsg("seed")
	c.rename("hidden/old", "hidden/new")
	c.write("ops/linker", "see [h](../hidden/new.md)\n")
	c.commitMsg("concept_move: hidden/old -> hidden/new\n\nReason: rename of hidden/old for the secret project\n\nAccepted-Finding: broken_link: hidden/old")

	res := c.s.callTool(opsOnlyCtx(), "concept_history", json.RawMessage(`{"id":"ops/linker"}`))
	if text := rawResult(res); res.IsError || strings.Contains(text, "hidden") || strings.Contains(text, "secret") || !strings.Contains(text, "concept_move") {
		t.Fatalf("history names the hidden commit text: %s", text)
	}
	res = c.s.callTool(opsOnlyCtx(), "changes_since", json.RawMessage(`{"since":"7d"}`))
	if text := rawResult(res); res.IsError || strings.Contains(text, "hidden") || strings.Contains(text, "secret") || !strings.Contains(text, "concept_move") {
		t.Fatalf("changes_since names the hidden commit text: %s", text)
	}
	// The whole-KB caller keeps the full text.
	h, _ := c.history(`{"id":"ops/linker"}`)
	if len(h.Revisions) == 0 || !strings.Contains(h.Revisions[0].Subject, "hidden/old") {
		t.Fatalf("whole-KB subject changed: %+v", h)
	}
	if got := staleConceptSuffix(opsOnlyCtx(), c.k, "ops/linker"); strings.Contains(got, "hidden") || strings.Contains(got, "secret") {
		t.Fatalf("stale hint names the hidden commit text: %s", got)
	}
}

// changes_since attributes a commit made at an old path to the page's final
// id; a commit made while the page lay outside the perimeter is not reported.
func TestChangesSinceSkipsCommitsMadeOutsideThePerimeter(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("hidden/page", "one\n")
	c.commitMsg("concept_write: hidden/page")
	c.write("hidden/page", "two\n")
	c.commitMsg("concept_write: hidden/page")
	c.rename("hidden/page", "ops/page")
	c.commitMsg("concept_move: hidden/page -> ops/page")

	res := c.s.callTool(opsOnlyCtx(), "changes_since", json.RawMessage(`{"since":"7d"}`))
	var out changesSinceResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	for _, cc := range out.Concepts {
		if cc.ID == "ops/page" && (cc.Added || len(cc.Ops) != 1) {
			t.Fatalf("ops/page = %+v, want only the move, not the commits made in hidden/", cc)
		}
	}
	if text := rawResult(res); strings.Contains(text, "hidden") {
		t.Fatalf("changes_since names a hidden path: %s", text)
	}
}

// concept_move with rewrite_links=false reports what the move left behind
// (findings on linkers): a linker whose type the caller cannot see is not named.
func TestConceptMoveLeftBehindNamesOnlyVisibleLinkers(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("ops/target", "T\n")
	c.typed("ops/secretlinker", "Secret", "link [t](target.md)\n")
	c.commitMsg("seed")
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"ops"}, Types: []string{"Note"}}}})
	res := c.s.callTool(ctx, "concept_move", json.RawMessage(`{"source_id":"ops/target","target_id":"ops/target2","rewrite_links":false}`))
	if text := rawResult(res); strings.Contains(text, "secretlinker") {
		t.Fatalf("move response (error=%v) names a linker the caller cannot see: %s", res.IsError, text)
	}
}

// changes_since links:true compares a moved source with its content at the
// base only when the file lay inside the caller's perimeter then.
func TestChangesSinceLinksSkipBaseContentFromOutsideThePerimeter(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("ops/target", "T\n")
	c.write("hidden/src", "[t](../ops/target.md)\n")
	c.commit(false)
	c.rename("hidden/src", "ops/src")
	c.write("ops/src", "No links now.\n")
	c.commit(true)

	since := fmt.Sprintf(`{"since":%q,"links":true}`, c.base.Format(time.RFC3339))
	if got := c.call(adminCtx, since).Links; got == nil || edgeList(got.Removed) != "ops/src>ops/target" {
		t.Fatalf("whole-KB removed = %+v", got)
	}
	got := c.call(opsOnlyCtx(), since).Links
	if got == nil || len(got.Removed) != 0 {
		t.Fatalf("scoped removed = %+v, want none: the old links belong to a page outside the perimeter", got)
	}
}

// A commit that retypes a page from a type the caller cannot see to one it can
// is cut down too: the page's former type counts.
func TestRetypingCommitTextIsCutDown(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.typed("ops/doc", "Secret", "text\n")
	c.commitMsg("seed")
	c.typed("ops/doc", "Note", "text\n")
	c.commitMsg("concept_patch: ops/doc\n\nReason: declassify the secret project page")
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}, Types: []string{"Note"}}}})
	res := c.s.callTool(ctx, "concept_history", json.RawMessage(`{"id":"ops/doc"}`))
	text := rawResult(res)
	if res.IsError || strings.Contains(text, "declassify") || strings.Contains(text, "concept_patch: ops/doc") {
		t.Fatalf("retyping commit text not cut down: %s", text)
	}
}

// The files of a rebase conflict outside the caller's perimeter are counted,
// never named.
func TestConflictMessageDoesNotNameFilesOutsideThePerimeter(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("ops/p", "x\n")
	c.write("hidden/p", "x\n")
	c.commitMsg("seed")
	msg := callerConflictMessage(opsOnlyCtx(), c.k, 2, nil, []string{"data/ops/p.md", "data/hidden/p.md"})
	if strings.Contains(msg, "hidden") || !strings.Contains(msg, "data/ops/p.md") || !strings.Contains(msg, "1 conflicting file(s)") {
		t.Fatalf("message = %q", msg)
	}
	only := callerConflictMessage(opsOnlyCtx(), c.k, 0, nil, []string{"data/hidden/p.md"})
	if strings.Contains(only, "hidden") || strings.Contains(only, "could not be identified") {
		t.Fatalf("message = %q", only)
	}
	if whole := callerConflictMessage(adminCtx, c.k, 0, nil, []string{"data/hidden/p.md"}); !strings.Contains(whole, "data/hidden/p.md") {
		t.Fatalf("whole-KB message = %q", whole)
	}
}

// The other KB of a concept_transfer is named only to a caller that can reach it.
func TestTransferCommitNamesOnlyReachableKBs(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.write("ops/page", "x\n")
	c.commitMsg("concept_transfer: ops/page to kb-secret\n\nMoved-To: kb-secret")
	scoped := opsOnlyCtx()
	for _, tool := range []string{"concept_history", "changes_since"} {
		args := `{"id":"ops/page"}`
		if tool == "changes_since" {
			args = `{"since":"7d"}`
		}
		if text := rawResult(c.s.callTool(scoped, tool, json.RawMessage(args))); strings.Contains(text, "kb-secret") {
			t.Fatalf("%s names a KB the caller cannot reach: %s", tool, text)
		}
		granted := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}, {KB: "kb-secret"}}})
		if text := rawResult(c.s.callTool(granted, tool, json.RawMessage(args))); !strings.Contains(text, "kb-secret") {
			t.Fatalf("%s hides a KB the caller can reach: %s", tool, text)
		}
	}
	if h, _ := c.history(`{"id":"ops/page"}`); len(h.Revisions) == 0 || !strings.Contains(h.Revisions[0].Subject, "kb-secret") {
		t.Fatalf("admin history changed: %+v", h)
	}
}
