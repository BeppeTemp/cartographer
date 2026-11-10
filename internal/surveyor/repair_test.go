package surveyor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

const repairOnlyYAML = `name: repair-night
server_url: https://meridian.example.com
token_env: SURVEYOR_TOKEN
kbs: [kb-a]
schedule:
  window: {start: "00:00", end: "23:59", timezone: UTC}
tasks:
`

func loadRepairCfg(t *testing.T, extra string) (*Config, error) {
	t.Helper()
	t.Setenv("SURVEYOR_TOKEN", "tok")
	t.Setenv("MODEL_API_KEY", "key")
	path := filepath.Join(t.TempDir(), "surveyor.yaml")
	if err := os.WriteFile(path, []byte(extra), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(path, DefaultKinds())
}

func TestRepairOnlyConfigNeedsNoModels(t *testing.T) {
	cfg, err := loadRepairCfg(t, repairOnlyYAML+"  - kind: repair\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 0 || cfg.Tasks[0].Profile != "" {
		t.Fatalf("models=%v profile=%q", cfg.Models, cfg.Tasks[0].Profile)
	}
	if d := cfg.Tasks[0].Options["repair_interval"]; d != 24*time.Hour {
		t.Errorf("default interval = %v", d)
	}
}

func TestRepairTaskRefusesAModelAndAShortInterval(t *testing.T) {
	_, err := loadRepairCfg(t, repairOnlyYAML+"  - kind: repair\n    model: cloud\n")
	if err == nil || !strings.Contains(err.Error(), "takes no model") {
		t.Errorf("model: err = %v", err)
	}
	_, err = loadRepairCfg(t, repairOnlyYAML+"  - kind: repair\n    repair_interval: 10m\n")
	if err == nil || !strings.Contains(err.Error(), "repair_interval") {
		t.Errorf("interval: err = %v", err)
	}
	_, err = loadRepairCfg(t, repairOnlyYAML+"  - kind: repair\n  - kind: repair\n")
	if err == nil {
		t.Error("a second repair entry loaded")
	}
}

// Models are still required when another task needs one.
func TestModelsStillRequiredByAModelTask(t *testing.T) {
	_, err := loadRepairCfg(t, repairOnlyYAML+"  - kind: repair\n  - kind: doctor\n    model: cloud\n")
	if err == nil || !strings.Contains(err.Error(), "model profile") {
		t.Errorf("err = %v", err)
	}
}

type repairTools struct {
	calls []map[string]any
	out   client.ToolOutput
	err   error
}

func (r *repairTools) ListTools() ([]client.ToolInfo, error) { return nil, nil }
func (r *repairTools) CallTool(name string, args any) (client.ToolOutput, error) {
	if name != "kb_repair" {
		return client.ToolOutput{}, errors.New("unexpected tool " + name)
	}
	r.calls = append(r.calls, args.(map[string]any))
	return r.out, r.err
}

func newRepair(now *time.Time) *repairInstance {
	return &repairInstance{now: func() time.Time { return *now }, interval: 24 * time.Hour}
}

func TestRepairInstanceCallsAutoRestsAndCountsTheCommit(t *testing.T) {
	now := time.Date(2026, 1, 10, 3, 0, 0, 0, time.UTC)
	r := newRepair(&now)
	tools := &repairTools{out: client.ToolOutput{Text: `{"applied_total":3}`}}
	it, _ := r.Next(context.Background(), "kb-a", tools)
	if it == nil {
		t.Fatal("not offered at first")
	}
	res, err := r.Run(context.Background(), it, Runtime{KB: "kb-a", Tools: tools})
	if err != nil || res.Commits != 1 || res.Tokens != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(tools.calls) != 1 || len(tools.calls[0]) != 2 || tools.calls[0]["check"] != "auto" || tools.calls[0]["dry_run"] != false {
		t.Fatalf("call = %v: exactly check auto, dry_run false", tools.calls)
	}
	if it, _ := r.Next(context.Background(), "kb-a", tools); it != nil {
		t.Error("offered again while rested")
	}
	if it, _ := r.Next(context.Background(), "kb-b", tools); it == nil {
		t.Error("another KB must not rest")
	}
	now = now.Add(24*time.Hour + time.Second)
	if it, _ := r.Next(context.Background(), "kb-a", tools); it == nil {
		t.Error("not offered after the interval")
	}
}

func TestRepairNothingAppliedStillRests(t *testing.T) {
	now := time.Now()
	r := newRepair(&now)
	tools := &repairTools{out: client.ToolOutput{Text: `{"applied_total":0}`}}
	res, err := r.Run(context.Background(), &WorkItem{Key: "repair"}, Runtime{KB: "kb-a", Tools: tools})
	if err != nil || res.Commits != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if it, _ := r.Next(context.Background(), "kb-a", tools); it != nil {
		t.Error("not rested")
	}
}

func TestRepairRefusalRestsAnHourAndIsNotAnError(t *testing.T) {
	now := time.Now()
	r := newRepair(&now)
	tools := &repairTools{out: client.ToolOutput{IsError: true, Text: "kb_repair: auto-repair refused: git sync is degraded"}}
	if _, err := r.Run(context.Background(), &WorkItem{Key: "repair"}, Runtime{KB: "kb-a", Tools: tools}); err != nil {
		t.Fatal(err)
	}
	if it, _ := r.Next(context.Background(), "kb-a", tools); it != nil {
		t.Error("offered during the refusal rest")
	}
	now = now.Add(time.Hour + time.Second)
	if it, _ := r.Next(context.Background(), "kb-a", tools); it == nil {
		t.Error("still resting after an hour")
	}
	// Any other error is returned, and does not rest the KB.
	tools.out = client.ToolOutput{IsError: true, Text: "kb_repair: boom"}
	r2 := newRepair(&now)
	if _, err := r2.Run(context.Background(), &WorkItem{Key: "repair"}, Runtime{KB: "kb-a", Tools: tools}); err == nil {
		t.Error("an unrelated tool error was swallowed")
	}
}

// A run with only a repair task makes no model call and charges nothing.
func TestRunnerRunsANoModelTaskWithoutProfileOrGrant(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tools := &repairTools{out: client.ToolOutput{Text: `{"applied_total":2}`}}
	log := &sharedLog{}
	cfg := &Config{Name: "repair-night", KBs: []string{"kb-a"}, PollSeconds: 60, Window: &Window{Start: 0, End: 1439, Location: time.UTC},
		Tasks: []TaskSpec{{Kind: "repair", Options: map[string]any{"repair_interval": 24 * time.Hour}, Line: 1}}}
	both := callerFunc(func(name string, args any) (client.ToolOutput, error) {
		if name == "kb_repair" {
			return tools.CallTool(name, args)
		}
		return log.CallTool(name, args)
	})
	var logs []string
	r, err := NewRunner(cfg, DefaultKinds(), RunnerOptions{Callers: func(string) ToolCaller { return both }, Now: func() time.Time { return now },
		Logf: func(f string, a ...any) { logs = append(logs, strings.TrimSpace(f)) }})
	if err != nil {
		t.Fatal(err)
	}
	if code := r.Run(context.Background(), true); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("kb_repair calls = %d", len(tools.calls))
	}
	if e := log.last(); !strings.Contains(e, "commits=1") || strings.Contains(e, "tokens.") {
		t.Errorf("closing entry %q", e)
	}
	r.Run(context.Background(), true)
	if len(tools.calls) != 1 {
		t.Errorf("a rested KB was repaired again: %d calls", len(tools.calls))
	}
}

type callerFunc func(name string, args any) (client.ToolOutput, error)

func (f callerFunc) ListTools() ([]client.ToolInfo, error) { return nil, nil }
func (f callerFunc) CallTool(n string, a any) (client.ToolOutput, error) {
	return f(n, a)
}

func TestHeartbeatBodyCarriesTheDeclaredTasks(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	cfg := &Config{Tasks: []TaskSpec{
		{Kind: "repair", Options: map[string]any{"repair_interval": 12 * time.Hour}},
		{Kind: "doctor"}, {Kind: "doctor"},
	}}
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Tasks = DeclaredTasks(cfg)
	b.Beat(context.Background(), State{Run: StateWaiting})
	raw, _ := json.Marshal(api.beats[0]["tasks"])
	if string(raw) != `[{"kind":"doctor"},{"interval_seconds":43200,"kind":"repair"}]` {
		t.Fatalf("tasks = %s", raw)
	}
	// An older shape: no tasks, no field.
	b2 := newBeater(&stubAPI{answer: &client.HeartbeatAnswer{}}, &now)
	if body, _ := json.Marshal(b2.body(State{Run: StateWaiting})); strings.Contains(string(body), "tasks") {
		t.Errorf("empty tasks sent: %s", body)
	}
}

// A repair run that applies nothing writes no closing log entry (it would be a
// commit in a KB nothing else changed, every interval); one that applies does.
func TestRepairWritesAClosingEntryOnlyWhenItDidSomething(t *testing.T) {
	run := func(applied string) *sharedLog {
		now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
		tools := &repairTools{out: client.ToolOutput{Text: `{"applied_total":` + applied + `}`}}
		log := &sharedLog{}
		cfg := &Config{Name: "repair-night", KBs: []string{"kb-a"}, PollSeconds: 60, Window: &Window{Start: 0, End: 1439, Location: time.UTC},
			Tasks: []TaskSpec{{Kind: "repair", Options: map[string]any{"repair_interval": 24 * time.Hour}, Line: 1}}}
		both := callerFunc(func(name string, args any) (client.ToolOutput, error) {
			if name == "kb_repair" {
				return tools.CallTool(name, args)
			}
			return log.CallTool(name, args)
		})
		r, err := NewRunner(cfg, DefaultKinds(), RunnerOptions{Callers: func(string) ToolCaller { return both }, Now: func() time.Time { return now },
			Logf: func(string, ...any) {}})
		if err != nil {
			t.Fatal(err)
		}
		r.Run(context.Background(), true)
		if len(tools.calls) != 1 {
			t.Fatalf("kb_repair calls = %d", len(tools.calls))
		}
		return log
	}
	if l := run("0"); l.count() != 0 {
		t.Errorf("a repair that applied nothing wrote %d log entries: %q", l.count(), l.last())
	}
	if l := run("2"); l.count() != 1 || !strings.Contains(l.last(), "commits=1") {
		t.Errorf("a repair that applied wrote %d entries: %q", l.count(), l.last())
	}
}
