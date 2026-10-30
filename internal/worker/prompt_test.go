package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

const promptCanary = "CANARY-instruction-text"

const promptYAML = `name: perimeter-a
server_url: https://wiki.example.com/mcp
token_env: WORKER_TOKEN
kbs: [kb-a, kb-b]
models:
  - {name: cloud, provider: anthropic, model: model-x, api_key_env: MODEL_API_KEY, budget: {daily_tokens: 300000, run_tokens: 120000}}
  - {name: tiny, provider: anthropic, model: model-x, api_key_env: MODEL_API_KEY, budget: {daily_tokens: 300000, run_tokens: 7999}}
schedule:
  window: {start: "02:00", end: "05:00", timezone: UTC}
tasks:
`

const goodTask = `  - kind: prompt
    name: weekly-digest
    model: cloud
    instructions_file: prompts/d.md
    tools: [changes_since, concept_read, concept_write]
    every: 168h
`

// loadPrompt writes dir with prompts/d.md = text and loads promptYAML + task.
func loadPromptCfg(t *testing.T, task, text string) (*Config, error) {
	t.Helper()
	t.Setenv("WORKER_TOKEN", "tok")
	t.Setenv("MODEL_API_KEY", "key")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "d.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadPromptIn(t, dir, task)
}

func loadPromptIn(t *testing.T, dir, task string) (*Config, error) {
	t.Helper()
	path := filepath.Join(dir, "worker.yaml")
	if err := os.WriteFile(path, []byte(promptYAML+task), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(path, DefaultKinds())
}

func TestPromptOptions(t *testing.T) {
	cfg, err := loadPromptCfg(t, goodTask, "Do "+promptCanary)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Tasks[0].Options[promptKeyKBs].([]string); len(got) != 2 {
		t.Errorf("kbs default = %v, want the perimeter", got)
	}
	if err := DefaultKinds().CheckSchema(workerconfig.Schema); err != nil {
		t.Error(err)
	}
	with := func(old, repl string) string { return strings.Replace(goodTask, old, repl, 1) }
	drop := func(key string) string {
		var out []string
		for _, l := range strings.Split(goodTask, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), key+":") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	bad := []struct{ name, task, want string }{
		{"missing name", drop("name"), "name"},
		{"missing instructions_file", drop("instructions_file"), "instructions_file"},
		{"missing tools", drop("tools"), "tools"},
		{"missing every", drop("every"), "every"},
		{"unknown key", goodTask + "    cron: x\n", "cron"},
		{"every 59m", with("168h", "59m"), "minimum"},
		{"every 0", with("168h", "0s"), "minimum"},
		{"every -1h", with("168h", "-1h"), "minimum"},
		{"every 1d", with("168h", "1d"), "duration"},
		{"name kb-doctor", with("weekly-digest", "my-kb-doctor"), "kb-doctor"},
		{"name grammar", with("weekly-digest", "Weekly_Digest"), "name"},
		{"profile under the floor", with("model: cloud", "model: tiny"), "tiny"},
		{"kbs empty", goodTask + "    kbs: []\n", "omit the key"},
		{"kbs duplicate", goodTask + "    kbs: [kb-a, kb-a]\n", "twice"},
		{"kbs outside", goodTask + "    kbs: [kb-z]\n", "kb-a, kb-b"},
		{"tools denied", with("concept_write", "concept_delete"), "denied"},
		{"tools artifact prefix", with("concept_write", "artifact_write"), "denied"},
		{"tools unknown", with("concept_write", "frobnicate"), "default allowlist"},
		{"tools duplicate", with("concept_write", "concept_read"), "twice"},
		{"tools empty", with("[changes_since, concept_read, concept_write]", "[]"), "tools"},
		{"duplicate name", goodTask + goodTask, "weekly-digest"},
	}
	for _, c := range bad {
		if _, err := loadPromptCfg(t, c.task, "text"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.want)
		}
	}
	// The unknown-tool error lists the allowed names, sorted.
	_, err = loadPromptCfg(t, with("concept_write", "frobnicate"), "text")
	if err == nil || strings.Index(err.Error(), "asset_list") > strings.Index(err.Error(), "work_list") {
		t.Errorf("allowed names not sorted: %v", err)
	}
	for _, d := range []string{"1h", "168h"} {
		if _, err := loadPromptCfg(t, with("168h", d), "text"); err != nil {
			t.Errorf("every %s: %v", d, err)
		}
	}
	// A read-only task loads with one warning.
	c, err := loadPromptCfg(t, with("[changes_since, concept_read, concept_write]", "[changes_since]"), "text")
	if err != nil || !strings.Contains(strings.Join(c.Warnings, "|"), "changes nothing") {
		t.Errorf("read-only task: %v %v", err, c)
	}
}

func TestPromptInstructionsFile(t *testing.T) {
	load := func(dir, file string) error {
		t.Helper()
		_, err := loadPromptIn(t, dir, strings.Replace(goodTask, "prompts/d.md", file, 1))
		return err
	}
	dir := t.TempDir()
	t.Setenv("WORKER_TOKEN", "tok")
	t.Setenv("MODEL_API_KEY", "key")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, data []byte) { must(os.WriteFile(filepath.Join(dir, name), data, 0o600)) }
	must(os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	write("ok.md", []byte("hello"))
	write("max.md", []byte(strings.Repeat("a", maxInstructionsBytes)))
	write("big.md", []byte(strings.Repeat("a", maxInstructionsBytes+1)))
	write("blank.md", []byte(" \n\t\n"))
	write("latin.md", []byte{0xff, 0xfe, 'x'})
	write("nul.md", []byte("a\x00b"))
	outside := t.TempDir()
	must(os.WriteFile(filepath.Join(outside, "secret.md"), []byte(promptCanary), 0o600))

	for _, f := range []string{"ok.md", "max.md"} {
		if err := load(dir, f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	cases := []struct{ file, want string }{
		{"missing.md", "no such file"}, {"sub", "regular"}, {"/etc/hostname", "relative"},
		{"../x.md", "inside"}, {"big.md", "limit"}, {"blank.md", "empty"}, {"latin.md", "UTF-8"}, {"nul.md", "NUL"},
	}
	for _, c := range cases {
		err := load(dir, c.file)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.file, err, c.want)
		}
	}
	if runtime.GOOS != "windows" {
		must(os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "abs.md")))
		must(os.Symlink("../"+filepath.Base(outside)+"/secret.md", filepath.Join(dir, "rel-out.md")))
		must(os.Symlink("ok.md", filepath.Join(dir, "rel-in.md")))
		for _, f := range []string{"abs.md", "rel-out.md"} {
			if err := load(dir, f); err == nil {
				t.Errorf("%s: a symlink leaving the directory was followed", f)
			}
		}
		if err := load(dir, "rel-in.md"); err != nil {
			t.Errorf("relative symlink inside the directory: %v", err)
		}
	}
	// No error echoes the file's content.
	write("canary-bad.md", append([]byte(promptCanary), 0))
	if err := load(dir, "canary-bad.md"); err == nil || strings.Contains(err.Error(), promptCanary) {
		t.Errorf("error = %v", err)
	}
}

// --- run-time fakes ---------------------------------------------------------

type scriptModel struct {
	mu    sync.Mutex
	turns []Response
	reqs  []Request
}

func (m *scriptModel) Complete(_ context.Context, r Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs = append(m.reqs, r)
	if len(m.reqs) > len(m.turns) {
		return Response{}, errors.New("script exhausted")
	}
	return m.turns[len(m.reqs)-1], nil
}

// promptTools is a fake KB: a real-shaped log (sharedLog) plus content tools
// whose schemas declare reason, as the committing tools of the server do.
type promptTools struct {
	log    *sharedLog
	offer  []string
	calls  []string
	args   map[string]string
	tailFn func() (client.ToolOutput, error)
}

func newPromptTools(offer ...string) *promptTools {
	return &promptTools{log: &sharedLog{}, offer: offer, args: map[string]string{}}
}

func (p *promptTools) ListTools() ([]client.ToolInfo, error) {
	var out []client.ToolInfo
	for _, n := range p.offer {
		schema := `{"type":"object","properties":{"entry":{}}}`
		if n == "concept_write" || n == "log_append" {
			schema = `{"type":"object","properties":{"entry":{},"path":{},"reason":{}}}`
		}
		out = append(out, client.ToolInfo{Name: n, InputSchema: json.RawMessage(schema)})
	}
	return out, nil
}

func (p *promptTools) CallTool(name string, args any) (client.ToolOutput, error) {
	var m map[string]any
	switch a := args.(type) {
	case map[string]any:
		m = a
	case json.RawMessage:
		p.args[name] = string(a)
		_ = json.Unmarshal(a, &m)
		if m == nil {
			m = map[string]any{}
		}
		for k, v := range m { // sharedLog wants ints for n
			if f, ok := v.(float64); ok {
				m[k] = int(f)
			}
		}
	}
	p.calls = append(p.calls, name)
	switch name {
	case "log_tail":
		if p.tailFn != nil {
			return p.tailFn()
		}
		// Like the server: only entries under the bracketed prefix of the path.
		view := &sharedLog{}
		for _, e := range p.log.entries {
			if strings.HasPrefix(e, "["+fmt.Sprint(m["path"])+"] ") {
				view.entries = append(view.entries, e)
			}
		}
		return view.CallTool(name, m)
	case "log_append":
		return p.log.CallTool(name, m)
	}
	return client.ToolOutput{Text: "ok"}, nil
}

func (p *promptTools) count(name string) (n int) {
	for _, c := range p.calls {
		if c == name {
			n++
		}
	}
	return
}

func mustPromptInstance(t *testing.T, mod func(*promptTask)) *promptTask {
	t.Helper()
	cfg, err := loadPromptCfg(t, goodTask, "Do "+promptCanary)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := promptKind().Build(cfg.Tasks[0].Options)
	if err != nil {
		t.Fatal(err)
	}
	p := inst.(*promptTask)
	if mod != nil {
		mod(p)
	}
	return p
}

func writeTurn(reason string) Response {
	args := `{"id":"digests/x","body":"b"}`
	if reason != "" {
		args = `{"id":"digests/x","reason":` + reason + `}`
	}
	return Response{Stop: StopToolUse, ToolCalls: []ToolCall{{ID: "1", Name: "concept_write", Arguments: json.RawMessage(args)}},
		Usage: Usage{InputTokens: 100, OutputTokens: 20}}
}

func doneTurn() Response {
	return Response{Stop: StopEnd, Text: "model said kb-doctor", Usage: Usage{InputTokens: 100, OutputTokens: 20}}
}

var t0 = time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)

func TestPromptCadenceAndEndToEnd(t *testing.T) {
	now := t0
	p := mustPromptInstance(t, func(p *promptTask) { p.now = func() time.Time { return now } })
	tools := newPromptTools("changes_since", "concept_read", "concept_write", "log_append", "log_tail")
	ctx := context.Background()

	if it, err := p.Next(ctx, "kb-a", tools); err != nil || it == nil {
		t.Fatalf("never run: %v %v", it, err)
	}
	if it, _ := p.Next(ctx, "kb-z", tools); it != nil {
		t.Error("a KB outside the task's kbs is due")
	}
	model := &scriptModel{turns: []Response{writeTurn(`"weekly"`), doneTurn()}}
	rt := Runtime{KB: "kb-a", Perimeter: "perimeter-a", Grant: 50_000, Model: model, Tools: tools}
	res, err := p.Run(ctx, &WorkItem{}, rt)
	if err != nil || res.Commits != 1 || res.Tokens != 240 || res.Units != 1 {
		t.Fatalf("run = %+v %v", res, err)
	}
	if strings.Contains(res.Summary, "kb-doctor") || res.Summary != "prompt weekly-digest: done, 2 steps, 1 writes" {
		t.Errorf("summary = %q", res.Summary)
	}
	// Instructions: preamble + operator file, nothing else.
	sys := model.reqs[0].System
	if !strings.Contains(sys, "Do "+promptCanary) || !strings.Contains(sys, "task/weekly-digest") || !strings.Contains(sys, "2026-10-29") {
		t.Errorf("system prompt = %q", sys)
	}
	// The worker set the trailer; the model's own text follows the name.
	if got := tools.args["concept_write"]; !strings.Contains(got, `"reason":"prompt task weekly-digest: weekly"`) {
		t.Errorf("concept_write args = %s", got)
	}
	// Marker: exact path and content, no tokens= key, no model text.
	marker := tools.log.last()
	want := "[worker/perimeter-a/task/weekly-digest] task=weekly-digest at=2026-10-29T02:00:00Z day=2026-10-29 stopped=done writes=1"
	if marker != want {
		t.Errorf("marker = %q, want %q", marker, want)
	}
	// The budget ledger reader never sees it.
	b := NewBudget("perimeter-a", testProfiles, time.UTC, func(string) ToolCaller { return tools })
	if got, bad, err := b.readKB("kb-a", "2026-10-29"); err != nil || bad != 0 || len(got) != 0 {
		t.Errorf("budget read over a marker = %v bad=%d err=%v", got, bad, err)
	}

	now = t0.Add(167 * time.Hour)
	if it, _ := p.Next(ctx, "kb-a", tools); it != nil {
		t.Error("due before every elapsed")
	}
	if it, _ := p.Next(ctx, "kb-b", newPromptTools()); it == nil {
		t.Error("the other KB has an independent cadence")
	}
	now = t0.Add(168 * time.Hour)
	if it, _ := p.Next(ctx, "kb-a", tools); it == nil {
		t.Error("not due once every elapsed")
	}
}

func TestPromptNextReadsTheLog(t *testing.T) {
	now := t0
	p := mustPromptInstance(t, func(p *promptTask) { p.now = func() time.Time { return now } })
	path := "worker/perimeter-a/task/weekly-digest"
	tools := newPromptTools()
	due := func() bool { it, err := p.Next(context.Background(), "kb-a", tools); return err == nil && it != nil }
	entry := func(at time.Time) string {
		return fmt.Sprintf("[%s] task=weekly-digest at=%s day=x stopped=done writes=1", path, at.Format(time.RFC3339))
	}

	tools.log.entries = nil // the JSON "no entries" note
	if !due() {
		t.Error("an empty tail is never-run")
	}
	// Newest block malformed: the older valid one decides.
	tools.log.entries = []string{"[" + path + "] garbage at=notatime", entry(now.Add(-time.Hour))}
	if due() {
		t.Error("fell through the malformed block to nothing")
	}
	// A marker of another task or path is not ours.
	tools.log.entries = []string{"[" + path + "] task=other at=" + now.Format(time.RFC3339)}
	if !due() {
		t.Error("another task's marker counted")
	}
	// Future at: clamped to now, so one period, not forever.
	tools.log.entries = []string{entry(now.Add(1000 * time.Hour))}
	if due() {
		t.Error("future marker counted as due")
	}
	now = now.Add(168 * time.Hour)
	if !due() {
		t.Error("a future marker froze the task")
	}
	// Seen again later, the clamped period keeps aging instead of restarting.
	now = now.Add(time.Hour)
	if !due() {
		t.Error("a moving clock keeps pushing the clamped marker away")
	}
	// log_tail failure: not due, an error for the runner to log.
	tools.tailFn = func() (client.ToolOutput, error) { return client.ToolOutput{}, errors.New("down") }
	if it, err := p.Next(context.Background(), "kb-a", tools); it != nil || err == nil {
		t.Errorf("failing log_tail: %v %v", it, err)
	}
}

func TestPromptRunStartRules(t *testing.T) {
	p := mustPromptInstance(t, func(p *promptTask) { p.now = func() time.Time { return t0 } })
	tools := newPromptTools("concept_write", "log_append", "log_tail")
	// Under the floor: not started, no marker, no stamp, still due.
	res, err := p.Run(context.Background(), &WorkItem{}, Runtime{KB: "kb-a", Grant: promptMinGrant - 1, Model: &scriptModel{}, Tools: tools})
	if err != nil || res != (Result{}) || len(tools.calls) != 0 {
		t.Fatalf("under the floor: %+v %v %v", res, err, tools.calls)
	}
	if it, _ := p.Next(context.Background(), "kb-a", newPromptTools()); it == nil {
		t.Error("not-started task is no longer due")
	}
	// Nothing offered: the loop errors before any model call, no marker.
	none := newPromptTools("search")
	model := &scriptModel{}
	_, err = p.Run(context.Background(), &WorkItem{}, Runtime{KB: "kb-a", Grant: 50_000, Model: model, Tools: none})
	if err == nil || len(model.reqs) != 0 || none.count("log_append") != 0 {
		t.Errorf("nothing offered: err=%v reqs=%d appends=%d", err, len(model.reqs), none.count("log_append"))
	}
	// ...and this process does not retry it before every elapses.
	if it, _ := p.Next(context.Background(), "kb-a", newPromptTools()); it != nil {
		t.Error("a failed attempt is retried on every poll")
	}
}

func TestPromptShutdownMarker(t *testing.T) {
	p := mustPromptInstance(t, func(p *promptTask) { p.now = func() time.Time { return t0 } })
	tools := newPromptTools("concept_write", "log_append", "log_tail")
	ctx, cancel := context.WithCancel(context.Background())
	m := &cancelModel{cancel: cancel}
	if _, err := p.Run(ctx, &WorkItem{}, Runtime{KB: "kb-a", Grant: 50_000, Model: m, Tools: tools}); err != nil {
		t.Fatal(err)
	}
	if got := tools.log.last(); !strings.Contains(got, "stopped=shutdown") {
		t.Errorf("marker = %q", got)
	}
}

type cancelModel struct{ cancel func() }

func (m *cancelModel) Complete(context.Context, Request) (Response, error) {
	m.cancel()
	return writeTurn(""), nil
}

func TestPromptMarkerWriteFailureIsNotRetriedByThisProcess(t *testing.T) {
	p := mustPromptInstance(t, func(p *promptTask) { p.now = func() time.Time { return t0 } })
	tools := newPromptTools("concept_write", "log_append", "log_tail")
	tools.log.entries = nil
	failing := &appendFails{promptTools: tools}
	_, err := p.Run(context.Background(), &WorkItem{}, Runtime{KB: "kb-a", Grant: 50_000, Model: &scriptModel{turns: []Response{doneTurn()}}, Tools: failing})
	if err != nil || len(tools.log.entries) != 0 {
		t.Fatalf("err=%v entries=%v", err, tools.log.entries)
	}
	if it, _ := p.Next(context.Background(), "kb-a", tools); it != nil {
		t.Error("re-run in the same process")
	}
	fresh := mustPromptInstance(t, func(p *promptTask) { p.now = func() time.Time { return t0 } })
	if it, _ := fresh.Next(context.Background(), "kb-a", tools); it == nil {
		t.Error("a new process finds it due again")
	}
}

type appendFails struct{ *promptTools }

func (a *appendFails) CallTool(name string, args any) (client.ToolOutput, error) {
	if name == "log_append" {
		if m, ok := args.(map[string]any); ok && strings.Contains(fmt.Sprint(m["path"]), "/task/") {
			return client.ToolOutput{IsError: true, Text: "refused"}, nil
		}
	}
	return a.promptTools.CallTool(name, args)
}

func TestPromptGuard(t *testing.T) {
	tools := newPromptTools("concept_write", "log_append", "search")
	g := newPromptGuard(tools, "weekly-digest")
	if _, err := g.ListTools(); err != nil {
		t.Fatal(err)
	}
	call := func(name, args string) client.ToolOutput {
		t.Helper()
		out, err := g.CallTool(name, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	reason := func(args string) string {
		var m map[string]any
		_ = json.Unmarshal([]byte(tools.args["concept_write"]), &m)
		call("concept_write", args)
		_ = json.Unmarshal([]byte(tools.args["concept_write"]), &m)
		s, _ := m["reason"].(string)
		return s
	}
	if got := reason(`{"id":"a"}`); got != "prompt task weekly-digest" {
		t.Errorf("missing reason -> %q", got)
	}
	if got := reason(`{"id":"a","reason":"why"}`); got != "prompt task weekly-digest: why" {
		t.Errorf("given reason -> %q", got)
	}
	if got := reason(`{"id":"a","reason":42}`); got != "prompt task weekly-digest" {
		t.Errorf("non-string reason -> %q", got)
	}
	long := strings.Repeat("x", 600)
	if got := reason(`{"id":"a","reason":"` + long + `"}`); !strings.HasPrefix(got, "prompt task weekly-digest: x") {
		t.Errorf("long reason lost the task name: %.40q", got)
	}
	// A tool whose schema has no reason is untouched, byte for byte.
	raw := `{"query":"q",  "z":1}`
	call("search", raw)
	if tools.args["search"] != raw {
		t.Errorf("search args = %s", tools.args["search"])
	}
	// log_append: path forced, forging refused with zero server calls.
	before := tools.count("log_append")
	call("log_append", `{"entry":"hello","path":"worker/perimeter-a"}`)
	if got := tools.log.last(); got != "[task/weekly-digest] hello" {
		t.Errorf("model log line filed as %q", got)
	}
	for _, e := range []string{"a\n## 2026-01-01T00:00:00Z\nb", "## x", "note kb-doctor ok"} {
		b, _ := json.Marshal(map[string]string{"entry": e, "path": "worker/perimeter-a"})
		if out := call("log_append", string(b)); !out.IsError {
			t.Errorf("entry %q accepted", e)
		}
	}
	if tools.count("log_append") != before+1 || g.Refused() != 3 {
		t.Errorf("appends=%d refused=%d", tools.count("log_append")-before, g.Refused())
	}
}
