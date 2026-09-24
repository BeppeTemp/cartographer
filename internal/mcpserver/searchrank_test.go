package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

type searchResponse struct {
	Mode    string      `json:"mode"`
	Results []searchHit `json:"results"`
}

func searchJSON(t *testing.T, s *Server, query string) searchResponse {
	t.Helper()
	var r searchResponse
	if err := json.Unmarshal([]byte(searchText(t, s, query)), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// D246 parity (D89: coherent modes): for a whole-word query equal to one
// concept's title, both backends rank that concept first, even when other
// concepts mention the word in their bodies more often.
func TestSearchParity_TitleRanksFirst(t *testing.T) {
	titles := []string{"Gateway", "Firewall", "Backup", "Certificati", "Attività", "Monitoraggio", "Dns", "Cluster", "Storage", "Rete"}
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		dir := filepath.Join(k.DataRoot(), "parity")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i, title := range titles {
			// Each body mentions the next two titles twice each.
			next1, next2 := titles[(i+1)%len(titles)], titles[(i+2)%len(titles)]
			body := fmt.Sprintf("Notes on %s. See %s and %s; again %s, %s.\n", strings.ToLower(title), next1, next2, next1, next2)
			content := fmt.Sprintf("---\ntype: Note\ntitle: %s\n---\n%s", title, body)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("c%d.md", i)), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for i, title := range titles {
			r := searchJSON(t, s, title)
			if len(r.Results) == 0 || r.Results[0].ID != fmt.Sprintf("parity/c%d", i) {
				t.Errorf("%s (%s): top = %+v, want parity/c%d", title, r.Mode, r.Results, i)
			}
		}
	})
}

// D246: an unaccented query finds accented prose, and the snippet shows the
// text as written.
func TestSearchSnippetKeepsAccents(t *testing.T) {
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "citta.md"),
			[]byte("---\ntype: Note\ntitle: Luoghi\n---\nLa città di Bari.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := searchJSON(t, s, "citta")
		if len(r.Results) != 1 || !strings.Contains(r.Results[0].Snippet, "città") {
			t.Fatalf("%s: results = %+v", r.Mode, r.Results)
		}
	})
}
