package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

var versionRe = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,32}$`)

const (
	maxSurveyorSettingsBody         = 128 << 10
	maxSurveyorSettingsPerPrincipal = 16
	maxSurveyorSettingsTotal        = 64
)

// surveyorSettings is one perimeter's report as stored, already scrubbed.
type surveyorSettings struct {
	Version    string
	KBs        []string
	Entries    []surveyorconfig.Entry
	ReceivedAt time.Time
}

func (s *surveyorStore) putSettings(principal, perimeter string, r surveyorSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	if s.settings == nil {
		s.settings = map[string]map[string]surveyorSettings{}
	}
	if s.settings[principal] == nil {
		s.settings[principal] = map[string]surveyorSettings{}
	}
	s.settings[principal][perimeter] = r
	for len(s.settings[principal]) > maxSurveyorSettingsPerPrincipal {
		s.evictSettingsLocked(principal)
	}
	for total := 0; ; total = 0 {
		for _, m := range s.settings {
			total += len(m)
		}
		if total <= maxSurveyorSettingsTotal {
			return
		}
		s.evictSettingsLocked("")
	}
}

// evictSettingsLocked drops the oldest report of one principal, or of anyone
// when principal is "".
func (s *surveyorStore) evictSettingsLocked(principal string) {
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
func (s *surveyorStore) pruneSettingsLocked() {
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

func (s *surveyorStore) hasSettings(principal, perimeter string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	_, ok := s.settings[principal][perimeter]
	return ok
}

// surveyorSettingsRow is what the Atlas reads of one perimeter.
type surveyorSettingsRow struct {
	Perimeter  string                 `json:"perimeter"`
	Version    string                 `json:"version,omitempty"`
	Replicas   int                    `json:"replicas"`
	KBs        []string               `json:"kbs"`
	Entries    []surveyorconfig.Entry `json:"entries"`
	ReceivedAt time.Time              `json:"received_at"`
}

// settingsRows returns the reports a live declaration backs, with replica counts and KBs.
func (s *surveyorStore) settingsRows() []surveyorSettingsRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	rows := []surveyorSettingsRow{}
	for p, m := range s.settings {
		for per, r := range m {
			row := surveyorSettingsRow{Perimeter: per, Version: r.Version, Entries: r.Entries, ReceivedAt: r.ReceivedAt}
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

// surveyorSettingsViews keeps the perimeters whose every KB the caller sees whole: its row names them.
func (m *MultiKBServer) surveyorSettingsViews(r *http.Request) []surveyorSettingsRow {
	out := []surveyorSettingsRow{}
	for _, row := range m.surveyors.settingsRows() {
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

// handleSurveyorSettings stores a report; surveyorconfig.Scrub runs before anything is kept.
func (m *MultiKBServer) handleSurveyorSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSurveyorSettingsBody))
	if err != nil {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "surveyor settings report too large", "")
		return
	}
	var rep struct {
		Perimeter string                 `json:"perimeter"`
		Surveyor  string                 `json:"surveyor"`
		Version   string                 `json:"version"`
		KBs       []string               `json:"kbs"`
		Entries   []surveyorconfig.Entry `json:"entries"`
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
	if rep.Surveyor != "" && !surveyorIDRe.MatchString(rep.Surveyor) {
		bad("surveyor must be 1-64 characters of [a-z0-9._-]", "surveyor")
		return
	}
	if len(rep.KBs) == 0 || len(rep.KBs) > maxSurveyorKBs {
		bad("kbs must list 1-32 names", "kbs")
		return
	}
	// A report carrying the caller's own token is refused, not masked.
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		// The raw body, and the decoded strings too: a JSON escape hides the token from the raw bytes.
		decoded, _ := json.Marshal(rep.Entries)
		if tok != "" && (bytes.Contains(data, []byte(tok)) || strings.Contains(rep.Version, tok) || bytes.Contains(decoded, []byte(tok))) {
			bad("the report contains the bearer token of the request", "")
			return
		}
	}
	entries := surveyorconfig.Scrub(rep.Entries)
	// A version is a short token or nothing: it is shown as text on the page.
	version := rep.Version
	if !versionRe.MatchString(version) {
		version = ""
	}
	p := auth.PrincipalFromContext(r.Context())
	// A surveyor is a writer: no KB it may write is the 404 of an unknown KB.
	// Only the KBs it may write are kept: the others are not its to name.
	var named []string
	for _, k := range rep.KBs {
		if _, mounted := m.servers[k]; mounted && p.ID != "" && p.Policy.HasKBAccess(k, true) && len(named) < maxSurveyorKBs {
			named = append(named, k)
		}
	}
	if len(named) == 0 {
		writeUINotFound(w)
		return
	}
	m.surveyors.putSettings(p.ID, rep.Perimeter, surveyorSettings{
		Version: version, KBs: named, Entries: entries,
		ReceivedAt: m.surveyors.clock().UTC(),
	})
	writeUIJSON(w, http.StatusOK, map[string]bool{"received": true})
}
