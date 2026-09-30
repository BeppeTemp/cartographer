package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
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
		if err := k.CreateMap("sources", "Sources", "journal", nil, ""); err != nil {
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
	k, s := sourceFixture(t, true)
	out := registerSource(t, s, `{"title":"Kickoff Call: Notes!","source_kind":"transcript","locator":"https://example.com/a","sha256":"`+shaA+`","timestamp":"2026-03-04","body":"# Kickoff\nfacts"}`)
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
