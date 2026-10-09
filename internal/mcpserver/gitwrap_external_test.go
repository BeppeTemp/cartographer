package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D357: a write's commit holds only the write. What the working tree held
// before is committed first, as its own commit.

func externalKB(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	k.AutoCommit = true
	k.GitAuthorExplicit = true
	k.GitAuthorName, k.GitAuthorEmail = "KB Identity", "kb@example.com"
	k.AutoRepair = []string{"nonstandard_field"}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

func editOutOfBand(t *testing.T, k *kb.KB, rel, content string) {
	t.Helper()
	abs := filepath.Join(k.DataRoot(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExternalChanges_NoOpWriteCommitsOnlyTheExternalWork(t *testing.T) {
	k, s := externalKB(t)
	before := commitCount(t, k)
	for _, name := range []string{"a", "b", "c"} {
		editOutOfBand(t, k, "notes/"+name+".md", "---\ntype: Note\ntitle: "+name+"\n---\n# "+name+"\n")
	}
	// A repair that applies nothing.
	text, isErr := callJSON(t, s, adminCtx, "kb_repair", `{"check":"nonstandard_field","dry_run":false}`)
	if isErr {
		t.Fatalf("kb_repair: %s", text)
	}
	if subj := gitOut(t, k, "log", "-1", "--format=%s"); subj != externalChangesSubject {
		t.Fatalf("head subject = %q, want %q", subj, externalChangesSubject)
	}
	if n := commitCount(t, k); n != before+1 {
		t.Fatalf("commits = %d, want exactly one more than %d", n, before)
	}
	body := gitOut(t, k, "log", "-1", "--format=%B")
	if !strings.Contains(body, "Reason: uncommitted changes found before kb_repair") {
		t.Fatalf("no reason trailer:\n%s", body)
	}
	if author := gitOut(t, k, "log", "-1", "--format=%an <%ae>"); author != "KB Identity <kb@example.com>" {
		t.Fatalf("author = %q", author)
	}
	gateClean(t, k)
}

func TestExternalChanges_RealWriteIsItsOwnCommit(t *testing.T) {
	k, s := externalKB(t)
	editOutOfBand(t, k, "notes/editor.md", "---\ntype: Note\ntitle: Editor\n---\n# Editor\n")
	text, isErr := callJSON(t, s, adminCtx, "concept_write",
		`{"id":"notes/written","frontmatter":{"type":"Note","title":"Written"},"body":"# Written\n"}`)
	if isErr {
		t.Fatalf("concept_write: %s", text)
	}
	log := gitOut(t, k, "log", "-2", "--format=%s")
	lines := strings.Split(log, "\n")
	if len(lines) != 2 || lines[1] != externalChangesSubject || lines[0] == externalChangesSubject {
		t.Fatalf("history:\n%s", log)
	}
	files := gitOut(t, k, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "notes/written.md") || strings.Contains(files, "editor.md") {
		t.Fatalf("the write's commit holds:\n%s", files)
	}
	if ext := gitOut(t, k, "show", "--name-only", "--format=", "HEAD~1"); !strings.Contains(ext, "notes/editor.md") || strings.Contains(ext, "written.md") {
		t.Fatalf("the external commit holds:\n%s", ext)
	}
}

func TestExternalChanges_CleanTreeUnchanged(t *testing.T) {
	k, s := externalKB(t)
	before := commitCount(t, k)
	text, isErr := callJSON(t, s, adminCtx, "concept_write",
		`{"id":"notes/written","frontmatter":{"type":"Note","title":"Written"},"body":"# Written\n"}`)
	if isErr {
		t.Fatalf("concept_write: %s", text)
	}
	if n := commitCount(t, k); n != before+1 {
		t.Fatalf("commits = %d, want one more than %d", n, before)
	}
	if strings.Contains(gitOut(t, k, "log", "--format=%s"), externalChangesSubject) {
		t.Fatal("an external-changes commit appeared on a clean tree")
	}
}
