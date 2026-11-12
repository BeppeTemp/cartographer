package surveyor

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
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
	ReportRuns(report any, timeout time.Duration) (*client.RunsAnswer, error)
}

// maxRunHistory is how many finished runs the beater keeps, and the most a
// post carries (the server keeps the same number).
const maxRunHistory = 20

// runsBackoffBeats is how many accepted beats go without a runs post, or a
// settings post, after a 4xx: ten beats of the default interval, so a server
// that lacks the route costs about one POST every ten minutes.
const runsBackoffBeats = 10

// Beater is the Heartbeat and the IdleSource of a surveyor (D1259), both served by
// the same answer of the component heartbeat (componentproto.HeartbeatPath). The runner hands it a snapshot with
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
	// Settings is the report posted after the first beat, whenever an answer
	// says settings_wanted and whenever Sources reports a new generation; nil
	// posts none. A 4xx backs the post off for runsBackoffBeats beats and it is
	// tried again: no refusal disables the report for the life of the process.
	Settings *surveyorconfig.Report
	// Sources, when set, gives the report's sources each time it is posted and a
	// generation that grows whenever one of them changed (a check ran: its age is
	// no change, so the array itself cannot be compared). A generation past the
	// one of the last accepted report posts the report again. It must return at
	// once: it runs on the beater's goroutine, never the runner's, and reads the
	// generation before the sources, so a change in between is posted next beat.
	Sources func() ([]surveyorconfig.Source, uint64)
	Now     func() time.Time // time.Now when nil
	Logf    func(format string, args ...any)

	sendMu sync.Mutex // one request at a time

	mu        sync.Mutex
	snap      State
	beaten    bool
	stopped   bool
	answer    *client.HeartbeatAnswer
	answerAt  time.Time // local clock when the answer arrived
	posted    bool
	postedGen uint64 // generation of the sources in the last report accepted
	// runsSkip is how many accepted beats still go without a runs post after a
	// 4xx; loggedRunsStatus is the status last logged, until a post succeeds.
	runsSkip         int
	loggedRunsStatus int
	// settingsSkip and loggedSettingsStatus are the same for the settings report.
	settingsSkip         int
	loggedSettingsStatus int
	// logged* keep a protocol complaint to once per distinct cause: the beat
	// repeats every interval and a server may be upgraded under the Surveyor.
	loggedRefusal [2]int // (protocol, min_protocol) of the last 426 logged
	loggedBehind  bool   // the "answer below ours" line was logged

	// runs are the newest finished runs (oldest first), sent or not: the
	// history is what refills a restarted server (runs_wanted).
	runs []queuedRun
	// alarms are the open alarms, by (code, kb, task); dirty means the server
	// has not seen the current set.
	alarms      map[alarmKey]time.Time
	alarmsDirty bool
	lastRunAt   time.Time
}

type queuedRun struct {
	Run
	sent bool
}

type alarmKey struct{ code, kb, task string }

// Finished queues a finished task for the runs route; it also raises or
// clears the alarm its outcome implies. It sends nothing: the post rides the
// next beat (Beat sends at once when a run ends).
func (b *Beater) Finished(r Run) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runs = append(b.runs, queuedRun{Run: r})
	if len(b.runs) > maxRunHistory {
		b.runs = b.runs[len(b.runs)-maxRunHistory:]
	}
	if b.alarms == nil {
		b.alarms = map[alarmKey]time.Time{}
	}
	if r.Outcome == componentproto.OutcomeOK {
		for k := range b.alarms {
			if k.kb == r.KB && k.task == r.Task {
				delete(b.alarms, k)
				b.alarmsDirty = true
			}
		}
		return
	}
	if r.Alarm != "" {
		k := alarmKey{r.Alarm, r.KB, r.Task}
		if _, open := b.alarms[k]; !open {
			b.alarms[k] = r.Started
			b.alarmsDirty = true
		}
	}
}

type runsRun struct {
	RunID       string    `json:"run_id"`
	Task        string    `json:"task"`
	KB          string    `json:"kb"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`
	Outcome     string    `json:"outcome"`
	Error       string    `json:"error,omitempty"`
	TokensSpent int       `json:"tokens_spent"`
}

type runsAlarm struct {
	Code   string    `json:"code"`
	KB     string    `json:"kb,omitempty"`
	Source string    `json:"source,omitempty"`
	Since  time.Time `json:"since"`
}

type runsBody struct {
	Protocol  int         `json:"protocol"`
	ID        string      `json:"id"`
	Perimeter string      `json:"perimeter"`
	Runs      []runsRun   `json:"runs"`
	Alarms    []runsAlarm `json:"alarms"`
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
	ended := !s.LastRun.At.Equal(b.lastRunAt)
	b.lastRunAt = s.LastRun.At
	b.mu.Unlock()
	if first || ended || s.Run == StateStopped {
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
	Protocol        int          `json:"protocol"`
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
	out := beatBody{Protocol: componentproto.Protocol, Surveyor: b.Surveyor, Perimeter: b.Perimeter, Version: b.Version, KBs: b.KBs, State: s.Run,
		IntervalSeconds: int(b.interval() / time.Second), Tasks: b.Tasks}
	if !s.LastRun.At.IsZero() {
		out.LastRun = &beatLastRun{At: s.LastRun.At.UTC(), UnitsDone: s.LastRun.Units, TokensSpent: s.LastRun.Tokens, Commits: s.LastRun.Commits}
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
		if errors.As(err, &re) {
			switch re.HTTPStatus {
			case http.StatusNotFound:
				b.mu.Lock()
				b.answer = nil // an older server: activity unknown
				b.mu.Unlock()
			case http.StatusUpgradeRequired:
				// The server refuses this protocol. Never exit: the budget is
				// enforced locally and the server may be upgraded under us, so
				// the beat goes on at its interval; the activity is unknown
				// meanwhile and the line is logged once per distinct window.
				b.mu.Lock()
				b.answer = nil
				pair := [2]int{re.ServerProtocol, re.ServerMinProtocol}
				first := b.loggedRefusal != pair
				b.loggedRefusal = pair
				b.mu.Unlock()
				if first {
					b.logf("surveyor: the server refuses component protocol %d (it speaks %d, serves from %d): idle signal unknown until it is upgraded or this Surveyor is",
						componentproto.Protocol, re.ServerProtocol, re.ServerMinProtocol)
				}
			}
		}
		return
	}
	// An answer whose protocol is more than one below ours comes from a server
	// this Surveyor was not written for: behave as against an older server.
	if a.Protocol > 0 && !componentproto.Supports(componentproto.Protocol, a.Protocol) && a.Protocol < componentproto.Protocol {
		b.mu.Lock()
		first := !b.loggedBehind
		b.loggedBehind = true
		b.answer = nil
		b.mu.Unlock()
		if first {
			b.logf("surveyor: the server speaks component protocol %d, two or more below %d: idle signal unknown", a.Protocol, componentproto.Protocol)
		}
		return
	}
	b.mu.Lock()
	b.answer, b.answerAt = a, b.now()
	posted, gen := b.posted, b.postedGen
	skip := b.settingsSkip > 0
	if skip && s.Run != StateStopped {
		b.settingsSkip--
	}
	b.mu.Unlock()
	post := b.Settings != nil && s.Run != StateStopped && !skip && (!posted || a.SettingsWanted || b.sourcesGen() != gen)
	if post {
		b.postSettings()
	}
	if s.Run != StateStopped {
		b.postRuns(a)
	}
}

// sourcesGen is the generation of the sources now; 0 when there is no source reporter.
func (b *Beater) sourcesGen() uint64 {
	if b.Sources == nil {
		return 0
	}
	_, gen := b.Sources()
	return gen
}

// reportNow is the settings report with the sources as they are now and their
// generation. The report is a copy: Settings stays what the caller built.
func (b *Beater) reportNow() (*surveyorconfig.Report, uint64) {
	rep := *b.Settings
	var gen uint64
	if b.Sources != nil {
		rep.Sources, gen = b.Sources()
	}
	if rep.Sources == nil {
		rep.Sources = []surveyorconfig.Source{}
	}
	return &rep, gen
}

func (b *Beater) postSettings() {
	rep, gen := b.reportNow()
	if err := b.API.ReportSurveyorSettings(rep, beatCallTimeout); err != nil {
		var re *client.RemoteError
		if errors.As(err, &re) && re.HTTPStatus >= 400 && re.HTTPStatus < 500 {
			// An older server (404), one that refuses the protocol (426), one that
			// holds no writable KB or no declaration of ours (404 after a restart):
			// any of these may pass, so back off for a while and try again; the
			// line is logged once per status, until a post succeeds.
			b.mu.Lock()
			b.settingsSkip = runsBackoffBeats
			first := b.loggedSettingsStatus != re.HTTPStatus
			b.loggedSettingsStatus = re.HTTPStatus
			b.mu.Unlock()
			if first {
				b.logf("surveyor: settings report refused (%v): retried in %d beats", err, runsBackoffBeats)
			}
			return
		}
		b.logf("surveyor: settings report: %v", err)
		return
	}
	b.mu.Lock()
	b.posted, b.postedGen = true, gen
	b.settingsSkip, b.loggedSettingsStatus = 0, 0
	b.mu.Unlock()
}

// postRuns sends the runs the server has not acknowledged (all of the history
// when the answer says runs_wanted) and the open alarms when they changed. A
// failed post keeps everything for the next beat; the runs route never makes
// the beat fail.
func (b *Beater) postRuns(a *client.HeartbeatAnswer) {
	b.mu.Lock()
	if b.runsSkip > 0 {
		b.runsSkip--
		b.mu.Unlock()
		return
	}
	var batch []int
	for i, q := range b.runs {
		if !q.sent || a.RunsWanted {
			batch = append(batch, i)
		}
	}
	if len(batch) == 0 && !b.alarmsDirty && !(a.RunsWanted && len(b.alarms) > 0) {
		b.mu.Unlock()
		return
	}
	body := runsBody{Protocol: componentproto.Protocol, ID: b.Surveyor, Perimeter: b.Perimeter, Runs: []runsRun{}, Alarms: []runsAlarm{}}
	for _, i := range batch {
		r := b.runs[i].Run
		body.Runs = append(body.Runs, runsRun{RunID: r.ID, Task: r.Task, KB: r.KB, StartedAt: r.Started.UTC(),
			DurationMS: r.Duration.Milliseconds(), Outcome: r.Outcome, Error: r.Error, TokensSpent: r.Tokens})
	}
	for k, since := range b.alarms {
		body.Alarms = append(body.Alarms, runsAlarm{Code: k.code, KB: k.kb, Source: k.task, Since: since.UTC()})
	}
	sort.Slice(body.Alarms, func(i, j int) bool {
		x, y := body.Alarms[i], body.Alarms[j]
		return x.Code+x.KB+x.Source < y.Code+y.KB+y.Source
	})
	dirty := b.alarmsDirty
	b.mu.Unlock()
	if _, err := b.API.ReportRuns(body, beatCallTimeout); err != nil {
		var re *client.RemoteError
		if errors.As(err, &re) && re.HTTPStatus >= 400 && re.HTTPStatus < 500 {
			// An older server (404), one that refuses the protocol (426), or a
			// server that does not hold this declaration (it answers 404 then
			// too, e.g. after a restart): back off for a while, then try again.
			b.mu.Lock()
			b.runsSkip = runsBackoffBeats
			first := b.loggedRunsStatus != re.HTTPStatus
			b.loggedRunsStatus = re.HTTPStatus
			b.mu.Unlock()
			if first {
				b.logf("surveyor: runs report refused (%v): retried in %d beats", err, runsBackoffBeats)
			}
			return
		}
		b.logf("surveyor: runs report: %v", err)
		return
	}
	b.mu.Lock()
	b.runsSkip, b.loggedRunsStatus = 0, 0
	sentIDs := map[string]bool{}
	for _, r := range body.Runs {
		sentIDs[r.RunID] = true
	}
	for i := range b.runs {
		if sentIDs[b.runs[i].ID] {
			b.runs[i].sent = true
		}
	}
	if dirty {
		b.alarmsDirty = false
	}
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
