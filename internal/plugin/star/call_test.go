package star

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"go.starlark.net/starlark"
)

func mustLoad(t *testing.T, src string) *Program {
	t.Helper()
	p, err := Load(entry(src))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func callErr(t *testing.T, err error, kind ErrKind) *CallError {
	t.Helper()
	var ce *CallError
	if !errors.As(err, &ce) || ce.Kind != kind {
		t.Fatalf("error %v (%T), want kind %v", err, err, kind)
	}
	return ce
}

const toolSrc = `
def search(a):
    return {"q": a["q"], "n": a.get("n", 0) + 1}

def plain(a):
    return "hello " + a.get("who", "you")

def nothing(a):
    pass

tool("search", search, "search", params={"type": "object", "properties": {"q": {"type": "string"}, "n": {"type": "integer"}}, "required": ["q"]})
tool("plain", plain, "plain")
tool("nothing", nothing, "nothing")
`

func toolReq(name, params string) Request {
	return Request{Kind: KindTool, Name: name, Plugin: "acme", Params: []byte(params)}
}

func TestCallToolArguments(t *testing.T) {
	p := mustLoad(t, toolSrc)
	res, err := p.Call(context.Background(), toolReq("search", `{"q":"x","n":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"n\": 3,\n  \"q\": \"x\"\n}"; res.Text != want {
		t.Errorf("result %q, want %q", res.Text, want)
	}
	for _, c := range []struct{ params, field string }{
		{`{}`, "q"},
		{`{"q": 5}`, "q"},
		{`{"q": "x", "n": "two"}`, "n"},
		{`[1]`, "object"},
	} {
		_, err := p.Call(context.Background(), toolReq("search", c.params))
		ce := callErr(t, err, Failed)
		if !strings.Contains(ce.Msg, "invalid arguments") || !strings.Contains(ce.Msg, c.field) {
			t.Errorf("%s: message %q does not name %q", c.params, ce.Msg, c.field)
		}
	}
	res, _ = p.Call(context.Background(), toolReq("plain", ``))
	if res.Text != "hello you" {
		t.Errorf("plain: %q", res.Text)
	}
	res, _ = p.Call(context.Background(), toolReq("nothing", `{}`))
	if res.Text != "ok" {
		t.Errorf("None: %q", res.Text)
	}
	_, err = p.Call(context.Background(), toolReq("absent", `{}`))
	callErr(t, err, Internal)
}

func TestCallFailVersusRuntimeError(t *testing.T) {
	p := mustLoad(t, `
def bad(a):
    fail("no luck:", 3)

def oops(a):
    return 1 // 0

tool("bad", bad, "bad")
tool("oops", oops, "oops")
`)
	_, err := p.Call(context.Background(), toolReq("bad", `{}`))
	if ce := callErr(t, err, Failed); ce.Msg != "no luck: 3" {
		t.Errorf("fail: %+v", ce)
	}
	_, err = p.Call(context.Background(), toolReq("oops", `{}`))
	ce := callErr(t, err, Failed)
	if !strings.HasPrefix(ce.Msg, "plugin.star:6:") || !strings.Contains(ce.Msg, "division by zero") {
		t.Errorf("runtime error: %q", ce.Msg)
	}
}

const spinSrc = `
def spin(a):
    n = 0
    for i in range(100000000):
        n += i
    return n
tool("spin", spin, "spin")
`

func TestCallStepCap(t *testing.T) {
	p := mustLoad(t, spinSrc)
	_, err := p.call(context.Background(), toolReq("spin", `{}`), callOpts{steps: 1000})
	callErr(t, err, LimitExceeded)
}

func TestCallDeadlineAndCancel(t *testing.T) {
	p := mustLoad(t, spinSrc)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.call(ctx, toolReq("spin", `{}`), callOpts{steps: 1 << 60})
	callErr(t, err, LimitExceeded)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}

	ctx, cancel = context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err = p.call(ctx, toolReq("spin", `{}`), callOpts{steps: 1 << 60})
	callErr(t, err, Cancelled)

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = p.Call(ctx, toolReq("spin", `{}`))
	callErr(t, err, Cancelled)
}

// builtins are the test adapters: they stand for what a capability module
// does wrong or refuses.
func builtins() starlark.StringDict {
	fn := func(name string, f func() (starlark.Value, error)) starlark.Value {
		return starlark.NewBuiltin(name, func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
			return f()
		})
	}
	return starlark.StringDict{
		"boom": fn("boom", func() (starlark.Value, error) { panic("bug in a builtin") }),
		"deny": fn("deny", func() (starlark.Value, error) {
			return nil, &deniedError{"files.read needs permissions.files.read in the manifest"}
		}),
		"big": fn("big", func() (starlark.Value, error) { return nil, &limitError{"too many requests"} }),
	}
}

func TestCallTypedErrorsFromBuiltins(t *testing.T) {
	src := "def a(x):\n    boom()\ndef b(x):\n    deny()\ndef c(x):\n    big()\n" +
		"tool(\"a\", a, \"a\")\ntool(\"b\", b, \"b\")\ntool(\"c\", c, \"c\")\n"
	extra := builtins()
	prog, err := runProgram(map[string][]byte{"plugin.star": []byte(src)}, &settingsValue{}, testOptions(extra), nil)
	if err != nil {
		t.Fatal(err)
	}
	do := func(name string) error {
		_, err := prog.call(context.Background(), toolReq(name, `{}`), callOpts{extra: extra})
		return err
	}
	if ce := callErr(t, do("a"), Internal); !strings.Contains(ce.Msg, "bug in a builtin") {
		t.Errorf("panic: %q", ce.Msg)
	}
	if ce := callErr(t, do("b"), Denied); !strings.Contains(ce.Msg, "permissions.files.read") {
		t.Errorf("denied: %q", ce.Msg)
	}
	callErr(t, do("c"), LimitExceeded)
}

func TestCallConcurrentSettings(t *testing.T) {
	p := mustLoad(t, `
def who(a):
    return settings.name + ":" + a["i"]
tool("who", who, "who", params={"type": "object"})
`)
	decl := map[string]core.Setting{"name": {Type: "string"}}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("g%d", g)
			for i := 0; i < 20; i++ {
				r := toolReq("who", fmt.Sprintf(`{"i":"%d"}`, i))
				r.Declared, r.Values = decl, map[string]any{"name": name}
				res, err := p.Call(context.Background(), r)
				if want := fmt.Sprintf("%s:%d", name, i); err != nil || res.Text != want {
					t.Errorf("got %q, %v, want %q", res.Text, err, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestCallSettings(t *testing.T) {
	p := mustLoad(t, `
def show(a):
    return [settings.token, settings["limit"], settings.on, settings.unset, settings.name]
tool("show", show, "show")

def bad(a):
    return settings.nope
tool("bad", bad, "bad")
`)
	decl := map[string]core.Setting{
		"token": {Type: "string"},
		"limit": {Type: "int", Default: 5},
		"on":    {Type: "bool", Default: true},
		"unset": {Type: "string"},
		"name":  {Type: "string", Default: "dflt"},
	}
	r := toolReq("show", `{}`)
	r.Declared, r.Values = decl, map[string]any{"token": "abc", "limit": int64(9)}
	res, err := p.Call(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[abc 9 true <nil> dflt]" {
		t.Errorf("settings %v", got)
	}

	r = toolReq("bad", `{}`)
	r.Declared = decl
	_, err = p.Call(context.Background(), r)
	ce := callErr(t, err, Failed)
	for _, k := range []string{"nope", "limit", "token"} {
		if !strings.Contains(ce.Msg, k) {
			t.Errorf("unknown key message %q lacks %q", ce.Msg, k)
		}
	}

	for _, vals := range []map[string]any{{"limit": "ten"}, {"token": true}, {"ghost": "x"}} {
		r = toolReq("show", `{}`)
		r.Declared, r.Values = decl, vals
		_, err = p.Call(context.Background(), r)
		callErr(t, err, Failed)
	}
}

func TestSettingsAreClosedAtLoad(t *testing.T) {
	if _, err := Load(entry("x = settings.a\n")); err == nil {
		t.Error("a settings read at load must fail")
	}
}

func TestCallPrintAndLog(t *testing.T) {
	p := mustLoad(t, `
def run_it(args):
    print("out", args[0])
    log.info("hello")
    log.warn("careful")
    return "done"

def t(a):
    print("from tool")
    log.error("bad")

command("go", run_it, "go")
tool("t", t, "t")
`)
	var out, logs bytes.Buffer
	res, err := p.Call(context.Background(), Request{Kind: KindCommand, Name: "go", Plugin: "acme", Args: []string{"x"}, Stdout: &out, Log: &logs})
	if err != nil || res.Text != "done" || res.ExitCode != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
	if out.String() != "out x\n" {
		t.Errorf("stdout %q", out.String())
	}
	if want := "plugin acme: hello\nplugin acme: warn: careful\n"; logs.String() != want {
		t.Errorf("log %q, want %q", logs.String(), want)
	}

	out.Reset()
	logs.Reset()
	r := toolReq("t", `{}`)
	r.Stdout, r.Log = &out, &logs
	if _, err := p.Call(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("a tool's print reached stdout: %q", out.String())
	}
	if want := "plugin acme: from tool\nplugin acme: error: bad\n"; logs.String() != want {
		t.Errorf("log %q, want %q", logs.String(), want)
	}
}

func TestCallPrintIsCapped(t *testing.T) {
	p := mustLoad(t, `
def noisy(args):
    for i in range(5000):
        print("x" * 100)
command("noisy", noisy, "noisy")
`)
	var out bytes.Buffer
	if _, err := p.Call(context.Background(), Request{Kind: KindCommand, Name: "noisy", Plugin: "acme", Stdout: &out}); err != nil {
		t.Fatal(err)
	}
	if n := out.Len(); n > MaxPrintBytes+64 || !strings.HasSuffix(out.String(), "[output truncated]\n") {
		t.Errorf("%d bytes, tail %q", n, out.String()[max(0, out.Len()-30):])
	}
}

func TestCallCommandAndEventResults(t *testing.T) {
	p := mustLoad(t, `
def code(args):
    return len(args)
def huge(args):
    return "x" * (1 << 21)
def neg(args):
    return -1
def listy(args):
    return [1]
def ev(ctx):
    log.info(ctx.provider + ":" + str(ctx.count))
    return "ignored"

command("code", code, "c")
command("huge", huge, "h")
command("neg", neg, "n")
command("listy", listy, "l")
on("sync.after", ev)
`)
	res, err := p.Call(context.Background(), Request{Kind: KindCommand, Name: "code", Args: []string{"a", "b"}})
	if err != nil || res.ExitCode != 2 {
		t.Errorf("exit code: %+v, %v", res, err)
	}
	_, err = p.Call(context.Background(), Request{Kind: KindCommand, Name: "huge"})
	callErr(t, err, LimitExceeded)
	_, err = p.Call(context.Background(), Request{Kind: KindCommand, Name: "neg"})
	callErr(t, err, Failed)
	_, err = p.Call(context.Background(), Request{Kind: KindCommand, Name: "listy"})
	callErr(t, err, Failed)

	var logs bytes.Buffer
	res, err = p.Call(context.Background(), Request{Kind: KindEvent, Name: "sync.after", Plugin: "acme", Event: map[string]any{"provider": "claude", "count": 3}, Log: &logs})
	if err != nil || res.Text != "" || logs.String() != "plugin acme: claude:3\n" {
		t.Errorf("event: %+v, %v, %q", res, err, logs.String())
	}
}

func TestCallCannotRegister(t *testing.T) {
	p := mustLoad(t, `
def sneaky(a):
    command("late", sneaky, "late")
tool("sneaky", sneaky, "sneaky")
`)
	_, err := p.Call(context.Background(), toolReq("sneaky", `{}`))
	if ce := callErr(t, err, Failed); !strings.Contains(ce.Msg, "can only run while plugin.star loads") {
		t.Errorf("message %q", ce.Msg)
	}
}

// A function defined while the script loads must see the settings and the
// phase of the call that runs it, and time.now works there (it does not at load).
func TestCallSeesCallTimeContext(t *testing.T) {
	p := mustLoad(t, `
def helper():
    return settings.mode + "/" + str(time.now().unix > 0)
def go(a):
    return helper()
tool("go", go, "go")
`)
	for _, mode := range []string{"one", "two"} {
		r := toolReq("go", `{}`)
		r.Declared, r.Values = map[string]core.Setting{"mode": {Type: "string"}}, map[string]any{"mode": mode}
		res, err := p.Call(context.Background(), r)
		if err != nil || res.Text != mode+"/True" {
			t.Errorf("%s: %q, %v", mode, res.Text, err)
		}
	}
}

// Without a grant a capability refuses in CALL, and no name reaches the
// filesystem or a process.
func TestCallNeverTouchesTheOutsideWorld(t *testing.T) {
	p := mustLoad(t, `
def go(a):
    return files.read("/etc/hosts")
tool("go", go, "go")
`)
	_, err := p.Call(context.Background(), toolReq("go", `{}`))
	callErr(t, err, Denied)
	for _, name := range []string{"open", "os", "exec"} {
		if _, err := Load(entry(name + "\n")); err == nil {
			t.Errorf("%s exists", name)
		}
	}
}

// counting returns a builtin that answers a different number on each call, to
// build a script whose second run differs from its first.
func counting() starlark.StringDict {
	n := 0
	return starlark.StringDict{
		"nth": starlark.NewBuiltin("nth", func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
			n++
			return starlark.MakeInt(n), nil
		}),
	}
}

func TestCallBoundsThePrivateRun(t *testing.T) {
	// The first run is cheap; every later run loops for a long time at load.
	src := `
def spin(n):
    x = 0
    for i in range(n):
        x += i
    return x

def go(a):
    return 1

spin(100 if nth() == 1 else 100000000)
tool("go", go, "go")
`
	extra := counting()
	prog, err := runProgram(map[string][]byte{"plugin.star": []byte(src)}, &settingsValue{}, testOptions(extra), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = prog.call(ctx, toolReq("go", `{}`), callOpts{extra: extra, steps: 1 << 40})
	callErr(t, err, LimitExceeded)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}

	// The same loop is cut by the call's step budget, not only by the clock.
	extra = counting()
	prog, _ = runProgram(map[string][]byte{"plugin.star": []byte(src)}, &settingsValue{}, testOptions(extra), nil)
	_, err = prog.call(context.Background(), toolReq("go", `{}`), callOpts{extra: extra, steps: 100000})
	ce := callErr(t, err, LimitExceeded)
	// The load inside a call says the cap that applied to it, not MaxLoadSteps.
	if !strings.Contains(ce.Msg, "the script ran more than ") || !strings.Contains(ce.Msg, " steps") || strings.Contains(ce.Msg, "20000000") {
		t.Errorf("message = %q", ce.Msg)
	}
}

func TestCallRefusesAScriptThatRegistersDifferently(t *testing.T) {
	src := `
def go(a):
    return 1
tool("go" if nth() == 1 else "other", go, "go")
`
	extra := counting()
	prog, err := runProgram(map[string][]byte{"plugin.star": []byte(src)}, &settingsValue{}, testOptions(extra), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prog.call(context.Background(), toolReq("go", `{}`), callOpts{extra: extra})
	callErr(t, err, Internal)
}

func TestCallResultSizeIsBoundedBeforeItIsBuilt(t *testing.T) {
	p := mustLoad(t, `
def big(a):
    x = [0]
    for i in range(30):
        x = [x, x]
    return x

def cyc(a):
    x = []
    x.append(x)
    return x

tool("big", big, "big")
tool("cyc", cyc, "cyc")
`)
	start := time.Now()
	_, err := p.Call(context.Background(), toolReq("big", `{}`))
	callErr(t, err, LimitExceeded)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
	_, err = p.Call(context.Background(), toolReq("cyc", `{}`))
	callErr(t, err, Failed)
}

func TestCallPanicLeaksNoWatcher(t *testing.T) {
	src := "def a(x):\n    boom()\ntool(\"a\", a, \"a\")\n"
	extra := builtins()
	prog, err := runProgram(map[string][]byte{"plugin.star": []byte(src)}, &settingsValue{}, testOptions(extra), nil)
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		_, err = prog.call(context.Background(), toolReq("a", `{}`), callOpts{extra: extra})
		callErr(t, err, Internal)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines after, %d before: a watcher leaked", n, before)
	}
}

func TestCallEscapesScriptText(t *testing.T) {
	p := mustLoad(t, `
def bad(a):
    fail("x\nplugin other: forged\x1b[2J")

def noisy(a):
    print("one\nplugin other: forged\x1b[2J")
    log.info("a\r\nb")

tool("bad", bad, "bad")
tool("noisy", noisy, "noisy")
`)
	_, err := p.Call(context.Background(), toolReq("bad", `{}`))
	ce := callErr(t, err, Failed)
	if strings.ContainsAny(ce.Msg, "\n\x1b") || strings.ContainsAny(ce.Trace, "\x1b") {
		t.Errorf("unescaped: %q / %q", ce.Msg, ce.Trace)
	}
	var logs bytes.Buffer
	r := toolReq("noisy", `{}`)
	r.Log = &logs
	if _, err := p.Call(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	want := "plugin acme: one\nplugin acme: plugin other: forged\\x1b[2J\nplugin acme: a\nplugin acme: b\n"
	if logs.String() != want {
		t.Errorf("log %q, want %q", logs.String(), want)
	}
}

func TestCallValidatesWhatTheScriptReceives(t *testing.T) {
	p := mustLoad(t, `
def kind(a):
    return type(a["n"])
tool("kind", kind, "kind", params={"type": "object", "properties": {"n": {"type": "integer"}}})
`)
	// 1e2 is an integer for the schema and reaches the script as one.
	res, err := p.Call(context.Background(), toolReq("kind", `{"n": 1e2}`))
	if err != nil || res.Text != "int" {
		t.Errorf("1e2: %q, %v", res.Text, err)
	}
	// Above 2^53 the exact value is validated, not a rounded copy.
	res, err = p.Call(context.Background(), toolReq("kind", `{"n": 9007199254740993}`))
	if err != nil || res.Text != "int" {
		t.Errorf("2^53+1: %q, %v", res.Text, err)
	}
	_, err = p.Call(context.Background(), toolReq("kind", `{"n": 1.5}`))
	callErr(t, err, Failed)
	// A whole number beyond int64 is a valid JSON integer that no int64 holds:
	// the script gets it as a float (documented in normalize).
	res, err = p.Call(context.Background(), toolReq("kind", `{"n": 1e30}`))
	if err != nil || res.Text != "float" {
		t.Errorf("1e30: %q, %v", res.Text, err)
	}
}

func TestSettingsIntAcceptsDecodedNumbers(t *testing.T) {
	decl := map[string]core.Setting{"n": {Type: "int"}}
	for _, v := range []any{float64(7), json.Number("7"), 7, int64(7), uint64(7)} {
		s, err := newSettings(decl, map[string]any{"n": v})
		if err != nil || s.vals["n"].String() != "7" {
			t.Errorf("%T: %v, %v", v, s, err)
		}
	}
	for _, v := range []any{1.5, json.Number("1.5"), "7", math.Inf(1)} {
		if _, err := newSettings(decl, map[string]any{"n": v}); err == nil {
			t.Errorf("%T %v accepted", v, v)
		}
	}
}

// testOptions are the client's options plus extra predeclared names.
func testOptions(extra starlark.StringDict) Options {
	o := clientOptions()
	o.Extra = merge(o.Extra, extra)
	return o
}

func runProgram(srcs map[string][]byte, st *settingsValue, o Options, lim *runLimits) (*Program, error) {
	sc, err := run(srcs, st, o, lim)
	if err != nil {
		return nil, err
	}
	return &Program{sc}, nil
}
