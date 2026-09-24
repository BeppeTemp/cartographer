package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
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
}

// setupFacts is everything planSetup reads, gathered once. Kept as plain data
// so the planning rules are testable without a machine to inspect.
type setupFacts struct {
	GitFound       bool
	ClientURL      string // the server URL the client config already points at, "" if none
	Service        service.Status
	DataDir        string
	KBs            []kbRow // direct subdirectories of the data dir
	Detected       []string
	ProvidersBound bool // every target provider already carries an explicit KB binding
	RemoteProbed   bool
	RemoteHasRefs  bool
}

// setupPlan is the decided sequence, rendered before it runs.
type setupPlan struct {
	Service   setupServiceAction
	KB        setupKBAction
	KBName    string
	Remote    string
	Existing  []string // KB names already in the data dir
	Agents    []string
	KBs       []string // the --kb selection handed to connect; nil lets connect decide
	Workspace string
}

// errSetupNeedsRemote is planSetup's answer when there is no KB and nothing
// says where the first one lives: the interview asks, a non-interactive run
// stops with the flags that answer it.
var errSetupNeedsRemote = errors.New("no KB is mounted yet and no remote was given")

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

	switch {
	case f.Service.Installed && f.Service.Running:
		p.Service = serviceKeep
	case f.Service.Installed:
		p.Service = serviceStart
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
			if f.RemoteHasRefs {
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
	if len(p.Agents) == 0 {
		return p, fmt.Errorf("no agent client detected on this machine: install one, or name it with --agents (%s)", providerNamesJoined())
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
		// The narrowest choice that does the job: the KB this run adds.
		p.KBs = []string{p.KBName}
	default:
		return p, errSetupNeedsKBChoice
	}
	return p, nil
}

// sameRemote compares two remote URLs the way an operator means them: a
// trailing slash or ".git" does not make a different repository.
func sameRemote(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(s), "/"), ".git")
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
		line("server       already running (%s) — keep", serviceAddr(f.Service))
	case serviceStart:
		line("server       installed but stopped — start it")
	case serviceInstall:
		line("server       install the native service on %s (starts at login, no admin rights)", serviceAddrOrDefault(f.Service))
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
			line("KB           create %q and push it to %s (empty repository)", p.KBName, p.Remote)
		}
	case kbClone:
		line("KB           mount %q from %s (the repository already has content)", p.KBName, p.Remote)
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
	line("agents       connect %s — KBs: %s; visible in %s", strings.Join(p.Agents, ", "), kbs, scope)
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
	return "127.0.0.1:39273"
}

// Indirections for tests: gathering touches the machine, and every step is a
// real command.
var (
	setupProbeRemote   = gitx.ProbeRemote
	setupLookGit       = func() bool { _, err := exec.LookPath("git"); return err == nil }
	setupServiceStatus = func() (service.Status, error) { return service.NewManager().Status("") }
	setupInstallSvc    = func() error { return installServiceAndWaitHealthy(service.NewManager(), 20*time.Second) }
	setupStartSvc      = func() error { return service.NewManager().Start() }
	setupKBCreate      = cmdKBCreate
	setupKBClone       = cmdKBClone
	setupConnect       = cmdConnect
	setupDetected      = func() []string { p, _ := resolveTargetProviders("", ""); return p }
	setupWaitReady     = waitSetupReady
	setupProbeAuth     = func(addr string) (bool, error) { return service.ProbeAuthRequired(addr, 5*time.Second) }
)

const setupProbeTimeout = 30 * time.Second

func gatherSetupFacts(o setupOptions) (setupFacts, error) {
	f := setupFacts{GitFound: setupLookGit()}
	if !f.GitFound {
		return f, nil
	}
	if dir, err := clientconfig.TargetDir(); err == nil {
		if cfg, err := clientconfig.Load(dir); err == nil {
			f.ClientURL = cfg.ServerURL
			agents := o.Agents
			if len(agents) == 0 {
				agents = setupDetected()
			}
			f.ProvidersBound = len(agents) > 0 && allProvidersBound(cfg, agents)
		}
	}
	if st, err := setupServiceStatus(); err == nil {
		f.Service = st
	}
	f.DataDir = resolveServerDataDir("")
	if rows, err := scanDataDir(f.DataDir); err == nil {
		f.KBs = rows
	}
	f.Detected = setupDetected()
	if o.Remote != "" {
		ctx, cancel := context.WithTimeout(context.Background(), setupProbeTimeout)
		defer cancel()
		has, err := setupProbeRemote(ctx, o.Remote)
		if err != nil {
			return f, fmt.Errorf("cannot reach %s with this machine's git credentials — nothing was changed:\n  %v", o.Remote, err)
		}
		f.RemoteProbed, f.RemoteHasRefs = true, has
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
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: cartographer setup [--remote <url> | --no-remote] [--name <kb>] [--agents a,b] [--kb <name>] [--workspace <repo>] [--yes] [--dry-run] [--no-input]")
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
	}
	if *agentsCSV != "" {
		agents, err := resolveProviderCSV(*agentsCSV)
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

	fmt.Println()
	renderSetupPlan(os.Stdout, plan, facts)
	fmt.Println()
	if dryRun {
		fmt.Println("dry run — nothing was changed")
		return 0
	}
	if interactive && !yes && !ask.confirm("Proceed?") {
		fmt.Println("cancelled — nothing was changed")
		return 1
	}
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
			fmt.Fprintln(os.Stderr, "Fix it (`cartographer service status` shows the details), then rerun `cartographer setup`: finished steps are skipped.")
			return 1
		}
		fmt.Println("service installed and started")
		printLingerHint()
	case serviceStart:
		if err := setupStartSvc(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Println("service started")
	default:
		fmt.Println("already running")
	}

	fmt.Println("==> knowledge base")
	switch p.KB {
	case kbCreate:
		args := []string{p.KBName, "--restart"}
		if p.Remote != "" {
			args = append(args, "--remote", p.Remote)
		} else {
			args = append(args, "--no-remote")
		}
		if code := setupKBCreate(args); code != 0 {
			fmt.Fprintln(os.Stderr, "Setup stopped at the KB step: fix the cause above, then rerun `cartographer setup`.")
			return code
		}
	case kbClone:
		if code := setupKBClone([]string{p.Remote, p.KBName, "--restart"}); code != 0 {
			fmt.Fprintln(os.Stderr, "Setup stopped at the KB step: fix the cause above, then rerun `cartographer setup`.")
			return code
		}
	case kbAlreadyThere:
		fmt.Printf("%q is already mounted\n", p.KBName)
	default:
		fmt.Printf("using %s\n", strings.Join(p.Existing, ", "))
	}

	if msg := setupAuthMismatch(); msg != "" {
		fmt.Fprintln(os.Stderr, "Error:", msg)
		return 1
	}

	fmt.Println("==> agents")
	args := []string{"--no-input", "--agents", strings.Join(p.Agents, ",")}
	for _, kb := range p.KBs {
		args = append(args, "--kb", kb)
	}
	if p.Workspace != "" {
		args = append(args, "--workspace", p.Workspace)
	}
	if code := setupConnect(args); code != 0 {
		fmt.Fprintln(os.Stderr, "Setup stopped at the connect step: fix the cause above, then rerun `cartographer setup`.")
		return code
	}

	fmt.Println("==> verify")
	st, ready := setupWaitReady()
	if !ready {
		fmt.Fprintln(os.Stderr, "Warning: the server did not report ready — check `cartographer service status` and `cartographer doctor`.")
		return 1
	}
	fmt.Println("server ready")

	fmt.Println()
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
	deadline := time.Now().Add(restartWaitTimeout)
	for {
		st, err := setupServiceStatus()
		if err == nil && st.Healthy {
			return st, true
		}
		if time.Now().After(deadline) {
			return st, false
		}
		time.Sleep(healthPollInterval)
	}
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
