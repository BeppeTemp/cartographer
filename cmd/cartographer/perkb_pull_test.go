package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// A client whose URL is the per-KB endpoint /mcp/kb-a pulls only kb-a, and the
// request carries no ?kb= next to the path, which the server refuses (#2172).
func TestFetchMergedManifestPerKBURLPullsOnlyItsKB(t *testing.T) {
	var pulled []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "kbs": []map[string]any{{"name": "kb-a"}, {"name": "kb-b"}}})
			return
		}
		if r.URL.Path != "/mcp/kb-a" || r.URL.Query().Has("kb") {
			http.Error(w, "conflicting kb selection", http.StatusBadRequest)
			return
		}
		pulled = append(pulled, r.URL.Path)
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": syncPullPayload(t, "kb-a", "skill-a")}},
		}})
	}))
	defer srv.Close()

	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp/kb-a", KnownKBs: []string{"kb-a", "kb-b"}}
	m, err := fetchMergedManifest(cfg)
	if err != nil {
		t.Fatalf("fetchMergedManifest: %v", err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Source != "kb:kb-a" || len(pulled) != 1 {
		t.Fatalf("artifacts %+v, pulls %v; want one kb-a pull", m.Artifacts, pulled)
	}
}
