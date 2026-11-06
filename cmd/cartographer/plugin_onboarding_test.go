package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/plugin"
)

func prep(t *testing.T, o pluginPrep) (code int, out, errOut string) {
	t.Helper()
	var ob, eb bytes.Buffer
	o.Out, o.Err = &ob, &eb
	code = preparePlugins(o)
	return code, ob.String(), eb.String()
}

func TestPreparePluginsApproveFlagPrintsWhatRunsThenEnablesAndRegisters(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	extra := map[string]string{"providers/fork-cli.yaml": forkProviderYAML}
	if execbit.Supported {
		extra["hooks/guard/hook.json"] = `{"event":"Stop","command":"./run.sh"}`
		extra["hooks/guard/run.sh"] = "#!/bin/sh\necho SCRIPT-BODY\n"
	}
	dir := writePlugin(t, "team-forks", extra)
	if execbit.Supported {
		if err := os.Chmod(filepath.Join(dir, "hooks", "guard", "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := prep(t, pluginPrep{Sources: []string{dir}, Approve: true})
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	// The flag is the consent and the printed block its record: the bytes come first.
	if execbit.Supported {
		i, j := strings.Index(out, "SCRIPT-BODY"), strings.Index(out, "Enabled team-forks 1.0.0")
		if i < 0 || j < 0 || i > j {
			t.Errorf("the hook script must be printed before the approval:\n%s", out)
		}
	}
	e := loadEntry(t, home, "team-forks")
	if !e.Enabled || e.Approved == "" || e.Approved != e.Installed {
		t.Errorf("entry: %+v", e)
	}
	// The same process sees the provider: registration ran again after the change.
	if !registered("fork-cli") {
		t.Error("the approved provider is not registered in this process")
	}
}

func TestPreparePluginsWithoutTheFlagOrATerminalApprovesNothing(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	dir := providerPlugin(t, "team-forks")
	code, out, _ := prep(t, pluginPrep{Sources: []string{dir}, Ask: true})
	if code != 0 || !strings.Contains(out, "pending approval: cartographer plugin approve team-forks") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if e := loadEntry(t, home, "team-forks"); e.Approved != "" || e.Installed == "" {
		t.Errorf("entry: %+v", e)
	}
	if registered("fork-cli") {
		t.Error("a pending plugin registered its provider")
	}
	// A second run changes nothing and, with the flag, still does not approve what a previous run left pending.
	before, _ := os.ReadFile(clientconfig.Path(home))
	code, out, _ = prep(t, pluginPrep{Sources: []string{dir}, Approve: true})
	after, _ := os.ReadFile(clientconfig.Path(home))
	if code != 0 || !bytes.Equal(before, after) || !strings.Contains(out, "already installed") {
		t.Errorf("second run: exit %d changed=%v\n%s", code, !bytes.Equal(before, after), out)
	}
}

func TestPreparePluginsAsksOnATerminal(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	pluginInteractive = func() bool { return true }
	for answer, approved := range map[string]bool{"n\n": false, "y\n": true} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		w.WriteString(answer)
		w.Close()
		old := os.Stdin
		os.Stdin = r
		dir := writePlugin(t, "team-"+strings.TrimSpace(answer), nil)
		code, out, _ := prep(t, pluginPrep{Sources: []string{dir}, Ask: true})
		os.Stdin = old
		// The question itself goes to the terminal, not to the run's writer.
		if code != 0 || !strings.Contains(out, "skill \"helper\"") {
			t.Fatalf("%q: exit %d\n%s", answer, code, out)
		}
		if e := loadEntry(t, home, "team-"+strings.TrimSpace(answer)); (e.Approved != "") != approved {
			t.Errorf("%q: entry %+v", answer, e)
		}
	}
}

func TestPreparePluginsRefusesAnotherSourceUnderAnInstalledName(t *testing.T) {
	pluginFixture(t)
	if code, _, errOut := prep(t, pluginPrep{Sources: []string{writePlugin(t, "team-forks", nil)}}); code != 0 {
		t.Fatal(errOut)
	}
	code, _, errOut := prep(t, pluginPrep{Sources: []string{writePlugin(t, "team-forks", nil)}})
	if code != 2 || !strings.Contains(errOut, "plugin remove team-forks") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
}

func TestPreparePluginsDryRunWritesNothing(t *testing.T) {
	home := pluginFixture(t)
	code, out, _ := prep(t, pluginPrep{Sources: []string{writePlugin(t, "team-forks", nil)}, Approve: true, DryRun: true})
	if code != 0 || !strings.Contains(out, "[dry-run] would install plugin from") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(clientconfig.Path(home)); err == nil {
		t.Error("a dry run wrote the client config")
	}
	store, _ := pluginStoreFn()
	if entries, _ := os.ReadDir(store.Root); len(entries) != 0 {
		t.Errorf("a dry run stored a tree: %v", entries)
	}
}

func TestPreparePluginsAFailingFetchOfAConfiguredPluginIsAWarning(t *testing.T) {
	home := pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	if code, _, errOut := prep(t, pluginPrep{Sources: []string{dir}, Approve: true}); code != 0 {
		t.Fatal(errOut)
	}
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(store.Root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := prep(t, pluginPrep{})
	if code != 0 || !strings.Contains(errOut, "warning: plugin team-forks") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
	if e := loadEntry(t, home, "team-forks"); e.Approved == "" {
		t.Errorf("the entry lost its approval: %+v", e)
	}
}

func TestPreparePluginsApprovesInDependencyOrderAndHoldsAnUnmetOne(t *testing.T) {
	home := pluginFixture(t)
	uses, base := dependsPlugin(t, "uses-base", "base-plugin"), writePlugin(t, "base-plugin", nil)
	code, out, errOut := prep(t, pluginPrep{Sources: []string{uses, base}, Approve: true})
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, n := range []string{"uses-base", "base-plugin"} {
		if e := loadEntry(t, home, n); !e.Enabled || e.Approved == "" {
			t.Errorf("%s: %+v", n, e)
		}
	}
	// Alone, the dependent is approved but not enabled and the dependency is named.
	home2 := pluginFixture(t)
	code, out, _ = prep(t, pluginPrep{Sources: []string{dependsPlugin(t, "uses-base", "base-plugin")}, Approve: true})
	e := loadEntry(t, home2, "uses-base")
	if code != 0 || e.Enabled || e.Approved == "" || !strings.Contains(out, "base-plugin") {
		t.Errorf("exit %d entry %+v\n%s", code, e, out)
	}
}

func TestPreparePluginsEscapesWhatAFailureQuotes(t *testing.T) {
	pluginFixture(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestFile), []byte("schema: 1\nname: x\n\x1b[31mred"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := prep(t, pluginPrep{Sources: []string{dir}})
	if code != 1 || strings.ContainsRune(errOut, 0x1b) {
		t.Errorf("exit %d, raw escape in %q", code, errOut)
	}
}

func TestPluginFlagsExistOnlyWhenEnabled(t *testing.T) {
	if pluginsEnabled() {
		t.Fatal("the plugin flags of setup and connect are dark until the turn-on change")
	}
}

func TestPlanSetupWithPluginsAndNoDetectedClient(t *testing.T) {
	facts := setupFacts{GitFound: true, RemoteProbed: true, Backend: "launchd"}
	o := setupOptions{Remote: "git@github.com:me/wiki.git"}
	if _, err := planSetup(facts, o); err == nil || !strings.Contains(err.Error(), "no agent client detected") {
		t.Fatalf("without plugins the existing error stands: %v", err)
	}
	o.Plugins = []string{"https://git.example.com/team/forks"}
	plan, err := planSetup(facts, o)
	if err != nil || len(plan.Agents) != 0 || !plan.AgentsAfterPlugins {
		t.Fatalf("plan %+v err %v", plan, err)
	}
	var b bytes.Buffer
	renderSetupPlan(&b, plan, facts)
	if !strings.Contains(b.String(), "detected after the plugins are installed") || !strings.Contains(b.String(), "git.example.com/team/forks") {
		t.Errorf("plan:\n%s", b.String())
	}
	// Plugins already configured count the same.
	o.Plugins, facts.PluginsConfigured = nil, true
	if plan, err = planSetup(facts, o); err != nil || !plan.AgentsAfterPlugins {
		t.Errorf("configured plugins: %+v %v", plan, err)
	}
}

// A machine whose only client comes from a plugin: setup installs, approves,
// detects it in the same process and connects it, forwarding the flag.
func TestRunSetupPluginOnlyMachine(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	setupDetected = func() []string {
		if registered("fork-cli") {
			return []string{"fork-cli"}
		}
		return nil
	}
	dir := providerPlugin(t, "team-forks")
	var code int
	out := withStdout(t, func() {
		code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{dir}, ApprovePlugins: true}, false, true, false, noPrompt())
	})
	if code != 0 {
		t.Fatalf("runSetup = %d\n%s", code, out)
	}
	got := strings.Join(*calls, "\n")
	if !strings.Contains(got, "connect --no-input --agents fork-cli --server-url http://127.0.0.1:39273/mcp --approve-plugins") {
		t.Errorf("calls:\n%s", got)
	}
	if !strings.Contains(out, "==> plugins") || loadEntry(t, home, "team-forks").Approved == "" {
		t.Errorf("plugins step:\n%s", out)
	}
}

// Without the flag the plugin stays pending: exit 0, the rest of setup runs.
func TestRunSetupPluginPendingBlocksNothing(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	setupDetected = func() []string {
		if registered("fork-cli") {
			return []string{"fork-cli"}
		}
		return nil
	}
	var code int
	out := withStdout(t, func() {
		code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{providerPlugin(t, "team-forks")}}, false, true, false, noPrompt())
	})
	if code != 0 || !strings.Contains(out, "pending approval: cartographer plugin approve team-forks") {
		t.Fatalf("runSetup = %d\n%s", code, out)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "connect") {
			t.Errorf("connected with nothing detected: %q", c)
		}
	}
	if !strings.Contains(strings.Join(*calls, " "), "verify") {
		t.Errorf("the verify step did not run: %q", *calls)
	}
}

func TestRunSetupDryRunNamesThePluginsAndWritesNothing(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	home := pluginFixture(t)
	setupDetected = func() []string { return nil }
	var code int
	out := withStdout(t, func() {
		code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{providerPlugin(t, "team-forks")}}, false, true, true, setupPrompter{in: bufio.NewReader(strings.NewReader("")), out: os.Stdout})
	})
	if code != 0 || !strings.Contains(out, "plugins      install") || !strings.Contains(out, "detected after the plugins are installed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(clientconfig.Path(home)); err == nil {
		t.Error("a dry run wrote the client config")
	}
	for _, c := range *calls {
		if !strings.HasPrefix(c, "probe") {
			t.Errorf("a dry run ran %q", c)
		}
	}
}
