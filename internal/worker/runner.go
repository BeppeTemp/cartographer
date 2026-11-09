package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// IdleSource says how long a KB has been without human activity. known=false or
// an error means the idle trigger does not hold, never a guess: a worker against
// a server that does not report the signal is exactly window-only.
type IdleSource interface {
	IdleFor(ctx context.Context, kb string) (d time.Duration, known bool, err error)
}

// Heartbeat receives the runner's state.
type Heartbeat interface {
	Beat(ctx context.Context, s State)
}

// Run states a heartbeat reports.
const (
	StateIdle            = "idle"
	StateRunning         = "running"
	StateWaiting         = "waiting"
	StateBudgetExhausted = "budget_exhausted"
	StateStopped         = "stopped"
)

// State is what Beat carries.
type State struct {
	Run             string
	LastRun         LastRun
	NextWindowStart time.Time // zero without a window
	Budgets         []ProfileBudget
}

// LastRun summarises the newest finished run of this process.
type LastRun struct {
	At             time.Time
	Units, Commits int
}

// ProfileBudget is one profile's spending today against its daily limit.
type ProfileBudget struct {
	Profile    string
	SpentToday int
	Daily      int
}

// NoIdle is the only IdleSource until the server reports activity.
type NoIdle struct{}

// IdleFor never knows.
func (NoIdle) IdleFor(context.Context, string) (time.Duration, bool, error) { return 0, false, nil }

// NoHeartbeat is the only Heartbeat until the server has an endpoint for it.
type NoHeartbeat struct{}

// Beat does nothing.
func (NoHeartbeat) Beat(context.Context, State) {}

// maxConsecutiveErrors on one KB skip that KB until the next poll; it is also
// how a read-only grant shows up.
const maxConsecutiveErrors = 3

// RunnerOptions are the parts of a Runner that tests and the command inject.
type RunnerOptions struct {
	// Callers returns the MCP caller scoped to a KB.
	Callers KBCaller
	// CloseCallers is the same with a short HTTP timeout, used to write the
	// closing entries: client calls take no context, so shutdown must not wait
	// the 30 s of a normal client.
	CloseCallers KBCaller
	Models       map[string]Model // by profile name
	Idle         IdleSource       // NoIdle when nil
	Heartbeat    Heartbeat        // NoHeartbeat when nil
	Now          func() time.Time // time.Now when nil
	// Sleep waits d or until ctx ends (false). A timer when nil.
	Sleep func(ctx context.Context, d time.Duration) bool
	Logf  func(format string, args ...any)
}

// Runner is the stateless loop: it decides when to work, hands the work to the
// kinds and keeps the budget. It knows no task. Nothing is written to local disk.
type Runner struct {
	cfg       *Config
	opt       RunnerOptions
	instances []Instance // by cfg.Tasks index
	budget    *Budget
	replica   string
	last      LastRun
}

// NewRunner builds the runner of a loaded config: one Instance per tasks entry.
func NewRunner(cfg *Config, kinds *Kinds, opt RunnerOptions) (*Runner, error) {
	if opt.Idle == nil {
		opt.Idle = NoIdle{}
	}
	if opt.Heartbeat == nil {
		opt.Heartbeat = NoHeartbeat{}
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Sleep == nil {
		opt.Sleep = timerSleep
	}
	if opt.Logf == nil {
		opt.Logf = log.Printf
	}
	if opt.CloseCallers == nil {
		opt.CloseCallers = opt.Callers
	}
	id := make([]byte, 6)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	r := &Runner{cfg: cfg, opt: opt, replica: cfg.Name + "-" + hex.EncodeToString(id)}
	for i, t := range cfg.Tasks {
		k, ok := kinds.Lookup(t.Kind)
		if !ok || k.Build == nil {
			return nil, fmt.Errorf("tasks[%d]: kind %q has no execution half in this binary", i, t.Kind)
		}
		inst, err := k.Build(t.Options)
		if err != nil {
			return nil, fmt.Errorf("tasks[%d] (kind %q, line %d): %w", i, t.Kind, t.Line, err)
		}
		if b, ok := inst.(Binder); ok {
			prof, _ := cfg.Profile(t.Profile)
			b.Bind(cfg, prof)
		}
		if _, ok := opt.Models[t.Profile]; !ok {
			return nil, fmt.Errorf("tasks[%d]: no model built for profile %q", i, t.Profile)
		}
		r.instances = append(r.instances, inst)
	}
	var loc *time.Location
	if cfg.Window != nil {
		loc = cfg.Window.Location
	}
	r.budget = NewBudget(cfg.Name, cfg.Models, loc, opt.Callers)
	r.budget.CloseCallers = opt.CloseCallers
	r.budget.Logf = opt.Logf
	return r, nil
}

// Replica is the identity of this process: the perimeter name plus a random id
// made at start and never persisted. A restarted replica is a new claimant and
// its old leases lapse by themselves.
func (r *Runner) Replica() string { return r.replica }

func timerSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Exit codes of Run, as the command returns them.
const (
	ExitOK      = 0
	ExitRuntime = 1
)

// Run polls until ctx ends (graceful: 0) or, with once, evaluates the triggers a
// single time. A revoked token or a refused host returns ExitRuntime.
func (r *Runner) Run(ctx context.Context, once bool) int {
	poll := time.Duration(r.cfg.PollSeconds) * time.Second
	for {
		if ctx.Err() != nil {
			r.beat(StateStopped)
			return ExitOK
		}
		if code := r.tick(ctx); code != ExitOK {
			r.beat(StateStopped)
			return code
		}
		if once || ctx.Err() != nil {
			r.beat(StateStopped)
			return ExitOK
		}
		if !r.opt.Sleep(ctx, poll) {
			r.beat(StateStopped)
			return ExitOK
		}
	}
}

func (r *Runner) beat(state string) {
	s := State{Run: state, LastRun: r.last}
	now := r.opt.Now()
	if w := r.cfg.Window; w != nil {
		s.NextWindowStart = nextWindowStart(*w, now)
	}
	for _, p := range r.cfg.Models {
		s.Budgets = append(s.Budgets, ProfileBudget{Profile: p.Name, SpentToday: r.budget.SpentToday(p.Name), Daily: p.DailyTokens})
	}
	// The beat is not part of the run: a context that ended must not skip the last one.
	r.opt.Heartbeat.Beat(context.WithoutCancel(context.Background()), s)
}

// nextWindowStart is the first start of w strictly after now.
func nextWindowStart(w Window, now time.Time) time.Time {
	l := now.In(w.Location)
	for d := 0; d <= 2; d++ {
		c := time.Date(l.Year(), l.Month(), l.Day()+d, w.Start/60, w.Start%60, 0, 0, w.Location)
		if c.After(now) {
			return c
		}
	}
	return time.Time{}
}

// triggers returns, per KB, why it may be worked now ("window", "idle") or "".
func (r *Runner) trigger(ctx context.Context, kb string, now time.Time) string {
	if w := r.cfg.Window; w != nil && w.Contains(now) {
		return StoppedWindow
	}
	if r.cfg.IdleMinutes > 0 {
		d, known, err := r.opt.Idle.IdleFor(ctx, kb)
		if err == nil && known && d >= time.Duration(r.cfg.IdleMinutes)*time.Minute {
			return StoppedIdle
		}
	}
	return ""
}

// tick evaluates the triggers once and runs if one holds.
func (r *Runner) tick(ctx context.Context) int {
	now := r.opt.Now()
	var due []string
	for _, kb := range r.cfg.KBs {
		if r.trigger(ctx, kb, now) != "" {
			due = append(due, kb)
		}
	}
	if len(due) == 0 {
		r.beat(StateWaiting)
		return ExitOK
	}
	// Begin reads the ledger of every KB, and every poll where a trigger holds
	// pays that read even when the run then finds no grant or nothing to do: the
	// grants are only known from the ledger, so reading first is the cost of
	// failing closed (about one log_tail per KB per poll_seconds, only while a
	// window or idle trigger holds).
	if err := r.budget.Begin(r.cfg.KBs, now); err != nil {
		r.opt.Logf("worker: run skipped: %v", err)
		r.beat(StateWaiting)
		return ExitOK
	}
	runnable := false
	for _, t := range r.cfg.Tasks {
		if r.budget.Grant(t.Profile) > 0 {
			runnable = true
		}
	}
	if !runnable {
		r.beat(StateBudgetExhausted)
		return ExitOK
	}
	r.beat(StateRunning)
	return r.work(ctx, due)
}

// work is one run over the due KBs, in kbs order, then its closing entries.
func (r *Runner) work(ctx context.Context, due []string) int {
	var (
		stopped  = StoppedDone
		cutBy    string // window or idle ended on some KB
		budget   bool   // a task was skipped for want of grant
		errored  bool
		fatal    error
		disabled = map[string]bool{}                         // profiles whose credential the model API refused
		tried    = make([]map[string]bool, len(r.instances)) // per entry: kb + "\x00" + key
	)
	for i := range tried {
		tried[i] = map[string]bool{}
	}

kbs:
	for _, kb := range due {
		consecutive := 0
		broken := map[int]bool{} // entries whose Next failed on this KB
		for {
			if ctx.Err() != nil {
				break kbs
			}
			if consecutive >= maxConsecutiveErrors {
				r.opt.Logf("worker: %d consecutive errors on %s: skipped until the next poll", consecutive, kb)
				break
			}
			if r.trigger(ctx, kb, r.opt.Now()) == "" {
				// Finish the current task (done above), start no other.
				cutBy = StoppedIdle
				if r.cfg.Window != nil {
					cutBy = StoppedWindow
				}
				break
			}
			idx, item := -1, (*WorkItem)(nil)
			for i, t := range r.cfg.Tasks {
				if disabled[t.Profile] || broken[i] {
					continue
				}
				if r.budget.Grant(t.Profile) <= 0 {
					budget = true
					continue
				}
				it, err := r.instances[i].Next(ctx, kb, r.opt.Callers(kb))
				if err != nil {
					if ctx.Err() != nil {
						break kbs
					}
					if fatalErr(err) {
						fatal = err
						break kbs
					}
					r.opt.Logf("worker: %s task %d (%s) on %s: %v", r.cfg.Name, i, t.Kind, kb, err)
					broken[i] = true
					consecutive++
					errored = true
					continue
				}
				if it == nil || tried[i][kb+"\x00"+it.Key] {
					continue
				}
				idx, item = i, it
				break
			}
			if item == nil {
				break
			}
			t := r.cfg.Tasks[idx]
			tried[idx][kb+"\x00"+item.Key] = true
			r.budget.Touch(kb)
			prof, _ := r.cfg.Profile(t.Profile)
			res, err := r.instances[idx].Run(ctx, item, Runtime{
				KB: kb, Perimeter: r.cfg.Name, Replica: r.replica, Profile: prof,
				Model: r.opt.Models[t.Profile], Grant: r.budget.Grant(t.Profile), DailyLeft: r.budget.DailyLeft(t.Profile), Tools: r.opt.Callers(kb),
			})
			r.budget.Charge(kb, t.Profile, res.Tokens)
			r.budget.Note(kb, res.Units, res.Commits)
			r.budget.Summarize(kb, res.Summary)
			switch {
			case err == nil:
				consecutive = 0
			case ctx.Err() != nil:
				break kbs // abandoned on shutdown: its claim lapses by lease, nothing is rolled back
			case fatalErr(err):
				fatal = err
				break kbs
			case errors.Is(err, ErrModelAuth):
				disabled[t.Profile] = true
				r.opt.Logf("worker: profile %q: the model API rejected the credential; its tasks are off until the next run", t.Profile)
			default:
				consecutive++
				errored = true
				r.opt.Logf("worker: %s task %d (%s) on %s: %v", r.cfg.Name, idx, t.Kind, kb, err)
			}
		}
	}

	switch {
	case ctx.Err() != nil:
		stopped = StoppedShutdown
	case fatal != nil:
		stopped = StoppedError
	case budget:
		stopped = StoppedBudget
	case cutBy != "":
		stopped = cutBy
	case errored:
		stopped = StoppedError
	}
	// The fatal line names no KB's content, so every KB's entry may carry it.
	tail := ""
	if fatal != nil {
		tail = "stopped: " + oneLine(fatal.Error())
	}
	r.budget.Close(stopped, tail)
	r.last = LastRun{At: r.opt.Now(), Units: r.budget.totalUnits(), Commits: r.budget.totalCommits()}
	if fatal != nil {
		r.opt.Logf("worker: ending the run: %v", fatal)
		return ExitRuntime
	}
	return ExitOK
}

// fatalErr is an error that must end the process: a bad or revoked token (401)
// or a refused host (403). Looping on it would only hammer the server.
func fatalErr(err error) bool {
	var re *client.RemoteError
	return errors.As(err, &re) && (re.Code == client.CodeUnauthorized || re.HTTPStatus == 403)
}
