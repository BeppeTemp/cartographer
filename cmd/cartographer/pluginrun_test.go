package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const cmdScript = `def hello(args):
    print("printed " + settings.greeting)
    log.info("logged")
    return "text\x1b[31m returned\nsecond"

def boom(args):
    fail("bad\x1b[2J thing")

def code(args):
    return 7

def deny(args):
    files.write("{home}/x", "y")

command("hello", hello, "say hello", usage="[name]")
command("boom", boom, "fail")
command("code", code, "exit with 7")
command("deny", deny, "needs a permission")
`

const cmdManifest = "schema: 1\nname: %s\nversion: 1.0.0\ndisplay_name: Team\ndescription: Commands.\ncartographer: \">=0.28.0\"\nsettings:\n  greeting:\n    type: string\n    default: hi\n    description: The greeting.\n  count:\n    type: int\n    default: 1\n    description: A count.\n  loud:\n    type: bool\n    default: false\n    description: Loud.\n"

func installScripted(t *testing.T, name, script string, approve bool) string {
	t.Helper()
	home := pluginFixture(t)
	dir := writePlugin(t, name, map[string]string{
		"cartographer-plugin.yaml": strings.Replace(cmdManifest, "%s", name, 1),
		"plugin.star":              script,
	})
	args := []string{"install", dir}
	if approve {
		args = append(args, "--yes")
	}
	if code, out := runPlugin(t, args...); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	return home
}

func runCmd(t *testing.T, name string, args ...string) (code int, handled bool, out, errOut string) {
	t.Helper()
	errOut = withStderr(t, func() {
		out = withStdout(t, func() { code, handled = pluginCommandFn(name, args) })
	})
	return
}

func TestPluginCommandRunsWithSettingsAndEscapesWhatReachesTheTerminal(t *testing.T) {
	installScripted(t, "team-cmds", cmdScript, true)
	code, handled, out, errOut := runCmd(t, "team-cmds", "hello")
	if !handled || code != 0 {
		t.Fatalf("exit %d handled %v\n%s", code, handled, errOut)
	}
	if !strings.Contains(out, "printed hi") || !strings.Contains(errOut, "plugin team-cmds: logged") {
		t.Errorf("print on stdout, log on stderr:\nout=%q\nerr=%q", out, errOut)
	}
	if strings.Contains(out, "\x1b") || !strings.Contains(out, `text\x1b[31m returned`+"\nsecond") {
		t.Errorf("a returned string must be escaped line by line: %q", out)
	}
	// fail(): exit 1, the message escaped; a denial and an exit code.
	code, _, _, errOut = runCmd(t, "team-cmds", "boom")
	if code != 1 || strings.Contains(errOut, "\x1b") || !strings.Contains(errOut, `bad\x1b[2J thing`) {
		t.Errorf("fail: exit %d %q", code, errOut)
	}
	if code, _, _, _ = runCmd(t, "team-cmds", "code"); code != 7 {
		t.Errorf("exit code %d, want 7", code)
	}
	code, _, _, errOut = runCmd(t, "team-cmds", "deny")
	if code != 1 || !strings.Contains(errOut, "permissions.files") {
		t.Errorf("denied: exit %d %q", code, errOut)
	}
}

func TestPluginCommandListingHelpAndUnknown(t *testing.T) {
	installScripted(t, "team-cmds", cmdScript, true)
	code, _, out, _ := runCmd(t, "team-cmds")
	if code != 0 || !strings.Contains(out, "hello") || !strings.Contains(out, "say hello") {
		t.Errorf("listing: %d %q", code, out)
	}
	if code, _, out, _ = runCmd(t, "team-cmds", "hello", "--help"); code != 0 || !strings.Contains(out, "[name]") {
		t.Errorf("help: %d %q", code, out)
	}
	code, _, _, errOut := runCmd(t, "team-cmds", "nope")
	if code != 2 || !strings.Contains(errOut, "hello") {
		t.Errorf("unknown command: %d %q", code, errOut)
	}
	lines := pluginHelpLines()
	if len(lines) != 4 || !strings.Contains(lines[0], "team-cmds boom") {
		t.Errorf("usage lines: %q", lines)
	}
	var b bytes.Buffer
	printUsage(&b)
	if !strings.Contains(b.String(), "Plugins:") || !strings.Contains(b.String(), "team-cmds hello") {
		t.Errorf("usage lacks the plugin commands:\n%s", b.String())
	}
}

func TestPluginCommandNotDispatchedUntilApprovedOrWhenUnknown(t *testing.T) {
	installScripted(t, "team-cmds", cmdScript, false)
	code, handled, _, errOut := runCmd(t, "team-cmds", "hello")
	if !handled || code != 2 || !strings.Contains(errOut, "installed but not approved") {
		t.Errorf("unapproved: %d %v %q", code, handled, errOut)
	}
	if lines := pluginHelpLines(); len(lines) != 0 {
		t.Errorf("an unapproved plugin's commands are listed: %q", lines)
	}
	if _, handled, _, _ = runCmd(t, "no-such-plugin"); handled {
		t.Error("a word that names no plugin must fall through to the unknown-command error")
	}
}

func TestPluginCommandAKeepsRunningTheApprovedTreeWhileAnUpdateWaits(t *testing.T) {
	home := installScripted(t, "team-cmds", cmdScript, true)
	e := loadEntry(t, home, "team-cmds")
	cfg := mustCfg(t, home)
	e.Installed = strings.Repeat("a", 64) // a newer tree is installed, not approved
	if err := cfg.SetPlugin("team-cmds", e); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if code, handled, _, _ := runCmd(t, "team-cmds", "code"); !handled || code != 7 {
		t.Errorf("the approved tree must keep running: %d %v", code, handled)
	}
}

type recordingKB struct{ calls []string }

func (r *recordingKB) Call(_ context.Context, tool string, args map[string]any) (string, error) {
	r.calls = append(r.calls, tool)
	return "{}", nil
}
func (r *recordingKB) ReadOnly(context.Context, string) (bool, error) { return true, nil }

func TestScopedKBRefusesAKBOutsideTheClientsSet(t *testing.T) {
	rec := &recordingKB{}
	s := scopedKB{KBCaller: rec, allowed: []string{"kb-a"}}
	for name, args := range map[string]map[string]any{
		"outside":  {"kb": "kb-b"},
		"missing":  {},
		"not text": {"kb": 3},
		"case":     {"kb": "KB-A"},
		"padded":   {"kb": "kb-a "},
	} {
		if _, err := s.Call(context.Background(), "concept_read", args); err == nil {
			t.Errorf("%s: a KB outside the set was reached", name)
		}
	}
	if len(rec.calls) != 0 {
		t.Fatalf("the server was asked: %v", rec.calls)
	}
	if _, err := s.Call(context.Background(), "concept_read", map[string]any{"kb": "kb-a"}); err != nil || len(rec.calls) != 1 {
		t.Errorf("a KB of the set: %v %v", err, rec.calls)
	}
	// With no routed endpoint recorded the adapter says so instead of reaching anywhere.
	cfg := clientconfig.Default()
	cfg.ServerURL = "https://example.com"
	if _, err := cliKB(cfg).Call(context.Background(), "x", map[string]any{"kb": "kb-a"}); err == nil || !strings.Contains(err.Error(), "routed") {
		t.Errorf("no routed endpoint: %v", err)
	}
}

const evScript = `def done(ctx):
    log.info("got " + ctx.event + " " + str(ctx.installed) + " " + str(ctx.updated) + " " + str(ctx.removed) + " " + str(ctx.pending))

def bad(ctx):
    fail("nope\x1b[2J")

on("sync.after", %s)
`

func TestEventsRunInPluginOrderAndAFailureIsOnlyAWarning(t *testing.T) {
	home := installScripted(t, "beta-ev", strings.Replace(evScript, "%s", "bad", 1), true)
	dir := writePlugin(t, "alpha-ev", map[string]string{
		"cartographer-plugin.yaml": strings.Replace(cmdManifest, "%s", "alpha-ev", 1),
		"plugin.star":              strings.Replace(evScript, "%s", "done", 1),
	})
	if code, out := runPlugin(t, "install", dir, "--yes"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	cfg := mustCfg(t, home)
	results := map[string]provisioning.AppliedResult{"p": {
		Written: []provisioning.ManagedFile{{Kind: "skill", Name: "old", Path: "a"}, {Kind: "skill", Name: "new", Path: "b"}},
		Pruned:  []provisioning.ManagedFile{{Kind: "skill", Name: "gone", Path: "c"}},
	}}
	fields := eventFields(eventSyncAfter, []string{"claude-code"}, map[string]bool{"skill\x00old\x00a": true}, results, nil)
	var w bytes.Buffer
	firePluginEvent(cfg, eventSyncAfter, fields, &w)
	out := w.String()
	ia, ib := strings.Index(out, "plugin alpha-ev: got sync.after 1 1 1 0"), strings.Index(out, "warning: plugin beta-ev: sync.after failed: nope")
	if ia < 0 || ib < 0 || ia > ib || strings.Contains(out, "\x1b") {
		t.Errorf("order, counts or escaping wrong:\n%q", out)
	}
	w.Reset()
	firePluginEvent(cfg, eventConnectAfter, fields, &w)
	if w.Len() != 0 {
		t.Errorf("a handler of another event ran: %q", w.String())
	}
}

func TestEventFieldsCountPendingAndConnectCountsWritesAsInstalled(t *testing.T) {
	r := map[string]provisioning.AppliedResult{"p": {Written: make([]provisioning.ManagedFile, 2), NeedsApproval: make([]contract.Artifact, 3)}}
	f := eventFields(eventConnectAfter, nil, nil, r, nil)
	if f["installed"] != 2 || f["updated"] != 0 || f["pending"] != 3 || len(f["errors"].([]string)) != 0 || len(f["providers"].([]string)) != 0 {
		t.Errorf("%v", f)
	}
}

func TestPluginConfigSetsValidatesAndNeverTouchesTheApproval(t *testing.T) {
	home := installScripted(t, "team-cmds", cmdScript, true)
	before := loadEntry(t, home, "team-cmds")
	if code, out := runPlugin(t, "config", "team-cmds", "greeting=yo", "count=5", "loud=true"); code != 0 || !strings.Contains(out, "greeting = yo  (set)") {
		t.Fatalf("%d %s", code, out)
	}
	after := loadEntry(t, home, "team-cmds")
	if after.Approved != before.Approved || after.Installed != before.Installed || !after.Enabled {
		t.Errorf("config changed the approval: %+v", after)
	}
	if _, _, out, _ := runCmd(t, "team-cmds", "hello"); !strings.Contains(out, "printed yo") {
		t.Errorf("the setting was not used: %q", out)
	}
	for _, bad := range [][]string{{"nokey=1"}, {"count=x"}, {"count=99999999999999999999"}, {"loud=maybe"}, {"greeting"}} {
		var code int
		errOut := withStderr(t, func() { code, _ = runPlugin(t, append([]string{"config", "team-cmds"}, bad...)...) })
		if code != 2 {
			t.Errorf("%v: exit %d %s", bad, code, errOut)
		}
	}
	if code, out := runPlugin(t, "config", "team-cmds", "--unset", "greeting"); code != 0 || !strings.Contains(out, "greeting = hi  (default)") {
		t.Errorf("unset: %d %s", code, out)
	}
	if code, out := runPlugin(t, "config", "team-cmds"); code != 0 || !strings.Contains(out, "count = 5  (set)") {
		t.Errorf("show: %d %s", code, out)
	}
	if code, out := runPlugin(t, "show", "team-cmds"); code != 0 || !strings.Contains(out, "setting count = 5  (set)") {
		t.Errorf("plugin show lacks the settings: %d %s", code, out)
	}
	if code := withStderrCode(t, func() int { c, _ := runPlugin(t, "config", "nope", "a=b"); return c }); code != 2 {
		t.Errorf("unknown plugin: %d", code)
	}
}

func withStderrCode(t *testing.T, f func() int) (code int) {
	t.Helper()
	withStderr(t, func() { code = f() })
	return
}

func TestPluginConfigShowsAnUnsetSettingAsUnset(t *testing.T) {
	var b bytes.Buffer
	p := core.Plugin{Manifest: core.Manifest{Settings: map[string]core.Setting{"token": {Type: "string"}}}}
	printPluginSettings(&b, p, clientconfig.PluginEntry{})
	if !strings.Contains(b.String(), "token = (unset)  (default)") || strings.Contains(b.String(), "<nil>") {
		t.Errorf("%q", b.String())
	}
}

// plugin approve records the tools of the approved tree as the transport hint;
// a plugin without tools records none.
func TestApproveRecordsTheToolsHint(t *testing.T) {
	installScripted(t, "team-tools", "def t(args):\n    return \"x\"\n\ndef w(args):\n    return \"y\"\n\ntool(\"bone\", t, \"one\")\ntool(\"atwo\", w, \"two\", writes=True)\ncommand(\"c\", t, \"c\")\n", true)
	cfg, _, code := pluginLoad()
	if code >= 0 {
		t.Fatal("load")
	}
	e, _ := cfg.Plugin("team-tools")
	if got := strings.Join(e.Tools, ","); got != "team_tools_atwo,team_tools_bone" {
		t.Fatalf("tools = %q", got)
	}
	if !cfg.PluginProvidesTools() {
		t.Error("PluginProvidesTools is false for an approved plugin with tools")
	}
}
