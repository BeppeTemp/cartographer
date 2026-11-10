package surveyor

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

// DefaultBeatInterval is how often a surveyor declares itself to the server (D1259).
const DefaultBeatInterval = 60 * time.Second

// beatCallTimeout bounds each request of the beater; the closing one included,
// so a shutdown fits the orchestrator's grace period.
const beatCallTimeout = 5 * time.Second

// BeatAPI is the part of the client the beater uses.
type BeatAPI interface {
	Heartbeat(report any, timeout time.Duration) (*client.HeartbeatAnswer, error)
	ReportSurveyorSettings(report any, timeout time.Duration) error
}

// Beater is the Heartbeat and the IdleSource of a surveyor (D1259), both served by
// the same answer of POST /api/surveyors. The runner hands it a snapshot with
// Beat (a mutex-guarded copy: the runner may spend minutes inside a model call)
// and its own goroutine, Start, declares the surveyor every Interval from that
// snapshot. A failed beat is a log line: it never blocks or fails a run, and a
// server too old for the route (404) leaves the idle signal unknown, so the
// surveyor idles on its time window only.
type Beater struct {
	API       BeatAPI
	Surveyor  string // the instance id: set from Runner.Replica before Start
	Perimeter string
	Version   string
	KBs       []string
	Tiers     map[string]string // profile name -> tier
	// Tasks are the task kinds this surveyor runs (DeclaredTasks), sent with
	// every beat; empty sends none, which is what a surveyor without the field did.
	Tasks    []TaskDecl
	Interval time.Duration // DefaultBeatInterval when zero
	// Settings is the report posted after the first beat and whenever an answer
	// says settings_wanted; nil posts none.
	Settings *surveyorconfig.Report
	Now      func() time.Time // time.Now when nil
	Logf     func(format string, args ...any)

	sendMu sync.Mutex // one request at a time

	mu       sync.Mutex
	snap     State
	beaten   bool
	stopped  bool
	answer   *client.HeartbeatAnswer
	answerAt time.Time // local clock when the answer arrived
	posted   bool
	refused  bool // the server answered the settings report with a 4xx: not sent again
}

func (b *Beater) interval() time.Duration {
	if b.Interval > 0 {
		return b.Interval
	}
	return DefaultBeatInterval
}

func (b *Beater) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Beater) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
}

// Beat keeps the runner's state; the first call, and the closing one (state
// stopped, after which the goroutine beats no more), are sent at once.
func (b *Beater) Beat(_ context.Context, s State) {
	b.mu.Lock()
	b.snap = s
	first := !b.beaten
	b.beaten = true
	b.stopped = b.stopped || s.Run == StateStopped
	b.mu.Unlock()
	if first || s.Run == StateStopped {
		b.send()
	}
}

// Start beats every interval until ctx ends. The closing beat is the runner's.
func (b *Beater) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(b.interval())
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.send()
			}
		}
	}()
}

type beatBudget struct {
	Profile string `json:"profile"`
	Tier    string `json:"tier,omitempty"`
	Spent   int    `json:"spent"`
	Limit   int    `json:"limit"`
}

type beatLastRun struct {
	At          time.Time `json:"at"`
	UnitsDone   int       `json:"units_done"`
	TokensSpent int       `json:"tokens_spent"`
	Commits     int       `json:"commits"`
}

type beatBody struct {
	Surveyor        string       `json:"surveyor"`
	Perimeter       string       `json:"perimeter"`
	Version         string       `json:"version,omitempty"`
	KBs             []string     `json:"kbs"`
	State           string       `json:"state"`
	IntervalSeconds int          `json:"interval_seconds"`
	LastRun         *beatLastRun `json:"last_run,omitempty"`
	Budgets         []beatBudget `json:"budgets,omitempty"`
	NextWindowAt    *time.Time   `json:"next_window_at,omitempty"`
	Tasks           []TaskDecl   `json:"tasks,omitempty"`
}

// TaskDecl is one task kind a heartbeat declares. IntervalSeconds, when set, is
// how often this surveyor intends to run it.
type TaskDecl struct {
	Kind            string `json:"kind"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
}

// DeclaredTasks lists the task kinds of a config for the heartbeat: names
// deduplicated and sorted, with the interval for the kinds that have one
// (repair, from repair_interval).
func DeclaredTasks(cfg *Config) []TaskDecl {
	byKind := map[string]TaskDecl{}
	for _, t := range cfg.Tasks {
		d := TaskDecl{Kind: t.Kind}
		if t.Kind == repairKindName {
			if iv, ok := t.Options["repair_interval"].(time.Duration); ok {
				d.IntervalSeconds = int(iv / time.Second)
			}
		}
		byKind[t.Kind] = d
	}
	out := make([]TaskDecl, 0, len(byKind))
	for _, d := range byKind {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func (b *Beater) body(s State) beatBody {
	out := beatBody{Surveyor: b.Surveyor, Perimeter: b.Perimeter, Version: b.Version, KBs: b.KBs, State: s.Run,
		IntervalSeconds: int(b.interval() / time.Second), Tasks: b.Tasks}
	if !s.LastRun.At.IsZero() {
		out.LastRun = &beatLastRun{At: s.LastRun.At.UTC(), UnitsDone: s.LastRun.Units, Commits: s.LastRun.Commits}
	}
	for _, p := range s.Budgets {
		out.Budgets = append(out.Budgets, beatBudget{Profile: p.Profile, Tier: b.Tiers[p.Profile], Spent: p.SpentToday, Limit: p.Daily})
	}
	if !s.NextWindowStart.IsZero() {
		t := s.NextWindowStart.UTC()
		out.NextWindowAt = &t
	}
	return out
}

// send posts the current snapshot and keeps the answer.
func (b *Beater) send() {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	b.mu.Lock()
	s, stopped := b.snap, b.stopped
	if stopped && s.Run != StateStopped {
		b.mu.Unlock()
		return // a tick that raced with the closing beat
	}
	b.mu.Unlock()
	a, err := b.API.Heartbeat(b.body(s), beatCallTimeout)
	if err != nil {
		b.logf("surveyor: heartbeat: %v", err)
		var re *client.RemoteError
		if errors.As(err, &re) && re.HTTPStatus == http.StatusNotFound {
			b.mu.Lock()
			b.answer = nil // an older server: activity unknown
			b.mu.Unlock()
		}
		return
	}
	b.mu.Lock()
	b.answer, b.answerAt = a, b.now()
	post := b.Settings != nil && s.Run != StateStopped && !b.refused && (!b.posted || a.SettingsWanted)
	b.mu.Unlock()
	if !post {
		return
	}
	if err := b.API.ReportSurveyorSettings(b.Settings, beatCallTimeout); err != nil {
		var re *client.RemoteError
		if errors.As(err, &re) && re.HTTPStatus >= 400 && re.HTTPStatus < 500 {
			// The report is constant for the life of the process: a refusal
			// would repeat on every beat, so it is logged once and not retried.
			b.mu.Lock()
			b.refused = true
			b.mu.Unlock()
			b.logf("surveyor: settings report refused (%v): not sent again", err)
			return
		}
		b.logf("surveyor: settings report: %v", err)
		return
	}
	b.mu.Lock()
	b.posted = true
	b.mu.Unlock()
}

// IdleFor reads the last answer: the KB's idle time on the server's clock
// (answer.now minus last_activity_at, or minus server_started_at when no call
// has happened since the server started), advanced by the local time elapsed
// since the answer arrived. Server timestamps are never compared with the
// surveyor's clock. Unknown when no answer, an answer more than three intervals
// old, authentication off (activity_known false) or a KB the answer omits.
func (b *Beater) IdleFor(_ context.Context, kb string) (time.Duration, bool, error) {
	b.mu.Lock()
	a, at := b.answer, b.answerAt
	b.mu.Unlock()
	if a == nil || !a.ActivityKnown {
		return 0, false, nil
	}
	local := b.now().Sub(at)
	if local > 3*b.interval() {
		return 0, false, nil
	}
	e, ok := a.KBs[kb]
	if !ok {
		return 0, false, nil
	}
	since := a.ServerStartedAt
	if e.LastActivityAt != nil {
		since = *e.LastActivityAt
	}
	if since.IsZero() || a.Now.IsZero() {
		return 0, false, nil
	}
	return max(a.Now.Sub(since), 0) + max(local, 0), true, nil
}
