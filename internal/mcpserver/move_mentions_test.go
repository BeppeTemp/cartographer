package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

type mentionsResult struct {
	Mentions        []mentionOut `json:"mentions"`
	MentionsOmitted int          `json:"mentions_omitted"`
	Findings        []struct {
		Check string `json:"check"`
	} `json:"findings"`
}

func mentionsMove(t *testing.T, tool, args string, files map[string]string) (*kb.KB, mentionsResult, string) {
	t.Helper()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	all := satMapFiles("ops", "dev", "archive")
	for p, c := range files {
		if p == "instructions.md" || strings.HasPrefix(p, "skills/") {
			// artifacts live at the KB root, not in the data root
			abs := filepath.Join(k.Root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		all[p] = c
	}
	satWriteFiles(t, k, all)
	if tool == "concept_archive" {
		args = strings.Replace(args, `"id":"ops/net"`, `"id":"ops/net","if_match":"`+hashOf(t, k, "ops/net")+`"`, 1)
	}
	res := callTool(t, s, tool, args)
	if res.IsError {
		t.Fatalf("%s: %+v", tool, res.Content)
	}
	var out mentionsResult
	raw := res.Content[0].Text
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	if testing.Verbose() {
		if c, err := k.ReadRaw("ops/a.md"); err == nil {
			t.Logf("ops/a.md:\n%s", c)
		}
	}
	return k, out, raw
}

func TestConceptMove_Mentions(t *testing.T) {
	t.Parallel()
	_, out, raw := mentionsMove(t, "concept_move", `{"source_id":"ops/net","target_id":"ops/network"}`, map[string]string{
		"ops/net.md":        "---\ntype: Note\ntitle: Net\n---\nI was ops/net once.\n",
		"ops/a.md":          "---\ntype: Note\ntitle: A\nprovenance:\n  - ops/net\n---\nSee `ops/net` here.\n\nAlso ops/net.md and [link](net.md) and [[ops/net]] and [ops/net](net.md).\n",
		"ops/b.md":          "---\ntype: Note\ntitle: B\n---\ndev/ops/net, ops/network-2, ops/netx, ops/net-old, ops/network.\n",
		"skills/s/SKILL.md": "---\nname: s\ndescription: Use when testing.\n---\n\nRead `ops/net`.\n",
		"instructions.md":   "line\nops/net\n",
	})
	want := []mentionOut{
		{"ops/net", "instructions.md", 2, "artifact"},
		{"ops/net", "ops/a.md", 4, "frontmatter"},
		{"ops/net", "ops/a.md", 6, "prose"},
		{"ops/net", "ops/a.md", 8, "prose"},
		{"ops/net", "ops/network.md", 5, "prose"},
		{"ops/net", "skills/s/SKILL.md", 6, "artifact"},
	}
	if fmt.Sprint(out.Mentions) != fmt.Sprint(want) {
		t.Errorf("mentions:\n got %v\nwant %v", out.Mentions, want)
	}
	if strings.Contains(raw, "mentions_omitted") {
		t.Errorf("mentions_omitted must be omitted when 0: %s", raw)
	}
}

func TestConceptMove_MentionsNotLinksWhenRewriteIsOff(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	files := satMapFiles("ops")
	files["ops/net.md"] = "---\ntype: Note\ntitle: Net\n---\nx\n"
	files["ops/a.md"] = "---\ntype: Note\ntitle: A\n---\n[l](net.md) [[ops/net]]\n"
	satWriteFiles(t, k, files)
	res, errRes := applyConceptMoves(k, []conceptMoveEntry{{SourceID: "ops/net", TargetID: "ops/network"}}, false, moveOptions{ReportMentions: true})
	if errRes != nil {
		t.Fatal(errRes.Content)
	}
	if m, ok := res["mentions"]; ok {
		t.Errorf("a surviving link is a finding, not a mention: %v", m)
	}
	found := false
	if fs, _ := res["findings"].([]findingOut); true {
		for _, f := range fs {
			found = found || f.Check == "broken_link"
		}
	}
	if !found {
		t.Errorf("surviving link not in findings: %+v", res["findings"])
	}
}

func TestConceptMove_MentionsBatchCarryTheirOwnID(t *testing.T) {
	t.Parallel()
	_, out, _ := mentionsMove(t, "concept_move", `{"moves":[{"source_id":"ops/a","target_id":"ops/aa"},{"source_id":"ops/b","target_id":"ops/bb"}]}`, map[string]string{
		"ops/a.md": "---\ntype: Note\ntitle: A\n---\nx\n",
		"ops/b.md": "---\ntype: Note\ntitle: B\n---\nx\n",
		"ops/c.md": "---\ntype: Note\ntitle: C\n---\nops/a\nops/b\n",
	})
	want := []mentionOut{{"ops/a", "ops/c.md", 5, "prose"}, {"ops/b", "ops/c.md", 6, "prose"}}
	if fmt.Sprint(out.Mentions) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", out.Mentions, want)
	}
}

func TestConceptMove_MentionsAreCappedAndStable(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	for i := 0; i < 60; i++ {
		body.WriteString("ops/net\n")
	}
	files := map[string]string{
		"ops/net.md": "---\ntype: Note\ntitle: Net\n---\nx\n",
		"ops/a.md":   "---\ntype: Note\ntitle: A\n---\n" + body.String(),
	}
	_, out1, _ := mentionsMove(t, "concept_move", `{"source_id":"ops/net","target_id":"ops/network"}`, files)
	_, out2, _ := mentionsMove(t, "concept_move", `{"source_id":"ops/net","target_id":"ops/network"}`, files)
	if len(out1.Mentions) != 50 || out1.MentionsOmitted != 10 {
		t.Fatalf("got %d listed, %d omitted", len(out1.Mentions), out1.MentionsOmitted)
	}
	if fmt.Sprint(out1) != fmt.Sprint(out2) {
		t.Error("order is not stable across runs")
	}
}

func TestConceptArchive_ReportsMentions(t *testing.T) {
	t.Parallel()
	_, out, _ := mentionsMove(t, "concept_archive", `{"id":"ops/net"}`, map[string]string{
		"ops/net.md": "---\ntype: Note\ntitle: Net\n---\nx\n",
		"ops/a.md":   "---\ntype: Note\ntitle: A\n---\nops/net\n",
	})
	if len(out.Mentions) != 1 || out.Mentions[0].Path != "ops/a.md" {
		t.Errorf("archive mentions: %v", out.Mentions)
	}
}

func TestApplyConceptMoves_MentionsVisibilityAndGate(t *testing.T) {
	t.Parallel()
	setup := func() *kb.KB {
		k := setupTestKB(t)
		files := satMapFiles("ops", "dev")
		files["ops/net.md"] = "---\ntype: Note\ntitle: Net\n---\nx\n"
		files["ops/a.md"] = "---\ntype: Note\ntitle: A\n---\nops/net\n"
		files["dev/secret.md"] = "---\ntype: Note\ntitle: S\n---\nops/net\n"
		satWriteFiles(t, k, files)
		if err := os.WriteFile(filepath.Join(k.Root, "instructions.md"), []byte("ops/net\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return k
	}
	moves := []conceptMoveEntry{{SourceID: "ops/net", TargetID: "ops/network"}}

	view := &callerView{visible: func(id string) bool { return !strings.HasPrefix(id, "dev/") }}
	res, errRes := applyConceptMoves(setup(), moves, true, moveOptions{View: view, ReportMentions: true})
	if errRes != nil {
		t.Fatal(errRes.Content)
	}
	got, _ := res["mentions"].([]mentionOut)
	if len(got) != 1 || got[0].Path != "ops/a.md" {
		t.Errorf("restricted caller: %v", got)
	}

	// kb_repair's path does not report, even with mentions present.
	res, errRes = applyConceptMoves(setup(), moves, true, moveOptions{})
	if errRes != nil {
		t.Fatal(errRes.Content)
	}
	if _, ok := res["mentions"]; ok {
		t.Error("a move without ReportMentions must not scan")
	}
}
