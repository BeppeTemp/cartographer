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
	uiCodeInvalidRequest = "invalid_request"
	uiCodeNotFound       = "not_found"
	uiCodeMethodNotAllow = "method_not_allowed"
	uiCodeInternal       = "internal"
	uiNotFoundMessage    = "not found"
	uiInternalMessage    = "internal error"
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

// handleUIAPI serves the Atlas mount: every tier of the route table.
func (m *MultiKBServer) handleUIAPI(w http.ResponseWriter, r *http.Request) {
	// The Atlas is the operator's own surface: it keeps the full texts (D1101).
	r = r.WithContext(withOperator(r.Context()))
	m.serveAPI(w, r, UIAPIPrefix, tierAll)
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

// uiKBRow is one KB of GET /kbs.
type uiKBRow struct {
	Name         string                  `json:"name"`
	Status       string                  `json:"status"`
	Ready        bool                    `json:"ready"`
	Capabilities map[string]KBCapability `json:"capabilities,omitempty"`
	// Artifacts says whether this principal may open the Artifacts
	// panel: they are whole-KB resources (D238).
	Artifacts bool `json:"artifacts"`
	// Secrets says whether this principal may open the Secrets panel.
	Secrets bool `json:"secrets"`
}

type uiKBList struct {
	KBs []uiKBRow `json:"kbs"`
}

func (m *MultiKBServer) uiListKBs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, infos, _ := m.readiness()
	rows := []uiKBRow{}
	for _, info := range infos {
		srv, ok := m.servers[info.Name]
		if !ok || srv.kbRef == nil || !uiKBVisible(ctx, srv.kbRef) {
			continue
		}
		rows = append(rows, uiKBRow{
			Name:         info.Name,
			Status:       info.Status,
			Ready:        info.Status == "normal",
			Capabilities: info.Capabilities,
			Artifacts:    uiArtifactsVisible(ctx, srv.kbRef),
			Secrets:      uiSecretsVisible(ctx, srv.kbRef),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	writeUIJSON(w, http.StatusOK, uiKBList{KBs: rows})
}

type uiCollectionRow struct {
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Kind     string `json:"kind"`
	Concepts int    `json:"concepts"`
	Expanded int    `json:"expanded_concepts"`
	// Archive marks a map that lists retired pages by design (D820).
	Archive bool `json:"archive"`
}

type uiConceptCounts struct {
	ByStatus map[string]int `json:"by_status"`
	ByType   map[string]int `json:"by_type"`
	Total    int            `json:"total"`
}

type uiLintCounts struct {
	ByCheck map[string]int `json:"by_check"`
	Total   int            `json:"total"`
}

// uiOverviewBody is the body of GET /kbs/{kb}/overview.
type uiOverviewBody struct {
	Collections []uiCollectionRow `json:"collections"`
	Concepts    uiConceptCounts   `json:"concepts"`
	Lint        uiLintCounts      `json:"lint"`
}

func (m *MultiKBServer) uiOverview(w http.ResponseWriter, r *http.Request, srv *Server) {
	k := srv.kbRef
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

	archives, err := k.ListArchives()
	if err != nil {
		writeUIInternal(w, "overview: list archives", err)
		return
	}
	collections := []uiCollectionRow{}
	counts := archiveCounts(res.Entries)
	for _, name := range archives {
		kind := uiArchiveKind(k, name)
		if !VisibleCollection(ctx, k, name, kind) {
			continue
		}
		row := uiCollectionRow{Name: name, Kind: kind, Archive: isArchiveCollection(k, name, counts[name])}
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

	findings, err := uiWholeFindings(ctx, srv)
	if err != nil {
		writeUIInternal(w, "overview: lint", err)
		return
	}
	byCheck := lint.CountsByCheck(findings)

	payload := uiOverviewBody{
		Collections: collections,
		Concepts:    uiConceptCounts{Total: len(res.Entries), ByType: byType, ByStatus: byStatus},
		Lint:        uiLintCounts{Total: len(findings), ByCheck: byCheck},
	}
	writeUIJSON(w, http.StatusOK, payload)
}

func uiGraph(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	ctx := r.Context()
	query := r.URL.Query()

	limit := 0
	if raw := query.Get("limit"); raw != "" {
		if uiBadLimit(w, r) {
			return
		}
		limit, _ = strconv.Atoi(raw)
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

func uiConcept(w http.ResponseWriter, r *http.Request, k *kb.KB, srv *Server) {
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

	// One graph read for both directions (D241).
	graph, err := k.Links()
	if err != nil {
		writeUIInternal(w, "concept: graph", err)
		return
	}
	outbound, brokenOutbound := uiVisibleNeighbors(ctx, k, graph, graph.Out[okf.ConceptID(id)], id)
	inbound, _ := uiVisibleNeighbors(ctx, k, graph, graph.In[okf.ConceptID(id)], id)

	// The supersede chain as relations (D818), not only as raw frontmatter.
	// superseded_by is a frontmatter relation, not a link (D243): supersede
	// writes no body link, so the predecessors are found on the facets of the
	// caller's view, and its own successor is only named when the caller
	// may see it.
	supersedes := []string{}
	if lg, err := visibleGraph(ctx, k); err == nil {
		for i, f := range lg.Facets {
			if f.SupersededBy == id {
				supersedes = append(supersedes, string(lg.IDs[i]))
			}
		}
	}
	supersededBy := ""
	if next := k.ConceptFacets(okf.ConceptID(id)).SupersededBy; next != "" && Visible(ctx, k, next) {
		supersededBy = next
	}

	outline := []uiOutlineRow{}
	for _, h := range okf.ListHeadings(data.Body) {
		outline = append(outline, uiOutlineRow{Level: h.Level, Title: h.Title, Bytes: h.Bytes})
	}
	writeUIJSON(w, http.StatusOK, uiConceptBody{
		ID: id, Title: title, Collection: conceptCollection(id), Frontmatter: frontmatter,
		Body: data.Body, BodyBytes: len(data.Body), Outline: outline, ContentHash: data.ContentHash,
		Outbound: outbound, Inbound: inbound, Broken: brokenOutbound, UsedBy: artifactsReading(ctx, srv, id),
		Supersedes: supersedes, SupersededBy: supersededBy,
	})
}

// uiConceptBody is the body of GET /kbs/{kb}/concept. The fields are in
// alphabetical order of their JSON keys: the body used to be a map, which
// marshals sorted, and the wire bytes must not change.
type uiConceptBody struct {
	Body         string                 `json:"body"`
	BodyBytes    int                    `json:"body_bytes"`
	Broken       []string               `json:"broken"`
	Collection   string                 `json:"collection"`
	ContentHash  string                 `json:"content_hash"`
	Frontmatter  map[string]interface{} `json:"frontmatter"`
	ID           string                 `json:"id"`
	Inbound      []string               `json:"inbound"`
	Outbound     []string               `json:"outbound"`
	Outline      []uiOutlineRow         `json:"outline"`
	SupersededBy string                 `json:"superseded_by"`
	Supersedes   []string               `json:"supersedes"`
	Title        string                 `json:"title"`
	// UsedBy is null for a principal that cannot see the whole KB.
	UsedBy []uiArtifactRef `json:"used_by"`
}

// uiOutlineRow is one heading of a concept's outline.
type uiOutlineRow struct {
	Bytes int    `json:"bytes"`
	Level int    `json:"level"`
	Title string `json:"title"`
}

// uiVisibleNeighbors returns, from one direction of id's links, the
// neighbours the caller may see, sorted, plus the targets that are not
// concepts at all. The concept itself is never its own neighbour.
//
// Two filters, for two different reasons. The link graph is not
// permission-aware — it answers about the files — so a concept the caller may
// not see is dropped here. And it records a link's target whether or not that
// target exists, so a missing one is kept apart instead of being offered as a
// neighbour the UI would render as a dead chip. An existing-but-invisible
// target falls out of both lists: reporting it as broken would disclose it.
func uiVisibleNeighbors(ctx requestContext, k *kb.KB, graph kb.Links, links map[okf.ConceptID]struct{}, id string) (neighbours, broken []string) {
	neighbours, broken = []string{}, []string{}
	for target := range links {
		neighbor := string(target)
		if neighbor == id {
			continue
		}
		if _, exists := graph.Exists[target]; !exists {
			broken = append(broken, neighbor)
		} else if Visible(ctx, k, neighbor) {
			neighbours = append(neighbours, neighbor)
		}
	}
	sort.Strings(neighbours)
	sort.Strings(broken)
	return neighbours, broken
}

func uiLint(w http.ResponseWriter, r *http.Request, srv *Server) {
	if uiBadLimit(w, r) {
		return
	}
	k := srv.kbRef
	ctx := r.Context()
	scope := strings.TrimSuffix(strings.ReplaceAll(r.URL.Query().Get("scope"), "\\", "/"), "/")

	var findings []lint.Finding
	var err error
	if scope == "" {
		findings, err = uiWholeFindings(ctx, srv)
	} else {
		findings, err = uiVisibleFindings(ctx, k, scope)
	}
	if err != nil {
		writeUIInternal(w, "lint: run", err)
		return
	}
	// Every finding is a problem (D947): the response lists them all, in the
	// registry's category order, with the totals by check.
	byCheck := lint.CountsByCheck(findings)
	kept := lint.Order(findings)
	type findingRow struct {
		Path    string `json:"path"`
		Concept string `json:"concept,omitempty"`
		Check   string `json:"check"`
		Message string `json:"message"`
		Handler string `json:"handler"`
	}
	auto := uiAutoChecks()
	rows := make([]findingRow, 0, len(kept))
	for _, f := range kept {
		rows = append(rows, findingRow{
			Path:    f.Path,
			Concept: findingConcept(f),
			Check:   f.Check,
			Message: f.Message,
			Handler: findingHandler(f, auto),
		})
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"findings": rows,
		"count":    len(rows),
		"by_check": byCheck,
	})
}

// Who acts on a finding (D365): "auto" when the background repair will fix it
// by itself (it carries a fix of a check the heartbeat runs unattended), else
// "doctor", the agent session that decides it or records it as a question for
// a person.
const (
	findingHandlerAuto   = "auto"
	findingHandlerDoctor = "doctor"
)

// uiAutoChecks is the set of checks the background repair runs:
// every check whose fix is safe to run unattended (D907).
func uiAutoChecks() map[string]bool {
	out := map[string]bool{}
	for _, c := range lint.AutoRepairChecks() {
		out[c] = true
	}
	return out
}

// uiChecks lists every lint check the server runs (D365), by category, with
// whether it can be fixed mechanically and whether this KB's background repair
// fixes it by itself. It says what the KB is checked for; the counts come from
// /lint, so a caller sees the coverage of exactly the findings it may see.
// Every check is active (D371) unless it has nothing to compare against on this
// KB or every map able to run it opted out; the row then says why.
func uiChecks(w http.ResponseWriter, k *kb.KB) {
	type checkRow struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Fixable  bool   `json:"fixable"`
		Auto     bool   `json:"auto"`
		// Active is false when the check cannot run on this KB (D371): Reason
		// says why, so a zero count is not read as "clean".
		Active bool   `json:"active"`
		Reason string `json:"reason,omitempty"`
	}
	auto := uiAutoChecks()
	inactive := lint.Inactive(k)
	rows := []checkRow{}
	for _, s := range lint.Checks() {
		if s.Category == "" {
			continue // a kb_review kind, not a lint check
		}
		rows = append(rows, checkRow{
			Name: s.Name, Category: s.Category,
			Fixable: len(s.FixKinds) > 0, Auto: auto[s.Name],
			Active: inactive[s.Name] == "", Reason: inactive[s.Name],
		})
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"categories": lint.Categories, "checks": rows})
}

func findingHandler(f lint.Finding, auto map[string]bool) string {
	if f.Fix != nil && auto[f.Check] {
		return findingHandlerAuto
	}
	return findingHandlerDoctor
}

// uiVisibleFindings runs lint (or uses precomputed findings when provided) and
// drops every finding the caller may not see. A finding that does not name a
// concept — a directory-level check — is only for a caller that can see the
// whole KB: its message can describe files the caller has no access to.
// uiWholeFindings is uiVisibleFindings over the whole KB, from kb_status'
// lint cache (D294) when the server has one: the overview asks for it on
// every page load, KB switch and live refresh, and an uncached whole-KB lint
// is most of what those cost. The visibility filter stays outside the cache.
func uiWholeFindings(ctx requestContext, srv *Server) ([]lint.Finding, error) {
	if srv.conformance != nil {
		if all, err := srv.conformance.lintFindings(srv.kbRef); err == nil {
			if all == nil {
				// A clean KB: nil would mean "no precomputed findings" below.
				all = []lint.Finding{}
			}
			return uiVisibleFindingsFrom(ctx, srv.kbRef, "", all)
		}
	}
	return uiVisibleFindings(ctx, srv.kbRef, "")
}

func uiVisibleFindings(ctx requestContext, k *kb.KB, scope string) ([]lint.Finding, error) {
	return uiVisibleFindingsFrom(ctx, k, scope, nil)
}

// uiVisibleFindingsFrom is uiVisibleFindings with an optional pre-computed
// findings slice: when non-nil it skips lint.Run entirely (D294 cache path).
func uiVisibleFindingsFrom(ctx requestContext, k *kb.KB, scope string, precomputed []lint.Finding) ([]lint.Finding, error) {
	var findings []lint.Finding
	var err error
	if precomputed != nil {
		findings = precomputed
	} else {
		findings, err = lint.Run(k, scope, false)
		if err != nil {
			return nil, err
		}
	}
	whole := WholeVisible(ctx, k, false)
	out := make([]lint.Finding, 0, len(findings))
	for _, f := range findings {
		// Structural checks read the whole graph (D243): on a visible concept
		// they still count and name hidden ones.
		if !whole && lint.WholeGraphChecks[f.Check] {
			continue
		}
		concept := findingConcept(f)
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

// findingConcept is uiFindingConcept for a finding: an artifact finding
// (skills/x/SKILL.md, agents/x.md, instructions.md) names a file, never a
// concept, even when its path ends in .md (D316). Being concept-less, it is
// shown only to a caller that can see the whole KB, like the artifacts.
func findingConcept(f lint.Finding) string {
	if f.Artifact {
		return ""
	}
	return uiFindingConcept(f.Path)
}

// uiFindingConcept maps a finding's path back to the concept id it belongs to,
// honouring the expanded form ("map/concept/index.md" → "map/concept").
// Returns "" when the path is not a concept file -- including a map's own
// index.md, log.md or descriptor: "infra/index.md" is the map's curated index,
// not a concept "infra", and naming one sent the Health panel to a concept that
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
