package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
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

// --- diff ---

// uiDiffMaxText caps each text of the diff route (256 KiB).
const uiDiffMaxText = 256 << 10

type uiDiffRev struct {
	SHA     string `json:"sha"`
	At      string `json:"at"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// uiDiffCut cuts s to max bytes on a rune boundary.
func uiDiffCut(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max], true
}

// uiDiffMaxFiles caps the files listed by the diff route.
const uiDiffMaxFiles = 50

type uiDiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	OldPath   string `json:"old_path,omitempty"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
}

// uiDiffFootprint is the folder an expanded concept owns (its index.md), "" for
// a flat one. A map's own index.md (data/<map>/index.md) is not a concept
// folder: it must not pull in every concept of the map. Maps do not nest (a
// ConceptID under data/ has at most three segments, docs/data-plane.md), so a
// two-segment directory data/<map>/<concept> is always an expanded concept and
// the depth guard below is all that tells it from a map's own folder.
func uiDiffFootprint(mainPath string) string {
	if path.Base(mainPath) != "index.md" {
		return ""
	}
	dir := path.Dir(mainPath)
	if strings.Count(dir, "/") < 2 || !strings.HasPrefix(dir, "data/") {
		return ""
	}
	return dir + "/"
}

// uiDiffOwnerVisible reports whether the principal may see the file rel of the
// folder of concept id. rel lists (has) every path of the folder in the tree.
// A satellite Markdown file is a concept of its own: Visible decides on its id,
// as concept_read would. Any other file is an asset of the deepest satellite
// folder above it (the concept asset_read would be asked about), else of id
// itself, which the caller already authorized.
func uiDiffOwnerVisible(ctx context.Context, k *kb.KB, id, rel string, has map[string]bool) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return false
		}
	}
	if strings.EqualFold(path.Ext(rel), ".md") && !okf.IsReserved(path.Base(rel)) {
		sat := strings.TrimSuffix(rel, path.Ext(rel))
		sat = strings.TrimSuffix(sat, "/index")
		return Visible(ctx, k, id+"/"+sat)
	}
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if has[dir+"/index.md"] {
			return Visible(ctx, k, id+"/"+dir)
		}
	}
	return true
}

// uiDiffTree maps what a concept owns at sha, and the principal may see: its
// main file under "" and every other file of its folder under the path
// relative to it. Only the footprint is listed, not the whole tree. id is the
// concept's current id: visibility of a satellite is judged on it.
func uiDiffTree(ctx context.Context, k *kb.KB, id, sha, mainPath string) (map[string]gitx.SizedBlob, error) {
	dir := uiDiffFootprint(mainPath)
	paths := []string{mainPath}
	if dir != "" {
		paths = append(paths, dir)
	}
	blobs, err := gitx.TreeBlobsUnder(k.Root, sha, paths)
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, b := range blobs {
		if dir != "" && strings.HasPrefix(b.Path, dir) {
			has[strings.TrimPrefix(b.Path, dir)] = true
		}
	}
	out := map[string]gitx.SizedBlob{}
	for _, b := range blobs {
		if b.Path == mainPath {
			out[""] = b
			continue
		}
		if dir == "" || !strings.HasPrefix(b.Path, dir) {
			continue
		}
		rel := strings.TrimPrefix(b.Path, dir)
		if uiDiffOwnerVisible(ctx, k, id, rel, has) {
			out[rel] = b
		}
	}
	return out, nil
}

// uiDiffText reads one blob as text: binary (a NUL in the first 8 KiB, or not
// UTF-8) gives no text, a long one is cut on a rune boundary. Only the first
// uiDiffMaxText bytes are loaded, however large the blob is.
func uiDiffText(root string, b gitx.SizedBlob) (text string, cut, binary bool, err error) {
	raw, err := gitx.BlobPrefix(root, b.ID, uiDiffMaxText+utf8.UTFMax)
	if err != nil {
		return "", false, false, err
	}
	s := string(raw)
	head := s
	if len(head) > 8192 {
		head = head[:8192]
	}
	text, cut = uiDiffCut(s, uiDiffMaxText)
	if strings.IndexByte(head, 0) >= 0 || !utf8.ValidString(text) {
		return "", false, true, nil
	}
	return text, cut, false, nil
}

// uiDiffFiles lists the files of the concept that differ between the trees of
// base (nil: none) and head, main file first. The main file is paired with the
// main file even when the concept was renamed or expanded in the span.
func uiDiffFiles(ctx context.Context, kbr *kb.KB, id string, base, head *uiDiffRev, basePath, headPath string) ([]uiDiffFile, bool, error) {
	var bt, ht map[string]gitx.SizedBlob
	var err error
	if base != nil {
		if bt, err = uiDiffTree(ctx, kbr, id, base.SHA, basePath); err != nil {
			return nil, false, err
		}
	}
	if head != nil {
		if ht, err = uiDiffTree(ctx, kbr, id, head.SHA, headPath); err != nil {
			return nil, false, err
		}
	}
	keys := map[string]bool{}
	for k := range bt {
		keys[k] = true
	}
	for k := range ht {
		keys[k] = true
	}
	order := make([]string, 0, len(keys))
	for k := range keys {
		order = append(order, k)
	}
	sort.Slice(order, func(i, j int) bool {
		if (order[i] == "") != (order[j] == "") {
			return order[i] == ""
		}
		pi, pj := ht[order[i]].Path, ht[order[j]].Path
		if pi == "" {
			pi = bt[order[i]].Path
		}
		if pj == "" {
			pj = bt[order[j]].Path
		}
		return pi < pj
	})
	files := []uiDiffFile{}
	for _, k := range order {
		b, hasB := bt[k]
		h, hasH := ht[k]
		if hasB && hasH && b.ID == h.ID && b.Path == h.Path {
			continue
		}
		f := uiDiffFile{}
		switch {
		case hasB && hasH:
			f.Path, f.Status = h.Path, "modified"
			if b.Path != h.Path {
				f.Status, f.OldPath = "renamed", b.Path
			}
		case hasH:
			f.Path, f.Status = h.Path, "added"
		default:
			f.Path, f.Status = b.Path, "deleted"
		}
		if len(files) >= uiDiffMaxFiles {
			return files, true, nil
		}
		if hasB {
			t, cut, bin, err := uiDiffText(kbr.Root, b)
			if err != nil {
				return nil, false, err
			}
			f.Before, f.Truncated, f.Binary = t, cut, bin
		}
		if hasH {
			t, cut, bin, err := uiDiffText(kbr.Root, h)
			if err != nil {
				return nil, false, err
			}
			f.After, f.Truncated, f.Binary = t, f.Truncated || cut, f.Binary || bin
		}
		files = append(files, f)
	}
	return files, false, nil
}

// GET /kbs/{kb}/diff?id=&from=&to= — the files of a concept that changed in a
// time span, each with its text before and after, for the client to diff. A
// concept is one file or, expanded, a folder (index.md, satellites, assets).
// Revisions come from concept_history (its guard authorizes the main concept);
// the file contents are read from git at those shas, and every file of the
// folder is shown only if Visible says so for the concept that owns it (a
// satellite is a concept of its own, an asset belongs to its folder's concept).
// base = newest revision strictly before from, head = newest at or before to;
// the files are those whose blob differs between the two trees (everything is
// added when there is no base, deleted when head removed it). commits counts
// the revisions in [from, to].
func uiDiff(w http.ResponseWriter, r *http.Request, srv *Server) {
	id, ok := uiConceptID(w, r, srv.kbRef)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "from is required and must be an RFC 3339 time", "from")
		return
	}
	to := time.Now().UTC()
	if raw := q.Get("to"); raw != "" {
		if to, err = time.Parse(time.RFC3339, raw); err != nil {
			writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "to must be an RFC 3339 time", "to")
			return
		}
	}
	if to.Before(from) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "to is before from", "to")
		return
	}
	histTool, found := uiTool(srv, "concept_history")
	if !found {
		writeUINotFound(w)
		return
	}
	args, _ := json.Marshal(map[string]interface{}{"id": id, "limit": conceptHistoryMaxLimit})
	res, err := histTool.Handler(r.Context(), args)
	if err != nil {
		writeUIInternal(w, "diff: history", err)
		return
	}
	if res.IsError || len(res.Content) == 0 {
		writeUINotFound(w)
		return
	}
	var hist struct {
		Revisions []struct {
			uiDiffRev
			Path string `json:"path"`
		} `json:"revisions"`
		Truncated bool   `json:"truncated"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &hist); err != nil {
		writeUIInternal(w, "diff: decode", err)
		return
	}
	out := map[string]interface{}{
		"id": id, "from": from.UTC().Format(time.RFC3339), "to": to.UTC().Format(time.RFC3339),
		"base": nil, "head": nil, "commits": 0, "truncated": hist.Truncated, "files": []uiDiffFile{},
	}
	mainPath := "data/" + id + ".md"
	if hist.Note != "" {
		// No git: the current text is all there is.
		data, err := srv.kbRef.ReadConcept(okf.ConceptID(id))
		if err != nil {
			writeUINotFound(w)
			return
		}
		after, cut := uiDiffCut(data.Content, uiDiffMaxText)
		out["note"], out["path"], out["truncated"] = hist.Note, mainPath, cut
		out["files"] = []uiDiffFile{{Path: mainPath, Status: "added", After: after, Truncated: cut}}
		writeUIJSON(w, http.StatusOK, out)
		return
	}
	// Newest first: the first revision at or before to is the head, the first
	// strictly before from the base.
	var base, head *uiDiffRev
	basePath, headPath := "", ""
	commits := 0
	for _, rev := range hist.Revisions {
		at, err := time.Parse(time.RFC3339, rev.At)
		if err != nil {
			continue
		}
		rv := rev.uiDiffRev
		if !at.After(to) {
			if head == nil {
				head, headPath = &rv, rev.Path
			}
			if !at.Before(from) {
				commits++
			}
		}
		if at.Before(from) {
			base, basePath = &rv, rev.Path
			break
		}
	}
	out["commits"] = commits
	if base != nil {
		out["base"] = base
	}
	if head != nil {
		out["head"] = head
	}
	switch {
	case headPath != "":
		mainPath = headPath
	case basePath != "":
		mainPath = basePath
	}
	out["path"] = mainPath
	if base != nil && head != nil && base.SHA == head.SHA {
		writeUIJSON(w, http.StatusOK, out)
		return
	}
	files, cut, err := uiDiffFiles(r.Context(), srv.kbRef, id, base, head, basePath, headPath)
	if err != nil {
		writeUIInternal(w, "diff: files", err)
		return
	}
	for _, f := range files {
		cut = cut || f.Truncated
	}
	out["files"] = files
	out["truncated"] = hist.Truncated || cut
	writeUIJSON(w, http.StatusOK, out)
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

// uiRuleRow is one permission rule of the caller, narrowed to mounted KBs.
type uiRuleRow struct {
	KB       string   `json:"kb"`
	Write    bool     `json:"write"`
	Maps     []string `json:"maps,omitempty"`
	Journals []string `json:"journals,omitempty"`
	Types    []string `json:"types,omitempty"`
	// Artifacts is "r" for a rule that withholds the artifact write tools.
	Artifacts string `json:"artifacts,omitempty"`
}

// uiWhoamiBody is the body of GET /whoami (fields in key order, as the map was).
type uiWhoamiBody struct {
	Admin       bool        `json:"admin"`
	ID          string      `json:"id"`
	Permissions []uiRuleRow `json:"permissions"`
}

// GET /whoami — who is looking: the principal behind the request and its
// scopes. It is the caller's own policy, so nothing in it is anyone else's;
// the rules are narrowed to the KBs this server mounts.
func (m *MultiKBServer) uiWhoami(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	rules := []uiRuleRow{}
	for _, rule := range p.Policy.Permissions {
		if _, mounted := m.servers[rule.KB]; !mounted {
			continue
		}
		row := uiRuleRow{KB: rule.KB, Write: rule.Write, Maps: rule.Maps, Journals: rule.Journals, Types: rule.Types}
		if rule.ReadOnlyArtifacts {
			row.Artifacts = "r"
		}
		rules = append(rules, row)
	}
	writeUIJSON(w, http.StatusOK, uiWhoamiBody{Admin: p.Policy.Admin, ID: p.ID, Permissions: rules})
}
