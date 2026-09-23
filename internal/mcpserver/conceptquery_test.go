package mcpserver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func queryFixture(t *testing.T) *kb.KB {
	t.Helper()
	k := setupTestKB(t)
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("infra/alpha.md", "---\ntype: Note\ntitle: Alpha\nstatus: active\ntimestamp: 2025-01-02\n---\nbody\n")
	write("infra/beta.md", "---\ntype: Runbook\ntitle: Beta\nstatus: [draft]\n---\nbody\n")
	write("infra/owner/index.md", "---\ntype: Note\ntitle: Owner\n---\nbody\n")
	write("infrastructure/gamma.md", "---\ntype: Note\ntitle: Gamma\n---\nbody\n")
	write("loose.md", "---\ntype: Note\ntitle: Loose\n---\nbody\n")
	write("infra/broken.md", "---\ntype: [not, a, string\n---\nbody\n")
	return k
}

func queryIDs(res ConceptQueryResult) []string {
	out := make([]string, 0, len(res.Entries))
	for _, e := range res.Entries {
		out = append(out, e.ID)
	}
	return out
}

func entryByID(t *testing.T, res ConceptQueryResult, id string) ConceptEntry {
	t.Helper()
	for _, e := range res.Entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %q missing: %v", id, queryIDs(res))
	return ConceptEntry{}
}

func TestQueryConcepts_Metadata(t *testing.T) {
	res, err := queryConcepts(queryFixture(t), ConceptQuery{Scope: "infra"})
	if err != nil {
		t.Fatal(err)
	}

	alpha := entryByID(t, res, "infra/alpha")
	want := ConceptEntry{
		ID: "infra/alpha", Title: "Alpha", Type: "Note", Status: "active",
		Collection: "infra", Timestamp: "2025-01-02",
	}
	if !reflect.DeepEqual(alpha, want) {
		t.Errorf("alpha: got %+v, want %+v", alpha, want)
	}

	// An expanded concept is flagged from its physical path.
	if owner := entryByID(t, res, "infra/owner"); !owner.Expanded {
		t.Error("infra/owner lives at owner/index.md: Expanded should be set")
	}
	if entryByID(t, res, "infra/alpha").Expanded {
		t.Error("a flat concept must not be flagged expanded")
	}

	// A non-string status is not an error and not a value.
	if beta := entryByID(t, res, "infra/beta"); beta.Status != "" {
		t.Errorf("a list status is not a status: got %q", beta.Status)
	}

	// Unparseable frontmatter yields an entry with no metadata, never a
	// failed walk: one bad file must not blank the whole inventory.
	broken := entryByID(t, res, "infra/broken")
	if broken.Title != "" || broken.Type != "" {
		t.Errorf("malformed frontmatter should yield empty metadata: %+v", broken)
	}
}

func TestQueryConcepts_ScopeMatchesWholeSegments(t *testing.T) {
	res, err := queryConcepts(queryFixture(t), ConceptQuery{Scope: "infra"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"infra/alpha", "infra/beta", "infra/broken", "infra/owner"}
	if got := queryIDs(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("scope infra: got %v, want %v (infrastructure/ must not be swallowed)", got, want)
	}
}

func TestQueryConcepts_RootConceptHasNoCollection(t *testing.T) {
	res, err := queryConcepts(queryFixture(t), ConceptQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got := entryByID(t, res, "loose").Collection; got != "" {
		t.Errorf("a KB-root concept has no collection, got %q", got)
	}
}

func TestQueryConcepts_IncludeFiltersBeforeAccounting(t *testing.T) {
	res, err := queryConcepts(queryFixture(t), ConceptQuery{
		Scope:   "infra",
		Include: func(id string) bool { return id != "infra/beta" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		if e.ID == "infra/beta" {
			t.Fatal("an excluded concept was returned")
		}
	}
	if res.Examined != 3 {
		t.Errorf("Examined must count what the predicate let through: got %d, want 3", res.Examined)
	}
}

func TestQueryConcepts_TimestampAccounting(t *testing.T) {
	after := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	res, err := queryConcepts(queryFixture(t), ConceptQuery{Scope: "infra", After: &after})
	if err != nil {
		t.Fatal(err)
	}
	if got := queryIDs(res); !reflect.DeepEqual(got, []string{"infra/alpha"}) {
		t.Fatalf("timestamp_after: got %v, want [infra/alpha]", got)
	}
	// beta and owner carry no timestamp, broken carries no parseable
	// frontmatter: only the first two are "skipped for lack of a timestamp".
	if res.SkippedTimestamp != 2 {
		t.Errorf("SkippedTimestamp: got %d, want 2", res.SkippedTimestamp)
	}
	if res.Examined != 4 {
		t.Errorf("Examined: got %d, want 4", res.Examined)
	}
}

func TestQueryConcepts_SortedAndDeterministic(t *testing.T) {
	k := queryFixture(t)
	first, err := queryConcepts(k, ConceptQuery{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := queryConcepts(k, ConceptQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two identical queries returned different results")
	}
	ids := queryIDs(first)
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("results are not sorted by id: %v", ids)
		}
	}
}
