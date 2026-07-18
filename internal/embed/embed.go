package embed

import "math"

// Vector is a float64 embedding vector.
type Vector []float64

// Embedder generates embedding vectors from text.
type Embedder interface {
	Embed(text string) (Vector, error)
	Model() string // returns the model identifier
}

// CosineSimilarity computes cosine similarity between two vectors.
// Returns 0 if vectors have different lengths or either is zero.
func CosineSimilarity(a, b Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// Normalize normalizes a vector to unit length in-place.
// No-op for zero vectors.
func Normalize(v Vector) {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] /= norm
	}
}
