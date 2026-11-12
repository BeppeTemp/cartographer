package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// `cartographer setup` (D253) is the first-run path in one command: the native
// service, the first KB and the agent clients, then a verification. It owns no
// behaviour of its own beyond deciding what to run — every step is the
// existing command (service install, kb create / kb clone, connect), so setup
// cannot drift from what those commands do and document.
//
// It is split in two on purpose. gatherSetupFacts + planSetup only read: they
// check git, probe the remote with `git ls-remote` (reachability and
// credentials, and whether it is empty) and decide every step before anything
// is written — so a wrong URL or a missing SSH key stops setup with nothing
// changed, instead of halfway through. runSetupPlan then executes. A re-run
// finds each step already done and skips it, which makes setup safe to repeat
// after fixing whatever stopped it.

// setupKBAction is what setup does about the first KB.
type setupKBAction int

const (
	kbKeepExisting setupKBAction = iota // KBs are already mounted: nothing to do
	kbAlreadyThere                      // the requested KB is already in the data dir
	kbCreate                            // empty remote (or --no-remote): kb create
	kbClone                             // remote holds commits: kb clone
)

// setupServiceAction is what setup does about the native service.
type setupServiceAction int

const (
	serviceKeep setupServiceAction = iota
	serviceInstall
	serviceStart
)

// setupOptions is the operator's input, from flags or from the interview.
type setupOptions struct {
	Remote    string
	NoRemote  bool
	Name      string
	Agents    []string
	KBs       []string
	Workspace string
	// Plugins are the --plugin sources; ApprovePlugins is --approve-plugins.
	Plugins        []string
	ApprovePlugins bool
	// AgentsDeferred: Agents holds names that are not all validated, because
	// --plugin may register a provider by that name; runSetupPlan checks them
	// after the plugins step.
	AgentsDeferred bool
}

// setupFacts is everything planSetup reads, gathered once. Kept as plain data
// so the planning rules are testable without a machine to inspect.
type setupFacts struct {
	GitFound  bool
	ClientURL string // the server URL the client config already points at, "" if none
	Service   service.Status
	// Backend is the mechanism the service verbs would act on (D1682);
	// ServerAnswers is true when a well-formed /health answers on the configured
	// address whether or not a service is installed (a server started by hand).
	Backend       service.Backend
	ServerAnswers bool
	// ForeignListener: something that is not a Cartographer server answers on
	// the configured address.
	ForeignListener bool
	DataDir         string
	KBs             []kbRow // direct subdirectories of the data dir
	Detected        []string
	// PluginsConfigured: the client config already lists plugins, whose
	// providers may be the only clients on this machine.
	PluginsConfigured bool
	ProvidersBound    bool // every target provider already carries an explicit KB binding
	// BoundProviders are the providers of the client config that carry an
	// explicit KB binding, whichever of them the run ends up connecting.
	BoundProviders []string
	TimerOptOut    bool // the operator uninstalled the sync timer on purpose (D325)
	TrayOptOut     bool // the operator opted out of the status icon (D1482)
	RemoteProbed   bool
	RemoteHasRefs  bool
	// RemoteForgeInit: the refs are only a forge's initial commit, which kb
	// create adopts (D710): planned as create, not mount.
	RemoteForgeInit bool
}

// setupPlan is the decided sequence, rendered before it runs.
type setupPlan struct {
	Service  setupServiceAction
	KB       setupKBAction
	KBName   string
	Remote   string
	Existing []string // KB names already in the data dir
	Agents   []string
	// Plugins are installed by the plugins step, before the agents are
	// detected; AgentsAfterPlugins says so in the plan when nothing is
	// detected yet and Agents is empty.
	Plugins            []string
	ApprovePlugins     bool
	AgentsAfterPlugins bool
	// Redetect: --agents was not given and plugins take part, so the clients
	// detected after the plugins step join Agents.
	Redetect bool
	// AgentsDeferred: see setupOptions.
	AgentsDeferred bool
	// NarrowKB: the KB selection is the narrowest one (the KB this run adds, the
	// bound agents kept as they are); BoundProviders feeds it again when the
	// agents change after the plugins step.
	NarrowKB       bool
	BoundProviders []string
	// AskPlugins: the plugins step may ask for consent (a terminal, no --no-input).
	AskPlugins bool
	KBs        []string // the --kb selection handed to connect; nil lets connect decide
	Workspace  string
	// KeepAgents are the agents that already carry an explicit binding when the
	// KB selection is implicit (the KB this run adds): they are connected
	// without --kb, because connect resets the binding of every provider it is
	// given a selection for, which silently dropped their earlier KBs.
	KeepAgents []string
	// ServerURL is the local service's MCP URL, passed to connect when the client
	// has none yet: connect's own default is the standard port, and a service
	// installed on another one left the agents pointed at nothing.
	ServerURL string
	// ServerUnmanaged marks serviceKeep decided because a server already
	// answers with no installed service: setup cannot restart it.
	ServerUnmanaged bool
}

// errSetupNeedsRemote is planSetup's answer when there is no KB and nothing
// says where the first one lives: the interview asks, a non-interactive run
// stops with the flags that answer it.
var errSetupNeedsRemote = errors.New("no KB is mounted yet and no remote was given")

// errSetupNoServiceManager is planSetup's answer on a host with no service
// manager and no server answering: a plan error, so nothing is created.
var errSetupNoServiceManager = errors.New("this machine has no service manager")

// errSetupForeignListener is planSetup's answer when another program holds the
// server address: setup would otherwise connect the agents to it.
var errSetupForeignListener = errors.New("something else is listening on the server address")

// errSetupNeedsKBChoice is planSetup's answer when the server will mount two or
// more KBs and the agents have no binding yet (D190).
var errSetupNeedsKBChoice = errors.New("choose the KBs the agents receive")

func planSetup(f setupFacts, o setupOptions) (setupPlan, error) {
	var p setupPlan
	if !f.GitFound {
		return p, errors.New("git is not installed or not on PATH: a KB is a git repository, so install git first, then rerun setup")
	}
	if f.ClientURL != "" && !isLoopbackURL(f.ClientURL) {
		return p, fmt.Errorf("this client is connected to %s, not to a local server: setup provisions a local one — to add agents to that server run `cartographer connect` instead", f.ClientURL)
	}
	if o.Remote != "" && o.NoRemote {
		return p, errors.New("--remote and --no-remote are mutually exclusive")
	}
	serviceAddr := serviceAddrOrDefault(f.Service)
	if f.ClientURL == "" {
		p.ServerURL = setupMCPURL(serviceAddr)
	} else if cp, sp := urlPort(f.ClientURL), addrPort(serviceAddr); cp != sp {
		return p, fmt.Errorf("this client points at %s, but the local service listens on %s: connect would configure the agents for a server that is not the one setup provisions — nothing was changed. Point the client at the service with `cartographer connect --server-url %s`, or remove %s", f.ClientURL, serviceAddr, setupMCPURL(serviceAddr), clientconfig.FileName)
	}

	switch {
	case f.Service.Installed && f.Service.Running:
		p.Service = serviceKeep
	case f.Service.Installed:
		p.Service = serviceStart
	case f.ServerAnswers:
		p.Service, p.ServerUnmanaged = serviceKeep, true
	case f.ForeignListener:
		return p, errSetupForeignListener
	case f.Backend == service.BackendNone:
		return p, errSetupNoServiceManager
	default:
		p.Service = serviceInstall
	}

	for _, row := range f.KBs {
		if row.IsKB {
			p.Existing = append(p.Existing, row.Name)
		}
	}

	switch {
	case o.Remote != "":
		p.Remote = o.Remote
		for _, row := range f.KBs {
			if row.IsKB && row.Origin != "" && sameRemote(row.Origin, o.Remote) {
				p.KB, p.KBName = kbAlreadyThere, row.Name
				break
			}
		}
		if p.KB != kbAlreadyThere {
			p.KBName = o.Name
			if p.KBName == "" {
				p.KBName = remoteKBName(o.Remote)
			}
			if err := validateKBName(p.KBName); err != nil {
				return p, fmt.Errorf("%w (pass --name)", err)
			}
			for _, row := range f.KBs {
				if row.Name == p.KBName {
					return p, fmt.Errorf("%s already exists in %s with origin %q: pass a different --name", p.KBName, f.DataDir, row.Origin)
				}
			}
			if !f.RemoteProbed {
				return p, errors.New("internal: the remote was not probed")
			}
			if f.RemoteHasRefs && !f.RemoteForgeInit {
				p.KB = kbClone
			} else {
				p.KB = kbCreate
			}
		}
	case o.NoRemote:
		p.KBName = o.Name
		if p.KBName == "" {
			p.KBName = "my-kb"
		}
		if err := validateKBName(p.KBName); err != nil {
			return p, err
		}
		p.KB = kbCreate
		for _, row := range f.KBs {
			if row.Name == p.KBName {
				if !row.IsKB {
					return p, fmt.Errorf("%s already exists in %s and is not a KB: pass a different --name", p.KBName, f.DataDir)
				}
				p.KB = kbAlreadyThere
			}
		}
	default:
		if len(p.Existing) == 0 {
			return p, errSetupNeedsRemote
		}
		p.KB = kbKeepExisting
	}

	p.Agents = o.Agents
	if len(p.Agents) == 0 {
		p.Agents = f.Detected
	}
	p.Plugins, p.ApprovePlugins = o.Plugins, o.ApprovePlugins
	p.AgentsDeferred = o.AgentsDeferred
	pluginRun := len(o.Plugins) > 0 || f.PluginsConfigured
	p.Redetect = len(o.Agents) == 0 && pluginRun
	if len(p.Agents) == 0 {
		if !pluginRun {
			return p, fmt.Errorf("no agent client detected on this machine: install one, or name it with --agents (%s)", providerNamesJoined())
		}
		// A plugin may bring the only client of this machine: the plugins
		// step installs them, then detection runs again (runSetupPlan).
		p.AgentsAfterPlugins = true
	}
	p.Workspace = o.Workspace

	total := append([]string(nil), p.Existing...)
	if (p.KB == kbCreate || p.KB == kbClone) && !slices.Contains(total, p.KBName) {
		total = append(total, p.KBName)
	}
	switch {
	case len(o.KBs) > 0:
		p.KBs = o.KBs
	case len(total) <= 1 || f.ProvidersBound:
		// One KB binds itself; an existing binding stands (D190).
	case p.KB == kbCreate || p.KB == kbClone:
		// The narrowest choice that does the job: the KB this run adds, for the
		// agents that have no binding yet; a bound agent keeps what it has.
		p.NarrowKB, p.BoundProviders = true, f.BoundProviders
		p.narrowKBs()
	default:
		return p, errSetupNeedsKBChoice
	}
	return p, nil
}

// narrowKBs sets the KB selection of the narrowest choice for the current
// Agents: the KB this run adds, and the agents that already carry a binding
// kept out of it (connect resets the binding of every provider it is given).
func (p *setupPlan) narrowKBs() {
	p.KBs, p.KeepAgents = []string{p.KBName}, nil
	for _, a := range p.Agents {
		if slices.Contains(p.BoundProviders, a) {
			p.KeepAgents = append(p.KeepAgents, a)
		}
	}
	if len(p.KeepAgents) == len(p.Agents) {
		p.KBs, p.KeepAgents = nil, nil
	}
}

// sameRemote compares two remote URLs the way an operator means them: a
// trailing slash or ".git" does not make a different repository.
func sameRemote(a, b string) bool {
	norm := func(s string) string {
		// Origin is stored redacted (it is printed); the operator's value may
		// not be, so both sides are compared in their redacted form.
		return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(gitx.RedactRemoteURL(strings.TrimSpace(s))), "/"), ".git")
	}
	return norm(a) == norm(b)
}

// renderSetupPlan prints the plan as numbered steps, each saying whether it
// will run or is already done — the operator's last look before anything
// changes.
func renderSetupPlan(w io.Writer, p setupPlan, f setupFacts) {
	fmt.Fprintln(w, "Setup plan:")
	step := 0
	line := func(format string, args ...any) {
		step++
		fmt.Fprintf(w, "  %d. %s\n", step, fmt.Sprintf(format, args...))
	}
	switch p.Service {
	case serviceKeep:
		if p.ServerUnmanaged {
			line("server       already answering on %s (not managed by Cartographer) - keep", serviceAddrOrDefault(f.Service))
		} else {
			line("server       already running (%s) — keep", serviceAddr(f.Service))
		}
	case serviceStart:
		line("server       installed but stopped — start it")
	case serviceInstall:
		when := "starts at login, no admin rights"
		if f.Backend == service.BackendCron {
			when = "cron: starts at boot, checked every 5 minutes, no admin rights"
		}
		line("server       install the native service on %s (%s)", serviceAddrOrDefault(f.Service), when)
	}
	switch p.KB {
	case kbKeepExisting:
		line("KB           keep the mounted KBs: %s", strings.Join(p.Existing, ", "))
	case kbAlreadyThere:
		line("KB           %q is already in the data dir — keep", p.KBName)
	case kbCreate:
		if p.Remote == "" {
			line("KB           create %q, LOCAL ONLY (not backed up, never synced)", p.KBName)
		} else {
			note := "empty repository"
			if f.RemoteForgeInit {
				note = "the forge's initial commit is adopted, D710"
			}
			line("KB           create %q and push it to %s (%s)", p.KBName, p.Remote, note)
		}
	case kbClone:
		line("KB           mount %q from %s (the repository already has content)", p.KBName, p.Remote)
	}
	if len(p.Plugins) > 0 {
		shown := make([]string, len(p.Plugins))
		for i, src := range p.Plugins {
			shown[i] = redactPluginArg(src)
		}
		line("plugins      install %s (approved only with --approve-plugins or on your answer)", escapeControl(strings.Join(shown, ", ")))
	}
	kbs := "every mounted KB"
	switch {
	case len(p.KBs) > 0:
		kbs = strings.Join(p.KBs, ", ")
	case f.ProvidersBound:
		kbs = "their existing binding"
	}
	scope := "every directory on this machine"
	if p.Workspace != "" {
		scope = "only inside " + p.Workspace
	}
	if p.AgentsAfterPlugins {
		line("agents       detected after the plugins are installed — KBs: %s; visible in %s", kbs, scope)
	} else if len(p.KeepAgents) > 0 {
		receiving := slices.DeleteFunc(slices.Clone(p.Agents), func(a string) bool { return slices.Contains(p.KeepAgents, a) })
		line("agents       connect %s — KBs: %s; visible in %s", strings.Join(receiving, ", "), kbs, scope)
		line("             connect %s — they keep their existing KB binding", strings.Join(p.KeepAgents, ", "))
	} else {
		more := ""
		if p.Redetect {
			more = ", and the clients the plugins add"
		}
		line("agents       connect %s%s — KBs: %s; visible in %s", escapeControl(strings.Join(p.Agents, ", ")), more, kbs, scope)
	}
	if p.ServerURL != "" {
		line("             server URL %s", p.ServerURL)
	}
	// D325: the timer is a background job connect installs on its own, so the
	// plan is where the operator sees it before it runs.
	if !f.TimerOptOut {
		if hookless, _ := providersNeedingSyncTimer(p.Agents); !p.AgentsAfterPlugins && len(hookless) > 0 {
			line("sync timer   install (every %s) — %s", service.DefaultSyncInterval, describeHookless(hookless))
		}
	}
	if traySupported() && !f.TrayOptOut && !trayInstalledFn() {
		line("status icon  install")
	}
	line("verify       the server reports ready")
}

func serviceAddr(st service.Status) string {
	if st.HTTPAddr != "" {
		return st.HTTPAddr
	}
	return "listening"
}

func serviceAddrOrDefault(st service.Status) string {
	if st.HTTPAddr != "" {
		return st.HTTPAddr
	}
	return defaults.DefaultListenAddress
}

// setupMCPURL is the client URL of a service listening on addr: a wildcard or
// empty host is the loopback, which is where the client reaches it.
func setupMCPURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return defaults.DefaultMCPURL
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/mcp"
}

func addrPort(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return port
}

// urlPort is the port of a client URL, the scheme's default when it names none.
func urlPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// Indirections for tests: gathering touches the machine, and every step is a
// real command.
var (
	setupProbeRemote   = gitx.ProbeRemoteContent
	setupProbePush     = func(ctx context.Context, remote string) error { return gitx.ProbePushAccess(ctx, remote) }
	setupLookGit       = func() bool { _, err := exec.LookPath("git"); return err == nil }
	setupServiceStatus = func() (service.Status, error) { return service.NewManager().Status("") }
	setupBackend       = func() service.Backend { return service.NewManager().Backend() }
	setupProbeListener = func(addr string) service.ListenerState { return service.ProbeListener(addr, 2*time.Second) }
	// setupMountedKBs lists the KBs the answering server mounts; listed is false
	// when /health does not say (routing off, or it does not answer).
	setupMountedKBs = func() (names []string, listed bool) {
		h, err := fetchHealth(serverBaseURL(""))
		if err != nil || h.KBs == nil {
			return nil, false
		}
		for _, k := range *h.KBs {
			names = append(names, k.Name)
		}
		return names, true
	}
	setupFailureHint = func() string { return service.NewManager().FailureHint() }
	setupInstallSvc  = func() error { return installServiceAndWaitHealthy(service.NewManager(), 20*time.Second) }
	setupStartSvc    = func() error { return service.NewManager().Start() }
	setupKBCreate    = cmdKBCreate
	setupKBClone     = cmdKBClone
	setupConnect     = cmdConnect
	setupDetected    = func() []string { p, _ := resolveTargetProviders("", ""); return p }
	setupWaitReady   = waitSetupReady
	// setupWaitBootstrapped waits until the server has mounted its KBs: /health
	// answers 200 while it is still cloning (D348), so "the service answers" is
	// not "the KBs are there", and connect run against a bootstrapping server
	// writes the bare entry and defers the sync.
	setupWaitBootstrapped = func() bool {
		return waitHealthyFor(serverBaseURL(""), "", setupReadyTimeout)
	}
	setupProbeAuth = func(addr string) (bool, error) { return service.ProbeAuthRequired(addr, 5*time.Second) }
)

const setupProbeTimeout = 30 * time.Second

// setupReadyTimeout bounds the wait for a server to finish mounting its KBs: a
// first clone of a large repository takes minutes, not the ten seconds of a
// restart.
const setupReadyTimeout = 5 * time.Minute

func gatherSetupFacts(o setupOptions) (setupFacts, error) {
	f := setupFacts{GitFound: setupLookGit()}
	if !f.GitFound {
		return f, nil
	}
	if dir, err := clientconfig.TargetDir(); err == nil {
		if cfg, err := clientconfig.Load(dir); err == nil {
			f.ClientURL = cfg.ServerURL
			f.TimerOptOut = cfg.SyncTimerOptOut
			f.TrayOptOut = cfg.TrayOptOut
			f.PluginsConfigured = len(cfg.Plugins) > 0
			agents := o.Agents
			if len(agents) == 0 {
				agents = setupDetected()
			}
			f.ProvidersBound = len(agents) > 0 && allProvidersBound(cfg, agents)
			for _, p := range slices.Sorted(maps.Keys(cfg.Clients)) {
				if _, explicit := cfg.BoundKBs(p); explicit {
					f.BoundProviders = append(f.BoundProviders, p)
				}
			}
		}
	}
	if st, err := setupServiceStatus(); err == nil {
		f.Service = st
	}
	f.Backend = setupBackend()
	// A bootstrapping server answers too; a listener that does not answer like
	// a Cartographer server (no version in /health) is not one of ours.
	switch setupProbeListener(serviceAddrOrDefault(f.Service)) {
	case service.ListenerCartographer:
		f.ServerAnswers = true
	case service.ListenerForeign:
		f.ForeignListener = true
	}
	f.DataDir = resolveServerDataDir("")
	if rows, err := scanDataDir(f.DataDir); err == nil {
		f.KBs = rows
	}
	f.Detected = setupDetected()
	if o.Remote != "" {
		// The probe's git runs in its own process group: Ctrl-C must cancel it
		// through the context, or it would be left running.
		sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(sigCtx, setupProbeTimeout)
		defer cancel()
		probe, err := setupProbeRemote(ctx, o.Remote)
		if err != nil {
			return f, fmt.Errorf("cannot reach %s with this machine's git credentials — nothing was changed:\n  %v", gitx.RedactRemoteURL(o.Remote), err)
		}
		// Reading is not writing: the server pushes every write, so a credential
		// that cannot push is reported now, not at the first write.
		if err := setupProbePush(ctx, o.Remote); err != nil {
			return f, fmt.Errorf("this machine's git credentials can read %s but cannot push to it — nothing was changed:\n  %v", gitx.RedactRemoteURL(o.Remote), err)
		}
		f.RemoteProbed, f.RemoteHasRefs, f.RemoteForgeInit = true, probe.HasRefs, probe.ForgeInit
	}
	return f, nil
}

// setupPrompter is the line-based interview. It reads from one buffered
// reader for the whole run: a fresh bufio.Reader per question would swallow
// input the previous one had already buffered.
type setupPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

func (s setupPrompter) ask(prompt, def string) string {
	if def != "" {
		fmt.Fprintf(s.out, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(s.out, "%s: ", prompt)
	}
	line, _ := s.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// confirm defaults to yes: it is asked after the full plan was shown.
func (s setupPrompter) confirm(prompt string) bool {
	a := strings.ToLower(s.ask(prompt+" [Y/n]", ""))
	return a == "" || a == "y" || a == "yes"
}

// interviewRemote asks where the first KB lives. The answer is either a remote
// or an explicit, confirmed local-only KB — never a silent default (D134).
func (s setupPrompter) interviewRemote(o *setupOptions) bool {
	fmt.Fprintln(s.out, "Your first knowledge base is a git repository, and its remote is what backs it up and syncs it.")
	fmt.Fprintln(s.out, "  - an EMPTY repository you own (GitHub, Gitea, any git host): setup creates the KB in it")
	fmt.Fprintln(s.out, "  - a repository that already holds a Cartographer KB: setup mounts it")
	fmt.Fprintln(s.out, "  - leave blank for a local-only KB (not backed up, never synced)")
	o.Remote = s.ask("Git remote URL", "")
	if o.Remote != "" {
		return true
	}
	if !s.confirm("Create a local-only KB?") {
		return false
	}
	o.NoRemote = true
	o.Name = s.ask("KB name", "my-kb")
	return true
}

func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	remote := fs.String("remote", "", "Git remote of the first KB: an empty repository (created) or one holding a KB (mounted)")
	noRemote := fs.Bool("no-remote", false, "Create a local-only first KB (not backed up, never synced)")
	name := fs.String("name", "", "Name of the first KB (default: the remote's repository name, or my-kb)")
	agentsCSV := fs.String("agents", "", "Comma-separated agent clients to connect (default: every detected one)")
	var kbSel repeatedString
	fs.Var(&kbSel, "kb", "KB the agents receive (repeatable, or comma-separated; 'all' for every mounted KB)")
	workspace := fs.String("workspace", "", "Confine the KBs to this repository instead of every directory (D193)")
	yes := fs.Bool("yes", false, "Do not ask for confirmation before running the plan")
	dryRun := fs.Bool("dry-run", false, "Print the plan and stop, changing nothing")
	noInput := fs.Bool("no-input", false, "Never ask questions, even in a terminal")
	var pluginSrcs repeatedString
	fs.Var(&pluginSrcs, "plugin", "Install a plugin (git URL[@ref] or folder) before the agents are detected (repeatable)")
	approvePlugins := fs.Bool("approve-plugins", false, "Approve the plugins installed by this run, after printing what each one runs")
	pluginFlags := " [--plugin <src>] [--approve-plugins]"
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: cartographer setup [--remote <url> | --no-remote] [--name <kb>] [--agents a,b] [--kb <name>] [--workspace <repo>]"+pluginFlags+" [--yes] [--dry-run] [--no-input]")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	o := setupOptions{
		Remote:    strings.TrimSpace(*remote),
		NoRemote:  *noRemote,
		Name:      strings.TrimSpace(*name),
		KBs:       splitCommaList(kbSel),
		Workspace: *workspace,
		Plugins:   pluginSrcs,
	}
	o.ApprovePlugins = *approvePlugins
	if *agentsCSV != "" {
		agents, err := resolveProviderCSV(*agentsCSV)
		if err != nil && len(o.Plugins) > 0 {
			// A name no provider has yet may be a provider the plugins register: it is
			// checked after the plugins step.
			agents, err = splitProviderCSV(*agentsCSV)
			o.AgentsDeferred = err == nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		o.Agents = agents
	}
	interactive := !*noInput && isInteractive()
	ask := setupPrompter{in: bufio.NewReader(os.Stdin), out: os.Stdout}
	return runSetup(o, interactive, *yes, *dryRun, ask)
}

func runSetup(o setupOptions, interactive, yes, dryRun bool, ask setupPrompter) int {
	var (
		facts setupFacts
		plan  setupPlan
		err   error
	)
	// The exit-3 check below reflects only the connects of this run, also in a
	// process that ran setup or connect before (#1757).
	connectNotWired = nil
	for {
		facts, err = gatherSetupFacts(o)
		if err == nil {
			plan, err = planSetup(facts, o)
		}
		if err == nil {
			break
		}
		switch {
		case errors.Is(err, errSetupNeedsRemote) && interactive:
			if !ask.interviewRemote(&o) {
				fmt.Println("cancelled — nothing was changed")
				return 1
			}
			continue
		case errors.Is(err, errSetupNeedsRemote):
			fmt.Fprintln(os.Stderr, "Error: no KB is mounted yet: say where the first one lives — nothing was changed.")
			fmt.Fprintln(os.Stderr, "  cartographer setup --remote <url>          an empty repository (created) or one holding a KB (mounted)")
			fmt.Fprintln(os.Stderr, "  cartographer setup --no-remote [--name n]  a local-only KB, not backed up and never synced")
			return 2
		case errors.Is(err, errSetupForeignListener):
			fmt.Fprintf(os.Stderr, "Error: something that is not a Cartographer server answers on %s - nothing was changed.\n  Free that address, or point the server elsewhere with `cartographer service install --http <addr>`, then rerun `cartographer setup`.\n", serviceAddrOrDefault(facts.Service))
			return 2
		case errors.Is(err, errSetupNoServiceManager):
			printNoServiceManager(os.Stderr, setupConfigPath(facts), true)
			return 2
		case errors.Is(err, errSetupNeedsKBChoice) && interactive:
			names := planKBNames(facts)
			selection, ok, ferr := runKBSelectForm(names)
			if ferr != nil || !ok {
				fmt.Println("cancelled — nothing was changed")
				return 1
			}
			o.KBs = selection
			continue
		case errors.Is(err, errSetupNeedsKBChoice):
			fmt.Fprintf(os.Stderr, "Error: this server mounts %d KBs (%s): choose which ones the agents receive with --kb <name> (repeatable), or --kb all — nothing was changed.\n",
				len(planKBNames(facts)), strings.Join(planKBNames(facts), ", "))
			return 2
		default:
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}

	if interactive && len(o.Agents) == 0 && len(plan.Agents) > 0 {
		fmt.Printf("Agent clients found on this machine: %s\n", strings.Join(plan.Agents, ", "))
		if answer := ask.ask("Connect which? (comma-separated)", strings.Join(plan.Agents, ",")); answer != strings.Join(plan.Agents, ",") {
			agents, perr := resolveProviderCSV(answer)
			if perr != nil {
				fmt.Fprintln(os.Stderr, "Error:", perr)
				return 2
			}
			o.Agents = agents
			if plan, err = planSetup(facts, o); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 2
			}
		}
	}

	// The plan and the prompt that follows it go through the one writer the
	// prompter uses, so the prompt always starts on its own line after the
	// plan (#415 saw "…reports readyProceed?" on Windows).
	fmt.Fprintln(ask.out)
	renderSetupPlan(ask.out, plan, facts)
	fmt.Fprintln(ask.out)
	if dryRun {
		fmt.Println("dry run — nothing was changed")
		return 0
	}
	if interactive && !yes && !ask.confirm("Proceed?") {
		fmt.Println("cancelled — nothing was changed")
		return 1
	}
	plan.AskPlugins = interactive
	return runSetupPlan(plan)
}

// planKBNames is the catalogue the KB choice is made from: the KBs already in
// the data dir. It is only asked for when this run adds none (planSetup binds a
// new KB by itself).
func planKBNames(f setupFacts) []string {
	var names []string
	for _, row := range f.KBs {
		if row.IsKB {
			names = append(names, row.Name)
		}
	}
	return names
}

func runSetupPlan(p setupPlan) int {
	fmt.Println("==> server")
	switch p.Service {
	case serviceInstall:
		if err := setupInstallSvc(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			fmt.Fprintf(os.Stderr, "Fix it (%s), then rerun `cartographer setup`: finished steps are skipped.\n", setupFailureHint())
			return 1
		}
		fmt.Println("service installed and started")
		if setupBackend() == service.BackendSystemd {
			printLingerHint()
		}
	case serviceStart:
		if err := setupStartSvc(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println("service started")
	default:
		if p.ServerUnmanaged {
			fmt.Println("already answering (not managed by Cartographer)")
		} else {
			fmt.Println("already running")
		}
	}

	// A hand-started server cannot be restarted by setup: the KB step runs
	// without --restart and setup says so afterwards.
	restart := []string{"--restart"}
	if p.ServerUnmanaged {
		restart = nil
	}
	fmt.Println("==> knowledge base")
	switch p.KB {
	case kbCreate:
		args := append([]string{p.KBName}, restart...)
		if p.Remote != "" {
			args = append(args, "--remote", p.Remote)
		} else {
			args = append(args, "--no-remote")
		}
		if code := setupKBCreate(args); code != 0 {
			fmt.Fprintln(os.Stderr, "Setup stopped at the KB step: fix the cause above, then rerun `cartographer setup`.")
			return 1
		}
	case kbClone:
		if code := setupKBClone(append([]string{p.Remote, p.KBName}, restart...)); code != 0 {
			fmt.Fprintln(os.Stderr, "Setup stopped at the KB step: fix the cause above, then rerun `cartographer setup`.")
			return 1
		}
	case kbAlreadyThere:
		fmt.Printf("%q is already mounted\n", p.KBName)
	default:
		fmt.Printf("using %s\n", strings.Join(p.Existing, ", "))
	}
	if p.ServerUnmanaged && (p.KB == kbAlreadyThere || p.KB == kbKeepExisting) {
		// The rerun after "restart it by hand": the server must really mount
		// the KBs, or the agents would be connected to one that does not serve them.
		want := p.Existing
		if p.KB == kbAlreadyThere {
			want = []string{p.KBName}
		}
		if mounted, listed := setupMountedKBs(); listed {
			if missing := slices.DeleteFunc(slices.Clone(want), func(n string) bool { return slices.Contains(mounted, n) }); len(missing) > 0 {
				fmt.Fprintf(os.Stderr, "Error: the running server does not mount %s (mounted: %s): it was started before the KB existed. Restart it by hand, then rerun `cartographer setup`.\n", strings.Join(missing, ", "), strings.Join(mounted, ", "))
				return 1
			}
		}
	}
	if p.ServerUnmanaged && (p.KB == kbCreate || p.KB == kbClone) {
		fmt.Fprintln(os.Stderr, "The running server mounts KBs only when it starts and was not started by Cartographer: restart it by hand, then rerun `cartographer setup` (finished steps are skipped).")
		return 1 // a KB was created: 2 means "nothing changed"
	}

	if msg := setupAuthMismatch(); msg != "" {
		fmt.Fprintln(os.Stderr, "Error:", msg)
		return 1
	}

	// A server still cloning answers /health too: connect would see no KB list,
	// write the bare entry and defer the sync, and the verify step would then
	// call it set up.
	if !setupWaitBootstrapped() {
		fmt.Fprintf(os.Stderr, "Error: the server is still loading its KBs after %s — check `cartographer service status`, then rerun `cartographer setup` (finished steps are skipped).\n", setupReadyTimeout)
		return 1
	}

	skipAgents := false
	var waiting []string // this run's plugins still waiting for an approval
	if len(p.Plugins) > 0 || p.AgentsAfterPlugins || p.Redetect {
		fmt.Println("==> plugins")
		var thisRun []string
		if code := preparePlugins(pluginPrep{Sources: p.Plugins, Approve: p.ApprovePlugins, Ask: p.AskPlugins, Names: &thisRun}); code != 0 {
			fmt.Fprintln(os.Stderr, "Setup stopped at the plugins step: fix the cause above, then rerun `cartographer setup`: finished steps are skipped.")
			return 1
		}
		waiting = pendingPluginNames(thisRun)
		// The providers of the approved plugins are registered once the step returns.
		if p.AgentsDeferred {
			if _, err := resolveProviderCSV(strings.Join(p.Agents, ",")); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", escapeControl(err.Error()))
				return 1 // the service and the KB exist: 2 means "nothing changed"
			}
		}
		if p.Redetect || p.AgentsAfterPlugins {
			for _, a := range setupDetected() {
				if !slices.Contains(p.Agents, a) {
					p.Agents = append(p.Agents, a)
				}
			}
			if p.NarrowKB {
				p.narrowKBs()
			}
			if len(p.Agents) == 0 {
				if len(waiting) == 0 {
					fmt.Fprintf(os.Stderr, "Error: no agent client detected on this machine: install one, or name it with --agents (%s)\n", providerNamesJoined())
					return 1
				}
				// Nothing else is blocked: the server is up and the KB mounted.
				fmt.Printf("no agent client detected yet; waiting for the approval of %s\n", escapeControl(strings.Join(waiting, ", ")))
				skipAgents = true
			}
		}
	}

	partial := false
	if !skipAgents {
		fmt.Println("==> agents")
		connect := func(agents []string, kbs []string) int {
			args := []string{"--no-input", "--agents", strings.Join(agents, ",")}
			if p.ServerURL != "" {
				args = append(args, "--server-url", p.ServerURL)
			}
			for _, kb := range kbs {
				args = append(args, "--kb", kb)
			}
			if p.Workspace != "" {
				args = append(args, "--workspace", p.Workspace)
			}
			if p.ApprovePlugins {
				args = append(args, "--approve-plugins")
			}
			return setupConnect(args)
		}
		receiving := slices.DeleteFunc(slices.Clone(p.Agents), func(a string) bool { return slices.Contains(p.KeepAgents, a) })
		groups := [][]string{receiving}
		if len(p.KeepAgents) > 0 {
			groups = append(groups, p.KeepAgents)
		}
		for i, agents := range groups {
			kbs := p.KBs
			if i == 1 {
				kbs = nil // the agents that keep their binding
			}
			// 3: some agent is partially or not connected (a refused destination);
			// the others went through, so the step goes on and the end says so.
			code := connect(agents, kbs)
			if code == exitPartiallyConnected {
				partial = true
			} else if code != 0 {
				fmt.Fprintln(os.Stderr, "Setup stopped at the connect step: fix the cause above, then rerun `cartographer setup`.")
				return 1 // not the step's own code: 2 means "nothing changed", and the service and the KB exist now
			}
		}
	}

	fmt.Println("==> verify")
	st, ready := setupWaitReady()
	if !ready {
		fmt.Fprintln(os.Stderr, "Warning: the server did not report ready — check `cartographer service status` and `cartographer doctor`.")
		return 1
	}
	fmt.Println("server ready")

	if partial || len(connectNotWired) > 0 {
		fmt.Fprintf(os.Stderr, "Setup finished, but not every agent is wired: %s. Replace the symlink named in the warnings above with a regular file or directory, or list it under providers.<provider>.allow_symlinks with `cartographer client symlinks <provider> --add <link>`, then rerun `cartographer setup` (finished steps are skipped).\n", cmp.Or(strings.Join(connectNotWired, "; "), "see the connect output above"))
		return exitPartiallyConnected
	}

	fmt.Println()
	if skipAgents {
		fmt.Println("The server and the KB are set up, but no agent is connected yet.")
		if st.UIURL != "" {
			fmt.Printf("  Atlas (read-only view of your KBs): %s\n", st.UIURL)
		}
		for _, n := range waiting {
			fmt.Printf("  Approve the plugin: cartographer plugin approve %s\n", escapeControl(n))
		}
		fmt.Println("  Then run `cartographer setup` again to connect the agent clients it brings.")
		return 0
	}
	fmt.Println("Cartographer is set up.")
	if st.UIURL != "" {
		fmt.Printf("  Atlas (read-only view of your KBs): %s\n", st.UIURL)
	}
	fmt.Println("  Restart your agent sessions now: the MCP tools and skills load at session start,")
	fmt.Println("  so a session that was already open does not see them.")
	return 0
}

// setupAuthMismatch returns why the agent step would fail with a 401, or "".
// The clients setup configures for the local server send no token, so a
// service that demands one rejects every call — which is what happens when a
// CARTOGRAPHER_TOKENS exported for another server reaches the service's
// environment under a server.yaml that leaves auth.mode to "auto" (D268: a
// config generated today sets it to "off", an older one does not). Caught here,
// before connect, the cause is named instead of surfacing as a sync_pull 401.
// A client already configured to send a token is left alone, and a probe
// that cannot run says nothing: connect reports reachability itself.
func setupAuthMismatch() string {
	if dir, err := clientconfig.TargetDir(); err == nil {
		if cfg, err := clientconfig.Load(dir); err == nil && cfg.Auth {
			return ""
		}
	}
	st, err := setupServiceStatus()
	if err != nil || st.HTTPAddr == "" {
		return ""
	}
	required, err := setupProbeAuth(st.HTTPAddr)
	if err != nil || !required {
		return ""
	}
	return authMismatchMessage(st.HTTPAddr, st.ConfigPath, os.Getenv("CARTOGRAPHER_TOKENS") != "", os.Getenv("CARTOGRAPHER_AUTH"))
}

// authMismatchMessage names the variable and the fix. inShell reports whether
// CARTOGRAPHER_TOKENS is set in setup's own environment: the service's may
// differ (launchd, systemd --user and Task Scheduler each have their own), so
// it is evidence, not proof.
func authMismatchMessage(addr, configPath string, inShell bool, authEnv string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the local server at %s requires a bearer token (HTTP 401), but setup connects the agents to it without one.\n", addr)
	b.WriteString("  The server turns authentication on when CARTOGRAPHER_TOKENS reaches its environment and its config leaves auth.mode unset")
	if authEnv != "" {
		fmt.Fprintf(&b, " (or CARTOGRAPHER_AUTH=%s forces it)", authEnv)
	}
	b.WriteString(".\n")
	if inShell {
		b.WriteString("  CARTOGRAPHER_TOKENS is set in this shell: probably meant for another server.\n")
	}
	fmt.Fprintf(&b, "  Fix: add\n\n    auth:\n      mode: \"off\"\n\n  to %s, run `cartographer service restart`, then rerun `cartographer setup`\n", configPath)
	b.WriteString("  (or remove CARTOGRAPHER_TOKENS from the service's environment).")
	return b.String()
}

// waitSetupReady polls the service until its health probe passes, and returns
// its last status (for the Atlas URL).
func waitSetupReady() (service.Status, bool) {
	// The same condition as before the agents step: answering AND done loading
	// its KBs (waitHealthyFor), then the status for the Atlas URL.
	ready := waitHealthyFor(serverBaseURL(""), "", restartWaitTimeout)
	st, _ := setupServiceStatus()
	return st, ready
}

// printLingerHint warns, on Linux only, when the user's systemd instance does
// not outlive their login: the user unit then stops at logout and does not come
// back at boot, which on a headless host reads as a server that vanished.
func printLingerHint() {
	if runtime.GOOS != "linux" {
		return
	}
	u, err := user.Current()
	if err != nil {
		return
	}
	out, err := exec.Command("loginctl", "show-user", u.Username, "--property=Linger").Output()
	if err != nil || strings.TrimSpace(string(out)) != "Linger=no" {
		return
	}
	fmt.Printf("note: the service stops when you log out; to keep it running (headless hosts): loginctl enable-linger %s\n", u.Username)
}

// setupConfigPath is the server config path the manual route names: the
// installed definition's, else the standard one.
func setupConfigPath(f setupFacts) string {
	if f.Service.ConfigPath != "" {
		return f.Service.ConfigPath
	}
	p, _ := service.ConfigPath()
	return p
}

// printNoServiceManager prints the manual route for a host with neither a
// reachable systemd user manager nor a usable crontab. setup passes
// nothingChanged: it stops at plan time, before creating anything.
func printNoServiceManager(w io.Writer, configPath string, nothingChanged bool) {
	tail := ""
	if nothingChanged {
		tail = " - nothing was changed"
	}
	fmt.Fprintf(w, "Error: this machine has neither a reachable systemd user manager nor a usable `crontab`, so Cartographer cannot install a service%s.\n", tail)
	fmt.Fprintln(w, "  Run the server yourself and hook it into your init system:")
	fmt.Fprintln(w, "    cartographer service install --config-only      write the config and the data directory, install nothing")
	logPath := "<state dir>/server.log"
	if d, err := service.StateDir(); err == nil {
		logPath = filepath.Join(d, "server.log")
	}
	fmt.Fprintf(w, "    nohup cartographer serve --config %s --log-file %s &\n", configPath, logPath)
	fmt.Fprintln(w, "  (examples for rc.local, OpenRC, runit and a container entrypoint: docs/deployment.md, \"Hosts without a service manager\")")
	fmt.Fprintln(w, "  Then rerun `cartographer setup`: it keeps a server that already answers.")
}

// pendingPluginNames keeps, of the plugins this run installed or took over,
// those that still wait for an approval.
func pendingPluginNames(names []string) []string {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return nil
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range names {
		if e, ok := cfg.Plugin(n); ok && e.Installed != "" && e.Approved == "" {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}
