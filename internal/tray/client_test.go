package tray

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/service"
)

const secretToken = "tok-secret-123"

func TestClassifyClient(t *testing.T) {
	two := []client.HealthKB{{Name: "kb-a"}, {Name: "work-kb"}}
	target := ClientTarget{Present: true, ServerURL: "https://example.com/mcp", Token: "x", Auth: true}
	ui := "https://example.com/ui/"
	starting := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeStarting}
	refused := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnreachable}
	fiveXX := &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeHTTPFailed, HTTPStatus: 503}
	base := Snapshot{Checked: true, Mode: ModeClient, Server: "example.com", UIURL: ui, KBCount: -1}
	with := func(f func(*Snapshot)) Snapshot { s := base; f(&s); return s }

	cases := []struct {
		name   string
		health *client.Health
		err    error
		target ClientTarget
		want   Snapshot
	}{
		{"starting", nil, starting, target, with(func(s *Snapshot) { s.State = StateStarting })},
		{"refused", nil, refused, target, with(func(s *Snapshot) { s.State = StateStopped })},
		{"5xx", nil, fiveXX, target, with(func(s *Snapshot) { s.State = StateStopped })},
		{"url refused", nil, errors.New("refusing http://example.com"), target, with(func(s *Snapshot) { s.State = StateStopped })},
		{"nil health", nil, nil, target, with(func(s *Snapshot) { s.State = StateStopped })},
		{"ok with KBs", &client.Health{Version: "1.2.3", Ready: boolp(true), KBs: &two}, nil, target,
			with(func(s *Snapshot) {
				s.State, s.Version, s.KBCount, s.KBNames = StateRunning, "1.2.3", 2, "kb-a\nwork-kb"
			})},
		{"ok without KBs", &client.Health{Version: "1"}, nil, target,
			with(func(s *Snapshot) { s.State, s.Version = StateRunning, "1" })},
		{"ready false", &client.Health{Version: "1", Ready: boolp(false)}, nil, target,
			with(func(s *Snapshot) { s.State, s.Version, s.NotReady = StateRunning, "1", true })},
		{"latest version", &client.Health{Version: "1", LatestVersion: "2.0.0"}, nil, target,
			with(func(s *Snapshot) { s.State, s.Version, s.LatestVersion = StateRunning, "1", "2.0.0" })},
		{"token missing", &client.Health{Version: "1"}, nil, ClientTarget{Present: true, ServerURL: "https://example.com/mcp", Auth: true},
			with(func(s *Snapshot) { s.State, s.Version, s.TokenMissing = StateRunning, "1", true })},
		{"no auth, no token is fine", &client.Health{Version: "1"}, nil, ClientTarget{Present: true, ServerURL: "https://example.com/mcp"},
			with(func(s *Snapshot) { s.State, s.Version = StateRunning, "1" })},
		{"401 is rejected, not unreachable", nil, &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeUnauthorized}, target,
			with(func(s *Snapshot) { s.State, s.Rejected = StateStopped, true })},
		{"403 is rejected", nil, &client.RemoteError{State: client.RemoteUnavailable, Code: client.CodeHTTPFailed, HTTPStatus: 403}, target,
			with(func(s *Snapshot) { s.State, s.Rejected = StateStopped, true })},
		{"newline in a KB name", &client.Health{Version: "1", KBs: &[]client.HealthKB{{Name: "kb-a\nforged"}}}, nil, target,
			with(func(s *Snapshot) { s.State = StateStopped })},
		{"control rune in the version", &client.Health{Version: "1\x1b[2J"}, nil, target,
			with(func(s *Snapshot) { s.State = StateStopped })},
		{"control rune in the latest version", &client.Health{Version: "1", LatestVersion: "2\r"}, nil, target,
			with(func(s *Snapshot) { s.State = StateStopped })},
		{"path prefix", &client.Health{Version: "1"}, nil, ClientTarget{Present: true, ServerURL: "https://example.com:8443/prefix/mcp"},
			with(func(s *Snapshot) {
				s.State, s.Version, s.Server, s.UIURL = StateRunning, "1", "example.com:8443", "https://example.com:8443/prefix/ui/"
			})},
	}
	for _, c := range cases {
		if got := ClassifyClient(c.health, c.err, c.target); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// clientEnv drives snapshot by hand with a local and a client side.
type clientEnv struct {
	mu          sync.Mutex
	status      service.Status
	target      ClientTarget
	targetErr   error
	clientCalls int
	probeCalls  int
	localProbed []string
	clientProbe []ClientTarget
	probeErr    error
	logs        []string
}

func (f *clientEnv) deps() Deps {
	return Deps{
		Status: func() (service.Status, error) { f.mu.Lock(); defer f.mu.Unlock(); return f.status, nil },
		Probe: func(addr string) (*client.Health, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.localProbed = append(f.localProbed, addr)
			return &client.Health{Version: "local"}, nil
		},
		Client: func() (ClientTarget, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.clientCalls++
			return f.target, f.targetErr
		},
		ProbeClient: func(t ClientTarget) (*client.Health, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.probeCalls++
			f.clientProbe = append(f.clientProbe, t)
			if f.probeErr != nil {
				return nil, f.probeErr
			}
			return &client.Health{Version: "remote", Ready: boolp(true)}, nil
		},
		Logf: func(format string, args ...any) {
			f.mu.Lock()
			f.logs = append(f.logs, fmt.Sprintf(format, args...))
			f.mu.Unlock()
		},
		LogPath: "/logs/server.log",
	}
}

func snap(d Deps) (Snapshot, *clientMemo) {
	var good *service.Status
	var lastErr string
	cm := &clientMemo{}
	return d.snapshot(&good, &lastErr, cm), cm
}

func TestSnapshotLocalServiceNeverTouchesTheClient(t *testing.T) {
	f := &clientEnv{status: service.Status{Installed: true, HTTPAddr: "127.0.0.1:1"},
		target: ClientTarget{Present: true, ServerURL: "https://example.com/mcp"}}
	s, _ := snap(f.deps())
	if s.Mode != ModeLocal || s.Version != "local" || s.LogPath != "/logs/server.log" || !s.Installed {
		t.Errorf("local snapshot = %+v", s)
	}
	if f.clientCalls != 0 || f.probeCalls != 0 || !reflect.DeepEqual(f.localProbed, []string{"127.0.0.1:1"}) {
		t.Errorf("client calls %d, client probes %d, local probes %v", f.clientCalls, f.probeCalls, f.localProbed)
	}
}

func TestSnapshotForceLocalIgnoresTheClient(t *testing.T) {
	f := &clientEnv{status: service.Status{HTTPAddr: "127.0.0.1:2"}, target: ClientTarget{Present: true, ServerURL: "https://example.com/mcp"}}
	d := f.deps()
	d.ForceLocal = true
	s, _ := snap(d)
	if s.Mode != ModeLocal || f.clientCalls != 0 || f.probeCalls != 0 || len(f.localProbed) != 1 {
		t.Errorf("forced local: %+v client calls %d probes %d local %v", s, f.clientCalls, f.probeCalls, f.localProbed)
	}
}

func TestSnapshotNoServiceWithTargetProbesOnlyTheClient(t *testing.T) {
	f := &clientEnv{status: service.Status{HTTPAddr: "127.0.0.1:2"},
		target: ClientTarget{Present: true, ServerURL: "https://example.com/mcp", Token: secretToken, Auth: true}}
	s, _ := snap(f.deps())
	if s.Mode != ModeClient || s.State != StateRunning || s.Version != "remote" || s.Server != "example.com" ||
		s.UIURL != "https://example.com/ui/" || s.LogPath != "" || s.Installed {
		t.Errorf("client snapshot = %+v", s)
	}
	if len(f.localProbed) != 0 || f.probeCalls != 1 {
		t.Errorf("local probes %v, client probes %d", f.localProbed, f.probeCalls)
	}
}

func TestSnapshotNoServiceNoClientFileKeepsTheLocalPath(t *testing.T) {
	f := &clientEnv{status: service.Status{HTTPAddr: "127.0.0.1:3"}}
	s, _ := snap(f.deps())
	if s.Mode != ModeLocal || s.State != StateRunning || s.Version != "local" {
		t.Errorf("snapshot = %+v", s)
	}
	if f.clientCalls != 1 || f.probeCalls != 0 || !reflect.DeepEqual(f.localProbed, []string{"127.0.0.1:3"}) {
		t.Errorf("client calls %d, client probes %d, local probes %v", f.clientCalls, f.probeCalls, f.localProbed)
	}
}

func TestSnapshotClientErrorKeepsTheLastGoodTargetAndLogsOnce(t *testing.T) {
	f := &clientEnv{target: ClientTarget{Present: true, ServerURL: "https://example.com/mcp"}}
	d := f.deps()
	var good *service.Status
	var lastErr string
	cm := &clientMemo{}
	if s := d.snapshot(&good, &lastErr, cm); s.State != StateRunning {
		t.Fatalf("first = %+v", s)
	}
	f.mu.Lock()
	f.targetErr = errors.New("clientconfig: parse: bad yaml")
	f.mu.Unlock()
	for range 5 {
		if s := d.snapshot(&good, &lastErr, cm); s.Mode != ModeClient || s.State != StateRunning || s.Server != "example.com" {
			t.Fatalf("with the last good target: %+v", s)
		}
	}
	if len(f.logs) != 1 {
		t.Errorf("logged %d lines, want 1: %q", len(f.logs), f.logs)
	}
	// With no good target yet: Stopped in client mode.
	s, _ := snap(f.deps())
	if s.Mode != ModeClient || s.State != StateStopped {
		t.Errorf("no good target: %+v", s)
	}
}

func TestSnapshotRefusedServerURLStopsAndNeverLogsTheToken(t *testing.T) {
	f := &clientEnv{
		target:   ClientTarget{Present: true, ServerURL: "http://remote.example.com/mcp", Token: secretToken, Auth: true},
		probeErr: client.CheckServerURL("http://remote.example.com/mcp", false),
	}
	d := f.deps()
	var good *service.Status
	var lastErr string
	cm := &clientMemo{}
	var s Snapshot
	for range 4 {
		s = d.snapshot(&good, &lastErr, cm)
	}
	if s.State != StateStopped || s.Mode != ModeClient {
		t.Errorf("snapshot = %+v", s)
	}
	if len(f.logs) != 1 {
		t.Errorf("logged %d lines, want 1: %q", len(f.logs), f.logs)
	}
	dump := strings.Join(f.logs, "\n") + fmt.Sprintf("%+v %#v", s, s)
	if strings.Contains(dump, secretToken) {
		t.Errorf("the token leaked into %q", dump)
	}
}

func TestClientTargetNeverPrintsTheToken(t *testing.T) {
	tg := ClientTarget{Present: true, ServerURL: "https://u:p@example.com/mcp?x=1", Token: secretToken, Auth: true}
	for _, out := range []string{fmt.Sprint(tg), fmt.Sprintf("%v %+v %#v %s", tg, tg, tg, &tg)} {
		if strings.Contains(out, secretToken) || strings.Contains(out, ":p@") || strings.Contains(out, "x=1") {
			t.Errorf("leaked: %q", out)
		}
	}
}

// The production probe sends the token once, to /health of the server_url
// base, under the client's own URL rules.
func TestProbeClient(t *testing.T) {
	var gotAuth atomic.Value
	var gotPath atomic.Value
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotAuth.Store(r.Header.Get("Authorization"))
		gotPath.Store(r.URL.Path)
		_, _ = w.Write([]byte(`{"status":"ok","version":"9","ready":true}`))
	}))
	defer srv.Close()

	for _, c := range []struct{ suffix, path string }{{"/mcp", "/health"}, {"/prefix/mcp", "/prefix/health"}, {"", "/health"}} {
		hits.Store(0)
		h, err := probeClient(ClientTarget{Present: true, ServerURL: srv.URL + c.suffix, Token: secretToken})
		if err != nil || h == nil || h.Version != "9" {
			t.Fatalf("%q: %v %+v", c.suffix, err, h)
		}
		if hits.Load() != 1 || gotAuth.Load() != "Bearer "+secretToken || gotPath.Load() != c.path {
			t.Errorf("%q: hits=%d auth=%v path=%v", c.suffix, hits.Load(), gotAuth.Load(), gotPath.Load())
		}
	}

	// A remote plain http:// URL is refused before any dial, unless opted in.
	if _, err := probeClient(ClientTarget{ServerURL: "http://remote.example.invalid/mcp", Token: secretToken}); err == nil ||
		!strings.Contains(err.Error(), "refusing") || strings.Contains(err.Error(), secretToken) {
		t.Errorf("remote http without opt-in: %v", err)
	}
	_, err := probeClient(ClientTarget{ServerURL: "http://remote.example.invalid/mcp", Token: secretToken, AllowInsecureHTTP: true})
	var remote *client.RemoteError
	if !errors.As(err, &remote) {
		t.Errorf("with the opt-in the probe must dial and fail as unreachable, got %v", err)
	}
}

func TestProbeHealthSendsNoAuthorization(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"status":"ok","version":"9"}`))
	}))
	defer srv.Close()
	if _, err := probeHealth(strings.TrimPrefix(srv.URL, "http://")); err != nil {
		t.Fatal(err)
	}
	if auth.Load() != "" {
		t.Errorf("the local probe sent Authorization %q", auth.Load())
	}
}

func TestClientModeMenu(t *testing.T) {
	m := DefaultMenu()
	agents := Agents{Known: true, N: 1}
	agents.Rows[0] = AgentRow{Name: "Claude Code", Sync: SyncOK}
	run := Snapshot{Checked: true, Mode: ModeClient, State: StateRunning, Server: "example.com", Version: "1.0", KBCount: 1, KBNames: "kb-a",
		UIURL: "https://example.com/ui/", Agents: agents}
	var texts []string
	for _, it := range MenuItems(m, run) {
		texts = append(texts, it.Text)
	}
	for _, bad := range []string{m.Restart, m.OpenLog} {
		for _, tx := range texts {
			if tx == bad {
				t.Errorf("client menu offers %q: %q", bad, texts)
			}
		}
	}
	items := MenuItems(m, run)
	last := items[len(items)-1]
	if last.Action != ActionQuit || items[len(items)-3].Text != m.OpenAtlas || !items[len(items)-3].Enabled {
		t.Errorf("tail of the client menu = %+v", items[len(items)-3:])
	}

	if got, want := StatusLine(run), "example.com 1.0 — 1 KB"; got != want {
		t.Errorf("status line = %q, want %q", got, want)
	}
	miss := run
	miss.TokenMissing = true
	if got, want := StatusLine(miss), "example.com 1.0 — 1 KB — token not available"; got != want {
		t.Errorf("status line = %q, want %q", got, want)
	}
	if info := InfoItems(miss); info[1].Text != "Token not available" || info[1].Tone != ToneIdle {
		t.Errorf("token row = %+v", info[1])
	}
	if info := InfoItems(run); info[0].Detail != "v1.0 · example.com" {
		t.Errorf("detail = %q", info[0].Detail)
	}

	down := Snapshot{Checked: true, Mode: ModeClient, State: StateStopped, Server: "example.com", KBCount: -1, UIURL: "https://example.com/ui/"}
	if got := StatusLine(down); got != "example.com: unreachable" {
		t.Errorf("down line = %q", got)
	}
	if h := InfoItems(down)[0]; h.Badge != "Unreachable" || h.Tone != ToneDown {
		t.Errorf("down header = %+v", h)
	}
	start := down
	start.State = StateStarting
	if got := StatusLine(start); got != "example.com: starting…" {
		t.Errorf("starting line = %q", got)
	}
	if got := StatusLine(Snapshot{Mode: ModeClient}); got != "checking…" {
		t.Errorf("unchecked = %q", got)
	}
}

func TestOpenURLRemoteAtlas(t *testing.T) {
	opened := stubOpen(t)
	good := []string{"https://example.com/ui/", "https://example.com/prefix/ui/?kb=kb-a", "http://127.0.0.1:1/ui/"}
	for _, g := range good {
		if err := OpenURL(g); err != nil {
			t.Errorf("OpenURL(%q): %v", g, err)
		}
	}
	for _, bad := range []string{
		"https://u:p@example.com/ui/", "file:///ui/", "ftp://example.com/ui/", "https://example.com/other",
		"https://example.com/prefix/ui", "https://example.com/../ui/", "https://example.com/a/./ui/",
		"https://example.com/%2e%2e/ui/", "https://example.com/ui/?kb=a&x=b", "https://example.com/ui/#f", "https://example.com/xui/",
	} {
		if err := OpenURL(bad); err == nil {
			t.Errorf("OpenURL(%q) accepted", bad)
		}
	}
	if len(*opened) != len(good) {
		t.Errorf("opened %v", *opened)
	}
}

func TestClientModeIgnoresRestartAndOpenLog(t *testing.T) {
	opened := stubOpen(t)
	restarts := 0
	a := newActions(func() error { restarts++; return nil }, nil)
	a.Observe(Snapshot{Mode: ModeClient, State: StateRunning, Installed: true, LogPath: "/logs/server.log", UIURL: "https://example.com/ui/"})
	a.Handle(ActionRestart)
	a.Handle(ActionOpenLog)
	a.Wait()
	if restarts != 0 || len(*opened) != 0 || a.Busy() {
		t.Errorf("restarts=%d opened=%v", restarts, *opened)
	}
	a.Handle(ActionOpenAtlas)
	if len(*opened) != 1 {
		t.Errorf("Open Atlas opened %v", *opened)
	}
}

func TestRunClientModeEmits(t *testing.T) {
	f := &clientEnv{target: ClientTarget{Present: true, ServerURL: "https://example.com/mcp"}}
	ticks := make(chan time.Time)
	d := f.deps()
	d.NewTicker = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
	out := make(chan Snapshot, 4)
	ctx, cancel := contextWithCancel()
	done := make(chan struct{})
	go func() { Run(ctx, d, 0, func(s Snapshot) { out <- s }); close(done) }()
	if s := <-out; s.Mode != ModeClient || s.State != StateRunning {
		t.Errorf("first = %+v", s)
	}
	cancel()
	<-done
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func TestSnapshotLogsAnInvalidHealthOnceAndNeverSplitsKBs(t *testing.T) {
	f := &clientEnv{target: ClientTarget{Present: true, ServerURL: "https://example.com/mcp"}}
	d := f.deps()
	d.ProbeClient = func(ClientTarget) (*client.Health, error) {
		return &client.Health{Version: "1", KBs: &[]client.HealthKB{{Name: "a"}, {Name: "b\nc"}}}, nil
	}
	var good *service.Status
	var lastErr string
	cm := &clientMemo{}
	var s Snapshot
	for range 3 {
		s = d.snapshot(&good, &lastErr, cm)
	}
	if s.State != StateStopped || s.KBNames != "" || len(s.KBs()) != 0 {
		t.Errorf("snapshot = %+v", s)
	}
	if len(f.logs) != 1 || !strings.Contains(f.logs[0], "control characters") {
		t.Errorf("logs = %q", f.logs)
	}
}

// CheckServerURL formats the raw URL, userinfo and query included, into its
// error: the tray log carries the redacted URL and a fixed reason instead.
func TestSnapshotLogNeverCarriesTheRawServerURL(t *testing.T) {
	raw := "http://user:pw-secret@exa mple.com/mcp?token=qs-secret"
	err := client.CheckServerURL(raw, false)
	if err == nil || !strings.Contains(err.Error(), "pw-secret") {
		t.Fatalf("premise: CheckServerURL(%q) = %v, want an error carrying the raw URL", raw, err)
	}
	f := &clientEnv{target: ClientTarget{Present: true, ServerURL: raw, Token: secretToken}, probeErr: err}
	d := f.deps()
	var good *service.Status
	var lastErr string
	cm := &clientMemo{}
	for range 3 {
		d.snapshot(&good, &lastErr, cm)
	}
	dump := strings.Join(f.logs, "\n")
	if len(f.logs) != 1 || strings.Contains(dump, "pw-secret") || strings.Contains(dump, "qs-secret") || strings.Contains(dump, secretToken) {
		t.Errorf("logs = %q", f.logs)
	}
}

func TestClientModeTokenRejected(t *testing.T) {
	s := Snapshot{Checked: true, Mode: ModeClient, State: StateStopped, Rejected: true, Server: "example.com", KBCount: -1}
	if got := StatusLine(s); got != "example.com: token rejected — run cartographer reconnect" {
		t.Errorf("status line = %q", got)
	}
	if h := InfoItems(s)[0]; h.Badge != "Token rejected" || h.Tone != ToneDown {
		t.Errorf("header = %+v", h)
	}
}
