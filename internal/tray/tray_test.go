package tray

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/png"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
)

func boolp(b bool) *bool { return &b }

func TestClassify(t *testing.T) {
	installed := service.Status{Installed: true, UIURL: "http://127.0.0.1:39273/ui/"}
	two := []client.HealthKB{{Name: "kb-a"}, {Name: "work-kb"}}
	refused := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnreachable}
	starting := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeStarting}
	notFound := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeHTTPFailed, HTTPStatus: http.StatusNotFound}
	unauth := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnauthorized}

	cases := []struct {
		name   string
		health *client.Health
		err    error
		status service.Status
		want   Snapshot
	}{
		{"healthy", &client.Health{Version: "1.2.3", Ready: boolp(true), KBs: &two}, nil, installed,
			Snapshot{State: StateRunning, Version: "1.2.3", KBCount: 2, KBNames: "kb-a\nwork-kb", UIURL: installed.UIURL, Installed: true}},
		{"older server without kbs", &client.Health{Version: "1.0.0"}, nil, installed,
			Snapshot{State: StateRunning, Version: "1.0.0", KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"ready false", &client.Health{Version: "1", Ready: boolp(false)}, nil, installed,
			Snapshot{State: StateStarting, Version: "1", KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"bootstrapping", nil, starting, installed,
			Snapshot{State: StateStarting, KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"connection refused", nil, refused, installed,
			Snapshot{State: StateStopped, KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"404", nil, notFound, installed, Snapshot{State: StateStopped, KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"401 cannot happen, is stopped", nil, unauth, installed,
			Snapshot{State: StateStopped, KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"plain error", nil, errors.New("boom"), installed,
			Snapshot{State: StateStopped, KBCount: -1, UIURL: installed.UIURL, Installed: true}},
		{"not installed and silent", nil, refused, service.Status{},
			Snapshot{State: StateNotInstalled, KBCount: -1}},
		{"answers but not installed: a hand-run server is running", &client.Health{Version: "1", Ready: boolp(true)}, nil, service.Status{},
			Snapshot{State: StateRunning, Version: "1", KBCount: -1}},
		{"latest_version rides along", &client.Health{Version: "1", LatestVersion: "2.0.0", Ready: boolp(true)}, nil, installed,
			Snapshot{State: StateRunning, Version: "1", LatestVersion: "2.0.0", KBCount: -1, UIURL: installed.UIURL, Installed: true}},
	}
	for _, c := range cases {
		if got := Classify(c.health, c.err, c.status); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestStatusLine(t *testing.T) {
	cases := []struct {
		s    Snapshot
		want string
	}{
		{Snapshot{State: StateRunning, Version: "0.9.1", KBCount: 3}, "Cartographer 0.9.1 — 3 KBs"},
		{Snapshot{State: StateRunning, Version: "0.9.1", KBCount: 1}, "Cartographer 0.9.1 — 1 KB"},
		{Snapshot{State: StateRunning, Version: "0.9.1", KBCount: -1}, "Cartographer 0.9.1"},
		{Snapshot{State: StateRunning, Version: "0.9.1", KBCount: 0, LatestVersion: "1.0.0"}, "Cartographer 0.9.1 — 0 KBs — update available: v1.0.0"},
		{Snapshot{State: StateRunning, Version: "0.9.1", KBCount: -1, LatestVersion: "v1.0.0"}, "Cartographer 0.9.1 — update available: v1.0.0"},
		{Snapshot{State: StateStopped, KBCount: -1}, "stopped"},
		{Snapshot{State: StateStarting, KBCount: -1}, "starting…"},
		{Snapshot{State: StateNotInstalled, KBCount: -1}, "not installed"},
		{Snapshot{State: StateRunning, Version: "1", Restarting: true}, "restarting…"},
	}
	for _, c := range cases {
		if got := StatusLine(c.s); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.s, got, c.want)
		}
	}
}

// fakeEnv drives Run by hand: the test sends ticks and reads emitted snapshots.
type fakeEnv struct {
	mu     sync.Mutex
	status service.Status
	sErr   error
	health *client.Health
	pErr   error
	probed []string
	ticks  chan time.Time
}

func (f *fakeEnv) deps() Deps {
	return Deps{
		Status: func() (service.Status, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.status, f.sErr
		},
		Probe: func(addr string) (*client.Health, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.probed = append(f.probed, addr)
			return f.health, f.pErr
		},
		NewTicker: func(time.Duration) (<-chan time.Time, func()) { return f.ticks, func() {} },
		LogPath:   "/logs/server.log",
	}
}

func (f *fakeEnv) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

func TestRunEmitsOnChangeOnly(t *testing.T) {
	f := &fakeEnv{
		status: service.Status{Installed: true, HTTPAddr: "127.0.0.1:1111"},
		health: &client.Health{Version: "1", Ready: boolp(true)},
		ticks:  make(chan time.Time),
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Snapshot, 16)
	done := make(chan struct{})
	go func() { Run(ctx, f.deps(), 0, func(s Snapshot) { out <- s }); close(done) }()

	first := <-out
	if first.State != StateRunning || first.LogPath != "/logs/server.log" {
		t.Fatalf("first snapshot %+v", first)
	}
	f.ticks <- time.Now() // unchanged: nothing emitted
	f.set(func() { f.health, f.pErr = nil, &client.RemoteError{Code: client.CodeUnreachable} })
	f.ticks <- time.Now()
	if got := <-out; got.State != StateStopped {
		t.Fatalf("after failure: %+v", got)
	}
	// The service is reinstalled with another address between ticks.
	f.set(func() {
		f.status.HTTPAddr = "127.0.0.1:2222"
		f.health, f.pErr = &client.Health{Version: "1", Ready: boolp(true)}, nil
	})
	f.ticks <- time.Now()
	if got := <-out; got.State != StateRunning {
		t.Fatalf("after recovery: %+v", got)
	}
	cancel()
	<-done
	if len(out) != 0 {
		t.Errorf("%d unexpected extra snapshots", len(out))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if last := f.probed[len(f.probed)-1]; last != "127.0.0.1:2222" {
		t.Errorf("last probe went to %q, want the new address", last)
	}
}

func TestRunSurvivesFailingStatusAndProbe(t *testing.T) {
	f := &fakeEnv{sErr: errors.New("no definition"), ticks: make(chan time.Time)}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Snapshot, 8)
	done := make(chan struct{})
	d := f.deps()
	d.Logf = func(string, ...any) {}
	go func() { Run(ctx, d, time.Hour, func(s Snapshot) { out <- s }); close(done) }()
	// No good Status read yet: Stopped, not NotInstalled.
	if got := <-out; got.State != StateStopped {
		t.Errorf("status error with no earlier read: %+v", got)
	}
	f.set(func() {
		f.sErr = nil
		f.status = service.Status{Installed: true, HTTPAddr: "x"}
		f.health = &client.Health{Version: "1", Ready: boolp(true)}
	})
	f.ticks <- time.Now()
	if got := <-out; got.State != StateRunning {
		t.Errorf("healthy: %+v", got)
	}
	// A transient Status error reuses the last good Status: still running.
	f.set(func() { f.sErr = errors.New("powershell failed") })
	f.ticks <- time.Now()
	f.set(func() { f.sErr = nil; f.pErr = errors.New("bad") })
	f.ticks <- time.Now()
	got := <-out // the only change since Running is the probe failure
	if got.State != StateStopped || !got.Installed {
		t.Errorf("probe error after a transient status error: %+v", got)
	}
	f.set(func() { f.pErr = nil; f.health = nil }) // a nil health with no error is still stopped
	f.ticks <- time.Now()
	f.set(func() { f.status.HTTPAddr = "" }) // stdio transport: nothing to probe
	f.ticks <- time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}

func TestRunReflectsBusy(t *testing.T) {
	f := &fakeEnv{
		status: service.Status{Installed: true, HTTPAddr: "a:1"},
		health: &client.Health{Ready: boolp(true)},
		ticks:  make(chan time.Time),
	}
	var mu sync.Mutex
	busy := false
	d := f.deps()
	d.Busy = func() bool { mu.Lock(); defer mu.Unlock(); return busy }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan Snapshot, 4)
	go Run(ctx, d, 0, func(s Snapshot) { out <- s })
	<-out
	mu.Lock()
	busy = true
	mu.Unlock()
	f.ticks <- time.Now()
	if got := <-out; !got.Restarting {
		t.Errorf("busy not reflected: %+v", got)
	}
}

func TestLatestKeepsOnlyTheNewest(t *testing.T) {
	ch, emit, _ := Latest()
	emit(Snapshot{Version: "1"})
	emit(Snapshot{Version: "2"})
	emit(Snapshot{Version: "3"})
	if got := <-ch; got.Version != "3" {
		t.Errorf("got %q", got.Version)
	}
	select {
	case s := <-ch:
		t.Errorf("extra snapshot %+v", s)
	default:
	}
}

func alphaMask(state State, size int) []uint8 {
	img := Render(state, size, color.White)
	out := make([]uint8, 0, size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			out = append(out, img.NRGBAAt(x, y).A)
		}
	}
	return out
}

func TestRenderShapes(t *testing.T) {
	states := []State{StateRunning, StateStarting, StateStopped, StateNotInstalled}
	for _, size := range []int{16, 32, 36} {
		masks := map[State][]uint8{}
		for _, s := range states {
			img := Render(s, size, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
			if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
				t.Fatalf("%v: bounds %v", s, b)
			}
			if img.NRGBAAt(0, 0).A != 0 || img.NRGBAAt(size-1, 0).A != 0 {
				t.Errorf("%v/%d: corner not transparent", s, size)
			}
			opaque := 0
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					p := img.NRGBAAt(x, y)
					if p.A == 0 {
						continue
					}
					opaque++
					if p.R != 10 || p.G != 20 || p.B != 30 {
						t.Fatalf("%v/%d: pixel (%d,%d) = %+v, want the fg colour", s, size, x, y, p)
					}
				}
			}
			if opaque == 0 {
				t.Errorf("%v/%d: empty icon", s, size)
			}
			masks[s] = alphaMask(s, size)
		}
		for i, a := range states {
			for _, b := range states[i+1:] {
				if bytes.Equal(masks[a], masks[b]) {
					t.Errorf("size %d: %v and %v render the same", size, a, b)
				}
			}
		}
		// The C's stroke at its leftmost point, and the centre of the point.
		at := func(st State, x, y float64) uint8 {
			return Render(st, size, color.White).NRGBAAt(int(x*float64(size)), int(y*float64(size))).A
		}
		if at(StateRunning, arcX-arcR, arcY) != 255 {
			t.Errorf("size %d: running mark has no C stroke", size)
		}
		if at(StateRunning, dotX, dotY) != 255 {
			t.Errorf("size %d: running mark has no point", size)
		}
		if at(StateStarting, dotX, dotY) != 0 {
			t.Errorf("size %d: starting mark has its point, want it absent until ready", size)
		}
		if at(StateStopped, 0.5, 0.5) != 255 {
			t.Errorf("size %d: stopped mark has no slash through its centre", size)
		}
		// Not installed is dimmed: no pixel above the dimmed alpha.
		for _, a := range masks[StateNotInstalled] {
			if float64(a) > DimmedAlpha*255+1 {
				t.Errorf("size %d: not-installed pixel alpha %d above the dimmed level", size, a)
				break
			}
		}
		// Partial coverage exists (anti-aliasing) on the filled pin.
		partial := false
		for _, a := range masks[StateRunning] {
			if a > 0 && a < 255 {
				partial = true
			}
		}
		if !partial {
			t.Errorf("size %d: running pin has no anti-aliased edge", size)
		}
	}
}

func TestPNGDecodesToTheBlackIcon(t *testing.T) {
	for _, s := range []State{StateRunning, StateStarting, StateStopped, StateNotInstalled} {
		img, err := png.Decode(bytes.NewReader(PNG(s, 36)))
		if err != nil {
			t.Fatalf("%v: %v", s, err)
		}
		if b := img.Bounds(); b.Dx() != 36 || b.Dy() != 36 {
			t.Errorf("%v: bounds %v", s, b)
		}
		if s == StateRunning {
			x, y := (arcX-arcR)*36.0, arcY*36.0
			if _, _, _, a := img.At(int(x), int(y)).RGBA(); a == 0 {
				t.Errorf("running PNG is empty on the C stroke")
			}
		}
	}
}

func TestStateString(t *testing.T) {
	if StateStopped.String() != "stopped" || State(99).String() != "unknown" {
		t.Error("State.String")
	}
}

func TestRunRefreshesAgentsSparingly(t *testing.T) {
	f := &fakeEnv{
		status: service.Status{Installed: true, HTTPAddr: "127.0.0.1:1"},
		health: &client.Health{Version: "1", Ready: boolp(true)},
		ticks:  make(chan time.Time),
	}
	d := f.deps()
	var mu sync.Mutex
	reads := 0
	d.Agents = func() Agents {
		mu.Lock()
		defer mu.Unlock()
		reads++
		return Agents{Known: true, N: reads}
	}
	waitReads := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			n := reads
			mu.Unlock()
			if n == want {
				return
			}
			if n > want || time.Now().After(deadline) {
				t.Fatalf("agents read %d times, want %d", n, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Run(ctx, d, 0, func(Snapshot) {}); close(done) }() // interval 0 → MinInterval
	waitReads(1)                                                   // at start
	for i := 1; i < int(AgentsInterval/MinInterval); i++ {
		f.ticks <- time.Now()
	}
	waitReads(1) // not before AgentsInterval worth of polls
	f.ticks <- time.Now()
	waitReads(2)
	// The server goes away and comes back: the agents are read again at once.
	// A send returns once Run has the tick, before its poll ran: the second
	// send proves the first poll saw the server down.
	f.set(func() { f.health, f.pErr = nil, errors.New("down") })
	f.ticks <- time.Now()
	f.ticks <- time.Now()
	f.set(func() { f.health, f.pErr = &client.Health{Version: "1", Ready: boolp(true)}, nil })
	f.ticks <- time.Now()
	waitReads(3)
	cancel()
	<-done
}

// A permanent Status() error is logged once, not on every tick: again only
// when the message changes or after a success.
func TestSnapshotLogsARepeatedStatusErrorOnce(t *testing.T) {
	var logged []string
	f := &fakeEnv{sErr: errors.New("no usable --config"), ticks: make(chan time.Time)}
	d := f.deps()
	d.Logf = func(format string, args ...any) { logged = append(logged, format) }
	var good *service.Status
	var lastErr string

	for range 101 {
		d.snapshot(&good, &lastErr)
	}
	if len(logged) != 1 {
		t.Fatalf("101 ticks with one error logged %d lines, want 1", len(logged))
	}
	f.set(func() { f.sErr = errors.New("another error") })
	d.snapshot(&good, &lastErr)
	if len(logged) != 2 {
		t.Fatalf("a different error logged %d lines in total, want 2", len(logged))
	}
	f.set(func() { f.sErr = nil })
	d.snapshot(&good, &lastErr)
	f.set(func() { f.sErr = errors.New("another error") })
	d.snapshot(&good, &lastErr)
	if len(logged) != 3 {
		t.Fatalf("the same error after a success logged %d lines in total, want 3", len(logged))
	}
}
