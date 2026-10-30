package mcpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The Atlas's reference routes: data the MCP tools already compute that a
// panel wants as a page of its own — the placeholder vocabulary, open
// contradictions, map contracts, a concept's history, the journal, the source
// ledger and a concept's assets. Every handler reuses the tool's own
// computation and visibility filtering, so the UI cannot disagree with the
// agents; nothing here locks, commits or writes.

const (
	uiJournalDefault = 20
	uiJournalMax     = 200
)

// uiToolJSON runs a registered tool and decodes its text result as JSON. ok is
// false when a response has already been written (tool missing, tool error,
// non-JSON result). A tool error is the caller's (400), as in uiRelay.
func uiToolJSON(w http.ResponseWriter, r *http.Request, srv *Server, name string, args map[string]interface{}) (payload interface{}, ok bool) {
	tool, found := uiTool(srv, name)
	if !found {
		writeUINotFound(w)
		return nil, false
	}
	raw, _ := json.Marshal(args)
	res, err := tool.Handler(r.Context(), raw)
	if err != nil {
		writeUIInternal(w, name, err)
		return nil, false
	}
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	if res.IsError {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, strings.TrimSpace(text), "")
		return nil, false
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		writeUIInternal(w, name+": decode", err)
		return nil, false
	}
	return payload, true
}

// uiConceptID reads the required ?id= and applies the uiConcept contract: a
// missing parameter is a 400, an id the principal may not see is the same 404
// as an id that does not exist.
func uiConceptID(w http.ResponseWriter, r *http.Request, k *kb.KB) (string, bool) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "id is required", "id")
		return "", false
	}
	if !Visible(r.Context(), k, id) {
		writeUINotFound(w)
		return "", false
	}
	return id, true
}

// --- placeholders ---

// uiPlaceholderConflict is another KB on this server declaring the same key
// with a different default.
type uiPlaceholderConflict struct {
	KB      string `json:"kb"`
	Default string `json:"default"`
}

type uiPlaceholderKey struct {
	Kind        string `json:"kind"`
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
	Remote      string `json:"remote,omitempty"`
	// Declared: the KB's paths.yaml declares the key. Always false for a
	// caller that cannot see the whole KB (the registry is withheld).
	Declared  bool                    `json:"declared"`
	Concepts  []string                `json:"concepts"`
	Artifacts []string                `json:"artifacts"`
	Conflicts []uiPlaceholderConflict `json:"conflicts,omitempty"`
}

// GET /kbs/{kb}/placeholders — the {{repo:…}}/{{path:…}} vocabulary (D263):
// the declared keys of paths.yaml, who cites each one, and the keys cited but
// never declared. paths.yaml and the artifacts are whole-KB resources (the
// sync_pull class), so a narrowed principal gets only the keys its visible
// concepts cite, with no declaration, artifact or conflict.
func (m *MultiKBServer) uiPlaceholders(w http.ResponseWriter, r *http.Request, srv *Server) {
	ctx, k := r.Context(), srv.kbRef
	whole := WholeVisible(ctx, k, false)

	var state kb.PathRegistryState
	if whole {
		var err error
		if state, err = k.ReadPathRegistry(); err != nil {
			writeUIInternal(w, "placeholders: registry", err)
			return
		}
	}
	lg, err := visibleGraph(ctx, k)
	if err != nil {
		writeUIInternal(w, "placeholders: graph", err)
		return
	}

	keys := map[string]*uiPlaceholderKey{}
	entry := func(id string) *uiPlaceholderKey {
		if e, ok := keys[id]; ok {
			return e
		}
		kind, key, _ := strings.Cut(id, ":")
		e := &uiPlaceholderKey{Kind: kind, Key: key, Concepts: []string{}, Artifacts: []string{}}
		keys[id] = e
		return e
	}
	for _, id := range state.Registry.IDs() {
		e := entry(id)
		d, _ := state.Registry.Declared(e.Kind, e.Key)
		e.Declared, e.Description, e.Default, e.Remote = true, d.Description, d.Default, d.Remote
	}
	for i, f := range lg.Facets {
		for _, id := range f.Placeholders {
			e := entry(id)
			e.Concepts = append(e.Concepts, string(lg.IDs[i]))
		}
	}
	if whole {
		catalog, err := listKBArtifacts(k, srv.kbArtifacts.signer)
		if err != nil {
			writeUIInternal(w, "placeholders: artifacts", err)
			return
		}
		for _, a := range catalog.Artifacts {
			seen := map[string]bool{}
			for _, f := range a.Files {
				for _, id := range okf.Placeholders(string(f.Content)) {
					if !seen[id] {
						seen[id] = true
						e := entry(id)
						e.Artifacts = append(e.Artifacts, a.Kind+"/"+a.Name)
					}
				}
			}
		}
		m.attachPlaceholderConflicts(r, srv, state.Registry, keys)
	}

	out := make([]*uiPlaceholderKey, 0, len(keys))
	for _, e := range keys {
		sort.Strings(e.Concepts)
		sort.Strings(e.Artifacts)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"registry": state.Present, "keys": out})
}

// attachPlaceholderConflicts records, for every key this KB declares, the
// other mounted KBs the principal sees whole that declare it with another
// default. A KB the principal cannot see whole never takes part: its registry
// would disclose what it declares.
func (m *MultiKBServer) attachPlaceholderConflicts(r *http.Request, self *Server, reg kb.PathRegistry, keys map[string]*uiPlaceholderKey) {
	ctx := r.Context()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		other := m.servers[name]
		if other == self || other.kbRef == nil || !WholeVisible(ctx, other.kbRef, false) {
			continue
		}
		st, err := other.kbRef.ReadPathRegistry()
		if err != nil || !st.Present {
			continue
		}
		for _, id := range reg.IDs() {
			e := keys[id]
			if theirs, ok := st.Registry.Declared(e.Kind, e.Key); ok && theirs.Default != e.Default {
				e.Conflicts = append(e.Conflicts, uiPlaceholderConflict{KB: name, Default: theirs.Default})
			}
		}
	}
}

// --- contradictions ---

// GET /kbs/{kb}/contradictions?status= — the Contradiction concepts that are
// not knowledge gaps, as contradiction_report lists them. status defaults to
// open; "all" lists every resolution status.
func uiContradictions(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "":
		status = "open"
	case "all":
		status = "*"
	}
	list, err := listContradictions(r.Context(), k, "", status, "contradiction")
	if err != nil {
		writeUIInternal(w, "contradictions", err)
		return
	}
	if list == nil {
		list = []contradictionEntry{}
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"contradictions": list})
}

// --- maps ---

// GET /kbs/{kb}/maps — map_list wrapped as {maps}: kind, ontology mode, types,
// accepted lint checks and field-value contracts of every Map and Journal the
// principal sees. A KB without any answers an empty list, not map_list's prose.
func uiMaps(w http.ResponseWriter, r *http.Request, srv *Server) {
	tool, ok := uiTool(srv, "map_list")
	if !ok {
		writeUINotFound(w)
		return
	}
	res, err := tool.Handler(r.Context(), json.RawMessage(`{}`))
	if err != nil {
		writeUIInternal(w, "maps", err)
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
	maps := []interface{}{}
	if strings.HasPrefix(strings.TrimSpace(text), "[") {
		if err := json.Unmarshal([]byte(text), &maps); err != nil {
			writeUIInternal(w, "maps: decode", err)
			return
		}
	}
	if maps == nil {
		maps = []interface{}{}
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"maps": maps})
}

// --- history ---

// GET /kbs/{kb}/history?id=&limit= — concept_history for a concept the
// principal sees. The tool does not check visibility itself, so this does.
func uiHistory(w http.ResponseWriter, r *http.Request, srv *Server) {
	if uiBadLimit(w, r) {
		return
	}
	id, ok := uiConceptID(w, r, srv.kbRef)
	if !ok {
		return
	}
	// The tool answers a missing concept with an empty history when the KB has
	// no git; the route answers the same 404 as uiConcept.
	if _, err := srv.kbRef.ReadConcept(okf.ConceptID(id)); err != nil {
		writeUINotFound(w)
		return
	}
	tool, found := uiTool(srv, "concept_history")
	if !found {
		writeUINotFound(w)
		return
	}
	args, _ := json.Marshal(map[string]interface{}{
		"id": id, "limit": uiLimit(r, conceptHistoryDefaultLimit, conceptHistoryMaxLimit),
	})
	res, err := tool.Handler(r.Context(), args)
	if err != nil {
		writeUIInternal(w, "history", err)
		return
	}
	if res.IsError {
		// A concept that has no history path is indistinguishable from one
		// that does not exist, as in uiConcept.
		writeUINotFound(w)
		return
	}
	uiRelay(w, "history", res, nil)
}

// --- journal ---

// GET /kbs/{kb}/journal?n= — the last n entries (default 20, at most 200) of
// the KB's root log.md as markdown: {text}. The log is a whole-KB resource.
func uiJournal(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	if !WholeVisible(r.Context(), k, false) {
		writeUINotFound(w)
		return
	}
	text, err := k.LogTail("", uiN(r, uiJournalDefault, uiJournalMax))
	if err != nil && !errors.Is(err, okf.ErrNotFound) {
		writeUIInternal(w, "journal", err)
		return
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"text": text})
}

// --- sources ---

// GET /kbs/{kb}/sources?status=&limit= — source_list wrapped as {sources}.
// status is pending (default), ingested, skipped or *.
func uiSources(w http.ResponseWriter, r *http.Request, srv *Server) {
	if uiBadLimit(w, r) {
		return
	}
	args := map[string]interface{}{"limit": uiLimit(r, defaultSourceLimit, maxSourceLimit)}
	if s := r.URL.Query().Get("status"); s != "" {
		args["status"] = s
	}
	payload, ok := uiToolJSON(w, r, srv, "source_list", args)
	if !ok {
		return
	}
	if payload == nil {
		payload = []interface{}{}
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"sources": payload})
}

// --- assets ---

// GET /kbs/{kb}/assets?id= — asset_list for a concept the principal sees:
// {id, assets}. A concept that is not expanded has no folder to list: 400,
// with the tool's message.
func uiAssets(w http.ResponseWriter, r *http.Request, srv *Server) {
	id, ok := uiConceptID(w, r, srv.kbRef)
	if !ok {
		return
	}
	if _, err := srv.kbRef.ReadConcept(okf.ConceptID(id)); err != nil {
		writeUINotFound(w)
		return
	}
	payload, ok := uiToolJSON(w, r, srv, "asset_list", map[string]interface{}{"concept_id": id})
	if !ok {
		return
	}
	if payload == nil {
		payload = []interface{}{}
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"id": id, "assets": payload})
}

// uiN reads the optional positive ?n=, capped.
func uiN(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("n"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

// --- services, conflicts, audit, whoami (D818) ---

// uiAuditMaxLimit bounds the audit entries one request returns.
const uiAuditMaxLimit = 500

// GET /kbs/{kb}/services — the visible concepts of type Service. Not a relay
// of service_list: that tool lists every Service of the KB, and here each one
// is filtered with the caller's principal like every other concept.
func uiServices(w http.ResponseWriter, r *http.Request, k *kb.KB) {
	ctx := r.Context()
	type serviceRow struct {
		ID     string `json:"id"`
		Title  string `json:"title,omitempty"`
		Status string `json:"status,omitempty"`
	}
	rows := []serviceRow{}
	// The cached graph of the caller's view already carries type, title and
	// status: no concept is read again.
	lg, err := visibleGraph(ctx, k)
	if err != nil {
		writeUIInternal(w, "services: graph", err)
		return
	}
	for i, f := range lg.Facets {
		if kb.IsServiceType(f.Type) {
			rows = append(rows, serviceRow{ID: string(lg.IDs[i]), Title: f.Title, Status: f.Status})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"services": rows})
}

// GET /kbs/{kb}/conflicts — conflicts_list: open git rebase conflicts. The
// registry is KB-wide and names files, so it is for a whole-KB caller.
func uiConflicts(w http.ResponseWriter, r *http.Request, srv *Server) {
	tool, ok := uiTool(srv, "conflicts_list")
	if !ok || !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	res, err := tool.Handler(r.Context(), json.RawMessage(`{}`))
	if err == nil && !res.IsError && len(res.Content) > 0 && strings.TrimSpace(res.Content[0].Text) == "No open conflicts." {
		writeUIJSON(w, http.StatusOK, []interface{}{})
		return
	}
	uiRelay(w, "conflicts", res, err)
}

// GET /kbs/{kb}/audit?limit= — the newest audit entries for this KB, newest
// first. The log names every principal that called a tool, so it is for an
// admin only; a KB without an audit sink answers enabled: false.
func uiAudit(w http.ResponseWriter, r *http.Request, name string, srv *Server) {
	if uiBadLimit(w, r) {
		return
	}
	if !auth.PrincipalFromContext(r.Context()).Policy.Admin {
		writeUINotFound(w)
		return
	}
	srv.mu.Lock()
	l := srv.auditLog
	srv.mu.Unlock()
	if l == nil {
		writeUIJSON(w, http.StatusOK, map[string]interface{}{"enabled": false, "entries": []interface{}{}})
		return
	}
	limit := uiLimit(r, 100, uiAuditMaxLimit)
	// The sink can be shared by every KB: the filter goes into the backward
	// read, so a quiet KB still gets a full page and nothing past it is parsed.
	all, err := l.TailFunc(limit, func(e audit.Entry) bool { return e.KB == "" || e.KB == name })
	if err != nil {
		writeUIInternal(w, "audit: tail", err)
		return
	}
	type auditRow struct {
		At         string            `json:"at"`
		Phase      string            `json:"phase,omitempty"`
		Principal  string            `json:"principal,omitempty"`
		Transport  string            `json:"transport,omitempty"`
		Tool       string            `json:"tool"`
		ReadOnly   bool              `json:"read_only,omitempty"`
		Resources  map[string]string `json:"resources,omitempty"`
		Outcome    string            `json:"outcome"`
		DurationMs int64             `json:"duration_ms,omitempty"`
		CommitSHA  string            `json:"commit_sha,omitempty"`
	}
	rows := []auditRow{}
	for _, e := range all {
		rows = append(rows, auditRow{
			At: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), Phase: e.Phase, Principal: e.PrincipalID,
			Transport: e.Transport, Tool: e.Tool, ReadOnly: e.ReadOnly, Resources: e.Resources,
			Outcome: e.Outcome, DurationMs: e.DurationMs, CommitSHA: e.CommitSHA,
		})
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"enabled": true, "state": l.State(), "entries": rows})
}

// GET /whoami — who is looking: the principal behind the request and its
// scopes. It is the caller's own policy, so nothing in it is anyone else's;
// the rules are narrowed to the KBs this server mounts.
func (m *MultiKBServer) uiWhoami(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	type ruleRow struct {
		KB       string   `json:"kb"`
		Write    bool     `json:"write"`
		Maps     []string `json:"maps,omitempty"`
		Journals []string `json:"journals,omitempty"`
		Types    []string `json:"types,omitempty"`
		// Artifacts is "r" for a rule that withholds the artifact write tools.
		Artifacts string `json:"artifacts,omitempty"`
	}
	rules := []ruleRow{}
	for _, rule := range p.Policy.Permissions {
		if _, mounted := m.servers[rule.KB]; !mounted {
			continue
		}
		row := ruleRow{KB: rule.KB, Write: rule.Write, Maps: rule.Maps, Journals: rule.Journals, Types: rule.Types}
		if rule.ReadOnlyArtifacts {
			row.Artifacts = "r"
		}
		rules = append(rules, row)
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"id":          p.ID,
		"admin":       p.Policy.Admin,
		"permissions": rules,
	})
}
