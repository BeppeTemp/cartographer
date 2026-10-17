package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

type statusSuppressions struct {
	Entries   int               `json:"entries"`
	Hidden    int               `json:"hidden"`
	Stale     int               `json:"stale"`
	Truncated bool              `json:"truncated"`
	List      []suppressionItem `json:"list"`
}

func gitCommit(t *testing.T, k *kb.KB, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = k.Root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func kbStatusSuppressions(t *testing.T, s *Server, ctx context.Context, args string) *statusSuppressions {
	t.Helper()
	res, err := s.Tools()["kb_status"].Handler(ctx, json.RawMessage(args))
	if err != nil || res.IsError {
		t.Fatalf("kb_status = %+v, %v", res, err)
	}
	var out struct {
		Suppressions *statusSuppressions `json:"suppressions"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out.Suppressions
}

const supNote = "---\ntype: Note\ntitle: %s\n%s---\n%s\n"

func TestKBStatus_Suppressions(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	k.AuthName = "kb-a"
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	writeKBFile(t, k, "ops/_map.md", "---\ntype: Map\ntitle: ops\nkind: map\n---\n")
	writeKBFile(t, k, "ops/b.md", fmt.Sprintf(supNote, "B", "", "[[ops/a]] [[ops/c]]"))
	writeKBFile(t, k, "ops/c.md", fmt.Sprintf(supNote, "C", "", "[[ops/b]] [[ops/a]]"))
	gitCommit(t, k, "add b and c")
	// a hides two broken links; its ignore lands with a Reason trailer.
	writeKBFile(t, k, "ops/a.md", fmt.Sprintf(supNote, "A", "lint_ignore: [broken_link]\n", "[[ops/b]] [[ops/gone-one]] [[ops/gone-two]]"))
	gitCommit(t, k, "docs: add a\n\nReason: links kept on purpose")
	// an ignore that hides nothing, added later; then an unrelated edit of a.
	writeKBFile(t, k, "ops/c.md", fmt.Sprintf(supNote, "C", "lint_ignore: [stale_claim]\n", "[[ops/b]] [[ops/a]]"))
	gitCommit(t, k, "docs: c ignores stale_claim")
	writeKBFile(t, k, "ops/a.md", fmt.Sprintf(supNote, "A", "lint_ignore: [broken_link]\n", "[[ops/b]] [[ops/gone-one]] [[ops/gone-two]] more"))
	gitCommit(t, k, "docs: touch a")

	got := kbStatusSuppressions(t, s, authLocalContext(), `{}`)
	if got == nil || got.Entries != 2 || got.Hidden != 2 || got.Stale != 1 || got.List != nil {
		t.Fatalf("summary = %+v", got)
	}

	got = kbStatusSuppressions(t, s, authLocalContext(), `{"suppressions":true}`)
	if len(got.List) != 2 {
		t.Fatalf("list = %+v", got.List)
	}
	a, c := got.List[0], got.List[1] // hidden descending
	if a.Path != "ops/a.md" || a.Check != "broken_link" || a.Hidden != 2 || a.Stale || a.Source != "concept" {
		t.Errorf("a = %+v", a)
	}
	if a.Reason != "links kept on purpose" || a.Commit == "" || a.Added == "" {
		t.Errorf("a origin = %+v (the later unrelated edit must not move it)", a)
	}
	if c.Path != "ops/c.md" || c.Check != "stale_claim" || c.Hidden != 0 || !c.Stale || c.Reason != "docs: c ignores stale_claim" {
		t.Errorf("c = %+v", c)
	}

	// A caller that sees only ops/b has no entry: neither listed nor counted.
	k2 := auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Maps: []string{"other"}}}}
	if got := kbStatusSuppressions(t, s, restrictedContext(k2), `{"suppressions":true}`); got != nil {
		t.Errorf("restricted caller saw %+v", got)
	}
}

func TestSuppressionsList_OrderAndTruncation(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	var sups []lint.Suppression
	for i := 0; i < suppressionListMax+5; i++ {
		sups = append(sups, lint.Suppression{Source: lint.SourceConcept, Path: fmt.Sprintf("ops/p%03d.md", i), Check: "broken_link", Hidden: i % 3})
	}
	items, truncated, _ := suppressionsList(k, sups, suppressionRevisionBudget)
	if !truncated || len(items) != suppressionListMax {
		t.Fatalf("len=%d truncated=%v", len(items), truncated)
	}
	for i := 1; i < len(items); i++ {
		a, b := items[i-1], items[i]
		if a.Hidden < b.Hidden || (a.Hidden == b.Hidden && a.Path > b.Path) {
			t.Fatalf("order broken at %d: %+v then %+v", i, a, b)
		}
	}
}

// A lint_accept entry's origin is read from instructions.md, per (key, check):
// a second entry added later has its own commit.
func TestKBStatus_SuppressionsAcceptOrigin(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	skill := func(name string) string {
		return "---\nname: " + name + "\ndescription: c\n---\nThen `git pull`.\n"
	}
	for _, n := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(k.Root, "skills", n), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(k.Root, "skills", n, "SKILL.md"), []byte(skill(n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	instr := func(keys ...string) {
		body := "---\nlint_accept:\n"
		for _, key := range keys {
			body += "  " + key + ": [skill_git_command]\n"
		}
		if err := os.WriteFile(filepath.Join(k.Root, "instructions.md"), []byte(body+"---\n# I\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	instr("skills/one/SKILL.md")
	gitCommit(t, k, "accept one\n\nReason: a code repo")
	instr("skills/one/SKILL.md", "skills/two/SKILL.md")
	gitCommit(t, k, "accept two")

	got := kbStatusSuppressions(t, s, authLocalContext(), `{"suppressions":true}`)
	if got == nil || len(got.List) != 2 {
		t.Fatalf("got %+v", got)
	}
	byPath := map[string]suppressionItem{}
	for _, it := range got.List {
		byPath[it.Path] = it
	}
	if it := byPath["skills/one/SKILL.md"]; it.Source != "accept" || it.Hidden != 1 || it.Reason != "a code repo" {
		t.Errorf("one = %+v", it)
	}
	if it := byPath["skills/two/SKILL.md"]; it.Reason != "accept two" || it.Commit == byPath["skills/one/SKILL.md"].Commit {
		t.Errorf("two = %+v", it)
	}
}

// A spent revision budget leaves the remaining entries without an origin and
// says so; nothing is dated from a half-read file.
func TestSuppressionsList_RevisionBudget(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	writeKBFile(t, k, "ops/a.md", fmt.Sprintf(supNote, "A", "lint_ignore: [broken_link]\n", "x"))
	gitCommit(t, k, "add a\n\nReason: because")
	sups := []lint.Suppression{{Source: lint.SourceConcept, Path: "ops/a.md", Check: "broken_link", Hidden: 1}}
	items, _, spent := suppressionsList(k, sups, 1)
	if spent || items[0].Reason != "because" {
		t.Fatalf("budget 1: spent=%v items=%+v", spent, items)
	}
	items, _, spent = suppressionsList(k, sups, 0)
	if !spent || items[0].Commit != "" || items[0].Reason != "" {
		t.Fatalf("budget 0: spent=%v items=%+v", spent, items)
	}
}
