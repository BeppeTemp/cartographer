package mcpserver

import (
	"encoding/json"
	"log"
	"net/http"
	gopath "path"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// UIAPIPrefix is the versioned read-only JSON surface the embedded web UI
// consumes. It is deliberately *not* an MCP endpoint and not a public path:
// it sits behind the same OriginGuard → TokenStore chain as /mcp, and every
// response is filtered with the caller's own principal.
//
// The version is in the path because a browser and a server are upgraded at
// different moments: a stale tab must fail on a route that no longer exists
// rather than misread a changed shape.
const UIAPIPrefix = "/api/ui/v1"

// uiError is the one error envelope every UI API failure uses. A client that
// has to pattern-match on prose cannot tell a bad request from a missing
// concept, so the machine-readable code is mandatory and the message is for a
// human reading a panel.
type uiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

const (
	uiCodeInvalidRequest  = "invalid_request"
	uiCodeNotFound        = "not_found"
	uiCodeMethodNotAllow  = "method_not_allowed"
	uiCodeInternal        = "internal"
	uiNotFoundMessage     = "not found"
	uiInternalMessage     = "internal error"
	uiDefaultLintSeverity = lint.SevInfo
)

// writeUIHeaders sets the headers shared by every UI API response, success or
// failure. no-store rather than a short max-age: these responses are derived
// from the caller's permissions, and a shared cache must never hand one
// principal's projection to another.
func writeUIHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func writeUIJSON(w http.ResponseWriter, status int, payload interface{}) {
	writeUIHeaders(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeUIError(w http.ResponseWriter, status int, code, message, field string) {
	writeUIJSON(w, status, map[string]interface{}{
		"error": uiError{Code: code, Message: message, Field: field},
	})
}

// writeUINotFound is the single answer for "no such KB", "no such concept" and
// "you may not see this one". Distinguishing them would turn the API into an
// existence oracle for a narrowed token, which is exactly what the MCP read
// path refuses to be.
func writeUINotFound(w http.ResponseWriter) {
	writeUIError(w, http.StatusNotFound, uiCodeNotFound, uiNotFoundMessage, "")
}

func writeUIInternal(w http.ResponseWriter, context string, err error) {
	log.Printf("ui api: %s: %v", context, err)
	writeUIError(w, http.StatusInternalServerError, uiCodeInternal, uiInternalMessage, "")
}

// handleUIAPI routes a /api/ui/v1 request. Read-only by construction: the
// handler registers no path that mutates anything, so a v1 UI cannot write to
// a KB even if its bearer token would allow it.
func (m *MultiKBServer) handleUIAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow,
			"the UI API is read-only: use GET or HEAD", "")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, UIAPIPrefix)
	rest = strings.Trim(rest, "/")
	segments := []string{}
	if rest != "" {
		segments = strings.Split(rest, "/")
	}

	if len(segments) == 1 && segments[0] == "kbs" {
		m.uiListKBs(w, r)
		return
	}
	if len(segments) != 3 || segments[0] != "kbs" {
		writeUINotFound(w)
		return
	}

	srv, ok := m.servers[segments[1]]
	if !ok || srv.kbRef == nil {
		writeUINotFound(w)
		return
	}
	k := srv.kbRef
	ctx := r.Context()
	if !uiKBVisible(ctx, k) {
		writeUINotFound(w)
		return
	}

	switch segments[2] {
	case "overview":
		m.uiOverview(w, r, k)
	case "graph":
		uiGraph(w, r, k)
	case "concept":
		uiConcept(w, r, k)
	case "lint":
		uiLint(w, r, k)
	default:
		writeUINotFound(w)
	}
}

// uiKBVisible reports whether the principal may see anything at all in this
// KB. A KB whose every collection is hidden is absent from the listing and
// answers 404 on every route, so its existence is not disclosed.
func uiKBVisible(ctx requestContext, k *kb.KB) bool {
	if WholeVisible(ctx, k, false) {
		return true
	}
	archives, err := k.ListArchives()
	if err != nil {
		return false
	}
	for _, name := range archives {
		if VisibleCollection(ctx, k, name, uiArchiveKind(k, name)) {
			return true
		}
	}
	return false
}

// uiArchiveKind reads a collection's declared kind, defaulting to "map" the
// same way map_list does: the kind decides whether a rule's `journals` or its
// `maps` list governs visibility.
func uiArchiveKind(k *kb.KB, name string) string {
	kind := "map"
	if meta, err := k.ReadArchiveMeta(name); err == nil {
		if v, ok := meta.Get("kind"); ok {
			if s, ok := v.(string); ok && s != "" {
				kind = s
			}
		}
	}
	return kind
}

func (m *MultiKBServer) uiListKBs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, infos, _ := m.readiness()
	type kbRow struct {
		Name         string                  `json:"name"`
		Status       string                  `json:"status"`
		Ready        bool                    `json:"ready"`
		ToolPrefix   string                  `json:"tool_prefix,omitempty"`
		Capabilities map[string]KBCapability `json:"capabilities,omitempty"`
	}
	rows := []kbRow{}
	for _, info := range infos {
		srv, ok := m.servers[info.Name]
		if !ok || srv.kbRef == nil || !uiKBVisible(ctx, srv.kbRef) {
			continue
		}
		rows = append(rows, kbRow{
			Name:         info.Name,
			Status:       info.Status,
			Ready:        info.Status == "normal",
			ToolPrefix:   info.ToolPrefix,
			Capabilities: info.Capabilities,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"kbs": rows})
}

func (m *MultiKBServer) uiOverview(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	ctx := r.Context()
	res, err := queryConcepts(k, ConceptQuery{Include: func(id string) bool { return Visible(ctx, k, id) }})
	if err != nil {
		writeUIInternal(w, "overview: walk", err)
		return
	}

	// Every count below is derived from the permission-filtered walk, never
	// from kb_status' own totals: kb_status walks the whole KB, so reusing its
	// numbers here would let a narrowed token infer how many concepts it is
	// not allowed to see.
	byType := map[string]int{}
	byStatus := map[string]int{}
	perCollection := map[string]*struct{ Concepts, Expanded int }{}
	for _, e := range res.Entries {
		if e.Type != "" {
			byType[e.Type]++
		}
		if e.Status != "" {
			byStatus[e.Status]++
		}
		if e.Collection == "" {
			continue
		}
		agg, ok := perCollection[e.Collection]
		if !ok {
			agg = &struct{ Concepts, Expanded int }{}
			perCollection[e.Collection] = agg
		}
		agg.Concepts++
		if e.Expanded {
			agg.Expanded++
		}
	}

	type collectionRow struct {
		Name     string `json:"name"`
		Title    string `json:"title,omitempty"`
		Kind     string `json:"kind"`
		Concepts int    `json:"concepts"`
		Expanded int    `json:"expanded_concepts"`
	}
	archives, err := k.ListArchives()
	if err != nil {
		writeUIInternal(w, "overview: list archives", err)
		return
	}
	collections := []collectionRow{}
	for _, name := range archives {
		kind := uiArchiveKind(k, name)
		if !VisibleCollection(ctx, k, name, kind) {
			continue
		}
		row := collectionRow{Name: name, Kind: kind}
		if agg, ok := perCollection[name]; ok {
			row.Concepts, row.Expanded = agg.Concepts, agg.Expanded
		}
		if meta, err := k.ReadArchiveMeta(name); err == nil {
			if v, ok := meta.Get("title"); ok {
				row.Title, _ = v.(string)
			}
		}
		collections = append(collections, row)
	}

	findings, err := uiVisibleFindings(ctx, k, "")
	if err != nil {
		writeUIInternal(w, "overview: lint", err)
		return
	}
	_, byCheck, bySeverity := lint.Filter(findings, uiDefaultLintSeverity)

	payload := map[string]interface{}{
		"collections": collections,
		"concepts": map[string]interface{}{
			"total":     len(res.Entries),
			"by_type":   byType,
			"by_status": byStatus,
		},
		"lint": map[string]interface{}{
			"total":       len(findings),
			"by_severity": bySeverity,
			"by_check":    byCheck,
		},
	}
	// Replication facts describe the KB as a whole, so they are for a caller
	// that can already see the whole KB.
	if WholeVisible(ctx, k, false) {
		remoteURL, hasRemote := k.RemoteInfo()
		git := map[string]interface{}{"has_remote": hasRemote}
		if hasRemote {
			git["remote_url"] = remoteURL
		}
		payload["git"] = git
	}
	writeUIJSON(w, http.StatusOK, payload)
}

func uiGraph(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	ctx := r.Context()
	query := r.URL.Query()

	limit := 0
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest,
				"limit must be an integer", "limit")
			return
		}
		limit = parsed
	}
	scope := query.Get("scope")
	if strings.ContainsAny(scope, "/\\") {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest,
			"scope is a single top-level collection name, not a path", "scope")
		return
	}

	snap, err := k.GraphSnapshot(kb.GraphSnapshotOptions{
		Scope:   scope,
		Limit:   limit,
		Include: func(id string) bool { return Visible(ctx, k, id) },
	})
	if err != nil {
		writeUIInternal(w, "graph: snapshot", err)
		return
	}
	writeUIJSON(w, http.StatusOK, snap)
}

func uiConcept(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	ctx := r.Context()
	id := r.URL.Query().Get("id")
	if id == "" {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "id is required", "id")
		return
	}
	if !Visible(ctx, k, id) {
		writeUINotFound(w)
		return
	}
	data, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		// A concept that cannot be read is indistinguishable from one that
		// does not exist, on purpose.
		writeUINotFound(w)
		return
	}

	frontmatter := map[string]interface{}{}
	var title string
	if fm, parseErr := okf.ParseFrontmatter(data.FrontmatterRaw); parseErr == nil {
		for _, key := range fm.Keys() {
			if v, ok := fm.Get(key); ok {
				frontmatter[key] = v
			}
		}
		title = frontmatterString(fm, "title")
	}

	outbound, brokenOutbound, err := uiVisibleNeighbors(ctx, k, id, "out")
	if err != nil {
		writeUIInternal(w, "concept: outbound", err)
		return
	}
	inbound, _, err := uiVisibleNeighbors(ctx, k, id, "in")
	if err != nil {
		writeUIInternal(w, "concept: inbound", err)
		return
	}

	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"id":           id,
		"title":        title,
		"collection":   conceptCollection(id),
		"frontmatter":  frontmatter,
		"body":         data.Body,
		"body_bytes":   len(data.Body),
		"outline":      headingsToOutline(okf.ListHeadings(data.Body)),
		"content_hash": data.ContentHash,
		"outbound":     outbound,
		"inbound":      inbound,
		"broken":       brokenOutbound,
	})
}

// uiVisibleNeighbors returns the 1-hop neighbours of id the caller may see,
// sorted, plus the outbound targets that are not concepts at all.
//
// Two filters, for two different reasons. GraphNeighbors is not
// permission-aware — it answers about the files — so a concept the caller may
// not see is dropped here. And it reports a link's target whether or not that
// target exists, so a missing one is kept apart instead of being offered as a
// neighbour the UI would render as a dead chip. An existing-but-invisible
// target falls out of both lists: reporting it as broken would disclose it.
func uiVisibleNeighbors(ctx requestContext, k *kb.KB, id, direction string) (neighbours, broken []string, err error) {
	found, err := k.GraphNeighbors(okf.ConceptID(id), 1, direction)
	if err != nil {
		return nil, nil, err
	}
	neighbours, broken = []string{}, []string{}
	for neighbor := range found {
		switch {
		case !conceptExists(k, neighbor):
			broken = append(broken, neighbor)
		case Visible(ctx, k, neighbor):
			neighbours = append(neighbours, neighbor)
		}
	}
	sort.Strings(neighbours)
	sort.Strings(broken)
	return neighbours, broken, nil
}

func uiLint(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	ctx := r.Context()
	severityMin := r.URL.Query().Get("severity_min")
	if severityMin == "" {
		severityMin = uiDefaultLintSeverity
	}
	if !lint.ValidSeverity(severityMin) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest,
			"severity_min must be one of "+strings.Join(lint.Severities, ", "), "severity_min")
		return
	}
	scope := strings.TrimSuffix(strings.ReplaceAll(r.URL.Query().Get("scope"), "\\", "/"), "/")

	findings, err := uiVisibleFindings(ctx, k, scope)
	if err != nil {
		writeUIInternal(w, "lint: run", err)
		return
	}
	// lint.Filter computes the counts on the *input*, so the totals describe
	// everything the caller may see, not only what cleared the floor: a panel
	// showing three errors still gets to say there are forty infos below it.
	kept, byCheck, bySeverity := lint.Filter(findings, severityMin)
	if kept == nil {
		kept = []lint.Finding{}
	}
	type findingRow struct {
		Path     string `json:"path"`
		Concept  string `json:"concept,omitempty"`
		Check    string `json:"check"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	}
	rows := make([]findingRow, 0, len(kept))
	for _, f := range kept {
		rows = append(rows, findingRow{
			Path:     f.Path,
			Concept:  uiFindingConcept(f.Path),
			Check:    f.Check,
			Severity: f.Severity,
			Message:  f.Message,
		})
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"findings":     rows,
		"count":        len(rows),
		"total":        len(findings),
		"by_severity":  bySeverity,
		"by_check":     byCheck,
		"severity_min": severityMin,
	})
}

// uiVisibleFindings runs lint and drops every finding the caller may not see.
// A finding that does not name a concept — a directory-level check — is only
// for a caller that can see the whole KB: its message can describe files the
// caller has no access to.
func uiVisibleFindings(ctx requestContext, k *kb.KB, scope string) ([]lint.Finding, error) {
	findings, err := lint.Run(k, scope, false)
	if err != nil {
		return nil, err
	}
	whole := WholeVisible(ctx, k, false)
	out := make([]lint.Finding, 0, len(findings))
	for _, f := range findings {
		concept := uiFindingConcept(f.Path)
		if concept == "" {
			if whole {
				out = append(out, f)
			}
			continue
		}
		if Visible(ctx, k, concept) {
			out = append(out, f)
		}
	}
	return out, nil
}

// uiFindingConcept maps a finding's path back to the concept id it belongs to,
// honouring the expanded form ("map/concept/index.md" → "map/concept").
// Returns "" when the path is not a concept file -- including a map's own
// index.md, log.md or descriptor: "infra/index.md" is the map's curated index,
// not a concept "infra", and naming one sent the Observatory to a concept that
// does not exist instead of saying there is no node to reveal (D228).
func uiFindingConcept(path string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	if !strings.HasSuffix(p, ".md") {
		return ""
	}
	switch gopath.Base(p) {
	case "log.md", "_map.md", "_archive.md":
		return ""
	}
	if p == "index.md" {
		return ""
	}
	if id, ok := strings.CutSuffix(p, "/index.md"); ok {
		// An expanded concept is <map>/<concept>/index.md; one segment before
		// index.md is a map, whose index is not a concept.
		if !strings.Contains(id, "/") {
			return ""
		}
		return id
	}
	return strings.TrimSuffix(p, ".md")
}
