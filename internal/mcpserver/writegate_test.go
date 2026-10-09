package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// gateKB is a git KB with AutoCommit and the write gate at mode: map "arch"
// carries field_values.status [active, deprecated], map "ops" is free.
func gateKB(t *testing.T, mode string) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	for _, m := range []string{"arch", "ops"} {
		if err := k.CreateMap(m, strings.ToUpper(m), "map", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	mapMD := "---\ntype: Map\nkind: map\ntitle: Arch\nfield_values.status: [active, deprecated]\n---\n# Arch\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "arch", "_map.md"), []byte(mapMD), 0o644); err != nil {
		t.Fatal(err)
	}
	k.AutoCommit = true
	k.WriteGate = mode
	return k, nil
}

func gateSeed(t *testing.T, k *kb.KB, files map[string]string) *Server {
	t.Helper()
	for id, content := range files {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s
}

func gateClean(t *testing.T, k *kb.KB) {
	t.Helper()
	if st := strings.TrimSpace(gitOut(t, k, "status", "--short")); st != "" {
		t.Fatalf("tree not clean after a refused write:\n%s", st)
	}
}

const draftPage = "---\ntype: Note\ntitle: P\nstatus: draft\n---\n# P\n"

func TestWriteGate_MoveRefusedBatchFixedAcceptedTrailer(t *testing.T) {
	k, _ := gateKB(t, "error")
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	head := gitOut(t, k, "rev-parse", "HEAD")

	// Acceptance 1: the plain move introduces an invalid_field_value error.
	text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"arch/p"}`)
	if !isErr || !strings.Contains(text, "write_gate (error): refused, nothing was written") || !strings.Contains(text, "invalid_field_value") {
		t.Fatalf("move = %q (error=%v)", text, isErr)
	}
	gateClean(t, k)
	if gitOut(t, k, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved on a refused write")
	}
	if _, err := k.ReadConcept("ops/p"); err != nil {
		t.Fatalf("source gone after a refused move: %v", err)
	}
	if _, err := k.ReadConcept("arch/p"); err == nil {
		t.Fatal("target exists after a refused move")
	}

	// Retry after the refusal works: the same move accepted with a reason.
	// (Idempotent retry: state is clean.)
	acc := `{"source_id":"ops/p","target_id":"arch/p","accept_findings":[{"check":"invalid_field_value","reason":"  legacy\nstatus kept "}]}`
	if text, isErr = callJSON(t, s, adminCtx, "concept_move", acc); isErr {
		t.Fatalf("accepted move refused: %s", text)
	}
	if body := gitOut(t, k, "log", "-1", "--format=%B"); !strings.Contains(body, "Accepted-Finding: invalid_field_value: legacy status kept") {
		t.Fatalf("commit body lacks the trailer:\n%s", body)
	}

	// concept_batch is one call: any introduced finding refuses the whole
	// batch, a clean batch passes with no trailer.
	k2, _ := gateKB(t, "error")
	s2 := gateSeed(t, k2, nil)
	bad := `{"operations":[{"op":"write","id":"arch/one","frontmatter":{"type":"Note","title":"One","status":"active"},"body":"b"},{"op":"write","id":"arch/two","frontmatter":{"type":"Note","title":"Two","status":"draft"},"body":"b"}]}`
	if text, isErr = callJSON(t, s2, adminCtx, "concept_batch", bad); !isErr || !strings.Contains(text, "write_gate (error)") {
		t.Fatalf("bad batch = %q (error=%v)", text, isErr)
	}
	gateClean(t, k2)
	if _, err := k2.ReadConcept("arch/one"); err == nil {
		t.Fatal("the clean half of a refused batch survived")
	}
	good := strings.Replace(bad, `"status":"draft"`, `"status":"deprecated"`, 1)
	if text, isErr = callJSON(t, s2, adminCtx, "concept_batch", good); isErr {
		t.Fatalf("fixed batch refused: %s", text)
	}
	if body := gitOut(t, k2, "log", "-1", "--format=%B"); strings.Contains(body, "Accepted-Finding") {
		t.Fatalf("unexpected trailer:\n%s", body)
	}
}

func TestWriteGate_PreexistingNeverBlocks(t *testing.T) {
	k, _ := gateKB(t, "warning")
	five := "---\ntype: Note\ntitle: P\naggiornato: 2026-01-01\ndata: 2026-01-01\nfonti: x\nstato: y\ndate: 2026-01-01\n---\n# P\n\nText.\n"
	s := gateSeed(t, k, map[string]string{"ops/p": five, "ops/q": "---\ntype: Note\ntitle: Q\n---\n# Q\n"})

	// Acceptance 2: a patch that adds nothing passes despite the baseline.
	cd, _ := k.ReadConcept("ops/p")
	patch := func(old, repl string) string {
		cd, _ = k.ReadConcept("ops/p")
		b, _ := json.Marshal(map[string]string{"id": "ops/p", "old_string": old, "new_string": repl, "if_match": cd.ContentHash})
		return string(b)
	}
	if text, isErr := callJSON(t, s, adminCtx, "concept_patch", patch("Text.", "More text.")); isErr {
		t.Fatalf("neutral patch refused: %s", text)
	}
	// A patch adding a duplicate link is refused with exactly that finding.
	text, isErr := callJSON(t, s, adminCtx, "concept_patch", strings.Replace(patch("More text.", "More text.\nQ again."), `"if_match"`, `"frontmatter":{"updated":"2026-02-02"},"if_match"`, 1))
	if !isErr || !strings.Contains(text, "introduced 1 finding(s)") || !strings.Contains(text, `"check":"nonstandard_field"`) || !strings.Contains(text, `"updated"`) || !strings.Contains(text, `"fix"`) {
		t.Fatalf("dup patch = %q (error=%v)", text, isErr)
	}
	gateClean(t, k)
	cd, _ = k.ReadConcept("ops/p")
	if strings.Contains(cd.Content, "Q again") {
		t.Fatal("refused patch left content on disk")
	}
	// read and search keep answering with the old content.
	read := mustText(t, s, "concept_read", `{"id":"ops/p"}`)
	if strings.Contains(read, "Q again") || !strings.Contains(read, "More text.") {
		t.Fatalf("concept_read after refusal: %s", read)
	}
	if hits := mustText(t, s, "search", `{"query":"again"}`); strings.Contains(hits, "ops/p") {
		t.Fatalf("search sees refused content: %s", hits)
	}

	// A moved page keeps its pre-existing findings: compared under the old path.
	if text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"ops/p2"}`); isErr {
		t.Fatalf("move of a page with pre-existing findings refused: %s", text)
	}
}

func TestWriteGate_SchemaOnlyWhenActive(t *testing.T) {
	schemaOf := func(k *kb.KB) map[string]string {
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		out := map[string]string{}
		for name, tool := range s.Tools() {
			out[name] = string(tool.InputSchema)
		}
		return out
	}
	off, _ := gateKB(t, "off")
	on, _ := gateKB(t, "error")
	offS, onS := schemaOf(off), schemaOf(on)
	grown, gatedTools := 0, 0
	for name, sch := range offS {
		if strings.Contains(sch, "accept_findings") {
			t.Errorf("%s: off KB carries accept_findings", name)
		}
		if strings.Contains(onS[name], "accept_findings") {
			gatedTools++
			if !acceptsFindings(name) {
				t.Errorf("%s carries accept_findings but is not gated", name)
			}
			grown += len(onS[name]) - len(sch)
		} else if onS[name] != sch {
			t.Errorf("%s: schema changed without accept_findings", name)
		}
	}
	if !strings.Contains(onS["concept_write"], "accept_findings") || !strings.Contains(onS["supersede"], "accept_findings") {
		t.Fatal("concept_write / supersede lack accept_findings on a gate-on KB")
	}
	for _, exempt := range []string{"kb_repair", "repair_revert", "conflict_resolve", "log_append", "snapshot", "map_update", "index_patch", "source_register"} {
		if strings.Contains(onS[exempt], "accept_findings") {
			t.Errorf("%s must be exempt", exempt)
		}
	}
	// Measured growth of tools/list for a gate-on KB: 2032 bytes over the 10
	// gated tools (~203 each), paid only by a KB that opts in; a gate-off KB
	// pays zero, so TestServer_ToolDescriptionBudget is unchanged (D350).
	if per := grown / gatedTools; per > 250 {
		t.Errorf("accept_findings costs %d bytes per gated tool, bound 250", per)
	}
}

func TestWriteGate_InvalidAcceptRejectedBeforeWrite(t *testing.T) {
	k, _ := gateKB(t, "error")
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	head := gitOut(t, k, "rev-parse", "HEAD")
	for _, tc := range []struct{ accept, want string }{
		{`[{"reason":"x"}]`, "accept_findings[0]: check is required"},
		{`[{"check":"Bad Name","reason":"x"}]`, "accept_findings[0]: check is required"},
		{`[{"check":"duplicate_link","reason":"  "}]`, "accept_findings[0]: reason is required"},
	} {
		text, isErr := callJSON(t, s, adminCtx, "concept_write",
			`{"id":"ops/new","frontmatter":{"type":"Note","title":"N"},"body":"b","accept_findings":`+tc.accept+`}`)
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s = %q (error=%v), want %q", tc.accept, text, isErr, tc.want)
		}
	}
	if _, err := k.ReadConcept("ops/new"); err == nil || gitOut(t, k, "rev-parse", "HEAD") != head {
		t.Fatal("a rejected accept_findings wrote something")
	}
}

// D357: the foreign change is committed on its own before the write, so the
// tree is clean when the gate looks (it used to skip the gate on a dirty tree).
func TestWriteGate_DirtyTreeIsCommittedFirst(t *testing.T) {
	k, _ := gateKB(t, "error")
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "foreign.md"), []byte("---\ntype: Note\ntitle: F\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"ops/p2"}`)
	w.Close()
	os.Stderr = old
	buf := make([]byte, 1<<16)
	n, _ := r.Read(buf)
	if isErr {
		t.Fatalf("write refused: %s", text)
	}
	if strings.Contains(string(buf[:n]), "write_gate skipped") {
		t.Fatalf("the gate was skipped:\n%s", buf[:n])
	}
	if _, err := k.ReadConcept("ops/p2"); err != nil {
		t.Fatalf("write did not happen: %v", err)
	}
	if subjects := gitOut(t, k, "log", "-2", "--format=%s"); !strings.HasPrefix(subjects, "concept_move") || !strings.HasSuffix(subjects, externalChangesSubject) {
		t.Fatalf("history:\n%s", subjects)
	}
}

func TestWriteGate_StashPopFailureRollsBack(t *testing.T) {
	k, _ := gateKB(t, "error")
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	head := gitOut(t, k, "rev-parse", "HEAD")
	orig := gateStashPop
	gateStashPop = func(string) error { return errors.New("injected") }
	defer func() { gateStashPop = orig }()
	text, isErr := callJSON(t, s, adminCtx, "concept_move", `{"source_id":"ops/p","target_id":"arch/p"}`)
	if !isErr || !strings.Contains(text, "write_gate: could not restore the write, nothing was written") {
		t.Fatalf("got %q (error=%v)", text, isErr)
	}
	gateClean(t, k)
	if gitOut(t, k, "rev-parse", "HEAD") != head || strings.TrimSpace(gitOut(t, k, "stash", "list")) != "" {
		t.Fatal("rollback left HEAD moved or a stash behind")
	}
}

func TestWriteGate_InactiveWithoutAutoCommit(t *testing.T) {
	k, _ := gateKB(t, "error")
	k.AutoCommit = false
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	if st := writeGateState(k); st != "inactive" {
		t.Fatalf("state = %q, want inactive", st)
	}
	caps := kbCapabilities(k)
	if caps["write_gate"].State != "inactive" || caps["write_gate"].Setting != "kbs[].write_gate" {
		t.Fatalf("capability = %+v", caps["write_gate"])
	}
	if strings.Contains(string(s.Tools()["concept_write"].InputSchema), "accept_findings") {
		t.Fatal("inactive gate changed the schema")
	}
	if text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"arch/w","frontmatter":{"type":"Note","title":"W","status":"bogus"},"body":"b"}`); isErr {
		t.Fatalf("write on an inactive gate refused: %s", text)
	}
	off, _ := gateKB(t, "")
	if st := writeGateState(off); st != "off" {
		t.Fatalf("default state = %q, want off", st)
	}
	on, _ := gateKB(t, "warning")
	if st := writeGateState(on); st != "warning" {
		t.Fatalf("state = %q", st)
	}
}

func TestWriteGate_HistorySurfacesAcceptedFindings(t *testing.T) {
	k, _ := gateKB(t, "error")
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	acc := `{"source_id":"ops/p","target_id":"arch/p","reason":"restructure","accept_findings":[{"check":"invalid_field_value","reason":"legacy status"}]}`
	if text, isErr := callJSON(t, s, adminCtx, "concept_move", acc); isErr {
		t.Fatal(text)
	}
	hist := decodeJSON(t, mustText(t, s, "concept_history", `{"id":"arch/p"}`))
	revs := hist["revisions"].([]interface{})
	top := revs[0].(map[string]interface{})
	af, _ := top["accepted_findings"].([]interface{})
	if len(af) != 1 || af[0] != "invalid_field_value: legacy status" || top["reason"] != "restructure" {
		t.Fatalf("history top revision = %v", top)
	}
	if len(revs) > 1 {
		if _, has := revs[1].(map[string]interface{})["accepted_findings"]; has {
			t.Fatal("a commit without the trailer reports accepted_findings")
		}
	}
	ch := decodeJSON(t, mustText(t, s, "changes_since", `{"since":"1d"}`))
	found := false
	for _, c := range ch["concepts"].([]interface{}) {
		m := c.(map[string]interface{})
		if m["id"] == "arch/p" {
			found = true
			if af, _ := m["accepted_findings"].([]interface{}); len(af) != 1 {
				t.Fatalf("changes_since entry = %v", m)
			}
		}
	}
	if !found {
		t.Fatalf("changes_since lacks arch/p: %v", ch)
	}
}
