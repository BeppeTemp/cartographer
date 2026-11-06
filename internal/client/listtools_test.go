package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
)

func toolsServer(t *testing.T, page func(cursor string, n int) map[string]any) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method != "tools/list" {
			t.Errorf("method %q", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": page(req.Params.Cursor, int(n.Add(1)))})
	}))
}

func TestListToolsFollowsCursorAndReadsAnnotation(t *testing.T) {
	var cursors []string
	srv := toolsServer(t, func(cursor string, _ int) map[string]any {
		cursors = append(cursors, cursor)
		if cursor == "c2" {
			return map[string]any{"tools": []map[string]any{{"name": "bare"}}}
		}
		return map[string]any{"tools": []map[string]any{
			{"name": "page_read", "annotations": map[string]any{"readOnlyHint": true}},
			{"name": "page_write", "annotations": map[string]any{"readOnlyHint": false}},
		}, "nextCursor": "c2"}
	})
	defer srv.Close()
	got, err := client.New(srv.URL, "").ListTools()
	if err != nil {
		t.Fatal(err)
	}
	want := []client.ToolInfo{{Name: "page_read", ReadOnly: true}, {Name: "page_write"}, {Name: "bare"}}
	if len(got) != len(want) {
		t.Fatalf("tools %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool %d = %v, want %v", i, got[i], want[i])
		}
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "c2" {
		t.Errorf("cursors %q", cursors)
	}
}

// A server that pages forever fails the call: half a list would classify the
// missing tools as writes, but a caller must not run on a partial answer.
func TestListToolsPageCapFailsClosed(t *testing.T) {
	srv := toolsServer(t, func(_ string, n int) map[string]any {
		return map[string]any{"tools": []map[string]any{{"name": "t"}}, "nextCursor": "c" + strings.Repeat("x", n)}
	})
	defer srv.Close()
	got, err := client.New(srv.URL, "").ListTools()
	if err == nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestListToolsContextCancelled(t *testing.T) {
	srv := toolsServer(t, func(string, int) map[string]any { return map[string]any{} })
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.New(srv.URL, "").ListToolsContext(ctx); err == nil {
		t.Fatal("a cancelled context still listed")
	}
	if _, err := client.New(srv.URL, "").CallContext(ctx, "t", map[string]any{}); err == nil {
		t.Fatal("a cancelled context still called")
	}
}
