package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// The tests below read process-wide counters (git processes, stamp walks,
// regexp compiles): they are deliberately not parallel, so no other test moves
// the counter while one runs.

// concept_move's mentions scan reads the candidates the SQLite index names,
// and reports exactly what the full scan reports.
func TestConceptMove_MentionsViaIndexEqualsFullScan(t *testing.T) {
	files := map[string]string{
		"ops/net.md":        "---\ntype: Note\ntitle: Net\n---\nI was ops/net once.\n",
		"ops/a.md":          "---\ntype: Note\ntitle: A\nrelated: ops/net\n---\nSee `ops/net` here.\n\nAlso ops/net.md and [[ops/net]].\n",
		"ops/b.md":          "---\ntype: Note\ntitle: B\n---\ndev/ops/net, ops/netx, ops/network.\n",
		"skills/s/SKILL.md": "---\nname: s\ndescription: Use when testing.\n---\n\nRead `ops/net`.\n",
	}
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("dev/other%d.md", i)] = fmt.Sprintf("---\ntype: Note\ntitle: O%d\n---\nNothing about it.\n", i)
	}
	_, full, _ := mentionsMove(t, "concept_move", `{"source_id":"ops/net","target_id":"ops/network"}`, files)
	if len(full.Mentions) == 0 {
		t.Fatal("fixture has no mentions")
	}

	k := setupTestKB(t)
	ix, err := sqlindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Skipf("sqlindex.Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	s := New("test")
	RegisterKBTools(s, k, Deps{SQLIndex: ix})
	all := satMapFiles("ops", "dev", "archive")
	for p, c := range files {
		if strings.HasPrefix(p, "skills/") {
			abs := filepath.Join(k.Root, filepath.FromSlash(p))
			_ = os.MkdirAll(filepath.Dir(abs), 0o755)
			if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		all[p] = c
	}
	satWriteFiles(t, k, all)
	res := callTool(t, s, "concept_move", `{"source_id":"ops/net","target_id":"ops/network"}`)
	if res.IsError {
		t.Fatalf("%+v", res.Content)
	}
	var got mentionsResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Mentions, full.Mentions) || got.MentionsOmitted != full.MentionsOmitted {
		t.Fatalf("indexed scan = %+v, full scan = %+v", got.Mentions, full.Mentions)
	}

	// The candidates are the few files that hold the id, not the KB.
	rec := newSearchReconciler(k, ix)
	set := rec.mentionCandidates([]string{"ops/network"})
	if set == nil || len(set) == 0 || len(set) > 4 {
		t.Fatalf("candidates = %v, want the few concepts naming ops/network", set)
	}
	rec.sqlStale = true // an earlier SQLite write failed: the index is not trusted
	if rec.mentionCandidates([]string{"ops/network"}) != nil {
		t.Error("a stale SQLite index must fall back to the full scan")
	}
	if rec2 := newSearchReconciler(k, nil); rec2.mentionCandidates([]string{"x"}) != nil {
		t.Error("without an SQLite index the candidates are unknown (nil)")
	}
}

// Line numbers come from one incremental pass: pinned on a file with many
// occurrences, several on one line and several lines apart.
func TestScanMentions_LineNumbers(t *testing.T) {
	k := setupTestKB(t)
	var b strings.Builder
	b.WriteString("---\ntype: Note\ntitle: L\n---\n") // lines 1-4
	want := []int{}
	for line := 5; line < 3000; line++ {
		switch {
		case line%500 == 0:
			b.WriteString("x ops/net and again ops/net\n") // two on one line: one mention
			want = append(want, line)
		case line == 7:
			b.WriteString("ops/net\n")
			want = append(want, line)
		default:
			b.WriteString("filler line\n")
		}
	}
	all := satMapFiles("ops")
	all["ops/l.md"] = b.String()
	satWriteFiles(t, k, all)
	got, _ := scanMentions(k, map[string]string{"ops/net": "ops/network"}, nil)
	var lines []int
	for _, m := range got {
		if m.Path == "ops/l.md" {
			lines = append(lines, m.Line)
		}
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
}

// kb_status computes the lint-inputs stamp once for its three getters, and the
// stamp still follows usage.json and the KB's other inputs.
func TestKBStatus_OneLintStampWalk(t *testing.T) {
	k := cacheTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	callTool(t, s, "kb_status", `{}`) // cold: fills the caches
	before := lintStampWalks.Load()
	if res := callTool(t, s, "kb_status", `{}`); res.IsError {
		t.Fatalf("%+v", res.Content)
	}
	if n := lintStampWalks.Load() - before; n != 1 {
		t.Fatalf("a warm kb_status walked the KB root %d times, want 1", n)
	}

	base := lintInputsStamp(k)
	dir := filepath.Join(k.Root, ".cartographer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reads.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lintInputsStamp(k) != base {
		t.Error("local state under .cartographer must not move the stamp")
	}
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	withUsage := lintInputsStamp(k)
	if withUsage == base {
		t.Error("usage.json must move the stamp")
	}
	if err := os.WriteFile(filepath.Join(k.Root, "glossary.yaml"), []byte("terms: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lintInputsStamp(k) == withUsage {
		t.Error("a root input must move the stamp")
	}
	// Without a memo in the context each ask is a fresh walk.
	w := lintStampWalks.Load()
	lintStampOf(context.Background(), k)
	lintStampOf(context.Background(), k)
	if lintStampWalks.Load()-w != 2 {
		t.Error("no memo, no sharing")
	}
}

// Reading an old revision of a page with many commits starts a constant number
// of git processes, not one per newer revision.
func TestConceptReadAtRev_ConstantGitProcesses(t *testing.T) {
	c := newChangesLinksKB(t)
	for i := 0; i < 50; i++ {
		c.write("ops/page", fmt.Sprintf("version %d\n", i))
		c.commit(true)
	}
	h, res := c.history(`{"id":"ops/page","limit":100}`)
	if res.IsError || len(h.Revisions) != 50 {
		t.Fatalf("history = %d revisions, %+v", len(h.Revisions), res)
	}
	oldest := h.Revisions[len(h.Revisions)-1].SHA
	before := gitx.RunCount()
	got, res := c.read(`{"id":"ops/page","rev":"` + oldest + `"}`)
	if res.IsError {
		t.Fatalf("%+v", res)
	}
	if body, _ := got["body"].(string); !strings.Contains(body, "version 0") {
		t.Errorf("body = %q", body)
	}
	if n := gitx.RunCount() - before; n > 6 {
		t.Fatalf("concept_read(rev) started %d git processes for 50 newer revisions", n)
	}
	// An unknown rev is still "not found at rev".
	if _, res := c.read(`{"id":"ops/page","rev":"deadbeefdeadbeef"}`); !res.IsError {
		t.Error("an unknown rev must be an error")
	}
}

// The tool-prefix repair compiles one regexp per distinct field, not per fix.
func TestRepair_LegacyToolNameCompilesOncePerField(t *testing.T) {
	k, s := repairKB(t, 0)
	seedLegacyFixes(t, k)
	if _, err := k.CommitOpAs("test: seed legacy", "", ""); err != nil {
		t.Fatal(err)
	}
	before := fieldReCompiles.Load()
	out := repairCall(t, s, `{"check":"legacy_tool_name","dry_run":false}`)
	if out["applied"].(float64) != 2 {
		t.Fatalf("apply = %v", out)
	}
	// Three fixes over two files, two distinct fields.
	if n := fieldReCompiles.Load() - before; n != 2 {
		t.Fatalf("compiled %d regexps, want 2 (one per field)", n)
	}
}

// A rebuild that fails leaves the index untrusted: the flag stays set and the
// mentions scan falls back to every concept.
func TestRebuild_FailureKeepsIndexUntrusted(t *testing.T) {
	k := setupTestKB(t)
	ix, err := sqlindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Skipf("sqlindex.Open: %v", err)
	}
	rec := newSearchReconciler(k, ix)
	if _, err := rec.reconcile(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rec.rebuild(); err != nil {
		t.Fatal(err)
	}
	if rec.sqlStale || rec.mentionCandidates([]string{"x"}) == nil {
		t.Fatal("a clean rebuild must leave the index trusted")
	}
	ix.Close() // every SQLite write now fails
	if _, _, err := rec.rebuild(); err != nil {
		t.Fatal(err)
	}
	if !rec.sqlStale {
		t.Fatal("a rebuild that lost rows cleared sqlStale")
	}
	if rec.mentionCandidates([]string{"x"}) != nil {
		t.Error("mentions must fall back to the full scan after a failed rebuild")
	}
}
