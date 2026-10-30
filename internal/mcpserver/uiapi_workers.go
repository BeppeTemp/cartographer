package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// WorkersPath is where a `cartographer worker` process declares itself, once
// a minute. Client-to-server metadata like UsagePath and ClientSettingsPath: an
// HTTP route inside the auth chain of /mcp, not an MCP tool (a tool would
// spend tools/list budget on every client for something no agent calls, D285).
//
// The declaration is a hint, never trusted beyond display: validated, bounded
// in time and ignored when stale (D369). The server never acts on it; the
// budget is enforced by the worker.
const WorkersPath = "/api/workers"

const (
	maxWorkerBody          = 64 << 10
	maxWorkerKBs           = 32
	maxWorkerBudgets       = 8
	maxWorkersPerPrincipal = 16
	maxWorkersTotal        = 256
	minWorkerInterval      = 15
	maxWorkerInterval      = 3600
	defaultWorkerInterval  = 60
	// staleIntervals is how many missed beats make a declaration stale.
	staleIntervals = 3
	// maxNextWindowAhead bounds next_window_at: a weekly window is further
	// than a day away, so it is omitted rather than clamped (past the bound).
	maxNextWindowAhead = 8 * 24 * time.Hour
	// workerMaxCount caps a reported counter (usageMaxCount's reasoning).
	workerMaxCount = int64(1) << 40
)

var (
	workerIDRe    = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
	perimeterRe   = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	profileRe     = regexp.MustCompile(`^[a-z0-9._-]{1,40}$`)
	workerStates  = map[string]bool{"idle": true, "running": true, "waiting": true, "budget_exhausted": true, "stopped": true}
	workerTierSet = map[string]bool{"": true, "small": true, "large": true}
)

// workerBudget is one model profile's spend against its daily limit (design D21).
type workerBudget struct {
	Profile string `json:"profile"`
	Tier    string `json:"tier,omitempty"`
	Spent   int64  `json:"spent"`
	Limit   int64  `json:"limit"`
}

// workerLastRun is the outcome of the worker's latest run.
type workerLastRun struct {
	At          time.Time `json:"at"`
	UnitsDone   int64     `json:"units_done"`
	TokensSpent int64     `json:"tokens_spent"`
	Commits     int64     `json:"commits"`
}

// workerDecl is one accepted heartbeat. KBs holds only the KBs the principal
// may write and this server mounts.
type workerDecl struct {
	Principal       string
	Worker          string
	Perimeter       string
	Version         string
	KBs             []string
	State           string
	IntervalSeconds int
	LastRun         *workerLastRun
	Budgets         []workerBudget
	NextWindowAt    *time.Time
	ReceivedAt      time.Time
}

func (d workerDecl) live(now time.Time) bool {
	return now.Sub(d.ReceivedAt) < time.Duration(staleIntervals*d.IntervalSeconds)*time.Second
}

// workerStore holds the declarations in memory, per principal and worker id.
// A restart forgets them until the next beat, which costs a minute of an empty
// panel and no stale file to expire (the pattern of clientReports, D818).
type workerStore struct {
	mu  sync.Mutex
	now func() time.Time // nil = time.Now; tests inject
	by  map[string]map[string]workerDecl
	// settings are the reports of POST /api/worker-settings, per principal and
	// perimeter. A report lives only while a live declaration of the same
	// principal and perimeter does (pruneSettingsLocked): it needs no clock.
	settings map[string]map[string]workerSettings
}

func (s *workerStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// sweepLocked drops stale declarations: there is no timer, expiry happens on
// the next put or read.
func (s *workerStore) sweepLocked(now time.Time) {
	for p, mine := range s.by {
		for id, d := range mine {
			if !d.live(now) {
				delete(mine, id)
			}
		}
		if len(mine) == 0 {
			delete(s.by, p)
		}
	}
	s.pruneSettingsLocked()
}

func (s *workerStore) put(d workerDecl) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	if s.by == nil {
		s.by = map[string]map[string]workerDecl{}
	}
	mine := s.by[d.Principal]
	if mine == nil {
		mine = map[string]workerDecl{}
		s.by[d.Principal] = mine
	}
	mine[d.Worker] = d
	for len(mine) > maxWorkersPerPrincipal {
		evictOldest(mine)
	}
	for total := s.totalLocked(); total > maxWorkersTotal; total-- {
		// Evict the oldest of everyone.
		var op, oid string
		var ot time.Time
		for p, m := range s.by {
			for id, x := range m {
				if op == "" || x.ReceivedAt.Before(ot) {
					op, oid, ot = p, id, x.ReceivedAt
				}
			}
		}
		delete(s.by[op], oid)
	}
	s.pruneSettingsLocked()
}

func evictOldest(m map[string]workerDecl) {
	oldest := ""
	for id, x := range m {
		if oldest == "" || x.ReceivedAt.Before(m[oldest].ReceivedAt) {
			oldest = id
		}
	}
	delete(m, oldest)
}

func (s *workerStore) totalLocked() int {
	n := 0
	for _, m := range s.by {
		n += len(m)
	}
	return n
}

// remove drops one declaration at once (a clean shutdown, state "stopped").
func (s *workerStore) remove(principal, worker string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.by[principal], worker)
	if len(s.by[principal]) == 0 {
		delete(s.by, principal)
	}
	s.pruneSettingsLocked()
}

// hasLive reports whether a principal has a live declaration. It is the one
// map lookup that tells a worker's own tool calls apart (activity.go).
func (s *workerStore) hasLive(principal string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	return len(s.by[principal]) > 0
}

// live returns every live declaration naming kb, sorted by worker id.
func (s *workerStore) live(kb string) []workerDecl {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	var out []workerDecl
	for _, m := range s.by {
		for _, d := range m {
			for _, k := range d.KBs {
				if k == kb {
					out = append(out, d)
					break
				}
			}
		}
	}
	// The store keys on principal, so two principals can declare one worker id:
	// worker, then perimeter, then principal keeps the order stable.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Worker != b.Worker {
			return a.Worker < b.Worker
		}
		if a.Perimeter != b.Perimeter {
			return a.Perimeter < b.Perimeter
		}
		return a.Principal < b.Principal
	})
	return out
}

// workerBeat is the request body. Pointer and zero values distinguish absent
// fields where it matters.
type workerBeat struct {
	Worker          string   `json:"worker"`
	Perimeter       string   `json:"perimeter"`
	Version         string   `json:"version"`
	KBs             []string `json:"kbs"`
	State           string   `json:"state"`
	IntervalSeconds int      `json:"interval_seconds"`
	LastRun         *struct {
		At          time.Time `json:"at"`
		UnitsDone   int64     `json:"units_done"`
		TokensSpent int64     `json:"tokens_spent"`
		Commits     int64     `json:"commits"`
	} `json:"last_run"`
	Budgets      []workerBudget `json:"budgets"`
	NextWindowAt *time.Time     `json:"next_window_at"`
}

// cleanText truncates to max runes and strips control characters: what a
// worker posts is shown as text and never as markup, but a newline in a name
// would still break a log line.
func cleanText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if n++; n > max {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func clampCount(n int64) int64 {
	if n < 0 || n > workerMaxCount {
		if n < 0 {
			return 0
		}
		return workerMaxCount
	}
	return n
}

// handleWorkers stores a worker's heartbeat under its principal and answers
// with the idle signal of each accepted KB (the worker already beats, so the
// answer costs no extra request and no tools/list byte).
//
// A worker is a writer: the declared KBs are filtered to those the principal
// can write and this server mounts. None left answers the 404 of an unknown KB,
// so the route is no existence oracle. The heartbeat grants nothing.
func (m *MultiKBServer) handleWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWorkerBody))
	if err != nil {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "worker heartbeat too large", "")
		return
	}
	var beat workerBeat
	if err := json.Unmarshal(data, &beat); err != nil {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a JSON object", "")
		return
	}
	bad := func(msg, field string) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, msg, field)
	}
	switch {
	case !workerIDRe.MatchString(beat.Worker):
		bad("worker must be 1-64 characters of [a-z0-9._-]", "worker")
		return
	case !perimeterRe.MatchString(beat.Perimeter):
		bad("perimeter must be 1-40 characters of [a-z0-9-]", "perimeter")
		return
	case len(beat.KBs) == 0 || len(beat.KBs) > maxWorkerKBs:
		bad("kbs must list 1-32 names", "kbs")
		return
	case !workerStates[beat.State]:
		bad("state must be idle, running, waiting, budget_exhausted or stopped", "state")
		return
	case len(beat.Budgets) > maxWorkerBudgets:
		bad("at most 8 budgets", "budgets")
		return
	}
	budgets := make([]workerBudget, 0, len(beat.Budgets))
	for _, b := range beat.Budgets {
		if !profileRe.MatchString(b.Profile) || !workerTierSet[b.Tier] {
			bad("a budget needs a profile of [a-z0-9._-] and a tier small or large", "budgets")
			return
		}
		b.Spent, b.Limit = clampCount(b.Spent), clampCount(b.Limit)
		budgets = append(budgets, b)
	}

	p := auth.PrincipalFromContext(r.Context())
	// A clean shutdown removes the caller's own declaration before the KB
	// filter: with access revoked or the KB unmounted the beat would 404 and
	// leave it live, excluding this principal from the idle signal. It touches
	// only the caller's own entry, so it leaks nothing.
	if beat.State == "stopped" {
		m.workers.remove(p.ID, beat.Worker)
	}
	seen := map[string]bool{}
	var accepted []string
	for _, name := range beat.KBs {
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, mounted := m.servers[name]; mounted && p.ID != "" && p.Policy.HasKBAccess(name, true) {
			accepted = append(accepted, name)
		}
	}
	if len(accepted) == 0 {
		writeUINotFound(w)
		return
	}
	sort.Strings(accepted)

	now := m.workers.clock()
	if beat.State != "stopped" {
		interval := beat.IntervalSeconds
		switch {
		case interval == 0:
			interval = defaultWorkerInterval
		case interval < minWorkerInterval:
			interval = minWorkerInterval
		case interval > maxWorkerInterval:
			interval = maxWorkerInterval
		}
		d := workerDecl{
			Principal: p.ID, Worker: beat.Worker, Perimeter: beat.Perimeter,
			Version: cleanText(beat.Version, 32), KBs: accepted, State: beat.State,
			IntervalSeconds: interval, Budgets: budgets, ReceivedAt: now.UTC(),
		}
		if lr := beat.LastRun; lr != nil {
			at := lr.At
			// A clock a day ahead is a clock; one further out is clamped to now
			// (the usage route's rule).
			if at.After(now.Add(24 * time.Hour)) {
				at = now
			}
			d.LastRun = &workerLastRun{At: at.UTC(), UnitsDone: clampCount(lr.UnitsDone), TokensSpent: clampCount(lr.TokensSpent), Commits: clampCount(lr.Commits)}
		}
		if nw := beat.NextWindowAt; nw != nil && nw.After(now) && nw.Before(now.Add(maxNextWindowAhead)) {
			t := nw.UTC()
			d.NextWindowAt = &t
		}
		m.workers.put(d)
	}

	type kbIdle struct {
		LastActivityAt *string `json:"last_activity_at"`
		IdleSeconds    *int64  `json:"idle_seconds,omitempty"`
	}
	kbs := make(map[string]kbIdle, len(accepted))
	for _, name := range accepted {
		var e kbIdle
		if at, ok := m.servers[name].lastActivity(); ok {
			s := at.Format(time.RFC3339Nano)
			idle := int64(now.Sub(at) / time.Second)
			if idle < 0 {
				idle = 0
			}
			e.LastActivityAt, e.IdleSeconds = &s, &idle
		}
		kbs[name] = e
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"accepted": accepted,
		// True when no settings report is held for this perimeter: the worker
		// posts it at start and again only after a server restart.
		"settings_wanted": !m.workers.hasSettings(p.ID, beat.Perimeter),
		"kbs":             kbs,
		// With authentication off every caller is the local admin and the
		// server cannot tell the worker from a person.
		"activity_known":    !p.Local,
		"server_started_at": processStartedAt,
		"now":               now.UTC().Format(time.RFC3339Nano),
	})
}

// GET /kbs/{kb}/workers — the live workers that declared this KB, for the
// Atlas (design D22). Read-only: it shows the heartbeats, it never acts on
// them. They describe the whole KB's activity, like status, so a caller that
// does not see the whole KB gets the 404 of an unknown route. No principal id
// and no token digest leaves the server.
func (m *MultiKBServer) uiWorkers(w http.ResponseWriter, r *http.Request, name string, srv *Server) {
	if !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	type row struct {
		Worker          string         `json:"worker"`
		Perimeter       string         `json:"perimeter"`
		Version         string         `json:"version,omitempty"`
		State           string         `json:"state"`
		LastBeatAt      string         `json:"last_beat_at"`
		IntervalSeconds int            `json:"interval_seconds"`
		LastRun         *workerLastRun `json:"last_run,omitempty"`
		Budgets         []workerBudget `json:"budgets"`
		NextWindowAt    *time.Time     `json:"next_window_at,omitempty"`
	}
	rows := []row{}
	for _, d := range m.workers.live(name) {
		rows = append(rows, row{
			Worker: d.Worker, Perimeter: d.Perimeter, Version: d.Version, State: d.State,
			LastBeatAt: d.ReceivedAt.Format(time.RFC3339), IntervalSeconds: d.IntervalSeconds,
			LastRun: d.LastRun, Budgets: d.Budgets, NextWindowAt: d.NextWindowAt,
		})
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"workers": rows})
}
