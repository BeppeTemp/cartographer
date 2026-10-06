package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// UsagePath is where a client reports which skills and agents it loaded
// (D326). It is client-to-server metadata, not an agent operation, so it is
// an HTTP route and not an MCP tool; it sits behind the same auth chain as
// /mcp. It is always routed, with or without the web UI.
const UsagePath = "/api/usage"

// maxUsageBody bounds a report: a few hundred rows of a few dozen bytes. A
// client with more artifacts than that has bigger problems.
const maxUsageBody = 1 << 20

// usageMaxCount caps a reported count. The client computes it from its own
// transcripts, so it is trusted for what it is — a hint — but never allowed
// to overflow what the readers do with it.
const usageMaxCount = 1 << 30

// handleUsage merges a client's usage report into the KB's local store. The
// merge is last-writer-wins per (artifact, provider) on the newer LastUsed,
// and Count is replaced rather than summed: the client already aggregated its
// whole window, so adding would count the same session on every sync.
//
// Writing needs the whole KB in write scope, the same as the artifact tools:
// usage is about artifacts, which are whole-KB resources. A caller without it
// gets the 404 of an unknown KB, so the route is no existence oracle.
func (m *MultiKBServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	name := r.URL.Query().Get("kb")
	var k *kb.KB
	if name == "" && len(m.servers) == 1 {
		for _, srv := range m.servers {
			k = srv.kbRef
		}
	} else if srv, ok := m.servers[name]; ok {
		k = srv.kbRef
	}
	if k == nil || !WholeVisible(r.Context(), k, true) {
		writeUINotFound(w)
		return
	}
	var entries []kb.UsageEntry
	body := io.LimitReader(r.Body, maxUsageBody+1)
	data, err := io.ReadAll(body)
	if err != nil || len(data) > maxUsageBody {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "usage report too large", "")
		return
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a JSON array of usage entries", "")
		return
	}
	now := time.Now()
	clean := entries[:0]
	for _, e := range entries {
		if e.Kind != "skill" && e.Kind != "agent" {
			continue
		}
		// A clock a day ahead is a clock, not a future use; one further out
		// would pin the artifact "recent" forever.
		if e.LastUsed.After(now.Add(24 * time.Hour)) {
			e.LastUsed = now
		}
		if e.Count < 0 || e.Count > usageMaxCount {
			e.Count = 0
		}
		clean = append(clean, e)
	}
	merged, err := k.MergeUsage(clean)
	if err != nil {
		writeUIInternal(w, "usage", err)
		return
	}
	writeUIJSON(w, http.StatusOK, map[string]int{"received": len(entries), "merged": merged})
}
