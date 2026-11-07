package main

import (
	"bufio"
	"bytes"
	"errors"
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
	// A second run without the flag changes nothing; with it, the same source takes the pending plugin over.
	before, _ := os.ReadFile(clientconfig.Path(home))
	code, out, _ = prep(t, pluginPrep{Sources: []string{dir}})
	after, _ := os.ReadFile(clientconfig.Path(home))
	if code != 0 || !bytes.Equal(before, after) || !strings.Contains(out, "already installed") {
		t.Errorf("second run: exit %d changed=%v\n%s", code, !bytes.Equal(before, after), out)
	}
	if code, _, _ = prep(t, pluginPrep{Sources: []string{dir}, Approve: true}); code != 0 || loadEntry(t, home, "team-forks").Approved == "" {
		t.Errorf("the flag of a rerun of the same source left it pending")
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
	// Alone, the dependent is refused as `plugin approve` refuses it: nothing is approved and the dependency is named.
	home2 := pluginFixture(t)
	code, out, errOut = prep(t, pluginPrep{Sources: []string{dependsPlugin(t, "uses-base", "base-plugin")}, Approve: true})
	e := loadEntry(t, home2, "uses-base")
	if code != 1 || e.Approved != "" || !strings.Contains(errOut, "base-plugin") ||
		!strings.Contains(errOut, "uses-base was not approved: enable what it depends on, then run `cartographer plugin approve uses-base`") {
		t.Errorf("exit %d entry %+v\n%s\n%s", code, e, out, errOut)
	}
}

// A run that stops at a later source leaves the earlier ones installed and
// pending: the rerun of the same sources approves them, and only them.
func TestPreparePluginsRerunTakesOverWhatAFailedRunLeftPending(t *testing.T) {
	home := pluginFixture(t)
	other := writePlugin(t, "other-plugin", nil)
	if code, _, errOut := prep(t, pluginPrep{Sources: []string{other}}); code != 0 {
		t.Fatal(errOut)
	}
	good := writePlugin(t, "team-forks", nil)
	bad := t.TempDir() // no manifest
	code, _, _ := prep(t, pluginPrep{Sources: []string{good, bad}, Approve: true})
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if e := loadEntry(t, home, "team-forks"); e.Installed == "" || e.Approved != "" {
		t.Fatalf("after the failed run: %+v", e)
	}
	var names []string
	code, out, errOut := prep(t, pluginPrep{Sources: []string{good}, Approve: true, Names: &names})
	if code != 0 {
		t.Fatalf("rerun: exit %d\n%s\n%s", code, out, errOut)
	}
	if e := loadEntry(t, home, "team-forks"); !e.Enabled || e.Approved != e.Installed {
		t.Errorf("rerun left %+v", e)
	}
	if !strings.Contains(out, "Enabled team-forks 1.0.0") || !strings.Contains(out, "skill \"helper\"") {
		t.Errorf("the consent block and the result are printed:\n%s", out)
	}
	if len(names) != 1 || names[0] != "team-forks" {
		t.Errorf("this run's plugins: %v", names)
	}
	if e := loadEntry(t, home, "other-plugin"); e.Approved != "" {
		t.Errorf("a plugin pending from another source was approved: %+v", e)
	}
}

// A source that now yields another tree is not the stored one: it stays pending.
func TestPreparePluginsRerunDoesNotApproveAChangedTree(t *testing.T) {
	home := pluginFixture(t)
	dir := writePlugin(t, "team-forks", nil)
	if code, _, errOut := prep(t, pluginPrep{Sources: []string{dir}}); code != 0 {
		t.Fatal(errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "helper", "SKILL.md"), []byte("---\nname: helper\ndescription: Helps with another thing, use it when asked.\n---\n\n# Helper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := prep(t, pluginPrep{Sources: []string{dir}, Approve: true}); code != 0 {
		t.Fatal(errOut)
	}
	if e := loadEntry(t, home, "team-forks"); e.Approved != "" {
		t.Errorf("approved a tree the user was not shown: %+v", e)
	}
}

func TestWarnPluginEscapesTheErrorItQuotes(t *testing.T) {
	var b bytes.Buffer
	warnPlugin(&b, "team-forks", errors.New("tree entry \x1b[31mred and git said \x1b]0;title\a"))
	if got := b.String(); strings.ContainsAny(got, "\x1b\a") || !strings.HasPrefix(got, "warning: plugin team-forks: ") {
		t.Errorf("%q", got)
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
	// Nothing is connected, and the end says so instead of "restart your sessions".
	if strings.Contains(out, "Restart your agent sessions") || strings.Contains(out, "Cartographer is set up.") ||
		!strings.Contains(out, "no agent is connected yet") || !strings.Contains(out, "cartographer plugin approve team-forks") || !strings.Contains(out, "cartographer setup") {
		t.Errorf("the closing message:\n%s", out)
	}
}

// Only this run's plugins are named as awaited: one pending from elsewhere is not.
func TestRunSetupPendingNamesOnlyThisRunsPlugins(t *testing.T) {
	stubSetup(t, setupFacts{})
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	if code, _, errOut := prep(t, pluginPrep{Sources: []string{writePlugin(t, "elsewhere", nil)}}); code != 0 {
		t.Fatal(errOut)
	}
	setupDetected = func() []string { return nil }
	var code int
	out := withStdout(t, func() {
		code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{providerPlugin(t, "team-forks")}}, false, true, false, noPrompt())
	})
	if code != 0 || !strings.Contains(out, "plugin approve team-forks") || strings.Contains(out, "plugin approve elsewhere") {
		t.Fatalf("runSetup = %d\n%s", code, out)
	}
}

func detectedWith(extra string) func() []string {
	return func() []string {
		out := []string{"claude"}
		if registered(extra) {
			out = append(out, extra)
		}
		return out
	}
}

// A client already detected must not hide the one a plugin brings.
func TestRunSetupConnectsThePluginProviderNextToTheDetectedClient(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	setupDetected = detectedWith("fork-cli")
	var code int
	out := withStdout(t, func() {
		code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{providerPlugin(t, "team-forks")}, ApprovePlugins: true}, false, true, false, noPrompt())
	})
	if code != 0 {
		t.Fatalf("runSetup = %d\n%s", code, out)
	}
	if got := strings.Join(*calls, "\n"); !strings.Contains(got, "connect --no-input --agents claude,fork-cli ") {
		t.Errorf("calls:\n%s", got)
	}
}

// --agents naming a provider that only a plugin registers is checked after the plugins step.
func TestRunSetupAgentsNamingAPluginProviderAreCheckedAfterThePluginsStep(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	var code int
	out := withStdout(t, func() {
		code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{providerPlugin(t, "team-forks")}, ApprovePlugins: true, Agents: []string{"fork-cli"}, AgentsDeferred: true}, false, true, false, noPrompt())
	})
	if code != 0 || !strings.Contains(strings.Join(*calls, "\n"), "connect --no-input --agents fork-cli ") {
		t.Fatalf("runSetup = %d\n%s\n%v", code, out, *calls)
	}

	// Still unknown once the plugins are registered: the usual message, nothing connected.
	calls = stubSetup(t, setupFacts{})
	freshProcess(t, home)
	errOut := withStderr(t, func() {
		out = withStdout(t, func() {
			code = runSetup(setupOptions{Remote: "git@h:me/wiki.git", Plugins: []string{writePlugin(t, "plain", nil)}, Agents: []string{"nonesuch"}, AgentsDeferred: true}, false, true, false, noPrompt())
		})
	})
	if code != 1 || !strings.Contains(errOut, `unknown provider "nonesuch"`) {
		t.Fatalf("runSetup = %d\n%s\n%s", code, out, errOut)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "connect") {
			t.Errorf("connected: %q", c)
		}
	}
}

func TestSplitProviderCSVValidatesNothingButTheShape(t *testing.T) {
	got, err := splitProviderCSV("fork-cli, claude,fork-cli")
	if err != nil || strings.Join(got, ",") != "fork-cli,claude" {
		t.Errorf("%v %v", got, err)
	}
	if _, err := splitProviderCSV("claude,"); err == nil {
		t.Error("an empty name is an error")
	}
}

func TestRenderSetupPlanRedactsThePluginSources(t *testing.T) {
	facts := setupFacts{GitFound: true, RemoteProbed: true, Backend: "launchd", Detected: []string{"claude"}}
	o := setupOptions{Remote: "git@github.com:me/wiki.git", Plugins: []string{"https://user:s3cret@git.example.com/team/forks@v1"}}
	plan, err := planSetup(facts, o)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	renderSetupPlan(&b, plan, facts)
	if strings.Contains(b.String(), "s3cret") || !strings.Contains(b.String(), "git.example.com/team/forks") {
		t.Errorf("plan:\n%s", b.String())
	}
}

// With nothing detected before the plugins step and the narrow KB choice, the agents it brings are connected to the KB this run adds.
func TestRunSetupNarrowKBAppliesToTheAgentsDetectedAfterThePlugins(t *testing.T) {
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
	plan := setupPlan{KB: kbAlreadyThere, KBName: "new-kb", Existing: []string{"old-kb"}, Plugins: []string{providerPlugin(t, "team-forks")}, ApprovePlugins: true, AgentsAfterPlugins: true, Redetect: true, NarrowKB: true}
	var code int
	withStdout(t, func() { code = runSetupPlan(plan) })
	if code != 0 || !strings.Contains(strings.Join(*calls, "\n"), "--agents fork-cli --kb new-kb") {
		t.Errorf("exit %d calls:\n%s", code, strings.Join(*calls, "\n"))
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
