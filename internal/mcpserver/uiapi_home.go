package mcpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// The Atlas's reader routes (D286): the reader's questions — find a concept, see what
// changed, see what needs attention — answered by the read-only tools the
// agents use, so the UI can never disagree with them. Each handler runs the
// tool's own handler with the request's principal: the per-element
// visibility filtering those handlers already apply is the one that governs
// here too. Nothing here writes.

// uiRelay writes a tool result as the route's JSON body: a tool error is the
// caller's (400), anything the tool returned that is not JSON is ours (500).
func uiRelay(w http.ResponseWriter, context string, res ToolResult, err error) {
	if err != nil {
		writeUIInternal(w, context, err)
		return
	}
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	if res.IsError {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, strings.TrimSpace(text), "")
		return
	}
	var payload interface{}
	if jsonErr := json.Unmarshal([]byte(text), &payload); jsonErr != nil {
		writeUIInternal(w, context+": decode", jsonErr)
		return
	}
	writeUIJSON(w, http.StatusOK, payload)
}

// uiTool finds a tool by its canonical name, whatever prefix this server
// registers its tools under (D102).
func uiTool(srv *Server, name string) (*Tool, bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.toolPrefix != "" {
		name = srv.toolPrefix + "__" + name
	}
	t, ok := srv.tools[name]
	return t, ok
}

// uiLimit reads an optional positive limit, capped.
func uiLimit(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// GET /kbs/{kb}/search?q=&scope=&limit= — the search tool, without recording
// misses (see Server.uiSearch).
func uiSearch(w http.ResponseWriter, r *http.Request, srv *Server) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "q is required", "q")
		return
	}
	if srv.uiSearch == nil {
		writeUINotFound(w)
		return
	}
	args, _ := json.Marshal(map[string]interface{}{
		"query": q,
		"scope": r.URL.Query().Get("scope"),
		"limit": uiLimit(r, 20, 100),
	})
	res, err := srv.uiSearch(r.Context(), args)
	uiRelay(w, "search", res, err)
}

// GET /kbs/{kb}/changes?since=&limit= — changes_since: what the agents (and
// everyone else) changed, newest first, with the reasons they recorded.
func uiChanges(w http.ResponseWriter, r *http.Request, srv *Server) {
	tool, ok := uiTool(srv, "changes_since")
	if !ok {
		writeUINotFound(w)
		return
	}
	since := r.URL.Query().Get("since")
	if since == "" {
		since = "7d"
	}
	args, _ := json.Marshal(map[string]interface{}{"since": since, "limit": uiLimit(r, 50, 500)})
	res, err := tool.Handler(r.Context(), args)
	uiRelay(w, "changes", res, err)
}

// GET /kbs/{kb}/status — kb_status: open knowledge gaps and the searches that
// found nothing. It describes the whole KB (its remote, its capabilities), so
// like the artifact routes it is only for a principal that sees all of it.
func uiStatus(w http.ResponseWriter, r *http.Request, srv *Server) {
	tool, ok := uiTool(srv, "kb_status")
	if !ok || !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	res, err := tool.Handler(r.Context(), json.RawMessage(`{}`))
	uiRelay(w, "status", res, err)
}

// GET /kbs/{kb}/work?scope=&include=&stale=&where=&order_by=&fields=&limit=&offset=
// — work_list (D302) for the request's principal: open-phase concepts and
// unchecked items, filtered by what it can see. where repeats (one predicate
// per parameter); order_by and fields repeat or take a comma list.
func uiWork(w http.ResponseWriter, r *http.Request, srv *Server) {
	tool, ok := uiTool(srv, "work_list")
	if !ok {
		writeUINotFound(w)
		return
	}
	q := r.URL.Query()
	list := func(key string) []string {
		var out []string
		for _, v := range q[key] {
			for _, part := range strings.Split(v, ",") {
				if part = strings.TrimSpace(part); part != "" {
					out = append(out, part)
				}
			}
		}
		return out
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	args, _ := json.Marshal(map[string]interface{}{
		"scope":    q.Get("scope"),
		"include":  q.Get("include"),
		"stale":    q.Get("stale") == "true" || q.Get("stale") == "1",
		"where":    q["where"],
		"order_by": list("order_by"),
		"fields":   list("fields"),
		"limit":    uiLimit(r, workListDefaultLimit, workListMaxLimit),
		"offset":   offset,
	})
	res, err := tool.Handler(r.Context(), args)
	uiRelay(w, "work", res, err)
}
