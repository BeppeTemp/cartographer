package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

// WorkerSettingsPath is where a worker reports its configuration, at start and
// again when a beat answers settings_wanted (D22). Not the heartbeat: the config
// is constant, and a refused report must never fail the liveness beat.
const WorkerSettingsPath = "/api/worker-settings"

const (
	maxWorkerSettingsBody         = 128 << 10
	maxWorkerSettingsPerPrincipal = 16
	maxWorkerSettingsTotal        = 64
)

// workerSettings is one perimeter's report as stored, already scrubbed.
type workerSettings struct {
	Version    string
	KBs        []string
	Entries    []workerconfig.Entry
	YAML       string
	ReceivedAt time.Time
}

func (s *workerStore) putSettings(principal, perimeter string, r workerSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	if s.settings == nil {
		s.settings = map[string]map[string]workerSettings{}
	}
	if s.settings[principal] == nil {
		s.settings[principal] = map[string]workerSettings{}
	}
	s.settings[principal][perimeter] = r
	for len(s.settings[principal]) > maxWorkerSettingsPerPrincipal {
		s.evictSettingsLocked(principal)
	}
	for total := 0; ; total = 0 {
		for _, m := range s.settings {
			total += len(m)
		}
		if total <= maxWorkerSettingsTotal {
			return
		}
		s.evictSettingsLocked("")
	}
}

// evictSettingsLocked drops the oldest report of one principal, or of anyone
// when principal is "".
func (s *workerStore) evictSettingsLocked(principal string) {
	var op, oper string
	var ot time.Time
	for p, m := range s.settings {
		if principal != "" && p != principal {
			continue
		}
		for per, x := range m {
			if op == "" || x.ReceivedAt.Before(ot) {
				op, oper, ot = p, per, x.ReceivedAt
			}
		}
	}
	delete(s.settings[op], oper)
}

// pruneSettingsLocked drops the reports no live declaration of the same principal and perimeter backs.
func (s *workerStore) pruneSettingsLocked() {
	for p, m := range s.settings {
		for per := range m {
			held := false
			for _, d := range s.by[p] {
				if d.Perimeter == per {
					held = true
					break
				}
			}
			if !held {
				delete(m, per)
			}
		}
		if len(m) == 0 {
			delete(s.settings, p)
		}
	}
}

func (s *workerStore) hasSettings(principal, perimeter string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	_, ok := s.settings[principal][perimeter]
	return ok
}

// workerSettingsRow is what the Atlas reads of one perimeter.
type workerSettingsRow struct {
	Perimeter  string               `json:"perimeter"`
	Version    string               `json:"version,omitempty"`
	Replicas   int                  `json:"replicas"`
	KBs        []string             `json:"kbs"`
	Entries    []workerconfig.Entry `json:"entries"`
	YAML       string               `json:"yaml"`
	ReceivedAt time.Time            `json:"received_at"`
}

// settingsRows returns the reports a live declaration backs, with replica counts and KBs.
func (s *workerStore) settingsRows() []workerSettingsRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	rows := []workerSettingsRow{}
	for p, m := range s.settings {
		for per, r := range m {
			row := workerSettingsRow{Perimeter: per, Version: r.Version, Entries: r.Entries, YAML: r.YAML, ReceivedAt: r.ReceivedAt}
			seen := map[string]bool{}
			for _, k := range r.KBs {
				seen[k] = true
			}
			for _, d := range s.by[p] {
				if d.Perimeter == per {
					row.Replicas++
					for _, k := range d.KBs {
						seen[k] = true
					}
				}
			}
			for k := range seen {
				row.KBs = append(row.KBs, k)
			}
			sort.Strings(row.KBs)
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Perimeter != rows[j].Perimeter {
			return rows[i].Perimeter < rows[j].Perimeter
		}
		return rows[i].ReceivedAt.Before(rows[j].ReceivedAt)
	})
	return rows
}

// workerSettingsViews keeps the perimeters whose every KB the caller sees whole: the YAML lists them.
func (m *MultiKBServer) workerSettingsViews(r *http.Request) []workerSettingsRow {
	out := []workerSettingsRow{}
	for _, row := range m.workers.settingsRows() {
		all := true
		for _, k := range row.KBs {
			srv, ok := m.servers[k]
			if !ok || !WholeVisible(r.Context(), srv.kbRef, false) {
				all = false
				break
			}
		}
		if all {
			out = append(out, row)
		}
	}
	return out
}

// handleWorkerSettings stores a report; workerconfig.Scrub runs before anything is kept.
func (m *MultiKBServer) handleWorkerSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWorkerSettingsBody))
	if err != nil {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "worker settings report too large", "")
		return
	}
	var rep struct {
		Perimeter string               `json:"perimeter"`
		Worker    string               `json:"worker"`
		Version   string               `json:"version"`
		Entries   []workerconfig.Entry `json:"entries"`
		YAML      string               `json:"yaml"`
	}
	bad := func(msg, field string) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, msg, field)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		bad("body must be a JSON object", "")
		return
	}
	if !perimeterRe.MatchString(rep.Perimeter) {
		bad("perimeter must be 1-40 characters of [a-z0-9-]", "perimeter")
		return
	}
	if rep.Worker != "" && !workerIDRe.MatchString(rep.Worker) {
		bad("worker must be 1-64 characters of [a-z0-9._-]", "worker")
		return
	}
	// A report carrying the caller's own token is refused, not masked.
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")); tok != "" && bytes.Contains(data, []byte(tok)) {
			bad("the report contains the bearer token of the request", "")
			return
		}
	}
	entries, text, kbs, err := workerconfig.Scrub(rep.Entries, rep.YAML)
	if err != nil {
		bad("yaml must be one YAML document with no merge key and no alias", "yaml")
		return
	}
	p := auth.PrincipalFromContext(r.Context())
	// A worker is a writer: no KB it may write is the 404 of an unknown KB.
	var named []string
	writable := false
	for _, k := range kbs {
		k = cleanText(k, 64)
		if len(named) < maxWorkerKBs {
			named = append(named, k)
		}
		if _, mounted := m.servers[k]; mounted && p.ID != "" && p.Policy.HasKBAccess(k, true) {
			writable = true
		}
	}
	if !writable {
		writeUINotFound(w)
		return
	}
	m.workers.putSettings(p.ID, rep.Perimeter, workerSettings{
		Version: cleanText(rep.Version, 32), KBs: named, Entries: entries, YAML: text,
		ReceivedAt: m.workers.clock().UTC(),
	})
	writeUIJSON(w, http.StatusOK, map[string]bool{"received": true})
}
