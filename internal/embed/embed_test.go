package embed

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// -- CosineSimilarity --

func TestCosineSimilarity(t *testing.T) {
	t.Run("identical vectors", func(t *testing.T) {
		v := Vector{1, 0, 0}
		got := CosineSimilarity(v, v)
		if math.Abs(got-1.0) > 1e-9 {
			t.Fatalf("want 1.0, got %f", got)
		}
	})
	t.Run("orthogonal vectors", func(t *testing.T) {
		a := Vector{1, 0}
		b := Vector{0, 1}
		got := CosineSimilarity(a, b)
		if math.Abs(got) > 1e-9 {
			t.Fatalf("want 0.0, got %f", got)
		}
	})
	t.Run("opposite vectors", func(t *testing.T) {
		a := Vector{1, 0}
		b := Vector{-1, 0}
		got := CosineSimilarity(a, b)
		if math.Abs(got-(-1.0)) > 1e-9 {
			t.Fatalf("want -1.0, got %f", got)
		}
	})
	t.Run("different lengths", func(t *testing.T) {
		a := Vector{1, 0}
		b := Vector{1, 0, 0}
		got := CosineSimilarity(a, b)
		if got != 0 {
			t.Fatalf("want 0, got %f", got)
		}
	})
}

// -- Normalize --

func TestNormalize(t *testing.T) {
	v := Vector{3, 4}
	Normalize(v)
	norm := math.Sqrt(v[0]*v[0] + v[1]*v[1])
	if math.Abs(norm-1.0) > 1e-9 {
		t.Fatalf("expected unit norm, got %f", norm)
	}
}

func TestNormalizeZero(t *testing.T) {
	v := Vector{0, 0, 0}
	Normalize(v) // must not panic, must remain zero
	for _, x := range v {
		if x != 0 {
			t.Fatal("zero vector should remain zero")
		}
	}
}

// -- Store --

func TestStoreAddSearchRemove(t *testing.T) {
	s := NewStore()

	// Three vectors: a≈[1,0], b≈[0,1], c≈[0.6,0.8] (already unit)
	a := Vector{1, 0}
	b := Vector{0, 1}
	c := Vector{0.6, 0.8}

	s.Add("a", a)
	s.Add("b", b)
	s.Add("c", c)

	if s.Count() != 3 {
		t.Fatalf("want 3, got %d", s.Count())
	}

	// Query close to c — should rank c first.
	hits := s.Search(Vector{0.6, 0.8}, 3)
	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d", len(hits))
	}
	if hits[0].ID != "c" {
		t.Fatalf("want c first, got %s", hits[0].ID)
	}
	// Similarity of top hit should be ~1.0
	if math.Abs(hits[0].Similarity-1.0) > 1e-9 {
		t.Fatalf("want similarity ~1.0, got %f", hits[0].Similarity)
	}

	// Remove b and search again — result must not contain b.
	s.Remove("b")
	if s.Count() != 2 {
		t.Fatalf("want 2 after remove, got %d", s.Count())
	}
	hits2 := s.Search(Vector{0, 1}, 2)
	for _, h := range hits2 {
		if h.ID == "b" {
			t.Fatal("b should have been removed")
		}
	}
}

func TestStoreEmpty(t *testing.T) {
	s := NewStore()
	hits := s.Search(Vector{1, 0}, 5)
	if len(hits) != 0 {
		t.Fatalf("want 0 hits on empty store, got %d", len(hits))
	}
}

// -- OllamaEmbedder --

func TestOllamaEmbed(t *testing.T) {
	want := []float64{0.1, 0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"embeddings": [][]float64{want},
		})
	}))
	defer srv.Close()

	emb := NewOllama(srv.URL, "test-model")
	if emb.Model() != "test-model" {
		t.Fatalf("unexpected model: %s", emb.Model())
	}

	vec, err := emb.Embed("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != len(want) {
		t.Fatalf("want len %d, got %d", len(want), len(vec))
	}
	for i, v := range vec {
		if math.Abs(v-want[i]) > 1e-9 {
			t.Fatalf("vec[%d]: want %f, got %f", i, want[i], v)
		}
	}
}

func TestOllamaEmbedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	emb := NewOllama(srv.URL, "test-model")
	_, err := emb.Embed("test")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

// -- ModelID --

func TestModelIDGuard(t *testing.T) {
	s := NewStore()
	if s.ModelID() != "" {
		t.Fatal("want empty model ID initially")
	}

	s.SetModelID("model-v1")
	if s.ModelID() != "model-v1" {
		t.Fatalf("want model-v1, got %s", s.ModelID())
	}

	// Change model ID — store tracks the change, does NOT auto-clear.
	s.Add("doc1", Vector{1, 0})
	s.SetModelID("model-v2")
	if s.ModelID() != "model-v2" {
		t.Fatalf("want model-v2, got %s", s.ModelID())
	}
	// Caller decides to clear — count still reflects old data until caller acts.
	if s.Count() != 1 {
		t.Fatalf("store should NOT auto-clear on model change, want 1 got %d", s.Count())
	}
}
