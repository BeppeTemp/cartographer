package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// POST /api/component/v1/runs — the runs a Surveyor finished and the alarms it
// raises. It is its own route and not part of the heartbeat because the beat is
// liveness and must never fail or grow because of a report (the reason the
// settings have their own route, D1259). Memory only, bounded, no KB write: a
// restart forgets it until the Surveyor posts again, which the heartbeat answer
// asks for (runs_wanted). Ages are computed from ReceivedAt, the server's clock;
// a Surveyor's own timestamps are shown but never decide how recent a run is.
const (
	maxRunsBody         = 32 << 10
	maxRunsPerPost      = 20
	maxRunsKept         = 20 // per (principal, perimeter)
	maxRunBooks         = 64 // perimeters in total
	maxAlarmsPerPost    = 16
	maxRunErrorRunes    = 200
	maxRunDurationMS    = int64(7 * 24 * 3600 * 1000)
	maxRunStartedAhead  = 24 * time.Hour
	maxRunStartedBehind = 30 * 24 * time.Hour
)

var runIDRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,96}$`)

// surveyorRun is one finished task as the server holds it.
type surveyorRun struct {
	RunID       string    `json:"run_id"`
	Replica     string    `json:"replica"`
	Task        string    `json:"task"`
	KB          string    `json:"kb"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`
	Outcome     string    `json:"outcome"`
	Error       string    `json:"error,omitempty"`
	TokensSpent int64     `json:"tokens_spent"`
	// ReceivedAt is the server's stamp: ages come from it.
	ReceivedAt time.Time `json:"received_at"`
}

// surveyorAlarm is an alarm a Surveyor reports, or one the server computes
// from health (Origin tells which).
type surveyorAlarm struct {
	Code       string     `json:"code"`
	KB         string     `json:"kb,omitempty"`
	Source     string     `json:"source,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	// Origin is "reported" (a Surveyor said so, shown as reported) or "server"
	// (computed from the component's health or protocol skew).
	Origin string `json:"origin"`
}

// runBook is what is held for one (principal, perimeter): it lives and dies
// with the live declaration of the same pair (pruneReportsLocked).
type runBook struct {
	runs    []surveyorRun // newest first
	alarms  []surveyorAlarm
	touched time.Time
}

func (s *surveyorStore) hasRuns(principal, perimeter string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	_, ok := s.runs[principal][perimeter]
	return ok
}

// pruneRunsLocked drops the books no live declaration of the same principal
// and perimeter backs.
func (s *surveyorStore) pruneRunsLocked() {
	for p, m := range s.runs {
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
			delete(s.runs, p)
		}
	}
}

// putRuns merges a post into the book: runs are deduplicated by run_id (the
// one already held wins, so a re-post is harmless), newest first by start,
// kept to maxRunsKept; the alarms replace the previous set (a post carries
// the Surveyor's whole current list). It returns the id of the newest run held.
func (s *surveyorStore) putRuns(principal, perimeter string, runs []surveyorRun, alarms []surveyorAlarm, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if s.runs == nil {
		s.runs = map[string]map[string]*runBook{}
	}
	if s.runs[principal] == nil {
		s.runs[principal] = map[string]*runBook{}
	}
	b := s.runs[principal][perimeter]
	if b == nil {
		b = &runBook{}
		s.runs[principal][perimeter] = b
	}
	have := map[string]bool{}
	for _, r := range b.runs {
		have[r.RunID] = true
	}
	for _, r := range runs {
		if !have[r.RunID] {
			have[r.RunID] = true
			b.runs = append(b.runs, r)
		}
	}
	sort.SliceStable(b.runs, func(i, j int) bool {
		if !b.runs[i].StartedAt.Equal(b.runs[j].StartedAt) {
			return b.runs[i].StartedAt.After(b.runs[j].StartedAt)
		}
		return b.runs[i].ReceivedAt.After(b.runs[j].ReceivedAt)
	})
	if len(b.runs) > maxRunsKept {
		b.runs = b.runs[:maxRunsKept]
	}
	b.alarms = alarms
	b.touched = now
	for total := s.booksLocked(); total > maxRunBooks; total-- {
		var op, oper string
		var ot time.Time
		for p, m := range s.runs {
			for per, x := range m {
				if (op == "" || x.touched.Before(ot)) && !(p == principal && per == perimeter) {
					op, oper, ot = p, per, x.touched
				}
			}
		}
		if op == "" {
			break
		}
		delete(s.runs[op], oper)
		if len(s.runs[op]) == 0 {
			delete(s.runs, op)
		}
	}
	if len(b.runs) == 0 {
		return ""
	}
	return b.runs[0].RunID
}

func (s *surveyorStore) booksLocked() (n int) {
	for _, m := range s.runs {
		n += len(m)
	}
	return n
}

// runsFor returns the runs and the reported alarms of a perimeter on kb, over
// the principals of the live declarations given (those naming kb): runs of
// that KB newest first, at most maxRunsKept; alarms of that KB or of none.
func (s *surveyorStore) runsFor(kb, perimeter string, decls []surveyorDecl) ([]surveyorRun, []surveyorAlarm) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	runs, alarms := []surveyorRun{}, []surveyorAlarm{}
	seen := map[string]bool{}
	for _, d := range decls {
		if d.Perimeter != perimeter || seen[d.Principal] {
			continue
		}
		seen[d.Principal] = true
		b := s.runs[d.Principal][perimeter]
		if b == nil {
			continue
		}
		for _, r := range b.runs {
			if r.KB == kb {
				runs = append(runs, r)
			}
		}
		for _, a := range b.alarms {
			switch a.KB {
			case kb:
				alarms = append(alarms, a)
			case "":
				// No writable-KB check vouched for this one: it carries a
				// code and a time to every KB view of the perimeter, not text.
				a.Source, a.Detail = "", ""
				alarms = append(alarms, a)
			}
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })
	if len(runs) > maxRunsKept {
		runs = runs[:maxRunsKept]
	}
	return runs, alarms
}

type runsBody struct {
	Protocol  int    `json:"protocol"`
	ID        string `json:"id"`
	Perimeter string `json:"perimeter"`
	Runs      []struct {
		RunID       string    `json:"run_id"`
		Task        string    `json:"task"`
		KB          string    `json:"kb"`
		StartedAt   time.Time `json:"started_at"`
		DurationMS  int64     `json:"duration_ms"`
		Outcome     string    `json:"outcome"`
		Error       string    `json:"error"`
		TokensSpent int64     `json:"tokens_spent"`
	} `json:"runs"`
	Alarms []struct {
		Code   string    `json:"code"`
		KB     string    `json:"kb"`
		Source string    `json:"source"`
		Since  time.Time `json:"since"`
		Detail string    `json:"detail"`
	} `json:"alarms"`
}

// handleSurveyorRuns stores the runs and alarms of a Surveyor. It needs a live
// declaration of the caller for the perimeter (a report with nothing to hang on
// would be swept at once): without one the answer is the 404 of an unknown
// route, so it is no existence oracle either. A run for a KB the caller cannot
// write or the server does not mount is dropped, not refused.
func (m *MultiKBServer) handleSurveyorRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "use POST", "")
		return
	}
	if !requireJSON(w, r) {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRunsBody))
	if err != nil {
		writeUIError(w, http.StatusRequestEntityTooLarge, uiCodeInvalidRequest, "runs report too large", "")
		return
	}
	var body runsBody
	if err := json.Unmarshal(data, &body); err != nil {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "body must be a JSON object", "")
		return
	}
	if _, ok := checkProtocol(w, body.Protocol); !ok {
		return
	}
	bad := func(msg, field string) {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, msg, field)
	}
	switch {
	case !surveyorIDRe.MatchString(body.ID):
		bad("id must be 1-64 characters of [a-z0-9._-]", "id")
		return
	case !perimeterRe.MatchString(body.Perimeter):
		bad("perimeter must be 1-40 characters of [a-z0-9-]", "perimeter")
		return
	case len(body.Runs) > maxRunsPerPost:
		bad("at most 20 runs per report", "runs")
		return
	case len(body.Alarms) > maxAlarmsPerPost:
		bad("at most 16 alarms per report", "alarms")
		return
	}
	now := m.surveyors.clock().UTC()
	clampStart := func(t time.Time) time.Time {
		if t.IsZero() || t.After(now.Add(maxRunStartedAhead)) {
			return now
		}
		if t.Before(now.Add(-maxRunStartedBehind)) {
			return now.Add(-maxRunStartedBehind)
		}
		return t.UTC()
	}
	runs := make([]surveyorRun, 0, len(body.Runs))
	p := auth.PrincipalFromContext(r.Context())
	writable := func(kb string) bool {
		_, mounted := m.servers[kb]
		return mounted && p.ID != "" && p.Policy.HasKBAccess(kb, true)
	}
	for _, x := range body.Runs {
		switch {
		case !runIDRe.MatchString(x.RunID):
			bad("a run needs a run_id of 1-96 characters of [A-Za-z0-9._:/-]", "runs")
			return
		case !taskKindRe.MatchString(x.Task):
			bad("a run needs a task of [a-z0-9-], 1-32 characters", "runs")
			return
		case !validOutcome(x.Outcome):
			bad("outcome must be ok, failed, skipped or budget_exhausted", "runs")
			return
		}
		if !writable(x.KB) {
			continue
		}
		runs = append(runs, surveyorRun{
			RunID: x.RunID, Replica: body.ID, Task: x.Task, KB: x.KB, StartedAt: clampStart(x.StartedAt),
			DurationMS: min(clampCount(x.DurationMS), maxRunDurationMS), Outcome: x.Outcome,
			Error: cleanText(x.Error, maxRunErrorRunes), TokensSpent: clampCount(x.TokensSpent), ReceivedAt: now,
		})
	}
	alarms := make([]surveyorAlarm, 0, len(body.Alarms))
	for _, a := range body.Alarms {
		if !validReportedAlarm(a.Code) {
			bad("code must be feed_failing, auth_refused or model_unreachable", "alarms")
			return
		}
		if a.KB != "" && !writable(a.KB) {
			continue
		}
		since := clampStart(a.Since)
		rec := now
		alarms = append(alarms, surveyorAlarm{
			Code: a.Code, KB: a.KB, Source: cleanText(a.Source, 40), Since: &since,
			Detail: cleanText(a.Detail, maxRunErrorRunes), ReceivedAt: &rec, Origin: "reported",
		})
	}
	if !m.surveyors.liveFor(p.ID, body.Perimeter, body.ID) {
		writeUINotFound(w)
		return
	}
	last := m.surveyors.putRuns(p.ID, body.Perimeter, runs, alarms, now)
	writeUIJSON(w, http.StatusOK, map[string]any{"received": len(runs), "last_run_id": last})
}

// liveFor reports whether principal holds a live declaration for perimeter
// carrying the replica id: a run is stamped with that id, so it must be one
// the principal declared.
func (s *surveyorStore) liveFor(principal, perimeter, replica string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.clock())
	for _, d := range s.by[principal] {
		if d.Perimeter == perimeter && d.Surveyor == replica {
			return true
		}
	}
	return false
}

func validOutcome(o string) bool {
	for _, k := range componentproto.Outcomes() {
		if k == o {
			return true
		}
	}
	return false
}

func validReportedAlarm(c string) bool {
	for _, k := range componentproto.ReportedAlarms() {
		if k == c {
			return true
		}
	}
	return false
}
