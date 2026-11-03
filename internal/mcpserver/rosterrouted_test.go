package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

func getClients(t *testing.T, h http.Handler, token string) []ClientStat {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/clients", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/clients: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Clients []ClientStat `json:"clients"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Clients
}

// The routed view records the traffic of every synchronized client; /clients
// used to read only the per-KB servers and answered {"clients":[]}.
func TestClientsRosterSeesRoutedTraffic(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-one", "kb-two"} {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	store := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: routedTestToken, Scopes: []auth.KBScope{{KB: "kb-one", Write: true}, {KB: "kb-two", Write: true}}},
		{Token: "other-tok", Scopes: []auth.KBScope{{KB: "kb-three", Write: true}}},
	})
	h := store.Middleware(multi.Handler())

	if rr := routedPost(t, h, RoutedMountPath, initializeBody); rr.Code != http.StatusOK {
		t.Fatalf("initialize: %d %s", rr.Code, rr.Body.String())
	}
	routedCall(t, h, "concept_read", map[string]any{"kb": "kb-one", "id": "manutenzione/marker"})

	rows := getClients(t, h, routedTestToken)
	if len(rows) == 0 {
		t.Fatal("/clients is empty after routed initialize and tools/call")
	}
	var total int64
	for _, r := range rows {
		if r.KB != "kb-one,kb-two" {
			t.Errorf("row kb = %q, want the view's set", r.KB)
		}
		total += r.Count
	}
	if total < 2 {
		t.Errorf("counted %d requests, want at least 2", total)
	}
	// A token that cannot read the view's KBs sees none of its rows.
	if rows := getClients(t, h, "other-tok"); len(rows) != 0 {
		t.Fatalf("a token with no grant on the view saw %+v", rows)
	}
}

// initialize advertises only what the server does: no tools.listChanged.
func TestInitializeDoesNotAdvertiseListChanged(t *testing.T) {
	t.Parallel()
	h := newRoutedTestHandler(t, "kb-one", "kb-two")
	rr := routedPost(t, h, RoutedMountPath, initializeBody)
	var resp struct {
		Result struct {
			Capabilities struct {
				Tools map[string]any `json:"tools"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %s", err, rr.Body.String())
	}
	if resp.Result.Capabilities.Tools == nil || len(resp.Result.Capabilities.Tools) != 0 {
		t.Fatalf("capabilities.tools = %v, want {} (body %s)", resp.Result.Capabilities.Tools, rr.Body.String())
	}
}

// The SDK caps an MCP POST body at 4 MiB and answers 413; this pins the limit
// docs/transport-auth.md §Protocol errors states.
func TestMCPPostOver4MiBIs413(t *testing.T) {
	t.Parallel()
	h := newRoutedTestHandler(t, "kb-one", "kb-two")
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"pad":"` + strings.Repeat("a", 5<<20) + `"}}`
	rr := routedPost(t, h, RoutedMountPath, big)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}
