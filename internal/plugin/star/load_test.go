package star

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"go.starlark.net/starlark"
)

func tree(kv ...string) []plugin.TreeFile {
	var out []plugin.TreeFile
	for i := 0; i < len(kv); i += 2 {
		out = append(out, plugin.TreeFile{Path: kv[i], Content: []byte(kv[i+1])})
	}
	return out
}

func entry(src string) []plugin.TreeFile { return tree("plugin.star", src) }

const full = `
def hello(args):
    return "hi"

def search(q):
    return q

def after():
    pass

command("hello", hello, "say hello", usage="hello [name]")
tool("search", search, "search things", params={"type": "object", "properties": {"q": {"type": "string"}}}, writes=True)
tool("ping", search, "ping")
on("sync.after", after)
on("connect.after", after)
`

func TestLoadRegistersOneOfEach(t *testing.T) {
	p, err := Load(entry(full))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"command acme hello", "event connect.after", "event sync.after", "tool acme_ping", "tool acme_search (writes)"}
	if got := p.Lines("acme"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("lines %q, want %q", got, want)
	}
	var cmd, search *Registration
	for i := range p.Registrations {
		switch p.Registrations[i].Name {
		case "hello":
			cmd = &p.Registrations[i]
		case "search":
			search = &p.Registrations[i]
		}
	}
	if cmd.Help != "say hello" || cmd.Usage != "hello [name]" || cmd.Pos != "plugin.star:11" {
		t.Errorf("command: %+v", cmd)
	}
	if !search.Writes || !strings.Contains(string(search.Params), `"properties"`) {
		t.Errorf("tool: %+v", search)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "plugin.star" || p.Files[0].Lines != strings.Count(full, "\n") || len(p.Files[0].SHA256) != 64 {
		t.Errorf("files: %+v", p.Files)
	}
}

func TestNotAScriptPlugin(t *testing.T) {
	if p, err := Load(tree("skills/x/SKILL.md", "x")); p != nil || err != nil {
		t.Errorf("got %v, %v", p, err)
	}
	if _, err := Load(tree("lib/a.star", "x = 1")); err == nil || !strings.Contains(err.Error(), "needs a plugin.star") {
		t.Errorf("lib without entry: %v", err)
	}
}

func TestLoadIsDeterministic(t *testing.T) {
	a, err1 := Load(entry(full))
	b, err2 := Load(entry(full))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if fmt.Sprint(a.Lines("p")) != fmt.Sprint(b.Lines("p")) || fmt.Sprint(a.Sorted()[0].Pos) != fmt.Sprint(b.Sorted()[0].Pos) || fmt.Sprint(a.Files) != fmt.Sprint(b.Files) {
		t.Error("two loads of one tree differ")
	}
}

func TestRegistrationRules(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"command upper", `command("Hello", f, "h")`, "must match"},
		{"command help", `command("help", f, "h")`, "not be help"},
		{"command long", `command("a123456789012345678901234567890123", f, "h")`, "must match"},
		{"command no help", `command("x", f, "")`, "help is empty"},
		{"command impl not a function", `command("x", 1, "h")`, "impl must be a function"},
		{"command impl a builtin", `command("x", len, "h")`, "impl must be a function"},
		{"tool upper", `tool("Bad", f, "d")`, "must match"},
		{"tool params not a dict", `tool("x", f, "d", params=1)`, "expected a dict"},
		{"tool params not an object", `tool("x", f, "d", params={"type": "string"})`, `"type": "object"`},
		{"tool params bad schema", `tool("x", f, "d", params={"type": "object", "$ref": "#/$defs/nope"})`, "params"},
		{"unknown event", `on("sync.before", f)`, "unknown event"},
		{"duplicate command", `command("x", f, "h")` + "\n" + `command("x", f, "h")`, "command x is registered twice"},
		{"duplicate tool", `tool("x", f, "d")` + "\n" + `tool("x", f, "d")`, "tool x is registered twice"},
		{"second handler", `on("sync.after", f)` + "\n" + `on("sync.after", f)`, "event sync.after is registered twice"},
		{"a command and a tool may share a name", `command("x", f, "h")` + "\n" + `tool("x", f, "d")`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(entry("def f():\n    pass\n\n" + c.src + "\n"))
			switch {
			case c.want == "" && err != nil:
				t.Errorf("unexpected: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestLoadLib(t *testing.T) {
	lib := "def greet(n):\n    return \"hi \" + n\n"
	p, err := Load(tree("plugin.star", "load(\"lib/util.star\", \"greet\")\ncommand(\"g\", greet, \"h\")\n", "lib/util.star", lib))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 || p.Files[0].Path != "lib/util.star" {
		t.Errorf("files: %+v", p.Files)
	}
	// A lib file may load another one.
	if _, err := Load(tree("plugin.star", "load(\"lib/a.star\", \"a\")\n", "lib/a.star", "load(\"lib/b.star\", \"b\")\na = b\n", "lib/b.star", "b = 1\n")); err != nil {
		t.Errorf("lib loading lib: %v", err)
	}
}

func TestLoadRefusals(t *testing.T) {
	cases := []struct {
		name  string
		files []plugin.TreeFile
		want  string
	}{
		{"missing lib", entry(`load("lib/nope.star", "x")`), "no such file"},
		{"traversal", entry(`load("lib/../plugin.star", "x")`), "only lib/<name>.star"},
		{"absolute", entry(`load("/etc/passwd", "x")`), "only lib/<name>.star"},
		{"url", entry(`load("https://example.com/x.star", "x")`), "only lib/<name>.star"},
		{"outside lib", entry(`load("plugin.star", "x")`), "only lib/<name>.star"},
		{"subdirectory", entry(`load("lib/sub/a.star", "x")`), "only lib/<name>.star"},
		{"cycle", tree("plugin.star", `load("lib/a.star", "a")`, "lib/a.star", `load("lib/b.star", "b")`, "lib/b.star", `load("lib/a.star", "a")`), "load cycle"},
		{"registrar in a lib", tree("plugin.star", `load("lib/a.star", "a")`, "lib/a.star", "def a():\n    command(\"x\", a, \"h\")\n"), "undefined: command"},
		{"syntax error", entry("def f(:\n"), "plugin.star:1"},
		{"runtime error with position", entry("x = 1\ny = 1 // 0\n"), "plugin.star:2"},
		{"while", entry("def f():\n    while True:\n        pass\n"), "while"},
		{"recursion", entry("def f():\n    f()\nf()\n"), "recurs"},
		{"top level loop", entry("for i in [1]:\n    pass\n"), "for"},
		{"global reassign", entry("x = 1\nx = 2\n"), "reassign"},
		{"over the cap", entry("# " + strings.Repeat("x", plugin.MaxScriptBytes)), "over the"},
		{"endless at top level", entry("def f():\n    for i in range(1000000000):\n        pass\nf()\n"), "plugin.star:2:5: the script ran more than 20000000 steps"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(c.files)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
	t.Run("too many lib files", func(t *testing.T) {
		files := entry("")
		for i := 0; i <= plugin.MaxLibFiles; i++ {
			files = append(files, plugin.TreeFile{Path: fmt.Sprintf("lib/f%d.star", i)})
		}
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "over the 32 cap") {
			t.Errorf("error %v", err)
		}
	})
}

// No name that reaches the host exists in any phase: there is no os, open or
// exec, and a script cannot grant itself a capability by reaching for one.
func TestNoAmbientAuthority(t *testing.T) {
	for _, name := range []string{"os", "open", "exec", "subprocess", "input", "getenv"} {
		for _, files := range [][]plugin.TreeFile{
			entry("def f():\n    return " + name + "\nf()\n"),
			tree("plugin.star", `load("lib/a.star", "a")`, "lib/a.star", "a = "+name+"\n"),
		} {
			if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "undefined: "+name) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}

func TestLoadPhaseReadsNothing(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"time.now", "t = time.now()", "time.now is not available while plugin.star loads"},
		{"settings", "x = settings.x", "settings.x is not available while plugin.star loads"},
		{"files", `x = files.read("a")`, "files.read is not available while plugin.star loads"},
		{"files.write", `files.write("a", "b")`, "files.write is not available"},
		{"kb", `kb.call("search")`, "kb.call is not available"},
		{"http", `http.request("GET", "https://example.com")`, "http.request is not available"},
		{"env", `env.get("HOME")`, "env.get is not available"},
		{"log", `log.info("x")`, "log.info is not available"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Load(entry(c.src + "\n")); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
	// The pure modules work, and a lib file gets them too.
	src := "x = json.encode({\"a\": math.floor(1.5)})\ny = time.parse_duration(\"1s\")\n"
	if _, err := Load(entry(src)); err != nil {
		t.Errorf("pure modules: %v", err)
	}
}

// callThread is a fresh thread of the CALL phase, as the runner a later change adds makes.
func callThread() *starlark.Thread {
	t := &starlark.Thread{Name: "call"}
	t.SetLocal(ctxKey, &callCtx{phase: phaseCall})
	return t
}

// A registered function keeps working after Load returns (its globals are
// frozen, not gone), and the late-bound names see the context of the thread
// that calls it, not the one that was loading when the function was made.
func TestImplsKeepWorkingAfterLoad(t *testing.T) {
	p, err := Load(entry(`
state = []

def impl():
    return time.now().year > 2000

def reg():
    command("late", impl, "h")

def mutate():
    state.append(1)

command("a", impl, "h")
on("sync.after", reg)
on("connect.after", mutate)
`))
	if err != nil {
		t.Fatal(err)
	}
	impls := map[string]*starlark.Function{}
	for _, r := range p.Registrations {
		impls[r.Name] = r.impl
	}
	// time.now fails while loading and works in CALL: the same function, two contexts.
	if v, err := starlark.Call(callThread(), impls["a"], nil, nil); err != nil || v != starlark.True {
		t.Errorf("a: %v, %v", v, err)
	}
	// A registration builtin called from an impl is refused: only plugin.star's LOAD registers.
	if _, err := starlark.Call(callThread(), impls["sync.after"], nil, nil); err == nil || !strings.Contains(err.Error(), "command() can only run while plugin.star loads") {
		t.Errorf("registering from an impl: %v", err)
	}
	// Globals are frozen after load.
	if _, err := starlark.Call(callThread(), impls["connect.after"], nil, nil); err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Errorf("mutating a frozen global: %v", err)
	}
	// A thread without any context is not a load either.
	if _, err := starlark.Call(&starlark.Thread{}, impls["sync.after"], nil, nil); err == nil || !strings.Contains(err.Error(), "can only run while plugin.star loads") {
		t.Errorf("no context: %v", err)
	}
}

func TestCheckReportsTheLoadError(t *testing.T) {
	if got := Check(plugin.SideClient, entry("x = 1\n")); got != nil {
		t.Errorf("valid: %v", got)
	}
	got := Check(plugin.SideClient, entry("x = 1\ny = z\n"))
	if len(got) != 1 || !strings.HasPrefix(got[0], "plugin.star:2:") {
		t.Errorf("got %q", got)
	}
}

// A script that keeps allocating is cancelled by the memory watchdog instead of
// exhausting the process. The cap is lowered so the test is quick.
const allocSrc = `
def grow():
    keep = []
    big = "x" * 1000000
    for i in range(1000000):
        keep.append(big + str(i))
    return len(keep)
`

func lowHeapCap(t *testing.T) {
	old := MaxHeapGrowth
	MaxHeapGrowth = 32 << 20
	t.Cleanup(func() { MaxHeapGrowth = old })
}

func TestLoadMemoryCap(t *testing.T) {
	lowHeapCap(t)
	for _, files := range map[string][]plugin.TreeFile{
		"entry": entry(allocSrc + "grow()\n"),
		"lib":   tree("plugin.star", `load("lib/a.star", "a")`, "lib/a.star", allocSrc+"a = grow()\n"),
	} {
		_, err := Load(files)
		if err == nil || !strings.Contains(err.Error(), "memory use grew past") {
			t.Errorf("error %v, want the memory cap", err)
		}
	}
}

func TestCallMemoryCap(t *testing.T) {
	lowHeapCap(t)
	p := mustLoad(t, allocSrc+"tool(\"grow\", lambda p: grow(), \"grow\")\n")
	_, err := p.Call(context.Background(), toolReq("grow", `{}`))
	ce := callErr(t, err, LimitExceeded)
	if !strings.Contains(ce.Msg, "memory use grew past") {
		t.Errorf("message %q", ce.Msg)
	}
}

// A call whose registered run of plugin.star allocates is stopped too.
func TestCallRerunMemoryCap(t *testing.T) {
	p := mustLoad(t, "def f(p):\n    return \"ok\"\ntool(\"f\", f, \"f\")\n")
	lowHeapCap(t)
	p.srcs[plugin.ScriptEntry] = []byte(allocSrc + "grow()\ndef f(p):\n    return \"ok\"\ntool(\"f\", f, \"f\")\n")
	_, err := p.Call(context.Background(), toolReq("f", `{}`))
	ce := callErr(t, err, LimitExceeded)
	if !strings.Contains(ce.Msg, "memory use grew past") {
		t.Errorf("message %q", ce.Msg)
	}
}

// The cap is one for the whole call: a re-run of plugin.star that keeps ~24 MB
// and a call that allocates ~24 MB more, each under the 32 MiB cap, trip it.
func TestCallMemoryCapIsOneBaseline(t *testing.T) {
	const tail = `
def f(p):
    keep = ["x" * 1000000 for i in range(24)]
    for i in range(3000000):
        pass
    return str(len(keep))
tool("f", f, "f")
`
	p := mustLoad(t, tail) // registers the same line numbers as the re-run below
	lowHeapCap(t)
	old := HeapSampleEvery
	HeapSampleEvery = time.Millisecond
	t.Cleanup(func() { HeapSampleEvery = old })
	p.srcs[plugin.ScriptEntry] = []byte(tail + `held = ["y" * 1000000 for i in range(24)]` + "\n")
	runtime.GC() // the baseline must not carry the garbage of earlier tests
	_, err := p.call(context.Background(), toolReq("f", `{}`), callOpts{steps: 1 << 40})
	ce := callErr(t, err, LimitExceeded)
	if !strings.Contains(ce.Msg, "memory use grew past") {
		t.Errorf("message %q", ce.Msg)
	}
}
