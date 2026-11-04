package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/service"
)

func baseSetupFacts() setupFacts {
	return setupFacts{GitFound: true, DataDir: "/data", Detected: []string{"claude"}}
}

func TestPlanSetup(t *testing.T) {
	kb := func(name, origin string) kbRow { return kbRow{Name: name, IsRepo: true, IsKB: true, Origin: origin} }

	tests := []struct {
		name    string
		facts   func(*setupFacts)
		opts    setupOptions
		want    setupPlan
		wantErr error
		errText string
	}{
		{
			name:  "fresh machine, empty remote: install, create, connect the detected agent",
			facts: func(f *setupFacts) { f.RemoteProbed = true },
			opts:  setupOptions{Remote: "git@github.com:me/wiki.git"},
			want: setupPlan{Service: serviceInstall, KB: kbCreate, KBName: "wiki", Remote: "git@github.com:me/wiki.git",
				Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name:  "a remote with commits is mounted, not created",
			facts: func(f *setupFacts) { f.RemoteProbed, f.RemoteHasRefs = true, true },
			opts:  setupOptions{Remote: "https://git.example.com/team/kb"},
			want: setupPlan{Service: serviceInstall, KB: kbClone, KBName: "kb", Remote: "https://git.example.com/team/kb",
				Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name:  "a remote holding only a forge's initial commit is created in, not mounted (D710)",
			facts: func(f *setupFacts) { f.RemoteProbed, f.RemoteHasRefs, f.RemoteForgeInit = true, true, true },
			opts:  setupOptions{Remote: "git@gitlab.example.com:group/kb-a.git"},
			want: setupPlan{Service: serviceInstall, KB: kbCreate, KBName: "kb-a", Remote: "git@gitlab.example.com:group/kb-a.git",
				Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name: "a re-run finds everything done",
			facts: func(f *setupFacts) {
				f.Service = service.Status{Installed: true, Running: true}
				f.KBs = []kbRow{kb("wiki", "git@github.com:me/wiki")}
				f.RemoteProbed, f.RemoteHasRefs = true, true
			},
			opts: setupOptions{Remote: "git@github.com:me/wiki.git/"},
			want: setupPlan{Service: serviceKeep, KB: kbAlreadyThere, KBName: "wiki", Remote: "git@github.com:me/wiki.git/",
				Existing: []string{"wiki"}, Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name:  "an installed but stopped service is started, not reinstalled",
			facts: func(f *setupFacts) { f.Service = service.Status{Installed: true}; f.KBs = []kbRow{kb("a", "")} },
			want:  setupPlan{Service: serviceStart, KB: kbKeepExisting, Existing: []string{"a"}, Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name:    "no KB and no remote: the interview's question",
			wantErr: errSetupNeedsRemote,
		},
		{
			name: "local-only defaults its name",
			opts: setupOptions{NoRemote: true},
			want: setupPlan{Service: serviceInstall, KB: kbCreate, KBName: "my-kb", Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name: "a new KB next to existing ones is the only one bound (D190, narrowest)",
			facts: func(f *setupFacts) {
				f.KBs = []kbRow{kb("old", "x")}
				f.RemoteProbed = true
			},
			opts: setupOptions{Remote: "git@h:me/new.git"},
			want: setupPlan{Service: serviceInstall, KB: kbCreate, KBName: "new", Remote: "git@h:me/new.git",
				Existing: []string{"old"}, Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp", KBs: []string{"new"}},
		},
		{
			name:    "two existing KBs and unbound agents need a choice",
			facts:   func(f *setupFacts) { f.KBs = []kbRow{kb("a", ""), kb("b", "")} },
			wantErr: errSetupNeedsKBChoice,
		},
		{
			name:  "an existing binding stands",
			facts: func(f *setupFacts) { f.KBs = []kbRow{kb("a", ""), kb("b", "")}; f.ProvidersBound = true },
			want:  setupPlan{Service: serviceInstall, KB: kbKeepExisting, Existing: []string{"a", "b"}, Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"},
		},
		{
			name: "a name collision with a different origin is refused",
			facts: func(f *setupFacts) {
				f.KBs = []kbRow{kb("wiki", "git@other:x/wiki")}
				f.RemoteProbed = true
			},
			opts:    setupOptions{Remote: "git@github.com:me/wiki.git"},
			errText: "pass a different --name",
		},
		{
			name:    "no git",
			facts:   func(f *setupFacts) { f.GitFound = false },
			errText: "git is not installed",
		},
		{
			name:    "a client pointed at a remote server is not re-pointed",
			facts:   func(f *setupFacts) { f.ClientURL = "https://kb.example.com/mcp" },
			opts:    setupOptions{NoRemote: true},
			errText: "cartographer connect",
		},
		{
			name:    "no agent anywhere",
			facts:   func(f *setupFacts) { f.Detected = nil },
			opts:    setupOptions{NoRemote: true},
			errText: "no agent client detected",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := baseSetupFacts()
			if tc.facts != nil {
				tc.facts(&f)
			}
			got, err := planSetup(f, tc.opts)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.errText != "":
				if err == nil || !strings.Contains(err.Error(), tc.errText) {
					t.Fatalf("err = %v, want one containing %q", err, tc.errText)
				}
				return
			case err != nil:
				t.Fatalf("planSetup: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("plan =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

// stubSetup replaces every machine-touching indirection and records the calls
// made, in order.
func stubSetup(t *testing.T, facts setupFacts) *[]string {
	t.Helper()
	calls := &[]string{}
	record := func(s string) { *calls = append(*calls, s) }
	saved := []any{setupProbeRemote, setupLookGit, setupServiceStatus, setupInstallSvc, setupStartSvc,
		setupKBCreate, setupKBClone, setupConnect, setupDetected, setupWaitReady, setupProbeAuth, setupWaitBootstrapped}
	t.Cleanup(func() {
		setupProbeRemote = saved[0].(func(context.Context, string) (gitx.RemoteProbe, error))
		setupLookGit = saved[1].(func() bool)
		setupServiceStatus = saved[2].(func() (service.Status, error))
		setupInstallSvc = saved[3].(func() error)
		setupStartSvc = saved[4].(func() error)
		setupKBCreate = saved[5].(func([]string) int)
		setupKBClone = saved[6].(func([]string) int)
		setupConnect = saved[7].(func([]string) int)
		setupDetected = saved[8].(func() []string)
		setupWaitReady = saved[9].(func() (service.Status, bool))
		setupProbeAuth = saved[10].(func(string) (bool, error))
		setupWaitBootstrapped = saved[11].(func() bool)
	})
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	setupProbeRemote = func(_ context.Context, remote string) (gitx.RemoteProbe, error) {
		record("probe " + remote)
		return gitx.RemoteProbe{HasRefs: facts.RemoteHasRefs, ForgeInit: facts.RemoteForgeInit}, nil
	}
	setupLookGit = func() bool { return true }
	setupServiceStatus = func() (service.Status, error) { return facts.Service, nil }
	setupInstallSvc = func() error { record("service install"); return nil }
	setupStartSvc = func() error { record("service start"); return nil }
	setupKBCreate = func(a []string) int { record("kb create " + strings.Join(a, " ")); return 0 }
	setupKBClone = func(a []string) int { record("kb clone " + strings.Join(a, " ")); return 0 }
	setupConnect = func(a []string) int { record("connect " + strings.Join(a, " ")); return 0 }
	setupDetected = func() []string { return []string{"claude"} }
	setupWaitReady = func() (service.Status, bool) { record("verify"); return service.Status{Healthy: true}, true }
	setupProbeAuth = func(addr string) (bool, error) { record("probe auth " + addr); return false, nil }
	setupWaitBootstrapped = func() bool { record("ready"); return true }
	return calls
}

func noPrompt() setupPrompter {
	return setupPrompter{in: bufio.NewReader(strings.NewReader("")), out: io.Discard}
}

func TestRunSetup_FreshMachineRunsEveryStepInOrder(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	code := runSetup(setupOptions{Remote: "git@h:me/wiki.git"}, false, true, false, noPrompt())
	if code != 0 {
		t.Fatalf("runSetup = %d, want 0", code)
	}
	want := []string{
		"probe git@h:me/wiki.git",
		"service install",
		"kb create wiki --restart --remote git@h:me/wiki.git",
		"ready",
		"connect --no-input --agents claude --server-url http://127.0.0.1:39273/mcp",
		"verify",
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls =\n  %q\nwant\n  %q", *calls, want)
	}
}

func TestRunSetup_UnreachableRemoteChangesNothing(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	setupProbeRemote = func(context.Context, string) (gitx.RemoteProbe, error) {
		return gitx.RemoteProbe{}, errors.New("the forge rejected this machine's SSH key")
	}
	if code := runSetup(setupOptions{Remote: "git@h:me/wiki.git"}, false, true, false, noPrompt()); code != 2 {
		t.Fatalf("runSetup = %d, want 2", code)
	}
	if len(*calls) != 0 {
		t.Errorf("a failed probe still ran %q: setup must stop before changing anything", *calls)
	}
}

func TestRunSetup_NonInteractiveWithoutRemoteNamesTheFlags(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	if code := runSetup(setupOptions{}, false, true, false, noPrompt()); code != 2 {
		t.Fatalf("runSetup = %d, want 2", code)
	}
	if len(*calls) != 0 {
		t.Errorf("ran %q without a remote", *calls)
	}
}

func TestRunSetup_DryRunChangesNothing(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	if code := runSetup(setupOptions{NoRemote: true}, false, false, true, noPrompt()); code != 0 {
		t.Fatalf("runSetup = %d, want 0", code)
	}
	if len(*calls) != 0 {
		t.Errorf("dry run ran %q", *calls)
	}
}

func TestRunSetup_InterviewAsksForTheRemote(t *testing.T) {
	calls := stubSetup(t, setupFacts{RemoteHasRefs: true})
	// Remote, then accept the detected agents, then confirm the plan.
	in := setupPrompter{in: bufio.NewReader(strings.NewReader("git@h:team/shared.git\n\n\n")), out: io.Discard}
	if code := runSetup(setupOptions{}, true, false, false, in); code != 0 {
		t.Fatalf("runSetup = %d, want 0", code)
	}
	if !slices.Contains(*calls, "kb clone git@h:team/shared.git shared --restart") {
		t.Errorf("a remote with commits was not cloned: %q", *calls)
	}
}

func TestRunSetup_InterviewDeclinedChangesNothing(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	// Blank remote, then refuse the local-only KB.
	in := setupPrompter{in: bufio.NewReader(strings.NewReader("\nn\n")), out: io.Discard}
	if code := runSetup(setupOptions{}, true, false, false, in); code != 1 {
		t.Fatalf("runSetup = %d, want 1", code)
	}
	if len(*calls) != 0 {
		t.Errorf("ran %q after the operator declined", *calls)
	}
}

func TestRunSetup_StopsAtTheFailingStep(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	setupKBCreate = func(a []string) int { *calls = append(*calls, "kb create"); return 1 }
	if code := runSetup(setupOptions{NoRemote: true}, false, true, false, noPrompt()); code != 1 {
		t.Fatalf("runSetup = %d, want 1", code)
	}
	if slices.Contains(*calls, "connect --no-input --agents claude --server-url http://127.0.0.1:39273/mcp") {
		t.Errorf("connect ran after the KB step failed: %q", *calls)
	}
}

// TestRunSetup_TokenRequiringServerStopsBeforeConnect: a local service that
// answers 401 (a CARTOGRAPHER_TOKENS meant for another server reached it, D268)
// stops setup before connect, instead of letting sync_pull fail with a bare 401.
func TestRunSetup_TokenRequiringServerStopsBeforeConnect(t *testing.T) {
	calls := stubSetup(t, setupFacts{Service: service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}})
	setupProbeAuth = func(addr string) (bool, error) {
		*calls = append(*calls, "probe auth "+addr)
		return true, nil
	}
	if code := runSetup(setupOptions{NoRemote: true, Name: "kb-a"}, false, true, false, noPrompt()); code != 1 {
		t.Fatalf("runSetup = %d, want 1", code)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "connect") || c == "verify" {
			t.Errorf("setup went on to %q against a server that rejects its agents", c)
		}
	}
	if !slices.Contains(*calls, "probe auth 127.0.0.1:39273") {
		t.Errorf("the service was never probed for auth: %q", *calls)
	}
}

func TestRunSetup_ServerWithoutAuthIsProbedThenConnected(t *testing.T) {
	calls := stubSetup(t, setupFacts{Service: service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:39273"}})
	if code := runSetup(setupOptions{NoRemote: true, Name: "kb-a"}, false, true, false, noPrompt()); code != 0 {
		t.Fatalf("runSetup = %d, want 0", code)
	}
	i := slices.Index(*calls, "probe auth 127.0.0.1:39273")
	j := slices.IndexFunc(*calls, func(c string) bool { return strings.HasPrefix(c, "connect") })
	if i < 0 || j < 0 || i > j {
		t.Errorf("want the auth probe before connect: %q", *calls)
	}
}

func TestAuthMismatchMessageNamesTheVariable(t *testing.T) {
	msg := authMismatchMessage("127.0.0.1:39273", "$HOME/.config/cartographer/server.yaml", true, "")
	for _, want := range []string{"401", "CARTOGRAPHER_TOKENS is set in this shell", `mode: "off"`, "$HOME/.config/cartographer/server.yaml", "service restart"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(authMismatchMessage("a", "b", false, ""), "this shell") {
		t.Error("claims the variable is in this shell when it is not")
	}
	if !strings.Contains(authMismatchMessage("a", "b", false, "on"), "CARTOGRAPHER_AUTH=on") {
		t.Error("a CARTOGRAPHER_AUTH forcing auth on is not named")
	}
}

// #415: the confirmation prompt must start on its own line after the plan,
// not glued to the last step ("…reports readyProceed?").
func TestRunSetup_ProceedPromptOnItsOwnLine(t *testing.T) {
	stubSetup(t, setupFacts{})
	var out strings.Builder
	// Accept the detected agents, then confirm the plan.
	in := setupPrompter{in: bufio.NewReader(strings.NewReader("\n\n")), out: &out}
	if code := runSetup(setupOptions{NoRemote: true}, true, false, false, in); code != 0 {
		t.Fatalf("runSetup = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "the server reports ready\n\nProceed? [Y/n]: ") {
		t.Errorf("the prompt does not follow the plan on its own line:\n%s", out.String())
	}
}

func TestRenderSetupPlan_ForgeInitialCommitIsAdoptedNotMounted(t *testing.T) {
	var b strings.Builder
	p := setupPlan{Service: serviceKeep, KB: kbCreate, KBName: "kb-a", Remote: "git@gitlab.example.com:group/kb-a.git", Agents: []string{"claude"}, ServerURL: "http://127.0.0.1:39273/mcp"}
	renderSetupPlan(&b, p, setupFacts{RemoteProbed: true, RemoteHasRefs: true, RemoteForgeInit: true})
	if got := b.String(); !strings.Contains(got, `create "kb-a" and push it to git@gitlab.example.com:group/kb-a.git (the forge's initial commit is adopted`) || strings.Contains(got, `mount "`) {
		t.Errorf("plan = %q", got)
	}
}

// With mixed bindings the new KB goes to the unbound agents only: connect resets
// the binding of every provider it is given --kb for, which silently dropped the
// bound agent's earlier KBs. The plan says what each agent receives.
func TestPlanSetup_MixedBindingsKeepTheBoundAgent(t *testing.T) {
	f := baseSetupFacts()
	f.Detected = []string{"claude", "codex"}
	f.BoundProviders = []string{"claude"}
	f.KBs = []kbRow{{Name: "old", IsRepo: true, IsKB: true, Origin: "x"}}
	f.RemoteProbed = true
	plan, err := planSetup(f, setupOptions{Remote: "git@h:me/new.git"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.KBs, []string{"new"}) || !reflect.DeepEqual(plan.KeepAgents, []string{"claude"}) {
		t.Fatalf("plan KBs/KeepAgents = %v / %v, want [new] / [claude]", plan.KBs, plan.KeepAgents)
	}
	var b strings.Builder
	renderSetupPlan(&b, plan, f)
	if !strings.Contains(b.String(), "connect codex — KBs: new") || !strings.Contains(b.String(), "connect claude — they keep their existing KB binding") {
		t.Errorf("the plan does not say what each agent receives:\n%s", b.String())
	}

	calls := stubSetup(t, f)
	code := runSetupPlan(plan)
	if code != 0 {
		t.Fatalf("runSetupPlan = %d", code)
	}
	var connects []string
	for _, c := range *calls {
		if strings.HasPrefix(c, "connect ") {
			connects = append(connects, c)
		}
	}
	want := []string{
		"connect --no-input --agents codex --server-url http://127.0.0.1:39273/mcp --kb new",
		"connect --no-input --agents claude --server-url http://127.0.0.1:39273/mcp",
	}
	if !reflect.DeepEqual(connects, want) {
		t.Errorf("connects =\n  %q\nwant\n  %q", connects, want)
	}

	// Every agent already bound: nothing to pass.
	f.BoundProviders = []string{"claude", "codex"}
	f.ProvidersBound = true
	if plan, _ = planSetup(f, setupOptions{Remote: "git@h:me/new.git"}); plan.KBs != nil || plan.KeepAgents != nil {
		t.Errorf("all agents bound: KBs/KeepAgents = %v / %v, want none", plan.KBs, plan.KeepAgents)
	}
}

// A service installed on another port: connect is told its URL, and a loopback
// client URL on a different port is refused at plan time.
func TestPlanSetup_ServiceOnAnotherPort(t *testing.T) {
	f := baseSetupFacts()
	f.Service = service.Status{Installed: true, Running: true, HTTPAddr: "127.0.0.1:40000"}
	f.KBs = []kbRow{{Name: "a", IsRepo: true, IsKB: true}}
	plan, err := planSetup(f, setupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ServerURL != "http://127.0.0.1:40000/mcp" {
		t.Errorf("ServerURL = %q, want the service's port", plan.ServerURL)
	}
	f.Service.HTTPAddr = ":40000"
	if plan, _ = planSetup(f, setupOptions{}); plan.ServerURL != "http://127.0.0.1:40000/mcp" {
		t.Errorf("a bare-port address must read as loopback, got %q", plan.ServerURL)
	}

	f.ClientURL = "http://127.0.0.1:39273/mcp"
	if _, err := planSetup(f, setupOptions{}); err == nil || !strings.Contains(err.Error(), "40000") {
		t.Errorf("a client on another loopback port: err = %v, want a refusal naming the service port", err)
	}
	f.ClientURL = "http://localhost:40000/mcp"
	if _, err := planSetup(f, setupOptions{}); err != nil {
		t.Errorf("a client on the service's port is fine: %v", err)
	}
}

// The agents step waits for the server to finish loading its KBs, and a server
// that does not stops setup before connect.
func TestRunSetup_WaitsForTheServerBeforeConnect(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	setupWaitBootstrapped = func() bool { *calls = append(*calls, "ready?"); return false }
	if code := runSetup(setupOptions{NoRemote: true, Name: "kb-a"}, false, true, false, noPrompt()); code != 1 {
		t.Fatalf("runSetup = %d, want 1", code)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "connect") || c == "verify" {
			t.Errorf("setup went on to %q with the server still bootstrapping", c)
		}
	}
}

// Setup can fail after it installed the service and created the KB, so every
// failing step maps to 1: 2 means "nothing changed" (docs/configurator.md).
func TestRunSetup_FailingStepsExitOne(t *testing.T) {
	calls := stubSetup(t, setupFacts{})
	setupConnect = func(a []string) int { *calls = append(*calls, "connect"); return 2 }
	if code := runSetup(setupOptions{NoRemote: true, Name: "kb-a"}, false, true, false, noPrompt()); code != 1 {
		t.Errorf("a failing connect after the install exited %d, want 1", code)
	}
	calls = stubSetup(t, setupFacts{})
	setupKBCreate = func(a []string) int { return 2 }
	if code := runSetup(setupOptions{NoRemote: true, Name: "kb-a"}, false, true, false, noPrompt()); code != 1 {
		t.Errorf("a failing KB step exited %d, want 1", code)
	}
}

func TestSameRemoteIgnoresCredentials(t *testing.T) {
	if !sameRemote(gitx.RedactRemoteURL("https://u:tok@example.com/o/kb.git"), "https://u:tok@example.com/o/kb.git") {
		t.Fatal("a redacted origin must still match the remote it came from")
	}
}
