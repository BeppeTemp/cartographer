package embed

import (
	"sort"
	"sync"
)

// SearchHit is a search result with similarity score.
type SearchHit struct {
	ID         string
	Similarity float64
}

// Store is an in-memory vector store with brute-force cosine search.
// Vectors are normalized on Add, so cosine similarity reduces to dot product.
type Store struct {
	mu      sync.RWMutex
	vecs    map[string]Vector
	modelID string
}

// NewStore creates a new vector store.
func NewStore() *Store {
	return &Store{
		vecs: make(map[string]Vector),
	}
}

// Add adds or updates a document's vector.
// The vector is normalized before storage.
func (s *Store) Add(id string, vec Vector) {
	// Copy and normalize.
	cp := make(Vector, len(vec))
	copy(cp, vec)
	Normalize(cp)

	s.mu.Lock()
	s.vecs[id] = cp
	s.mu.Unlock()
}

// Remove removes a document's vector.
func (s *Store) Remove(id string) {
	s.mu.Lock()
	delete(s.vecs, id)
	s.mu.Unlock()
}

// Search returns the top-k most similar documents to the query vector.
// Returns results sorted by descending similarity.
// Because stored vectors are normalized, cosine similarity == dot product.
func (s *Store) Search(query Vector, k int) []SearchHit {
	// Normalize query for dot-product shortcut.
	q := make(Vector, len(query))
	copy(q, query)
	Normalize(q)

	s.mu.RLock()
	hits := make([]SearchHit, 0, len(s.vecs))
	for id, vec := range s.vecs {
		var dot float64
		if len(q) == len(vec) {
			for i := range q {
				dot += q[i] * vec[i]
			}
		}
		hits = append(hits, SearchHit{ID: id, Similarity: dot})
	}
	s.mu.RUnlock()

	sort.Slice(hits, func(i, j int) bool {
		return hits[i].Similarity > hits[j].Similarity
	})
	if k > 0 && k < len(hits) {
		hits = hits[:k]
	}
	return hits
}

// Count returns the number of stored vectors.
func (s *Store) Count() int {
	s.mu.RLock()
	n := len(s.vecs)
	s.mu.RUnlock()
	return n
}

// ModelID returns the model identifier used for stored vectors, or "" if not set.
func (s *Store) ModelID() string {
	s.mu.RLock()
	m := s.modelID
	s.mu.RUnlock()
	return m
}

// SetModelID sets the model identifier.
// If it changes, the caller is responsible for clearing and rebuilding the store.
func (s *Store) SetModelID(model string) {
	s.mu.Lock()
	s.modelID = model
	s.mu.Unlock()
}
