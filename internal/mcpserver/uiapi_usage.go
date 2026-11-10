package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// Usage reports (D326) are served at componentproto.UsagePath and, frozen, at the
// 0.28 alias componentproto.LegacyUsagePath. They are client-to-server metadata, not
// an agent operation, so an HTTP route and not an MCP tool, behind the same auth
// chain as /mcp and always routed, with or without the web UI.

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
func (m *MultiKBServer) handleUsage(w http.ResponseWriter, r *http.Request, versioned bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	// The body is an array, so the protocol travels in the query on the new
	// route; the 0.28 alias is protocol 0 and negotiates nothing.
	if versioned {
		peer, _ := strconv.Atoi(r.URL.Query().Get("protocol"))
		if _, ok := checkProtocol(w, peer); !ok {
			return
		}
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
	if k == nil || !ArtifactWriteVisible(r.Context(), k) {
		writeUINotFound(w)
		return
	}
	if !requireJSON(w, r) {
		return
	}
	var entries []kb.UsageEntry
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUsageBody))
	if err != nil {
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
	if errors.Is(err, kb.ErrUsageTooLarge) {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "usage store is full", "")
		return
	}
	if err != nil {
		writeUIInternal(w, "usage", err)
		return
	}
	writeUIJSON(w, http.StatusOK, map[string]int{"received": len(entries), "merged": merged})
}

// requireJSON refuses (415) a body not declared as application/json. A
// cross-site form can send text/plain or form encodings without a preflight,
// but cannot set this type, so the requirement is a second barrier behind the
// Origin check.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && strings.EqualFold(mt, "application/json") {
		return true
	}
	writeUIError(w, http.StatusUnsupportedMediaType, uiCodeInvalidRequest, "Content-Type must be application/json", "")
	return false
}
