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

// WorkerSettingsPath is where a worker reports its effective configuration,
// once at start and again only when a beat answers settings_wanted (D22). Its
// own route, not the heartbeat: the config is constant for the life of the
// process, and a report the server refuses must never fail the liveness beat.
// Inside the auth chain of /mcp, read-only in the Atlas.
const WorkerSettingsPath = "/api/worker-settings"

const (
	maxWorkerSettingsBody         = 128 << 10
	maxWorkerSettingsPerPrincipal = 16
	maxWorkerSettingsTotal        = 64
)

// workerSettings is one perimeter's report as stored: already scrubbed
// (workerconfig.Scrub), so nothing in it needs masking again.
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
		evictOldestSettings(s.settings[principal])
	}
	for {
		total := 0
		for _, m := range s.settings {
			total += len(m)
		}
		if total <= maxWorkerSettingsTotal {
			return
		}
		var op, oper string
		var ot time.Time
		for p, m := range s.settings {
			for per, x := range m {
				if op == "" || x.ReceivedAt.Before(ot) {
					op, oper, ot = p, per, x.ReceivedAt
				}
			}
		}
		delete(s.settings[op], oper)
	}
}

func evictOldestSettings(m map[string]workerSettings) {
	oldest := ""
	for per, x := range m {
		if oldest == "" || x.ReceivedAt.Before(m[oldest].ReceivedAt) {
			oldest = per
		}
	}
	delete(m, oldest)
}

// pruneSettingsLocked drops the reports that no live declaration of the same
// principal and perimeter backs.
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

// settingsRows returns the reports backed by a live declaration, each with its
// replica count and the KBs its report and its declarations name together.
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

// workerSettingsViews narrows the rows to the perimeters whose every KB the
// caller sees whole (decision 5 of #1259): the YAML lists the perimeter's KBs,
// so a partial view would disclose names the caller may not see.
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

// handleWorkerSettings stores a worker's settings report under its principal
// and perimeter. The worker rendered the YAML, but the server does not trust
// the shape: workerconfig.Scrub runs before anything is kept.
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
	// A report that carries the caller's own token is refused whole, not masked.
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
	// Like the heartbeat: a worker is a writer, and the answer for a report
	// naming no KB the caller may write is the 404 of an unknown KB.
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
