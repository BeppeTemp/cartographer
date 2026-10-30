package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D278: the source ledger.

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func sourceFixture(t *testing.T, withMap bool) (*kb.KB, *Server) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	if withMap {
		if err := k.CreateMap("sources", "Sources", "journal", nil); err != nil {
			t.Fatal(err)
		}
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

func registerSource(t *testing.T, s *Server, args string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(callOK(t, s, "source_register", args)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSourceRegisterCreates(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	out := registerSource(t, s, `{"title":"Kickoff Call: Notes!","source_kind":"transcript","locator":"https://example.com/a","sha256":"`+shaA+`","timestamp":"2026-03-04","body":"# Kickoff Call: Notes!\nfacts"}`)
	if out["duplicate"] != false || out["id"] != "sources/2026-03-04-kickoff-call-notes" {
		t.Fatalf("got %v", out)
	}
	data, err := k.ReadConcept("sources/2026-03-04-kickoff-call-notes")
	if err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter(data.FrontmatterRaw)
	for key, want := range map[string]string{"type": "Source", "source_kind": "transcript", "ingest_status": "pending", "sha256": shaA, "locator": "https://example.com/a"} {
		if v, _ := fm.Get(key); v != want {
			t.Errorf("%s = %v, want %s", key, v, want)
		}
	}
	// Same title, different content, same day: new ID with a suffix.
	out = registerSource(t, s, `{"title":"Kickoff Call: Notes!","source_kind":"transcript","sha256":"`+shaB+`","timestamp":"2026-03-04"}`)
	if out["duplicate"] != false || out["id"] != "sources/2026-03-04-kickoff-call-notes-2" {
		t.Fatalf("same title, different sha256 must be a new source: %v", out)
	}
}

func TestSourceRegisterDuplicateBySHA(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	registerSource(t, s, `{"title":"A","source_kind":"document","sha256":"`+shaA+`","timestamp":"2026-01-01"}`)
	before, _ := k.ReadRaw("sources/log.md")
	out := registerSource(t, s, `{"title":"Other title","source_kind":"document","sha256":"`+shaA+`","locator":"https://example.com/other"}`)
	if out["duplicate"] != true || out["id"] != "sources/2026-01-01-a" || out["ingest_status"] != "pending" {
		t.Fatalf("got %v", out)
	}
	after, _ := k.ReadRaw("sources/log.md")
	if before != after {
		t.Fatal("a duplicate must write nothing")
	}
}

func TestSourceRegisterDuplicateByLocator(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	registerSource(t, s, `{"title":"A","source_kind":"web","locator":"https://example.com/p"}`)
	if out := registerSource(t, s, `{"title":"B","source_kind":"web","locator":"https://example.com/p"}`); out["duplicate"] != true {
		t.Fatalf("same locator, no sha: want duplicate, got %v", out)
	}
	// Both have a sha256 and they differ: the locator no longer decides.
	registerSource(t, s, `{"title":"C","source_kind":"web","locator":"https://example.com/q","sha256":"`+shaA+`"}`)
	if out := registerSource(t, s, `{"title":"C2","source_kind":"web","locator":"https://example.com/q","sha256":"`+shaB+`"}`); out["duplicate"] != false {
		t.Fatalf("differing sha256 must win over the locator: %v", out)
	}
	// A title match alone is never a duplicate.
	if out := registerSource(t, s, `{"title":"A","source_kind":"web"}`); out["duplicate"] != false {
		t.Fatalf("title match is not a duplicate: %v", out)
	}
}

func TestSourceRegisterValidation(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, false)
	res := callTool(t, s, "source_register", `{"title":"A","source_kind":"document"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, `map_create(name: "sources", kind: "journal")`) {
		t.Fatalf("missing map must hint map_create: %+v", res)
	}
	_, s = sourceFixture(t, true)
	for name, args := range map[string]string{
		"bad sha":      `{"title":"A","source_kind":"x","sha256":"ABC"}`,
		"upper sha":    `{"title":"A","source_kind":"x","sha256":"` + strings.ToUpper(shaA) + `"}`,
		"machine path": `{"title":"A","source_kind":"x","locator":"/Users/user/doc.pdf"}`,
		"bad status":   `{"title":"A","source_kind":"x","ingest_status":"done"}`,
		"bad ts":       `{"title":"A","source_kind":"x","timestamp":"yesterday"}`,
		"no kind":      `{"title":"A"}`,
		"not journal":  `{"title":"A","source_kind":"x","map":"manutenzione"}`,
	} {
		if res := callTool(t, s, "source_register", args); !res.IsError {
			t.Errorf("%s: want an error, got %+v", name, res)
		}
	}
	// A placeholder locator is accepted.
	registerSource(t, s, `{"title":"P","source_kind":"document","locator":"{{path:inbox}}/a.pdf"}`)
}

func TestSourceRegisterNarrowedTokenRefused(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Journals: []string{"sources"}, Write: true}}}
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "narrow", Policy: policy})
	res := s.callTool(ctx, "source_register", json.RawMessage(`{"title":"A","source_kind":"x"}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "forbidden") {
		t.Fatalf("narrowed token must be refused: %+v", res)
	}
}

type listedSource struct {
	ID           string `json:"id"`
	IngestStatus string `json:"ingest_status"`
	CitedBy      int    `json:"cited_by"`
	SourceKind   string `json:"source_kind"`
}

func listSources(t *testing.T, s *Server, args string) []listedSource {
	t.Helper()
	var out []listedSource
	if err := json.Unmarshal([]byte(callOK(t, s, "source_list", args)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSourceListOrderFilterAndCitations(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	registerSource(t, s, `{"title":"Late","source_kind":"document","timestamp":"2026-05-01"}`)
	registerSource(t, s, `{"title":"Early","source_kind":"ticket","timestamp":"2026-01-01"}`)
	registerSource(t, s, `{"title":"Done","source_kind":"web","timestamp":"2026-03-01","ingest_status":"ingested"}`)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: N\nprovenance: [sources/2026-03-01-done, https://example.com/x]")
	if _, err := k.WriteConcept("manutenzione/n", fm, "# N\n", ""); err != nil {
		t.Fatal(err)
	}

	pending := listSources(t, s, `{}`)
	if len(pending) != 2 || pending[0].ID != "sources/2026-01-01-early" || pending[1].ID != "sources/2026-05-01-late" {
		t.Fatalf("pending, oldest first: %+v", pending)
	}
	all := listSources(t, s, `{"status":"*"}`)
	if len(all) != 3 || all[1].ID != "sources/2026-03-01-done" || all[1].CitedBy != 1 || all[1].IngestStatus != "ingested" {
		t.Fatalf("all: %+v", all)
	}
	if got := listSources(t, s, `{"status":"*","limit":1}`); len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}
	if res := callTool(t, s, "source_list", `{"status":"bogus"}`); !res.IsError {
		t.Fatal("bad status must error")
	}

	// A reader that cannot see the citing concept does not count it.
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Journals: []string{"sources"}}}}
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "narrow", Policy: policy})
	res := s.callTool(ctx, "source_list", json.RawMessage(`{"status":"*"}`))
	var narrow []listedSource
	if err := json.Unmarshal([]byte(res.Content[0].Text), &narrow); err != nil || len(narrow) != 3 {
		t.Fatalf("narrow list: %v %+v", err, res)
	}
	for _, e := range narrow {
		if e.CitedBy != 0 {
			t.Fatalf("hidden citer counted: %+v", narrow)
		}
	}
}

func TestKBStatusSources(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	var m map[string]json.RawMessage
	_ = json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m)
	if _, ok := m["sources"]; ok {
		t.Fatal("sources present with no Source")
	}
	registerSource(t, s, `{"title":"A","source_kind":"x"}`)
	registerSource(t, s, `{"title":"B","source_kind":"x","ingest_status":"skipped"}`)
	_ = json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m)
	var got map[string]int
	if err := json.Unmarshal(m["sources"], &got); err != nil || got["pending"] != 1 || got["skipped"] != 1 || got["ingested"] != 0 {
		t.Fatalf("sources = %s (%v)", m["sources"], err)
	}
}

func TestConceptDeleteSourceCitedNeedsForce(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	id := registerSource(t, s, `{"title":"A","source_kind":"x"}`)["id"].(string)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: N\nprovenance: [" + id + "]")
	if _, err := k.WriteConcept("manutenzione/citer", fm, "# N\n", ""); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "concept_delete", `{"id":"`+id+`"}`)
	if !res.IsError || !strings.HasPrefix(res.Content[0].Text, "inbound_links:") || !strings.Contains(res.Content[0].Text, "manutenzione/citer") {
		t.Fatalf("want inbound_links naming the citer: %+v", res)
	}

	// A hidden citer neither blocks nor leaks.
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Journals: []string{"sources"}, Write: true}}}
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "narrow", Policy: policy})
	if res := s.callTool(ctx, "concept_delete", json.RawMessage(`{"id":"`+id+`"}`)); res.IsError || strings.Contains(res.Content[0].Text, "citer") {
		t.Fatalf("hidden citer: %+v", res)
	}
}

func TestConceptDeleteSourceForce(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	id := registerSource(t, s, `{"title":"A","source_kind":"x"}`)["id"].(string)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: N\nprovenance: [" + id + "]")
	if _, err := k.WriteConcept("manutenzione/citer", fm, "# N\n", ""); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "concept_delete", `{"id":"`+id+`","force":true}`)
	if res.IsError || !strings.Contains(res.Content[0].Text, "manutenzione/citer") {
		t.Fatalf("want deleted with warning: %+v", res)
	}
}

// The registration is gated (#1297): a title over the limit is refused with the
// finding, nothing is written, instead of registered with a warning.
func TestSourceRegisterRefusesATitleOverTheLimit(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	long := strings.TrimSpace(strings.Repeat("word ", 21))
	tr := s.callTool(authLocalContext(), "source_register", json.RawMessage(`{"title":"`+long+`","source_kind":"document"}`))
	got := ""
	if len(tr.Content) > 0 {
		got = tr.Content[0].Text
	}
	if !tr.IsError || !strings.Contains(got, "title_quality") || !strings.Contains(got, "limit 100") || strings.Contains(got, "index_entry: true") {
		t.Fatalf("got %q", got)
	}
	if out := registerSource(t, s, `{"title":"Short","source_kind":"document"}`); out["warnings"] != nil {
		t.Fatalf("short title warned: %v", out)
	}
}

// #1376: the advice follows the findings present: a heading that differs from
// the title is not answered with "shorten the title".
func TestSourceRegisterGateAdviceFollowsTheFinding(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	tr := s.callTool(authLocalContext(), "source_register", json.RawMessage(`{"title":"Repo example","source_kind":"document","body":"# Another heading\n\ntext\n"}`))
	got := ""
	if len(tr.Content) > 0 {
		got = tr.Content[0].Text
	}
	if !tr.IsError || !strings.Contains(got, "title_h1_mismatch") || strings.Contains(got, "shorten the title") || !strings.Contains(got, "first heading") || !strings.Contains(got, "index entry is added by source_register itself") {
		t.Fatalf("got %q", got)
	}
	long := strings.TrimSpace(strings.Repeat("word ", 21))
	tr = s.callTool(authLocalContext(), "source_register", json.RawMessage(`{"title":"`+long+`","source_kind":"document"}`))
	if !tr.IsError || !strings.Contains(tr.Content[0].Text, "shorten the title") {
		t.Fatalf("title_quality lost its advice: %+v", tr)
	}
}

func TestMapCreateUpdateWarnOnPoorTitle(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, false)
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(callOK(t, s, "map_create", `{"name":"infra","title":"Infrastructure: everything that runs on the cluster"}`)), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "created" || out["warnings"] == nil {
		t.Fatalf("map_create = %v", out)
	}
	out = nil
	if err := json.Unmarshal([]byte(callOK(t, s, "map_update", `{"map":"infra","title":"Infra"}`)), &out); err != nil {
		t.Fatal(err)
	}
	if out["warnings"] != nil {
		t.Fatalf("good title warned: %v", out["warnings"])
	}
	out = nil
	if err := json.Unmarshal([]byte(callOK(t, s, "map_update", `{"map":"infra","title":"Infrastructure: everything that runs on the cluster"}`)), &out); err != nil {
		t.Fatal(err)
	}
	if out["warnings"] == nil {
		t.Fatalf("map_update = %v", out)
	}
}

// D1072: source_register provides the source template.

func sourceNotes(out map[string]interface{}) []string {
	var r []string
	if l, ok := out["notes"].([]interface{}); ok {
		for _, v := range l {
			r = append(r, v.(string))
		}
	}
	return r
}

func readRel(t *testing.T, k *kb.KB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(k.Root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sourceMapContract(t *testing.T, k *kb.KB) kb.MapContract {
	t.Helper()
	c, err := k.ReadMapContract("sources")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSourceRegisterWritesAndDeclaresTemplate(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	out := registerSource(t, s, `{"title":"First","source_kind":"note","timestamp":"2026-03-04"}`)
	if len(sourceNotes(out)) != 2 {
		t.Fatalf("want two notes, got %v", out["notes"])
	}
	if got := readRel(t, k, "templates/source.md"); got != kb.SourceTemplate() {
		t.Fatalf("template differs:\n%s", got)
	}
	if c := sourceMapContract(t, k); c.DefaultTemplate != "source" || !slices.Equal(c.Templates, []string{"source"}) {
		t.Fatalf("contract = %+v", c)
	}
	mapBefore := readRel(t, k, "data/sources/_map.md")
	tplBefore := readRel(t, k, "templates/source.md")
	out = registerSource(t, s, `{"title":"Second","source_kind":"note","timestamp":"2026-03-05","ingest_status":"ingested","body":"# Second\n\n## Facts\n\nx\n"}`)
	if n := sourceNotes(out); len(n) != 0 {
		t.Fatalf("second call must write nothing, got %v", n)
	}
	if readRel(t, k, "data/sources/_map.md") != mapBefore || readRel(t, k, "templates/source.md") != tplBefore {
		t.Fatal("second call rewrote the template or the map")
	}
	findings, err := lint.Run(k, "sources", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Check, "template_") {
			t.Errorf("template finding after registration: %+v", f)
		}
	}
	items, err := lint.Review(k, findings)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Kind == lint.ReviewTemplateProposal {
			t.Errorf("kb_review still proposes a template: %+v", it)
		}
	}
}

func TestSourceRegisterDuplicateSetsNothingUp(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	registerSource(t, s, `{"title":"A","source_kind":"note","sha256":"`+shaA+`"}`)
	if err := os.Remove(filepath.Join(k.Root, "templates/source.md")); err != nil {
		t.Fatal(err)
	}
	out := registerSource(t, s, `{"title":"A","source_kind":"note","sha256":"`+shaA+`"}`)
	if out["duplicate"] != true || out["notes"] != nil {
		t.Fatalf("got %v", out)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "templates/source.md")); err == nil {
		t.Fatal("a duplicate must not write the template")
	}
}

func TestSourceRegisterKeepsExistingSourceTemplate(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	custom := "---\ntype: Source\ntitle: \"{{title}}\"\nx-template.required_fields: [source_kind]\nx-template.open_sections: true\n---\n# {{title}}\n"
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates/source.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if readRel(t, k, "templates/source.md") != custom {
		t.Fatal("an existing template was rewritten")
	}
	if sourceMapContract(t, k).DefaultTemplate != "source" {
		t.Fatal("map not declared")
	}
}

func TestSourceRegisterTemplateOfOtherType(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	other := "---\ntype: Host\ntitle: \"{{title}}\"\n---\n# {{title}}\n"
	os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755)
	os.WriteFile(filepath.Join(k.Root, "templates/source.md"), []byte(other), 0o644)
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if readRel(t, k, "templates/source.md") != other || sourceMapContract(t, k).DefaultTemplate != "" {
		t.Fatal("must leave template and map alone")
	}
	if len(sourceNotes(out)) != 1 || out["id"] == nil {
		t.Fatalf("got %v", out)
	}
}

func TestSourceRegisterMixedJournalGetsTemplateOnly(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	if _, err := k.WriteConcept("sources/inc", mustFM(t, "type: Incident\ntitle: Inc"), "# Inc\n", ""); err != nil {
		t.Fatal(err)
	}
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if _, err := os.Stat(filepath.Join(k.Root, "templates/source.md")); err != nil {
		t.Fatal("template should be written")
	}
	if sourceMapContract(t, k).HasTemplateKeys() {
		t.Fatal("a mixed journal must stay flexible")
	}
	if n := sourceNotes(out); len(n) != 2 || !strings.Contains(n[1], "not Source") {
		t.Fatalf("notes = %v", n)
	}
}

func TestSourceRegisterMapWithTemplatesUntouched(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755)
	os.WriteFile(filepath.Join(k.Root, "templates/other.md"), []byte("---\ntype: Source\ntitle: \"{{title}}\"\nx-template.open_sections: true\n---\n# {{title}}\n"), 0o644)
	if _, err := k.UpdateMapContract("sources", kb.MapContractUpdate{Templates: &[]string{"other"}}); err != nil {
		t.Fatal(err)
	}
	before := readRel(t, k, "data/sources/_map.md")
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if readRel(t, k, "data/sources/_map.md") != before {
		t.Fatal("map rewritten")
	}
	if n := sourceNotes(out); len(n) != 1 || !strings.Contains(n[0], "already declares") {
		t.Fatalf("notes = %v", n)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "templates/source.md")); err == nil {
		t.Fatal("a source template was written into a map that accepts other templates")
	}
	if data, err := k.ReadConcept(okf.ConceptID(out["id"].(string))); err != nil || !strings.Contains(data.FrontmatterRaw, "shape: other") {
		t.Fatalf("the page does not take the map's only Source template: %v %+v", err, data)
	}
}

func TestSourceRegisterPageNotFollowingTemplateLeavesMapFlexible(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	if _, err := k.WriteConcept("sources/old", mustFM(t, "type: Source\ntitle: Old"), "# Old\n", ""); err != nil {
		t.Fatal(err)
	}
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if sourceMapContract(t, k).HasTemplateKeys() {
		t.Fatal("map declared although a page fails the template")
	}
	if n := sourceNotes(out); len(n) != 2 || !strings.Contains(n[1], "1 page") {
		t.Fatalf("notes = %v", n)
	}
}

func TestSourceRegisterSymlinkedTemplatesStillRegisters(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(k.Root, "templates")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if out["id"] == nil || out["warnings"] == nil {
		t.Fatalf("got %v", out)
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatal("wrote through the symlink")
	}
	if sourceMapContract(t, k).HasTemplateKeys() {
		t.Fatal("map declared")
	}
}

func TestSourceRegisterFailedDeclarationStillRegisters(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	// A legacy _archive.md descriptor is readable but UpdateMapContract refuses
	// to rewrite it (D77): the declaration fails, the registration must not.
	dir := filepath.Join(k.DataRoot(), "sources")
	if err := os.Rename(filepath.Join(dir, "_map.md"), filepath.Join(dir, "_archive.md")); err != nil {
		t.Fatal(err)
	}
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if out["id"] == nil || out["warnings"] == nil {
		t.Fatalf("want the source registered with a warning, got %v", out)
	}
	if _, err := k.ReadConcept(okf.ConceptID(out["id"].(string))); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRegisterOneCommit(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKBWithRemote(t)
	if err := k.CreateMap("sources", "Sources", "journal", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: journal"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	before := commitCount(t, k)
	registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if got := commitCount(t, k); got != before+1 {
		t.Fatalf("commits: %d -> %d, want one more", before, got)
	}
	out, err := exec.Command("git", "-C", k.Root, "show", "--name-only", "--format=", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"templates/source.md", "data/sources/_map.md", "data/sources/20"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("commit lacks %s:\n%s", want, out)
		}
	}
}

// The text the server writes passes the validation artifact_write applies.
func TestSourceTemplatePassesArtifactValidation(t *testing.T) {
	t.Parallel()
	if _, _, _, err := validateTemplateArtifact([]byte(kb.SourceTemplate())); err != nil {
		t.Fatal(err)
	}
}

// An operator-edited template the new page does not follow: the first
// registration must not declare the map (the new page is checked too).
func TestSourceRegisterNewPageNotFollowingEditedTemplate(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	edited := "---\ntype: Source\ntitle: \"{{title}}\"\nx-template.required_fields: [source_kind, owner]\nx-template.open_sections: true\n---\n# {{title}}\n"
	os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755)
	os.WriteFile(filepath.Join(k.Root, "templates/source.md"), []byte(edited), 0o644)
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if sourceMapContract(t, k).HasTemplateKeys() {
		t.Fatal("map declared although the new page fails the template")
	}
	if n := sourceNotes(out); len(n) != 1 || !strings.Contains(n[0], "1 page") {
		t.Fatalf("notes = %v", n)
	}
}

// A registration that fails writes no template and no contract.
func TestSourceRegisterFailedWriteSetsNothingUp(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	dir := filepath.Join(k.DataRoot(), "sources")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skipf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if f, err := os.Create(filepath.Join(dir, "probe")); err == nil {
		f.Close()
		t.Skip("directory still writable (root?)")
	}
	res := s.callTool(authLocalContext(), "source_register", json.RawMessage(`{"title":"A","source_kind":"note"}`))
	if !res.IsError {
		t.Fatalf("expected an error result, got %v", res)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "templates/source.md")); err == nil {
		t.Fatal("template written by a failed registration")
	}
}

// #1280: source slugs fold accents and fall back to a stable hash, never to
// "source" and "source-2".
func TestSourceSlugTransliteratesAndFallsBackToHash(t *testing.T) {
	t.Parallel()
	if got := sourceSlug("Σ ünï Straße"); got != "uni-strasse" {
		t.Errorf("accented title = %q, want uni-strasse", got)
	}
	a, b := sourceSlug("日本語の資料"), sourceSlug("中文资料")
	if !strings.HasPrefix(a, "source-") || a == b {
		t.Errorf("non-latin slugs = %q, %q, want distinct source-<hash>", a, b)
	}
	if a != sourceSlug("日本語の資料") {
		t.Error("the hash fallback is not stable")
	}
}

// #1297: a registration is a gated concept write: when the ledger journal has a
// curated index it gets the entry (the inbound link) in the same commit, so
// lint reports no index_incomplete or orphan after two registrations.
func TestSourceRegisterIndexesTheNewSource(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	var ids []string
	for _, title := range []string{"Src One", "Src Two"} {
		out := registerSource(t, s, `{"title":"`+title+`","source_kind":"document","timestamp":"2026-10-28"}`)
		ids = append(ids, out["id"].(string))
		if notes, _ := out["curated_indexes"].([]interface{}); len(notes) != 1 {
			t.Fatalf("curated_indexes = %v", out["curated_indexes"])
		}
	}
	idx, _, err := k.IndexHash("sources")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !strings.Contains(idx, strings.TrimPrefix(id, "sources/")) {
			t.Errorf("sources/index.md has no entry for %s:\n%s", id, idx)
		}
	}
	res, err := toolLint(k).Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("lint: %v %+v", err, res)
	}
	for _, bad := range []string{"index_incomplete", "orphan"} {
		if strings.Contains(res.Content[0].Text, bad) {
			t.Errorf("lint reports %s after registrations:\n%s", bad, res.Content[0].Text)
		}
	}
}

// A registration that worked before the gate still works (#1297): a journal
// whose templates settle no shape for a Source registers anyway, and so does one
// whose curated index is missing.
func TestSourceRegisterIsNotRefusedForTemplateMissing(t *testing.T) {
	t.Parallel()
	for name, tpls := range map[string]map[string]string{
		"no Source template":   {"memo": "Note"},
		"two Source templates": {"one": "Source", "two": "Source"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k, s := sourceFixture(t, true)
			if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
				t.Fatal(err)
			}
			var slugs []string
			for slug, typ := range tpls {
				raw := "---\ntype: " + typ + "\ntitle: \"{{title}}\"\nx-template.open_sections: true\n---\n# {{title}}\n"
				if err := os.WriteFile(filepath.Join(k.Root, "templates", slug+".md"), []byte(raw), 0o644); err != nil {
					t.Fatal(err)
				}
				slugs = append(slugs, slug)
			}
			if _, err := k.UpdateMapContract("sources", kb.MapContractUpdate{Templates: &slugs}); err != nil {
				t.Fatal(err)
			}
			if out := registerSource(t, s, `{"title":"A","source_kind":"note"}`); out["duplicate"] != false {
				t.Fatalf("got %v", out)
			}
		})
	}
}

func TestSourceRegisterWithoutCuratedIndexStillRegisters(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	if err := os.Remove(filepath.Join(k.DataRoot(), "sources", "index.md")); err != nil {
		t.Fatal(err)
	}
	out := registerSource(t, s, `{"title":"A","source_kind":"note"}`)
	if out["duplicate"] != false {
		t.Fatalf("got %v", out)
	}
	if _, err := k.ReadConcept(okf.ConceptID(out["id"].(string))); err != nil {
		t.Fatal(err)
	}
}

// D1201: the ledger knows collections, feeds and systems.
func TestSourceRegisterShapes(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	for _, c := range []struct{ shape, loc string }{
		{"collection", "https://docs.example.com/space"},
		{"feed", "https://example.com/feed.xml"},
		{"system", "https://metrics.example.com"},
	} {
		out := registerSource(t, s, `{"title":"T `+c.shape+`","source_kind":"space","source_shape":"`+c.shape+`","connector":"wiki-reader","recipe":"snap","locator":"`+c.loc+`"}`)
		data, err := k.ReadConcept(okfID(out["id"]))
		if err != nil {
			t.Fatal(err)
		}
		fm, _ := okf.ParseFrontmatter(data.FrontmatterRaw)
		if fmString(fm, "source_shape") != c.shape || fmString(fm, "connector") != "wiki-reader" || fmString(fm, "recipe") != "snap" {
			t.Fatalf("%s: frontmatter = %q", c.shape, data.FrontmatterRaw)
		}
	}
	var listed []map[string]interface{}
	if err := json.Unmarshal([]byte(callOK(t, s, "source_list", `{}`)), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 || listed[0]["connector"] != "wiki-reader" || listed[0]["source_shape"] == nil || listed[0]["recipe"] != "snap" {
		t.Fatalf("listed = %v", listed)
	}
	dup := registerSource(t, s, `{"title":"Again","source_kind":"space","source_shape":"collection","locator":"https://docs.example.com/space2"}`)
	if dup["duplicate"] != false {
		t.Fatal(dup)
	}
	dup = registerSource(t, s, `{"title":"Again","source_kind":"space","source_shape":"collection","locator":"https://docs.example.com/space"}`)
	if dup["duplicate"] != true {
		t.Fatalf("collection must dedup by locator: %v", dup)
	}
}

func okfID(v interface{}) okf.ConceptID { s, _ := v.(string); return okf.ConceptID(s) }

func TestSourceRegisterShapeAndCredentialsRefused(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	res := callTool(t, s, "source_register", `{"title":"A","source_kind":"x","source_shape":"stream"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "document, collection, feed, system") {
		t.Fatalf("shape: %+v", res)
	}
	res = callTool(t, s, "source_register", `{"title":"A","source_kind":"x","locator":"https://user:secret@example.com/a"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "credentials") {
		t.Fatalf("password: %+v", res)
	}
	for _, loc := range []string{
		"https://tok3n@example.com/a", "ssh://git:pw@example.com/a",
		"https://example.com/a?token=abc", "https://example.com/a?access_key=abc",
		"https://example.com/a?api-key=abc", "https://example.com/a?x=1&Password=abc",
		"https://example.com/a?client_secret=abc",
	} {
		res = callTool(t, s, "source_register", `{"title":"A","source_kind":"x","locator":"`+loc+`"}`)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "credential") || strings.Contains(res.Content[0].Text, "abc") {
			t.Errorf("locator %s: %+v", loc, res)
		}
	}
	for _, f := range []string{"connector", "recipe"} {
		res = callTool(t, s, "source_register", `{"title":"A","source_kind":"x","`+f+`":"https://u:p@example.com/x"}`)
		if !res.IsError || !strings.Contains(res.Content[0].Text, f) {
			t.Errorf("%s: %+v", f, res)
		}
	}
	registerSource(t, s, `{"title":"S","source_kind":"x","locator":"ssh://git@example.com/org/repo.git"}`)
	registerSource(t, s, `{"title":"B","source_kind":"x","locator":"https://example.com/a?page=2&sort=asc","connector":"wiki-reader"}`)
}

func TestSourceRegisterWithoutShapeFieldsWritesNone(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	out := registerSource(t, s, `{"title":"Plain","source_kind":"document","timestamp":"2026-03-04"}`)
	data, _ := k.ReadConcept(okfID(out["id"]))
	for _, f := range []string{"source_shape", "connector", "recipe"} {
		if strings.Contains(data.FrontmatterRaw, f) {
			t.Fatalf("%s written: %q", f, data.FrontmatterRaw)
		}
	}
}

// D1262: the worker keeps a feed's cursor in the Source's own frontmatter,
// written with concept_patch under if_match. The arguments below are what
// internal/worker/feed builds (this package cannot import the worker).
func TestFeedFieldsRoundTripThroughConceptPatchAndLint(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, true)
	out := registerSource(t, s, `{"title":"Docs feed","source_kind":"git-repo","source_shape":"feed","recipe":"git-docs","connector":"git","locator":"https://git.example.com/docs.git"}`)
	id := out["id"].(string)
	read := func() (string, string) {
		data, err := k.ReadConcept(okfID(id))
		if err != nil {
			t.Fatal(err)
		}
		return data.FrontmatterRaw, data.ContentHash
	}
	_, h0 := read()
	lease := callOK(t, s, "concept_patch", `{"id":"`+id+`","if_match":"`+h0+`","frontmatter":{"feed_checked_at":"2026-10-30T12:00:00Z"}}`)
	var r struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal([]byte(lease), &r); err != nil || r.ContentHash == "" {
		t.Fatalf("lease = %s, %v", lease, err)
	}
	callOK(t, s, "concept_patch", `{"id":"`+id+`","if_match":"`+r.ContentHash+`","frontmatter":{"feed_checked_at":"2026-10-30T12:00:05Z","feed_cursor":"abc123..def456@dir/a.go","feed_interval":"6h","feed_include":["docs/**/*.md"],"feed_error":"list_failed: boom"}}`)
	// A second replica holding the old hash loses the lease.
	res := callTool(t, s, "concept_patch", `{"id":"`+id+`","if_match":"`+h0+`","frontmatter":{"feed_checked_at":"2026-10-30T12:00:06Z"}}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "stale_write") {
		t.Fatalf("stale lease = %+v", res)
	}
	_, h2 := read()
	callOK(t, s, "concept_patch", `{"id":"`+id+`","if_match":"`+h2+`","frontmatter":{"feed_cursor":"def456"},"unset":["feed_error"]}`)
	fm, _ := read()
	if !strings.Contains(fm, "feed_cursor: def456") || strings.Contains(fm, "feed_error") || !strings.Contains(fm, "feed_interval") {
		t.Fatalf("frontmatter = %q", fm)
	}
	listed := callOK(t, s, "concept_list", `{"where":["type=Source","source_shape=feed"],"fields":["locator","connector","source_kind","recipe","feed_checked_at","feed_interval","feed_cursor","feed_include","content_hash"]}`)
	if !strings.Contains(listed, "def456") || !strings.Contains(listed, "docs/**/*.md") || !strings.Contains(listed, "content_hash") {
		t.Fatalf("concept_list = %s", listed)
	}
	if lint := callOK(t, s, "lint", `{}`); strings.Contains(lint, "template_") && strings.Contains(lint, id) {
		t.Fatalf("lint flags the feed Source: %s", lint)
	}
}
