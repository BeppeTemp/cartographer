package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// readCostServer: visible/a ↔ visible/b, hidden/h → visible/a (D301).
func readCostServer(t *testing.T) (*Server, *kb.KB, map[string]int64) {
	t.Helper()
	k, err := kb.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k.AuthName = "docs"
	for _, m := range []string{"visible", "hidden"} {
		writeKBFile(t, k, m+"/_map.md", "---\ntype: Map\ntitle: "+m+"\n---\n")
	}
	files := map[string]string{
		"visible/a": "---\ntype: Note\ntitle: A\n---\n# A\n\n[b](b.md)\n" + strings.Repeat("x", 300) + "\n",
		"visible/b": "---\ntype: Note\ntitle: B\n---\n# B\n\n[a](a.md)\n",
		"hidden/h":  "---\ntype: Note\ntitle: H\n---\n# H\n\n[a](../visible/a.md)\n" + strings.Repeat("y", 5000) + "\n",
	}
	size := map[string]int64{}
	for id, c := range files {
		writeKBFile(t, k, id+".md", c)
		size[id] = int64(len(c))
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k, size
}

func TestGraphResultsCarryBytes(t *testing.T) {
	s, _, size := readCostServer(t)
	res := callTool(t, s, "graph_neighbors", `{"id":"visible/a","direction":"both"}`)
	var nb struct {
		Neighbors []struct {
			ID    string `json:"id"`
			Bytes int64  `json:"bytes"`
		} `json:"neighbors"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &nb); err != nil || len(nb.Neighbors) != 2 {
		t.Fatalf("graph_neighbors: %v %s", err, res.Content[0].Text)
	}
	for _, n := range nb.Neighbors {
		if n.Bytes != size[n.ID] {
			t.Fatalf("%s bytes %d, file %d", n.ID, n.Bytes, size[n.ID])
		}
	}
	res = callTool(t, s, "graph_context", `{"ids":["visible/b"]}`)
	var gc struct {
		Results []struct {
			ID    string `json:"id"`
			Bytes int64  `json:"bytes"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &gc); err != nil || len(gc.Results) == 0 {
		t.Fatalf("graph_context: %v %s", err, res.Content[0].Text)
	}
	for _, r := range gc.Results {
		if r.Bytes != size[r.ID] {
			t.Fatalf("%s bytes %d, file %d", r.ID, r.Bytes, size[r.ID])
		}
	}
}

func TestKBStatusReadCost(t *testing.T) {
	s, _, size := readCostServer(t)
	var rc kb.ReadCost
	if err := json.Unmarshal(kbStatusResult(t, s)["read_cost"], &rc); err != nil {
		t.Fatal(err)
	}
	a, b, h := size["visible/a"], size["visible/b"], size["hidden/h"]
	// Sorted concept sizes b < a < h; neighbourhoods b+a, a+b+h, h+a.
	if rc.ConceptBytes != (kb.ByteStats{P50: a, P90: h, Max: h}) {
		t.Fatalf("concept_bytes %+v", rc.ConceptBytes)
	}
	if rc.NeighbourhoodBytes != (kb.ByteStats{P50: h + a, P90: a + b + h, Max: a + b + h}) {
		t.Fatalf("neighbourhood_bytes %+v", rc.NeighbourhoodBytes)
	}

	// A caller who cannot see hidden/ gets the numbers of a KB without it.
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	res, err := s.Tools()["kb_status"].Handler(ctx, json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("kb_status: %v %+v", err, res.Content)
	}
	var out struct {
		ReadCost kb.ReadCost `json:"read_cost"`
	}
	_ = json.Unmarshal([]byte(res.Content[0].Text), &out)
	if out.ReadCost.ConceptBytes.Max != a || out.ReadCost.NeighbourhoodBytes.Max != a+b {
		t.Fatalf("restricted read_cost counts a hidden concept: %+v", out.ReadCost)
	}
}

func TestAuditRecordsResultBytes(t *testing.T) {
	s, _, path := auditServer(t, audit.Options{})
	s.RegisterTool(Tool{
		Name: "sized", ReadOnly: true,
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return ToolResult{Content: []ContentBlock{{Type: "text", Text: "12345"}, {Type: "text", Text: "678"}}}, nil
		},
	})
	callTool(t, s, "sized", `{}`)
	var got int64 = -1
	for _, e := range auditEntries(t, path) {
		if e.Phase == audit.PhaseCompletion {
			got = e.ResultBytes
		}
	}
	if got != 8 {
		t.Fatalf("result_bytes = %d, want 8", got)
	}
}
