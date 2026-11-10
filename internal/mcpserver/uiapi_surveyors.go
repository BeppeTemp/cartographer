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
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// componentproto.HeartbeatPath is where a `cartographer surveyor` process
// declares itself, once a minute. Client-to-server metadata like the usage and
// settings reports: an HTTP route inside the auth chain of /mcp, not an MCP
// tool (a tool would spend tools/list budget on every client for something no
// agent calls, D285).
//
// The declaration is a hint, validated, bounded in time and ignored when stale
// (D369). The server acts on it in one way only: it pauses its own background
// repair of a KB a live surveyor declaring the repair task keeps repairing
// (repairOwners). That needs a principal that can write the KB (the KB filter
// below), a successful kb_repair check "auto" the server saw that principal
// make on that KB within the timer's own period plus a slack
// (Server.noteSurveyorRepair), and a beat within three intervals. The worst a
// hostile or mistaken writer buys is that the timer leaves its KBs alone for as
// long as it beats AND repeats that call at least once per timer period; it
// could already commit to them, and repair on write is unaffected.
// The budget is enforced by the surveyor.
const (
	maxSurveyorBody          = 64 << 10
	maxSurveyorKBs           = 32
	maxSurveyorBudgets       = 8
	maxSurveyorTasks         = 16
	minTaskInterval          = 3600
	maxTaskInterval          = 8 * 24 * 3600
	defaultTaskInterval      = 24 * 3600
	maxSurveyorsPerPrincipal = 16
	maxSurveyorsTotal        = 256
	minSurveyorInterval      = 15
	maxSurveyorInterval      = 3600
	defaultSurveyorInterval  = 60
	// staleIntervals is how many missed beats make a declaration stale.
	staleIntervals = 3
	// maxNextWindowAhead bounds next_window_at: a weekly window is further
	// than a day away, so it is omitted rather than clamped (past the bound).
	maxNextWindowAhead = 8 * 24 * time.Hour
	// surveyorMaxCount caps a reported counter (usageMaxCount's reasoning).
	surveyorMaxCount = int64(1) << 40
)

var (
	surveyorIDRe    = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
	perimeterRe     = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	profileRe       = regexp.MustCompile(`^[a-z0-9._-]{1,40}$`)
	surveyorStates  = map[string]bool{"idle": true, "running": true, "waiting": true, "budget_exhausted": true, "stopped": true}
	surveyorTierSet = map[string]bool{"": true, "small": true, "large": true}
	taskKindRe      = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// surveyorBudget is one model profile's spend against its daily limit (D1258).
type surveyorBudget struct {
	Profile string `json:"profile"`
	Tier    string `json:"tier,omitempty"`
	Spent   int64  `json:"spent"`
	Limit   int64  `json:"limit"`
}

// surveyorLastRun is the outcome of the surveyor's latest run.
type surveyorLastRun struct {
	At          time.Time `json:"at"`
	UnitsDone   int64     `json:"units_done"`
	TokensSpent int64     `json:"tokens_spent"`
	Commits     int64     `json:"commits"`
}

// surveyorTask is one task kind a surveyor declares it runs, with how often it
// intends to (seconds, clamped to 3600..691200).
type surveyorTask struct {
	Kind            string `json:"kind"`
	IntervalSeconds int    `json:"interval_seconds"`
}

// surveyorDecl is one accepted heartbeat. KBs holds only the KBs the principal
// may write and this server mounts.
type surveyorDecl struct {
	Principal       string
	Surveyor        string
	Perimeter       string
	Version         string
	KBs             []string
	State           string
	IntervalSeconds int
	LastRun         *surveyorLastRun
	Budgets         []surveyorBudget
	NextWindowAt    *time.Time
	Tasks           []surveyorTask
	// Protocol is the component protocol the surveyor declared and ProtocolSkew
	// where it stands against this server ("behind", "" or "ahead").
	Protocol     int
	ProtocolSkew string
	ReceivedAt   time.Time
}

func (d surveyorDecl) live(now time.Time) bool {
	return now.Sub(d.ReceivedAt) < time.Duration(staleIntervals*d.IntervalSeconds)*time.Second
}

// repairTaskKind is the task kind whose declaration lets the server's timer
// step aside (the surveyor's repair task).
const repairTaskKind = "repair"

// repairHandoverSlack is added to the timer period to form the window in which
// the last observed repair must lie: the Surveyor's own pace has jitter.
const repairHandoverSlack = time.Hour

// repairOwners returns the live declarations that cover kb: accepted for it,
// declaring the repair task, and whose principal observed returns a successful
// kb_repair check "auto" on kb no longer ago than the server's own timer period
// (autoRepairInterval) plus repairHandoverSlack. The window is the server's,
// never the declared interval: a declaration cannot stretch it, and a Surveyor
// that repairs less often than the timer would never take the KB over. observed
// is the server's own record, never the surveyor's word. Sorted by surveyor id.
func (s *surveyorStore) repairOwners(kb string, observed func(principal string) (time.Time, bool)) []surveyorDecl {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	var out []surveyorDecl
	for _, mine := range s.by {
		for _, d := range mine {
			if !d.live(now) || !containsString(d.KBs, kb) {
				continue
			}
			declared := false
			for _, t := range d.Tasks {
				declared = declared || t.Kind == repairTaskKind
			}
			if !declared {
				continue
			}
			window := autoRepairInterval + repairHandoverSlack
			if at, ok := observed(d.Principal); ok && now.Sub(at) <= window && !at.After(now.Add(time.Minute)) {
				out = append(out, d)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Surveyor < out[j].Surveyor })
	return out
}

// RepairDelegates returns the ids of the live surveyors that own the repair of
// kb: while it is non-empty the server's background repair of kb is paused. It
// is the seam the maintenance view reads.
func (m *MultiKBServer) RepairDelegates(kb string) []string {
	srv, ok := m.servers[kb]
	if !ok {
		return []string{}
	}
	return m.repairDelegatesOf(kb, srv)
}

// RepairDelegation is RepairDelegates bound to a mounted server, for
// SetRepairDelegation: the closure reads no map of the MultiKBServer, which
// MountKB is still filling when the repair loop is started from its callback.
func (m *MultiKBServer) RepairDelegation(kb string, srv *Server) func() []string {
	return func() []string { return m.repairDelegatesOf(kb, srv) }
}

func (m *MultiKBServer) repairDelegatesOf(kb string, srv *Server) []string {
	owners := m.surveyors.repairOwners(kb, srv.surveyorRepairAt)
	ids := make([]string, 0, len(owners))
	for _, d := range owners {
		ids = append(ids, d.Surveyor)
	}
	return ids
}

// surveyorStore holds the declarations in memory, per principal and surveyor id.
// A restart forgets them until the next beat, which costs a minute of an empty
// panel and no stale file to expire (the pattern of clientReports, D818).
type surveyorStore struct {
	mu  sync.Mutex
	now func() time.Time // nil = time.Now; tests inject
	by  map[string]map[string]surveyorDecl
	// settings are the reports of POST /api/surveyor-settings, per principal and
	// perimeter. A report lives only while a live declaration of the same
	// principal and perimeter does (pruneSettingsLocked): it needs no clock.
	settings map[string]map[string]surveyorSettings
}

func (s *surveyorStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// sweepLocked drops stale declarations: there is no timer, expiry happens on
// the next put or read.
func (s *surveyorStore) sweepLocked(now time.Time) {
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

func (s *surveyorStore) put(d surveyorDecl) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	if s.by == nil {
		s.by = map[string]map[string]surveyorDecl{}
	}
	mine := s.by[d.Principal]
	if mine == nil {
		mine = map[string]surveyorDecl{}
		s.by[d.Principal] = mine
	}
	mine[d.Surveyor] = d
	for len(mine) > maxSurveyorsPerPrincipal {
		evictOldest(mine)
	}
	for total := s.totalLocked(); total > maxSurveyorsTotal; total-- {
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

func evictOldest(m map[string]surveyorDecl) {
	oldest := ""
	for id, x := range m {
		if oldest == "" || x.ReceivedAt.Before(m[oldest].ReceivedAt) {
			oldest = id
		}
	}
	delete(m, oldest)
}

func (s *surveyorStore) totalLocked() int {
	n := 0
	for _, m := range s.by {
		n += len(m)
	}
	return n
}

// remove drops one declaration at once (a clean shutdown, state "stopped").
func (s *surveyorStore) remove(principal, surveyor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.by[principal], surveyor)
	if len(s.by[principal]) == 0 {
		delete(s.by, principal)
	}
	s.pruneSettingsLocked()
}

// hasLive reports whether a principal has a live declaration. It is the one
// map lookup that tells a surveyor's own tool calls apart (activity.go).
func (s *surveyorStore) hasLive(principal string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	return len(s.by[principal]) > 0
}

// live returns every live declaration naming kb, sorted by surveyor id.
func (s *surveyorStore) live(kb string) []surveyorDecl {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	var out []surveyorDecl
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
	// The store keys on principal, so two principals can declare one surveyor id:
	// surveyor, then perimeter, then principal keeps the order stable.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Surveyor != b.Surveyor {
			return a.Surveyor < b.Surveyor
		}
		if a.Perimeter != b.Perimeter {
			return a.Perimeter < b.Perimeter
		}
		return a.Principal < b.Principal
	})
	return out
}

// surveyorBeat is the request body. Pointer and zero values distinguish absent
// fields where it matters.
type surveyorBeat struct {
	Surveyor        string   `json:"surveyor"`
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
	Budgets      []surveyorBudget `json:"budgets"`
	NextWindowAt *time.Time       `json:"next_window_at"`
	Tasks        []surveyorTask   `json:"tasks"`
	// Protocol is the component protocol the sender speaks (required, componentproto).
	Protocol int `json:"protocol"`
}

// cleanText truncates to max runes and strips control characters: what a
// surveyor posts is shown as text and never as markup, but a newline in a name
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
	if n < 0 || n > surveyorMaxCount {
		if n < 0 {
			return 0
		}
		return surveyorMaxCount
	}
	return n
}

// handleSurveyors stores a surveyor's heartbeat under its principal and answers
// with the idle signal of each accepted KB (the surveyor already beats, so the
// answer costs no extra request and no tools/list byte).
//
// A surveyor is a writer: the declared KBs are filtered to those the principal
// can write and this server mounts. None left answers the 404 of an unknown KB,
// so the route is no existence oracle. The heartbeat grants nothing.
func (m *MultiKBServer) handleSurveyors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSurveyorBody))
	if err != nil {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "surveyor heartbeat too large", "")
		return
	}
	var beat surveyorBeat
	if err := json.Unmarshal(data, &beat); err != nil {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a JSON object", "")
		return
	}
	// The version check runs first: a component outside N-1..N+1 is refused
	// whatever else is wrong with its body.
	skew, ok := checkProtocol(w, beat.Protocol)
	if !ok {
		return
	}
	bad := func(msg, field string) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, msg, field)
	}
	switch {
	case !surveyorIDRe.MatchString(beat.Surveyor):
		bad("surveyor must be 1-64 characters of [a-z0-9._-]", "surveyor")
		return
	case !perimeterRe.MatchString(beat.Perimeter):
		bad("perimeter must be 1-40 characters of [a-z0-9-]", "perimeter")
		return
	case len(beat.KBs) == 0 || len(beat.KBs) > maxSurveyorKBs:
		bad("kbs must list 1-32 names", "kbs")
		return
	case !surveyorStates[beat.State]:
		bad("state must be idle, running, waiting, budget_exhausted or stopped", "state")
		return
	case len(beat.Budgets) > maxSurveyorBudgets:
		bad("at most 8 budgets", "budgets")
		return
	case len(beat.Tasks) > maxSurveyorTasks:
		bad("tasks lists at most 16 kinds", "tasks")
		return
	}
	tasks := make([]surveyorTask, 0, len(beat.Tasks))
	for _, t := range beat.Tasks {
		if !taskKindRe.MatchString(t.Kind) {
			bad("a task needs a kind of [a-z0-9-], 1-32 characters", "tasks")
			return
		}
		switch {
		case t.IntervalSeconds == 0:
			t.IntervalSeconds = defaultTaskInterval
		case t.IntervalSeconds < minTaskInterval:
			t.IntervalSeconds = minTaskInterval
		case t.IntervalSeconds > maxTaskInterval:
			t.IntervalSeconds = maxTaskInterval
		}
		tasks = append(tasks, t)
	}
	budgets := make([]surveyorBudget, 0, len(beat.Budgets))
	for _, b := range beat.Budgets {
		if !profileRe.MatchString(b.Profile) || !surveyorTierSet[b.Tier] {
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
		m.surveyors.remove(p.ID, beat.Surveyor)
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

	now := m.surveyors.clock()
	if beat.State != "stopped" {
		interval := beat.IntervalSeconds
		switch {
		case interval == 0:
			interval = defaultSurveyorInterval
		case interval < minSurveyorInterval:
			interval = minSurveyorInterval
		case interval > maxSurveyorInterval:
			interval = maxSurveyorInterval
		}
		d := surveyorDecl{
			Principal: p.ID, Surveyor: beat.Surveyor, Perimeter: beat.Perimeter,
			Version: cleanText(beat.Version, 32), KBs: accepted, State: beat.State,
			IntervalSeconds: interval, Budgets: budgets, Tasks: tasks, ReceivedAt: now.UTC(),
			Protocol: beat.Protocol, ProtocolSkew: skew,
		}
		if lr := beat.LastRun; lr != nil {
			at := lr.At
			// A clock a day ahead is a clock; one further out is clamped to now
			// (the usage route's rule).
			if at.After(now.Add(24 * time.Hour)) {
				at = now
			}
			d.LastRun = &surveyorLastRun{At: at.UTC(), UnitsDone: clampCount(lr.UnitsDone), TokensSpent: clampCount(lr.TokensSpent), Commits: clampCount(lr.Commits)}
		}
		if nw := beat.NextWindowAt; nw != nil && nw.After(now) && nw.Before(now.Add(maxNextWindowAhead)) {
			t := nw.UTC()
			d.NextWindowAt = &t
		}
		m.surveyors.put(d)
		m.components.noteBeat(componentproto.KindSurveyor, beat.Perimeter, accepted, now.UTC())
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
		// The server's side of the negotiation (componentproto): a component
		// at N+1 reads its own N-1 here and must speak it.
		"protocol":     componentproto.Protocol,
		"min_protocol": componentproto.MinProtocol,
		// True when no settings report is held for this perimeter: the surveyor
		// posts it at start and again only after a server restart.
		"settings_wanted": !m.surveyors.hasSettings(p.ID, beat.Perimeter),
		"kbs":             kbs,
		// With authentication off every caller is the local admin and the
		// server cannot tell the surveyor from a person.
		"activity_known":    !p.Local,
		"server_started_at": processStartedAt,
		"now":               now.UTC().Format(time.RFC3339Nano),
	})
}

// GET /kbs/{kb}/surveyors — the live surveyors that declared this KB, for the
// Atlas (D1259). Read-only: it shows the heartbeats, it never acts on
// them. They describe the whole KB's activity, like status, so a caller that
// does not see the whole KB gets the 404 of an unknown route. No principal id
// and no token digest leaves the server.
func (m *MultiKBServer) uiSurveyors(w http.ResponseWriter, r *http.Request, name string, srv *Server) {
	if !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	type row struct {
		Surveyor        string           `json:"surveyor"`
		Perimeter       string           `json:"perimeter"`
		Version         string           `json:"version,omitempty"`
		State           string           `json:"state"`
		LastBeatAt      string           `json:"last_beat_at"`
		IntervalSeconds int              `json:"interval_seconds"`
		LastRun         *surveyorLastRun `json:"last_run,omitempty"`
		Budgets         []surveyorBudget `json:"budgets"`
		NextWindowAt    *time.Time       `json:"next_window_at,omitempty"`
	}
	rows := []row{}
	for _, d := range m.surveyors.live(name) {
		rows = append(rows, row{
			Surveyor: d.Surveyor, Perimeter: d.Perimeter, Version: d.Version, State: d.State,
			LastBeatAt: d.ReceivedAt.Format(time.RFC3339), IntervalSeconds: d.IntervalSeconds,
			LastRun: d.LastRun, Budgets: d.Budgets, NextWindowAt: d.NextWindowAt,
		})
	}
	writeUIJSON(w, http.StatusOK, map[string]any{"surveyors": rows})
}
