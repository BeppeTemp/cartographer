package search

import (
	"testing"
)

func TestTokenize(t *testing.T) {
	got := Tokenize("Hello, World! 123-test")
	want := []string{"hello", "world", "123", "test"}
	if len(got) != len(want) {
		t.Fatalf("Tokenize: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Tokenize[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIndex_AddAndSearch(t *testing.T) {
	idx := New()
	idx.Add("arch/runbook", "How to rotate certificates in production")
	idx.Add("arch/overview", "Architecture overview of the production system")
	idx.Add("notes/meeting", "Meeting notes about the new design")

	hits := idx.Search("production", "", 10)
	if len(hits) != 2 {
		t.Fatalf("Search 'production': got %d hits, want 2", len(hits))
	}
	if hits[0].ID != "arch/overview" && hits[0].ID != "arch/runbook" {
		t.Errorf("Search 'production': unexpected top hit %q", hits[0].ID)
	}
}

func TestIndex_SearchMultiTerm(t *testing.T) {
	idx := New()
	idx.Add("a", "foo bar baz")
	idx.Add("b", "foo qux")
	idx.Add("c", "bar baz")

	hits := idx.Search("foo bar", "", 10)
	if len(hits) != 1 {
		t.Fatalf("Search 'foo bar': got %d hits, want 1", len(hits))
	}
	if hits[0].ID != "a" {
		t.Errorf("Search 'foo bar': got %q, want 'a'", hits[0].ID)
	}
}

func TestIndex_SearchScope(t *testing.T) {
	idx := New()
	idx.Add("arch/runbook", "production deploy runbook")
	idx.Add("notes/deploy", "production deploy notes")

	hits := idx.Search("production", "arch/", 10)
	if len(hits) != 1 {
		t.Fatalf("Search with scope 'arch/': got %d hits, want 1", len(hits))
	}
	if hits[0].ID != "arch/runbook" {
		t.Errorf("unexpected hit: %q", hits[0].ID)
	}
}

func TestIndex_SearchNoResults(t *testing.T) {
	idx := New()
	idx.Add("a", "foo bar")

	hits := idx.Search("nonexistent", "", 10)
	if len(hits) != 0 {
		t.Fatalf("Search 'nonexistent': got %d hits, want 0", len(hits))
	}
}

func TestIndex_SearchEmptyQuery(t *testing.T) {
	idx := New()
	idx.Add("a", "foo bar")

	hits := idx.Search("", "", 10)
	if hits != nil {
		t.Fatalf("Search empty: got %v, want nil", hits)
	}
}

func TestIndex_Count(t *testing.T) {
	idx := New()
	if idx.Count() != 0 {
		t.Fatalf("Count empty: got %d", idx.Count())
	}
	idx.Add("a", "foo")
	idx.Add("b", "bar")
	if idx.Count() != 2 {
		t.Fatalf("Count: got %d, want 2", idx.Count())
	}
}

func TestIndex_AddReplace(t *testing.T) {
	idx := New()
	idx.Add("a", "old content here")
	idx.Add("a", "new content replaced")

	if idx.Count() != 1 {
		t.Fatalf("Count after replace: got %d, want 1", idx.Count())
	}

	hits := idx.Search("old", "", 10)
	if len(hits) != 0 {
		t.Fatalf("Search 'old' after replace: got %d hits, want 0", len(hits))
	}

	hits = idx.Search("replaced", "", 10)
	if len(hits) != 1 {
		t.Fatalf("Search 'replaced' after replace: got %d hits, want 1", len(hits))
	}
}

func TestIndex_SearchLimit(t *testing.T) {
	idx := New()
	for i := 0; i < 30; i++ {
		idx.Add("doc"+string(rune('a'+i)), "common keyword content")
	}

	hits := idx.Search("common", "", 5)
	if len(hits) != 5 {
		t.Fatalf("Search with limit 5: got %d hits, want 5", len(hits))
	}
}
