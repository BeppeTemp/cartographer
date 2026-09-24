package mcpserver

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// D245: the search indexes follow the files by validation. Every test here
// changes a concept file by some means other than the ordinary write tools —
// or through a path that used to forget the index — and asserts that the next
// search reflects it, with no reindex in between.

func searchText(t *testing.T, s *Server, query string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"query": query})
	tr := s.callTool(authLocalContext(), "search", args)
	if tr.IsError {
		t.Fatalf("search %q: %+v", query, tr.Content)
	}
	return tr.Content[0].Text
}

func callOK(t *testing.T, s *Server, name, args string) string {
	t.Helper()
	tr := s.callTool(authLocalContext(), name, json.RawMessage(args))
	if tr.IsError {
		t.Fatalf("%s %s: %+v", name, args, tr.Content)
	}
	return tr.Content[0].Text
}

// freshnessBackends runs body once with the in-memory index alone and once
// with SQLite FTS5 in front of it.
func freshnessBackends(t *testing.T, body func(t *testing.T, k *kb.KB, s *Server)) {
	t.Run("memory", func(t *testing.T) {
		k := setupTestKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		body(t, k, s)
	})
	t.Run("sqlite", func(t *testing.T) {
		k := setupTestKB(t)
		sqlIdx, err := sqlindex.Open(filepath.Join(t.TempDir(), "index.db"))
		if err != nil {
			t.Skipf("sqlindex.Open: %v", err)
		}
		t.Cleanup(func() { sqlIdx.Close() })
		s := New("test")
		RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})
		body(t, k, s)
	})
}

func TestSearchFollowsExternalEdits(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		if strings.Contains(searchText(t, s, "reconcileunique"), "out-of-band") {
			t.Fatal("found before the file exists")
		}
		p := writeOutOfBand(t, k)
		if !strings.Contains(searchText(t, s, "reconcileunique"), "manutenzione/out-of-band") {
			t.Fatal("external write not seen by the next search")
		}
		if err := os.WriteFile(p, []byte("---\ntype: Note\n---\nnow about zebrafish\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if out := searchText(t, s, "zebrafish"); !strings.Contains(out, "manutenzione/out-of-band") {
			t.Fatalf("external edit not seen: %s", out)
		}
		if out := searchText(t, s, "reconcileunique"); strings.Contains(out, "out-of-band") {
			t.Fatalf("old content still found after an external edit: %s", out)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if out := searchText(t, s, "zebrafish"); strings.Contains(out, "out-of-band") {
			t.Fatalf("external delete not seen: %s", out)
		}
	})
}

func TestSearchFollowsConflictResolve(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "contra.md"),
			[]byte("---\ntype: Contradiction\ntitle: Contra\nresolution_status: open\n---\nTwo claims.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(searchText(t, s, "okapiverdict"), "contra") {
			t.Fatal("resolution found before it exists")
		}
		callOK(t, s, "conflict_resolve", `{"contradiction_id":"manutenzione/contra","resolution":"okapiverdict"}`)
		if out := searchText(t, s, "okapiverdict"); !strings.Contains(out, "manutenzione/contra") {
			t.Fatalf("conflict_resolve not seen by search: %s", out)
		}
	})
}

// git_conflict_resolve finalizes a merge commit outside gitWrap: the resolved
// content reaches the files through git, never through a write handler.
func TestSearchFollowsGitConflictResolve(t *testing.T) {
	k := setupTestKB(t)
	root := k.Root
	gitRun := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	foo := filepath.Join(k.DataRoot(), "foo.md")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(foo, []byte("---\ntype: Note\n---\n"+body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("base")
	gitRun("add", "-A")
	gitRun("commit", "-m", "base")
	branch := gitRun("rev-parse", "--abbrev-ref", "HEAD")
	gitRun("checkout", "-b", "remoteline")
	write("wombatremote")
	gitRun("commit", "-am", "remote")
	remoteSHA := gitRun("rev-parse", "HEAD")
	gitRun("checkout", branch)
	write("localversion")
	gitRun("commit", "-am", "local")
	localSHA := gitRun("rev-parse", "HEAD")

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if err := k.RegisterConflict(kb.Conflict{ConceptID: "foo", Path: "data/foo.md", LocalSHA: localSHA, RemoteSHA: remoteSHA, Branch: branch, Files: []string{"data/foo.md"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(searchText(t, s, "wombatremote"), `"foo"`) {
		t.Fatal("remote version found before the resolution")
	}
	callOK(t, s, "git_conflict_resolve", `{"concept_id":"foo","strategy":"theirs"}`)
	if out := searchText(t, s, "wombatremote"); !strings.Contains(out, `"foo"`) {
		t.Fatalf("git_conflict_resolve not seen by search: %s", out)
	}
}

// Without SQLite, a pull used to reach search only on restart: OnSyncIn was
// wired only with a SQLite index.
func TestSearchFollowsPullWithoutSQLite(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.GitSync = true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if k.OnSyncIn == nil {
		t.Fatal("OnSyncIn not wired without SQLite")
	}
	before, _ := gitx.HeadSHA(k.Root)
	pushRemoteFile(t, bare, branch, "data/remote/pulled.md", "---\ntype: Note\n---\nquokkapulled\n", "remote")
	syncInForTest(t, k)
	if after, _ := gitx.HeadSHA(k.Root); after == before {
		t.Fatal("the pull did not move HEAD")
	}
	if out := searchText(t, s, "quokkapulled"); !strings.Contains(out, "remote/pulled") {
		t.Fatalf("pulled concept not seen by search: %s", out)
	}
}

// A batch that fails after its first file is written rolls the files back;
// search must agree with the restored tree, not with the attempted one.
func TestSearchFollowsFailedBatchRollback(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write into")
	}
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		ro := filepath.Join(k.DataRoot(), "locked")
		if err := os.MkdirAll(ro, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ro, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(ro, 0o755) })

		data, err := k.ReadConcept("manutenzione/test-runbook")
		if err != nil {
			t.Fatal(err)
		}
		searchText(t, s, "runbook") // warm: the reconciler has reported the original
		ops := []map[string]any{
			{"op": "write", "id": "manutenzione/test-runbook", "if_match": data.ContentHash, "frontmatter": map[string]any{"type": "Runbook", "title": "Test Runbook"}, "body": "narwhalattempt"},
			{"op": "write", "id": "batch/fresh-attempt", "frontmatter": map[string]any{"type": "Note"}, "body": "narwhalattempt"},
			{"op": "write", "id": "locked/blocked", "frontmatter": map[string]any{"type": "Note"}, "body": "narwhalattempt"},
		}
		args, _ := json.Marshal(map[string]any{"operations": ops})
		if tr := s.callTool(authLocalContext(), "concept_batch", args); !tr.IsError {
			t.Fatalf("batch into a read-only directory succeeded: %+v", tr.Content)
		}
		if out := searchText(t, s, "narwhalattempt"); !strings.Contains(out, `"count": 0`) {
			t.Fatalf("search sees a rolled-back batch: %s", out)
		}
		if out := searchText(t, s, "runbook"); !strings.Contains(out, "manutenzione/test-runbook") {
			t.Fatalf("restored concept lost from search: %s", out)
		}
	})
}

// Moving an expanded concept renames a directory: its index.md and its
// satellites change ids without any file being written through WriteConcept.
func TestSearchFollowsExpandedMove(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		callOK(t, s, "concept_expand", `{"id":"manutenzione/test-runbook"}`)
		callOK(t, s, "concept_write", `{"id":"manutenzione/test-runbook/step1","frontmatter":{"type":"Note","title":"Step"},"body":"platypusstep"}`)
		if out := searchText(t, s, "platypusstep"); !strings.Contains(out, "manutenzione/test-runbook/step1") {
			t.Fatalf("satellite not indexed: %s", out)
		}
		callOK(t, s, "concept_move", `{"source_id":"manutenzione/test-runbook","target_id":"archivio/runbook"}`)
		out := searchText(t, s, "platypusstep")
		if !strings.Contains(out, "archivio/runbook/step1") || strings.Contains(out, "manutenzione/test-runbook") {
			t.Fatalf("expanded move not followed: %s", out)
		}
		if out := searchText(t, s, "runbook"); !strings.Contains(out, `"archivio/runbook"`) || strings.Contains(out, `"manutenzione/test-runbook"`) {
			t.Fatalf("moved index.md not followed: %s", out)
		}
	})
}

// The same across the services/ boundary (D269): services/ is rooted at the
// KB root, not under data/, and the reconciler must see both ends.
func TestSearchFollowsExpandedMoveAcrossServices(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		callOK(t, s, "concept_write", `{"id":"services/gateway","frontmatter":{"type":"Service","title":"Gateway"},"body":"wombatowner"}`)
		callOK(t, s, "concept_expand", `{"id":"services/gateway"}`)
		callOK(t, s, "concept_write", `{"id":"services/gateway/notes","frontmatter":{"type":"Note","title":"Notes"},"body":"wombatsatellite"}`)
		if out := searchText(t, s, "wombatsatellite"); !strings.Contains(out, "services/gateway/notes") {
			t.Fatalf("satellite not indexed: %s", out)
		}
		callOK(t, s, "concept_move", `{"source_id":"services/gateway","target_id":"manutenzione/gateway"}`)
		if out := searchText(t, s, "wombatsatellite"); !strings.Contains(out, "manutenzione/gateway/notes") || strings.Contains(out, "services/gateway") {
			t.Fatalf("expanded move out of services/ not followed: %s", out)
		}
		callOK(t, s, "concept_move", `{"source_id":"manutenzione/gateway","target_id":"services/edge"}`)
		out := searchText(t, s, "wombatowner")
		if !strings.Contains(out, `"services/edge"`) || strings.Contains(out, "manutenzione/gateway") {
			t.Fatalf("expanded move into services/ not followed: %s", out)
		}
	})
}

// The warm path is one stat walk: a search over an unchanged KB reads no file.
func TestSearchWarmPathReadsNoFile(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	old := time.Now().Add(-time.Hour)
	_ = filepath.Walk(k.DataRoot(), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			return os.Chtimes(p, old, old)
		}
		return nil
	})
	searchText(t, s, "runbook") // re-reads the backdated files once
	reads := k.GraphFileReads()
	for i := 0; i < 3; i++ {
		searchText(t, s, "runbook")
	}
	if got := k.GraphFileReads(); got != reads {
		t.Fatalf("warm searches read %d file(s)", got-reads)
	}
}

// TestIndexUpdatesOnlyInReconciler keeps D245 from eroding: a write path that
// updates an index itself is the notification scheme this replaced, and the
// one that forgot is how conflict resolutions went missing from search. Only
// the reconciler, the live index itself and the full rebuild may call them.
func TestIndexUpdatesOnlyInReconciler(t *testing.T) {
	allowed := map[string]bool{"liveindex.go": true, "reconcile.go": true}
	allowedFuncs := map[string]bool{"rebuildSQLIndex": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || allowed[name] {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || allowedFuncs[fn.Name.Name] {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "add", "remove", "swap", "Upsert", "Delete":
				default:
					return true
				}
				if sel.Sel.Name == "Delete" && !isSQLIndexExpr(sel.X) {
					return true
				}
				if (sel.Sel.Name == "add" || sel.Sel.Name == "remove" || sel.Sel.Name == "swap") && !isLiveExpr(sel.X) {
					return true
				}
				t.Errorf("%s: %s calls %s on a search index — only the reconciler may (D245)", fset.Position(call.Pos()), fn.Name.Name, sel.Sel.Name)
				return true
			})
		}
	}
}

func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

func isLiveExpr(e ast.Expr) bool {
	n := strings.ToLower(exprName(e))
	return strings.Contains(n, "live")
}

func isSQLIndexExpr(e ast.Expr) bool {
	n := strings.ToLower(exprName(e))
	return strings.Contains(n, "sql") || n == "ix"
}
