package embed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OllamaEmbedder calls the Ollama HTTP API for embeddings.
type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllama creates an Ollama embedder.
// baseURL is the Ollama API URL (e.g. "http://localhost:11434").
// model is the embedding model name (e.g. "nomic-embed-text").
func NewOllama(baseURL, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed sends a request to POST /api/embed and returns the vector.
func (o *OllamaEmbedder) Embed(text string) (Vector, error) {
	reqBody, err := json.Marshal(map[string]string{
		"model": o.model,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	resp, err := o.client.Post(o.baseURL+"/api/embed", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("embed: http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(result.Embeddings) == 0 || len(result.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embed: empty embeddings in response")
	}
	return Vector(result.Embeddings[0]), nil
}

// Model returns the model identifier.
func (o *OllamaEmbedder) Model() string {
	return o.model
}
