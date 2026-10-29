package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// sharedLog is a fake KB log: log_append puts the entry at the front with the
// path prefix the real server adds, log_tail renders it back like the real tool.
type sharedLog struct {
	mu      sync.Mutex
	entries []string
	tailErr error
	appends int
}

func (l *sharedLog) ListTools() ([]client.ToolInfo, error) { return nil, nil }

func (l *sharedLog) CallTool(name string, args any) (client.ToolOutput, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := args.(map[string]any)
	switch name {
	case "log_tail":
		if l.tailErr != nil {
			return client.ToolOutput{}, l.tailErr
		}
		if len(l.entries) == 0 {
			return client.ToolOutput{Text: `{"entries": 0}`}, nil
		}
		var sb strings.Builder
		for i, e := range l.entries {
			if i >= a["n"].(int) {
				break
			}
			fmt.Fprintf(&sb, "## 2026-10-29T10:00:00Z\n\n%s\n\n", e)
		}
		return client.ToolOutput{Text: sb.String()}, nil
	case "log_append":
		l.appends++
		l.entries = append([]string{"[" + a["path"].(string) + "] " + a["entry"].(string)}, l.entries...)
		return client.ToolOutput{Text: "ok"}, nil
	}
	return client.ToolOutput{}, errors.New("unexpected tool " + name)
}

func (l *sharedLog) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return ""
	}
	return l.entries[0]
}

func (l *sharedLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.appends
}

// fakeClock is advanced by Sleep, so a run never really waits.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// noopKind is the test-only kind: it runs the items it is scripted with, each
// costing cost tokens. Production code registers no kind in this change.
type noopKind struct {
	mu     sync.Mutex
	items  []string // keys not yet done
	cost   int
	runErr error
	nextFn func() (*WorkItem, error) // overrides items when set
	ran    []string
	onRun  func(ctx context.Context, rt Runtime) error
}

func (k *noopKind) Next(_ context.Context, _ string, _ ToolCaller) (*WorkItem, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.nextFn != nil {
		return k.nextFn()
	}
	if len(k.items) == 0 {
		return nil, nil
	}
	return &WorkItem{Key: k.items[0]}, nil
}

func (k *noopKind) Run(ctx context.Context, it *WorkItem, rt Runtime) (Result, error) {
	k.mu.Lock()
	k.ran = append(k.ran, it.Key+"@"+rt.Profile.Name)
	if len(k.items) > 0 && k.items[0] == it.Key {
		k.items = k.items[1:]
	}
	k.mu.Unlock()
	if k.onRun != nil {
		if err := k.onRun(ctx, rt); err != nil {
			return Result{Tokens: k.cost}, err
		}
	}
	return Result{Tokens: k.cost, Units: 1, Commits: 1, Summary: "did " + it.Key}, k.runErr
}

type runnerFixture struct {
	r     *Runner
	log   *sharedLog
	clock *fakeClock
	logs  []string
}

var testProfiles = []Profile{
	{Name: "cloud", DailyTokens: 1000, RunTokens: 400},
	{Name: "local", DailyTokens: 100000, RunTokens: 100000},
}

// newFixture builds a runner over one KB ("kb-a"); tasks[i] is the profile of
// entry i, whose instance is kinds[i]. The window covers the whole day unless mod
// changes it.
func newFixture(t *testing.T, tasks []string, kinds []*noopKind, mod func(*Config, *RunnerOptions)) *runnerFixture {
	t.Helper()
	f := &runnerFixture{log: &sharedLog{}, clock: &fakeClock{t: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}}
	cfg := &Config{
		Name: "docs-night", KBs: []string{"kb-a"}, Models: testProfiles, PollSeconds: 60,
		Window: &Window{Start: 0, End: 1439, Location: time.UTC},
	}
	var reg []Kind
	for i, p := range tasks {
		name := fmt.Sprintf("noop%d", i)
		k := kinds[i]
		reg = append(reg, Kind{Name: name, Build: func(map[string]any) (Instance, error) { return k, nil }})
		cfg.Tasks = append(cfg.Tasks, TaskSpec{Kind: name, Profile: p, Options: map[string]any{}, Line: i + 1})
	}
	opt := RunnerOptions{
		Callers: func(string) ToolCaller { return f.log },
		Models:  map[string]Model{"cloud": nil, "local": nil},
		Now:     f.clock.Now,
		Sleep: func(ctx context.Context, d time.Duration) bool {
			f.clock.set(f.clock.Now().Add(d))
			return ctx.Err() == nil
		},
		Logf: func(format string, args ...any) { f.logs = append(f.logs, fmt.Sprintf(format, args...)) },
	}
	if mod != nil {
		mod(cfg, &opt)
	}
	r, err := NewRunner(cfg, NewKinds(reg...), opt)
	if err != nil {
		t.Fatal(err)
	}
	f.r = r
	return f
}

func TestRunnerNoopKindEndToEnd(t *testing.T) {
	k := &noopKind{items: []string{"u1", "u2", "u3"}, cost: 100}
	f := newFixture(t, []string{"cloud"}, []*noopKind{k}, func(c *Config, _ *RunnerOptions) {
		c.Models = []Profile{{Name: "cloud", DailyTokens: 5000, RunTokens: 5000}, testProfiles[1]}
	})
	if code := f.r.Run(context.Background(), true); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if got := strings.Join(k.ran, ","); got != "u1@cloud,u2@cloud,u3@cloud" {
		t.Errorf("ran %s", got)
	}
	e := f.log.last()
	for _, want := range []string{"[worker/docs-night] run ", "day=2026-10-29", "tokens.cloud=300", "units=3", "commits=3", "stopped=done", " — did u1; did u2; did u3"} {
		if !strings.Contains(e, want) {
			t.Errorf("entry %q lacks %q", e, want)
		}
	}
	// A run that found nothing writes nothing: no entry per minute.
	before := f.log.count()
	f.r.Run(context.Background(), true)
	if f.log.count() != before {
		t.Errorf("an empty run wrote %d entries", f.log.count()-before)
	}
}

func TestRunnerWindowAcrossMidnightDSTAndOutside(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip(err)
	}
	k := &noopKind{items: []string{"u1"}, cost: 10}
	f := newFixture(t, []string{"local"}, []*noopKind{k}, func(c *Config, _ *RunnerOptions) {
		c.Window = &Window{Start: 23 * 60, End: 6 * 60, Location: rome}
	})
	// 2026-10-25 is the day Rome leaves DST; 02:30 local happens twice.
	for _, tc := range []struct {
		utc  time.Time
		want bool
	}{
		{time.Date(2026, 10, 24, 20, 59, 0, 0, time.UTC), false}, // 22:59 CEST
		{time.Date(2026, 10, 24, 21, 0, 0, 0, time.UTC), true},   // 23:00 CEST
		{time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), true},   // 02:30 CEST
		{time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), true},   // 02:30 CET, the repeated hour
		{time.Date(2026, 10, 25, 4, 59, 0, 0, time.UTC), true},   // 05:59 CET
		{time.Date(2026, 10, 25, 5, 0, 0, 0, time.UTC), false},   // 06:00 CET, end excluded
	} {
		if got := f.r.cfg.Window.Contains(tc.utc); got != tc.want {
			t.Errorf("%s: in window = %v, want %v", tc.utc, got, tc.want)
		}
	}
	f.clock.set(time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC))
	f.r.Run(context.Background(), true)
	if len(k.ran) != 0 || f.log.count() != 0 {
		t.Errorf("worked outside the window: %v", k.ran)
	}
	f.clock.set(time.Date(2026, 10, 24, 21, 30, 0, 0, time.UTC))
	f.r.Run(context.Background(), true)
	if len(k.ran) != 1 {
		t.Errorf("did not work in the window: %v", k.ran)
	}
}

type fakeIdle struct {
	d     time.Duration
	known bool
	err   error
}

func (i fakeIdle) IdleFor(context.Context, string) (time.Duration, bool, error) {
	return i.d, i.known, i.err
}

func TestRunnerIdleTrigger(t *testing.T) {
	for name, tc := range map[string]struct {
		idle fakeIdle
		ran  int
	}{
		"idle long enough":       {fakeIdle{d: 45 * time.Minute, known: true}, 1},
		"not idle enough":        {fakeIdle{d: 5 * time.Minute, known: true}, 0},
		"unknown is window-only": {fakeIdle{d: 24 * time.Hour, known: false}, 0},
		"error is no idle":       {fakeIdle{d: 24 * time.Hour, known: true, err: errors.New("x")}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			k := &noopKind{items: []string{"u1"}, cost: 10}
			f := newFixture(t, []string{"local"}, []*noopKind{k}, func(c *Config, o *RunnerOptions) {
				c.Window = nil
				c.IdleMinutes = 30
				o.Idle = tc.idle
			})
			f.r.Run(context.Background(), true)
			if len(k.ran) != tc.ran {
				t.Errorf("ran %d, want %d", len(k.ran), tc.ran)
			}
		})
	}
}

func TestRunnerPriorityPreemptsBetweenTasks(t *testing.T) {
	hi := &noopKind{cost: 10}
	lo := &noopKind{items: []string{"l1", "l2"}, cost: 10}
	released := false
	hi.nextFn = func() (*WorkItem, error) {
		if released && len(hi.ran) == 0 {
			return &WorkItem{Key: "h1"}, nil
		}
		return nil, nil
	}
	lo.onRun = func(context.Context, Runtime) error { released = true; return nil } // after l1 the higher entry becomes runnable
	f := newFixture(t, []string{"local", "local"}, []*noopKind{hi, lo}, nil)
	f.r.Run(context.Background(), true)
	// l1 runs, then the walk restarts from the first entry: h1 runs before l2.
	if len(lo.ran) != 2 || len(hi.ran) != 1 {
		t.Fatalf("lo=%v hi=%v", lo.ran, hi.ran)
	}
}

func TestRunnerBudgetPerProfileDoesNotStopAnother(t *testing.T) {
	cloud := &noopKind{items: []string{"c1", "c2"}, cost: 450}
	local := &noopKind{items: []string{"a", "b"}, cost: 50}
	f := newFixture(t, []string{"cloud", "local"}, []*noopKind{cloud, local}, nil)
	f.r.Run(context.Background(), true)
	if len(cloud.ran) != 1 {
		t.Errorf("cloud ran %v: the run cap of 400 is passed after one task", cloud.ran)
	}
	if len(local.ran) != 2 {
		t.Errorf("local ran %v: a spent cloud budget must not stop it", local.ran)
	}
	e := f.log.last()
	if !strings.Contains(e, "tokens.cloud=450") || !strings.Contains(e, "tokens.local=100") || !strings.Contains(e, "stopped=budget") {
		t.Errorf("entry %q", e)
	}
}

func TestRunnerSameKindOnTwoProfilesAreTwoInstances(t *testing.T) {
	a := &noopKind{items: []string{"a"}, cost: 1}
	b := &noopKind{items: []string{"b"}, cost: 1}
	f := newFixture(t, []string{"cloud", "local"}, []*noopKind{a, b}, nil)
	f.r.Run(context.Background(), true)
	if strings.Join(a.ran, "") != "a@cloud" || strings.Join(b.ran, "") != "b@local" {
		t.Errorf("a=%v b=%v", a.ran, b.ran)
	}
}

func TestRunnerHandsTheGrantAndIdentityToTheKind(t *testing.T) {
	k := &noopKind{items: []string{"u"}, cost: 1}
	var grant int
	var replica string
	k.onRun = func(_ context.Context, rt Runtime) error { grant, replica = rt.Grant, rt.Replica; return nil }
	f := newFixture(t, []string{"cloud"}, []*noopKind{k}, nil)
	f.r.Run(context.Background(), true)
	if grant != 400 {
		t.Errorf("grant %d, want min(1000, 400)", grant)
	}
	if !strings.HasPrefix(replica, "docs-night-") || replica != f.r.Replica() {
		t.Errorf("replica %q", replica)
	}
}

func TestRunnerModelAuthDisablesOneProfile(t *testing.T) {
	cloud := &noopKind{items: []string{"c1", "c2"}, cost: 5, runErr: fmt.Errorf("%w: 401", ErrModelAuth)}
	local := &noopKind{items: []string{"a"}, cost: 5}
	f := newFixture(t, []string{"cloud", "local"}, []*noopKind{cloud, local}, nil)
	f.r.Run(context.Background(), true)
	if len(cloud.ran) != 1 || len(local.ran) != 1 {
		t.Errorf("cloud %v local %v", cloud.ran, local.ran)
	}
}

func TestRunnerThreeConsecutiveErrorsSkipTheKB(t *testing.T) {
	k := &noopKind{cost: 1, runErr: errors.New("write refused")}
	n := 0
	k.nextFn = func() (*WorkItem, error) { n++; return &WorkItem{Key: fmt.Sprint("u", n)}, nil }
	f := newFixture(t, []string{"local"}, []*noopKind{k}, nil)
	if code := f.r.Run(context.Background(), true); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if len(k.ran) != 3 {
		t.Errorf("ran %v, want 3 attempts then the KB is skipped", k.ran)
	}
	if !strings.Contains(f.log.last(), "stopped=error") {
		t.Errorf("entry %q", f.log.last())
	}
}

func TestRunnerFailingTaskIsNotRetriedInTheSameRun(t *testing.T) {
	k := &noopKind{cost: 1, runErr: errors.New("boom")}
	k.nextFn = func() (*WorkItem, error) { return &WorkItem{Key: "poison"}, nil }
	f := newFixture(t, []string{"local"}, []*noopKind{k}, nil)
	f.r.Run(context.Background(), true)
	if len(k.ran) != 1 {
		t.Errorf("ran %v", k.ran)
	}
}

func TestRunnerUnauthorizedEndsTheRun(t *testing.T) {
	for name, err := range map[string]error{
		"401": &client.RemoteError{Code: client.CodeUnauthorized},
		"403": &client.RemoteError{Code: client.CodeHTTPFailed, HTTPStatus: 403},
	} {
		t.Run(name, func(t *testing.T) {
			k := &noopKind{items: []string{"a", "b"}, cost: 3, runErr: err}
			f := newFixture(t, []string{"local"}, []*noopKind{k}, nil)
			if code := f.r.Run(context.Background(), false); code != ExitRuntime {
				t.Errorf("exit %d, want %d", code, ExitRuntime)
			}
			if len(k.ran) != 1 || !strings.Contains(f.log.last(), "stopped=error") {
				t.Errorf("ran %v entry %q", k.ran, f.log.last())
			}
		})
	}
}

func TestRunnerLedgerFailureSkipsTheRun(t *testing.T) {
	k := &noopKind{items: []string{"a"}, cost: 1}
	f := newFixture(t, []string{"local"}, []*noopKind{k}, nil)
	f.log.tailErr = errors.New("unreachable")
	if code := f.r.Run(context.Background(), true); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if len(k.ran) != 0 || f.log.count() != 0 {
		t.Errorf("spent without reading the ledger: %v", k.ran)
	}
}

func TestRunnerShutdownMidTaskWritesTheEntryThroughTheCloseCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	k := &noopKind{items: []string{"a", "b"}, cost: 7}
	k.onRun = func(ctx context.Context, _ Runtime) error { cancel(); <-ctx.Done(); return ctx.Err() }
	closeLog := &sharedLog{}
	f := newFixture(t, []string{"local"}, []*noopKind{k}, func(_ *Config, o *RunnerOptions) {
		o.CloseCallers = func(string) ToolCaller { return closeLog }
	})
	if code := f.r.Run(ctx, false); code != ExitOK {
		t.Fatalf("exit %d, want 0 on a graceful shutdown", code)
	}
	if len(k.ran) != 1 {
		t.Errorf("ran %v: no task starts after the signal", k.ran)
	}
	if e := closeLog.last(); !strings.Contains(e, "stopped=shutdown") || !strings.Contains(e, "tokens.local=7") {
		t.Errorf("closing entry %q", e)
	}
	if f.log.count() != 0 {
		t.Error("the closing entry must go through the short-timeout caller")
	}
}

func TestRunnerPollsUntilShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	polls := 0
	k := &noopKind{cost: 1}
	f := newFixture(t, []string{"local"}, []*noopKind{k}, func(c *Config, o *RunnerOptions) {
		c.Window = nil
		c.IdleMinutes = 1
		o.Sleep = func(ctx context.Context, d time.Duration) bool {
			if d != 60*time.Second {
				t.Errorf("poll %v", d)
			}
			if polls++; polls == 3 {
				cancel()
			}
			return ctx.Err() == nil
		}
	})
	if code := f.r.Run(ctx, false); code != ExitOK || polls != 3 {
		t.Errorf("exit %d after %d polls", code, polls)
	}
}

func TestRunnerTwoReplicasStayWithinTheBound(t *testing.T) {
	shared := &sharedLog{}
	var kinds []*noopKind
	var runners []*Runner
	for i := 0; i < 2; i++ {
		k := &noopKind{cost: 300}
		k.nextFn = func() (*WorkItem, error) { return &WorkItem{Key: fmt.Sprint("u", len(k.ran))}, nil }
		kinds = append(kinds, k)
		f := newFixture(t, []string{"cloud"}, []*noopKind{k}, func(_ *Config, o *RunnerOptions) {
			o.Callers = func(string) ToolCaller { return shared }
		})
		runners = append(runners, f.r)
	}
	var wg sync.WaitGroup
	for _, r := range runners {
		wg.Add(1)
		go func() { defer wg.Done(); r.Run(context.Background(), true) }()
	}
	wg.Wait()
	spent := 0
	for _, k := range kinds {
		spent += len(k.ran) * 300
	}
	// remaining (1000) + replicas x run_tokens (400) for the profile.
	if spent > 1000+2*400 {
		t.Errorf("spent %d", spent)
	}
}

func TestNextWindowStart(t *testing.T) {
	w := Window{Start: 23 * 60, End: 6 * 60, Location: time.UTC}
	got := nextWindowStart(w, time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 10, 29, 23, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("%v", got)
	}
	got = nextWindowStart(w, time.Date(2026, 10, 29, 23, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 10, 30, 23, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("%v", got)
	}
}

func TestRunnerRefusesAKindWithoutExecutionHalf(t *testing.T) {
	cfg := &Config{Name: "p", Tasks: []TaskSpec{{Kind: "x", Profile: "cloud"}}}
	_, err := NewRunner(cfg, NewKinds(Kind{Name: "x"}), RunnerOptions{Models: map[string]Model{"cloud": nil}})
	if err == nil || !strings.Contains(err.Error(), "no execution half") {
		t.Errorf("err %v", err)
	}
}

// The runner's own output names counts and ids, never the content of a result.
func TestRunnerOutputCarriesNoSecretMarker(t *testing.T) {
	k := &noopKind{items: []string{"a"}, cost: 1, runErr: errors.New("write refused")}
	f := newFixture(t, []string{"local"}, []*noopKind{k}, func(c *Config, _ *RunnerOptions) {
		c.Models = []Profile{testProfiles[0], {Name: "local", DailyTokens: 100000, RunTokens: 100000}}
	})
	f.r.Run(context.Background(), true)
	if all := strings.Join(f.logs, "\n") + f.log.last(); strings.Contains(all, cfgCanary) {
		t.Errorf("secret marker in output: %s", all)
	}
}
