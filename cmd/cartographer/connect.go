package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

type repeatedString []string

func (r *repeatedString) String() string         { return strings.Join(*r, ",") }
func (r *repeatedString) Set(value string) error { *r = append(*r, value); return nil }

// splitCommaList flattens a repeatable flag that also accepts comma-separated
// values, so --kb a --kb b and --kb a,b are the same selection. An empty
// element is preserved rather than dropped: `--kb ""` asked for something and
// the caller must be able to reject it, not silently receive "nothing given".
func splitCommaList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			out = append(out, strings.TrimSpace(part))
		}
	}
	return out
}

// kbSelectionAll is the sentinel that selects every mounted KB. It is recorded
// as an explicit binding to their names, never as the implicit default: that
// difference is what keeps a KB mounted later from widening a client that
// already exists.
const kbSelectionAll = "all"

// resolveKBSelection turns the operator's --kb (or form) choice into the list
// of KBs this connect may deliver, validated against what the server actually
// mounts (D190).
//
// selection empty is "not chosen", which is an error with two or more KBs
// mounted: defaulting to all of them is the over-exposure this exists to close,
// and it is sticky, because "all known" silently grows with the server.
func resolveKBSelection(selection, mounted []string, listed bool) ([]string, error) {
	if len(selection) == 0 {
		switch {
		case !listed || len(mounted) == 0:
			// The server did not answer, or mounts nothing: there is no
			// catalogue to narrow and nothing to materialize either.
			return nil, nil
		case len(mounted) == 1:
			return append([]string(nil), mounted...), nil
		default:
			return nil, fmt.Errorf("this server mounts %d KBs (%s): choose which ones this client receives with --kb <name> (repeatable, or comma-separated), or --kb all",
				len(mounted), strings.Join(mounted, ", "))
		}
	}

	if len(selection) == 1 && selection[0] == kbSelectionAll {
		if !listed {
			return nil, fmt.Errorf("--kb all needs the server's KB list, which could not be read: name the KBs explicitly")
		}
		return append([]string(nil), mounted...), nil
	}

	seen := map[string]bool{}
	var out []string
	for _, name := range selection {
		if name == "" {
			return nil, fmt.Errorf("--kb was given an empty name: pass a KB name, or --kb all")
		}
		if name == kbSelectionAll {
			return nil, fmt.Errorf("--kb all cannot be combined with a KB name: it already means every mounted KB")
		}
		if listed && !slices.Contains(mounted, name) {
			if len(mounted) == 0 {
				return nil, fmt.Errorf("--kb %q: this server mounts no KB", name)
			}
			return nil, fmt.Errorf("--kb %q: this server mounts %s", name, strings.Join(mounted, ", "))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// probeTimeout bounds probeServer's reachability check (D64): short enough
// that a down server fails fast in the interactive form/CLI, well under the
// client package's normal 30s HTTP timeout used for the real sync_pull calls.
const probeTimeout = 5 * time.Second

type probeState int

const (
	// probeReady is zero so existing asynchronous success messages that do not
	// carry an explicit state remain successful.
	probeReady probeState = iota
	probeUnreachable
	probeNoKB
)

// probeServer checks /health before writing anything to disk. It distinguishes
// an unreachable server, a reachable server with no mounted KB, and a usable
// server. Health parsing accepts pre-D84 servers: without ready, only an
// explicitly present empty kbs list is treated as no KB.
func probeServer(opts connectOptions) (probeState, error) {
	token := ""
	if opts.Auth && opts.TokenEnv != "" {
		token = os.Getenv(opts.TokenEnv)
	}
	health, err := client.New(opts.ServerURL, token).Health(probeTimeout)
	if err != nil {
		return probeUnreachable, err
	}
	if health.Ready != nil && !*health.Ready {
		return probeNoKB, nil
	}
	if health.Ready == nil && health.KBs != nil && len(*health.KBs) == 0 {
		return probeNoKB, nil
	}
	return probeReady, nil
}

// probeErrorMessage renders a probe result for display, distinguishing the
// first-KB onboarding case from auth and network failures.
func probeErrorMessage(state probeState, err error) string {
	if state == probeNoKB {
		return "server is up but no KB is mounted — create one with: cartographer kb create <name> --remote <url> " +
			"(or --no-remote for a local-only KB that is neither backed up nor synchronized), then: cartographer service restart"
	}
	if errors.Is(err, client.ErrUnauthorized) {
		return fmt.Sprintf("server reached but the token was rejected (check Token env var / Auth): %v", err)
	}
	return fmt.Sprintf("server unreachable: %v", err)
}

// isLoopbackURL reports whether rawURL's host is localhost/127.0.0.1/::1 —
// used to decide whether an unreachable server is plausibly this machine's
// own cartographer service (worth offering to install), as opposed to a
// remote server the user doesn't control.
func isLoopbackURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// shouldOfferServiceInstall decides whether cmdConnect should offer to
// install+start the local cartographer service after a failed probe: the
// server URL must be loopback, and the service must not already be running
// (if it were running and still unreachable, installing again wouldn't help
// — e.g. wrong port, firewalled).
func shouldOfferServiceInstall(loopback bool, running bool) bool {
	return loopback && !running
}

var installService = func(mgr *service.Manager, opts service.InstallOptions) error {
	_, err := mgr.Install(opts)
	return err
}

// installServiceAndWaitHealthy installs+starts the local cartographer
// service with default options (mirroring `cartographer service install`
// with no flags) and polls its /health endpoint until it responds or
// timeout elapses. Returns the first error encountered (install failure, or
// "still unhealthy after timeout").
func installServiceAndWaitHealthy(mgr *service.Manager, timeout time.Duration) error {
	if err := installService(mgr, service.InstallOptions{DataDir: defaultDataDir(), HTTPAddr: defaults.DefaultListenAddress}); err != nil {
		return fmt.Errorf("service install: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		st, err := mgr.Status("")
		if err == nil && st.Healthy {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service installed but not healthy after %s", timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// promptYesNo prints prompt, reads a line from stdin, and reports whether the
// (trimmed, case-insensitive) answer is "y"/"yes". Anything else — including
// a blank line — is "no", the safe default (mirrors the TUI's disconnect
// confirmation picker, which also defaults to the safe option).
func promptYesNo(prompt string) bool {
	fmt.Fprint(os.Stdout, prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// isTerminal reports whether the file descriptor fd is attached to a
// terminal. It is a var, not a plain function call, so tests can substitute
// it to force TTY on/off without a real terminal (see connect_test.go).
var isTerminal = func(fd uintptr) bool {
	return xterm.IsTerminal(fd)
}

// connectFormFlagNames are the `connect` flags the interactive form
// (connectform.go) collects itself: server URL, name, auth, token env. If the
// user passed any of these explicitly, cmdConnect treats that as an explicit
// choice and skips the form even in a TTY. Behavior flags (--dry-run,
// --auto-trust) are orthogonal to the form and don't suppress it.
var connectFormFlagNames = map[string]bool{
	"server-url": true,
	"auth":       true,
	"token-env":  true,
	"agents":     true,
}

// wantsConnectForm decides whether cmdConnect should open the interactive
// connect form: true iff noInput is false, none of the form flags
// (connectFormFlagNames) were passed explicitly on fs, and both stdin and
// stdout are a TTY (isTerminal, injectable for tests). fs must already have
// been Parse'd.
func wantsConnectForm(fs *flag.FlagSet, noInput bool) bool {
	if noInput {
		return false
	}
	formFlagPassed := false
	fs.Visit(func(f *flag.Flag) {
		if connectFormFlagNames[f.Name] {
			formFlagPassed = true
		}
	})
	if formFlagPassed {
		return false
	}
	return isTerminal(os.Stdin.Fd()) && isTerminal(os.Stdout.Fd())
}

// connectSettings holds the connect parameters that live in .cartographer.yaml
// and follow flag > config > default precedence.
type connectSettings struct {
	ServerURL string
	Auth      bool
	TokenEnv  string
	Name      string
	Trust     bool
}

// resolveConnectSettings applies flag > config > default precedence for the
// settings persisted in .cartographer.yaml. A form flag NOT passed explicitly
// (absent from passed) inherits the value already in existing rather than the
// hard-coded flag default — so a bare `connect <agent>` on a machine already
// pointed at a remote server never silently rewrites server_url/auth/token_env
// to the local default / auth:false. existing is nil on a first-ever
// connect, where the flag defaults (and Name "cartographer", Trust from
// clientconfig.Default) apply as-is. The interactive form, when opened,
// overrides ServerURL/Auth/TokenEnv/Trust afterwards with the user's input.
func resolveConnectSettings(passed map[string]bool, flagURL string, flagAuth bool, flagTokenEnv string, existing *clientconfig.Config) connectSettings {
	s := connectSettings{
		ServerURL: flagURL,
		Auth:      flagAuth,
		TokenEnv:  flagTokenEnv,
		Name:      "cartographer",
		Trust:     clientconfig.Default().Trust,
	}
	if existing == nil {
		return s
	}
	if existing.ServerName != "" {
		s.Name = existing.ServerName
	}
	s.Trust = existing.Trust
	if !passed["server-url"] && existing.ServerURL != "" {
		s.ServerURL = existing.ServerURL
	}
	if !passed["auth"] {
		s.Auth = existing.Auth
	}
	if !passed["token-env"] && existing.TokenEnv != "" {
		s.TokenEnv = existing.TokenEnv
	}
	return s
}

// cmdConnect generates the MCP client config (HTTP transport only, see
// docs/decisions/client-configurator.md) for the requested agent provider(s) — default "all" = every agent
// detected on this machine (internal/agents.Detect) — materializes skills via
// sync_pull, and records the connection in .cartographer.yaml.
//
// If the server is unreachable, the MCP configs are still written and
// .cartographer.yaml still updated; skill materialization is skipped with a
// warning (exit 0) — run `cartographer sync` once the server is up.
//
// If none of the form flags (--server-url/--auth/--token-env) were
// passed explicitly and both stdin and stdout are a TTY, cmdConnect opens the
// interactive connect form (connectform.go, shared with the TUI dashboard)
// instead of using the flag defaults — pass --no-input to force the
// non-interactive behavior regardless of TTY.
func cmdConnect(args []string) int {
	target, rest := splitPositional(args, "")

	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	serverURL := fs.String("server-url", defaults.DefaultMCPURL, "Cartographer server URL")
	auth := fs.Bool("auth", false, "Enable bearer-token auth in generated configs")
	tokenEnv := fs.String("token-env", "CARTOGRAPHER_TOKENS", "Env var holding the bearer token")
	dryRun := fs.Bool("dry-run", false, "Print what would be written without writing")
	autoTrust := fs.Bool("auto-trust", false, "Trust KB-sourced skills without explicit signature")
	var pinKeys repeatedString
	fs.Var(&pinKeys, "pin-key", "Pin a KB Ed25519 public key as KB=PUBLIC_KEY (repeatable)")
	noInput := fs.Bool("no-input", false, "Never open the interactive form, even in a TTY")
	agentsCSV := fs.String("agents", "", "Comma-separated agent subset: claude,codex")
	var kbSel repeatedString
	fs.Var(&kbSel, "kb", "KB this client may receive (repeatable, or comma-separated; 'all' for every mounted KB)")
	workspaceFlag := fs.String("workspace", "", "Project the selected KBs into this repository instead of the global catalogue (D193)")
	fs.Parse(rest)

	interactive := wantsConnectForm(fs, *noInput)

	providers, err := resolveTargetProviders(target, *agentsCSV)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(providers) == 0 {
		fmt.Fprintf(os.Stderr, "No agent detected on this machine; nothing to connect (pass an explicit provider name to force it: %s).\n", providerNamesJoined())
		return 1
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	// The name the server is registered under in the MCP configs is no longer a
	// flag/form field: it is always "cartographer" (the project name), unless a
	// server_name is already present in .cartographer.yaml (escape hatch).
	passed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { passed[f.Name] = true })

	var existing *clientconfig.Config
	if c, err := clientconfig.Load(dir); err == nil {
		existing = c
	}
	settings := resolveConnectSettings(passed, *serverURL, *auth, *tokenEnv, existing)

	opts := connectOptions{
		Providers: providers,
		Dir:       dir,
		ServerURL: settings.ServerURL,
		Name:      settings.Name,
		Auth:      settings.Auth,
		TokenEnv:  settings.TokenEnv,
		DryRun:    *dryRun,
		AutoTrust: *autoTrust,
		Trust:     settings.Trust,
		PinKeys:   pinKeys,
		KBs:       splitCommaList(kbSel),
		Workspace: *workspaceFlag,
	}

	if interactive {
		prefill, err := clientconfig.Load(dir)
		if err != nil {
			prefill = clientconfig.Default()
		}
		title := fmt.Sprintf("Connect %s", strings.Join(providers, ", "))
		formPrefill := connectOptions{Providers: providers, ServerURL: prefill.ServerURL, Name: prefill.ServerName, Auth: prefill.Auth, TokenEnv: prefill.TokenEnv, Trust: prefill.Trust}
		errMsg := ""

		// Loop: form → probe (with a y/N override) → doConnect. A failure at
		// either the probe or doConnect redisplays the form precompiled with
		// the values just entered plus an inline error, instead of losing them
		// (D64) — esc/ctrl+c inside runConnectForm is the only way out.
		for {
			formOpts, ok, err := runConnectForm(title, formPrefill, errMsg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 2
			}
			if !ok {
				fmt.Println("cancelled")
				return 1
			}

			if len(formOpts.Providers) == 0 {
				formPrefill, errMsg = formOpts, "select at least one agent"
				continue
			}

			state, perr := probeServer(formOpts)
			if state != probeReady {
				msg := probeErrorMessage(state, perr)
				fmt.Fprintln(os.Stderr, "Warning:", msg)

				recovered := false
				if state == probeUnreachable && !errors.Is(perr, client.ErrUnauthorized) && isLoopbackURL(formOpts.ServerURL) {
					mgr := service.NewManager()
					st, statusErr := mgr.Status("")
					if statusErr == nil && shouldOfferServiceInstall(true, st.Running) {
						if promptYesNo("The local server is not responding. Install and start the cartographer service on this machine? [y/N] ") {
							if instErr := installServiceAndWaitHealthy(mgr, 10*time.Second); instErr != nil {
								fmt.Fprintln(os.Stderr, "Error:", instErr)
							} else if state2, perr2 := probeServer(formOpts); state2 != probeReady {
								msg = probeErrorMessage(state2, perr2)
								fmt.Fprintln(os.Stderr, "Warning:", msg)
							} else {
								fmt.Println("service installed and started")
								recovered = true
							}
						}
					}
				}

				if !recovered {
					if !promptYesNo("Proceed anyway? The configuration may be correct even if the probe fails. [y/N] ") {
						formPrefill, errMsg = formOpts, msg
						continue
					}
					fmt.Println("proceeding anyway (probe overridden)")
				}
			}

			providers = formOpts.Providers
			opts.Providers, opts.ServerURL, opts.Name, opts.Auth, opts.TokenEnv, opts.Trust =
				providers, formOpts.ServerURL, formOpts.Name, formOpts.Auth, formOpts.TokenEnv, formOpts.Trust

			// The KB choice (D190) comes after the probe, because that is when
			// the names exist, and before doConnect, because that is when the
			// first artifact would be written. Only asked when there is a
			// choice to make and the operator has not already made it.
			if len(opts.KBs) == 0 {
				facts, ferr := enumerateKBs(opts.ServerURL, opts.Auth, opts.TokenEnv)
				if ferr == nil && facts.Listed && len(facts.Names) > 1 && !allProvidersBound(existingOrDefault(dir), providers) {
					selection, ok, err := runKBSelectForm(facts.Names)
					if err != nil {
						fmt.Fprintln(os.Stderr, "Error:", err)
						return 2
					}
					if !ok {
						fmt.Println("cancelled")
						return 1
					}
					opts.KBs = selection
				}
			}

			res, err := doConnect(opts)
			if err != nil {
				formPrefill = formOpts
				errMsg = fmt.Sprintf("connect failed: %v (connect is idempotent: no need to disconnect — fix the values and press Connect again, esc to quit)", err)
				fmt.Fprintln(os.Stderr, "Error:", errMsg)
				continue
			}

			printConnectResult(dir, providers, opts, res)
			return 0
		}
	}

	res, err := doConnect(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if res.Deferred && isLoopbackURL(opts.ServerURL) {
		state, probeErr := probeServer(opts)
		switch state {
		case probeNoKB:
			fmt.Fprintln(os.Stderr, "hint:", probeErrorMessage(state, nil))
		case probeUnreachable:
			if !errors.Is(probeErr, client.ErrUnauthorized) {
				fmt.Fprintln(os.Stderr, "hint: the local server isn't responding — run `cartographer service install` to run it as a native background service")
			}
		}
	}
	printConnectResult(dir, providers, opts, res)
	return 0
}

// printConnectResult prints the standard `connect` output (configs written,
// apply summary or deferral warning, per-provider "connected:" lines) —
// factored out so both the non-interactive path and the interactive retry
// loop in cmdConnect (D64) share exactly one rendering of a successful result.
func printConnectResult(dir string, providers []string, opts connectOptions, res connectResult) {
	printMCPEntryLines(providers, res.MCPEntries, opts.DryRun)
	for _, p := range res.ConfigsWritten {
		if opts.DryRun {
			fmt.Printf("[dry-run] would write %s\n", p)
		} else {
			fmt.Printf("wrote %s\n", p)
		}
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	if res.Deferred {
		fmt.Fprintf(os.Stderr, "Warning: skill sync deferred, server unreachable: %v\n", res.DeferredErr)
	} else {
		printApplySummary(dir, res.Applied, opts.DryRun)
	}

	for _, p := range providers {
		if opts.DryRun {
			fmt.Printf("[dry-run] would connect: %s\n", p)
			continue
		}
		fmt.Printf("connected: %s\n", p)
	}
	// Only providers whose MCP configuration Cartographer writes have tools to
	// load: telling someone to restart Hermes for MCP tools it never received
	// contradicts the warning printed above (D147).
	if mcpProviders := providersManagingMCP(providers); !opts.DryRun && len(mcpProviders) > 0 {
		fmt.Printf("restart the %s sessions to load the MCP tools\n", strings.Join(mcpProviders, ", "))
	}
	if res.Deferred {
		fmt.Println("warning: skill sync deferred (server unreachable); run `cartographer sync` once the server is up")
	}
	printSyncTimerHint(providers)
}

// providersNeedingSyncTimer returns the providers among those given that have
// no session-start hook and are therefore covered by no trigger at all: nil
// when every provider has a hook, and also nil when the scheduled timer is
// installed, since that is what covers the hook-less ones (D140). The timer
// status is returned alongside so a caller can name its path.
//
// One predicate, two callers — printSyncTimerHint below (connect, reconnect and
// status) and doctor's checkTriggerCoverage — because two copies of it
// disagreed: the hint never consulted the timer, so it advised installing a
// trigger that was already installed and running, on the command an operator
// reads last.
func providersNeedingSyncTimer(providers []string) ([]string, service.SyncTimerStatus) {
	var hookless []string
	for _, p := range providers {
		if !provisioning.SupportsSessionHook(configurator.Provider(p)) {
			hookless = append(hookless, p)
		}
	}
	if len(hookless) == 0 {
		return nil, service.SyncTimerStatus{}
	}
	// A status that cannot be read is not evidence the timer is there, so the
	// caller still warns.
	st, err := syncTimerStatusFn()
	if err == nil && st.Installed {
		return nil, st
	}
	return hookless, st
}

// printSyncTimerHint names the scheduled trigger once per invocation when a
// provider being configured has no session hook and the trigger is not already
// installed (D140): without either, that client only syncs when a human
// remembers to. The timer is never installed automatically — see
// cmdServiceSyncTimer.
func printSyncTimerHint(providers []string) {
	hookless, _ := providersNeedingSyncTimer(providers)
	if len(hookless) == 0 {
		return
	}
	fmt.Printf("%s has no session-start hook: it syncs only on demand — install the scheduled trigger with `cartographer service sync-timer install`\n",
		strings.Join(hookless, ", "))
}

// connectOptions bundles the parameters of a connect operation, shared by the
// CLI `connect` subcommand and the connect form (connectform.go, used both
// standalone by cmdConnect and embedded in the TUI dashboard, tui.go).
type connectOptions struct {
	Providers []string
	Dir       string
	ServerURL string
	Name      string
	Auth      bool
	TokenEnv  string
	DryRun    bool
	AutoTrust bool
	// Trust is the persistent per-server trust decision (D54), collected by
	// the connect form's toggle (or carried through from the flag path's
	// existing/default config, see cmdConnect) and persisted to
	// .cartographer.yaml by doConnect. Unlike AutoTrust (a one-time flag),
	// Trust applies to every future sync until changed again.
	Trust   bool
	PinKeys []string
	// KBs is the operator's explicit KB selection for this connect (D190),
	// from --kb or the form. Empty means "not chosen": with two or more KBs
	// mounted and no existing binding that is an error, not a silent
	// fall-back to all of them. The single sentinel "all" selects every
	// mounted KB and is recorded as an explicit binding to their names, which
	// is what stops a KB mounted later from widening this client.
	KBs []string
	// Workspace, when set, makes this connect a WORKSPACE binding rather than
	// a provider-global one (D193): the selected KBs are projected into that
	// repository's own project-local directories and nothing KB-sourced is
	// written to the global catalogue. It is offered at connect because the
	// choice between the two has to be made *before the first write*: a
	// provider-global connect materializes every selected KB's artifacts into
	// $HOME, and moving them afterwards is a migration rather than a choice.
	Workspace string
}

// connectResult is the outcome of doConnect: which providers were connected, the
// per-provider materialization result (nil when Deferred), the deferral error
// if the server was unreachable during skill materialization, the
// absolute MCP config paths written (or that would be written, in DryRun) by
// configurator.Apply, and the non-fatal warnings it produced — callers render
// their own "wrote <path>" output from this (cmdConnect for the CLI; the TUI
// stays silent on stdout).
type connectResult struct {
	Providers      []string
	Applied        map[string]provisioning.AppliedResult
	Deferred       bool
	DeferredErr    error
	ConfigsWritten []string
	MCPEntries     []string
	Warnings       []string
}

// doConnect runs the connect flow for opts.Providers against opts.Dir: writes the
// MCP client configs (HTTP transport only), best-effort materializes skills via
// sync_pull (deferred, not fatal, if the server is unreachable), and persists
// .cartographer.yaml. It is the single source of truth for "connect" business
// logic, shared by cmdConnect (CLI) and the TUI connect form.
func doConnect(opts connectOptions) (connectResult, error) {
	if len(opts.Providers) == 0 {
		return connectResult{}, fmt.Errorf("no providers to connect")
	}

	// A provider with a root of its own (D141: $HERMES_HOME) cannot be
	// connected without it. Fail here, naming the variable, rather than
	// materializing into the home directory where the agent never looks.
	for _, p := range opts.Providers {
		if _, err := provisioning.BaseDirFor(configurator.Provider(p), opts.Dir); err != nil {
			return connectResult{}, err
		}
	}

	// 1. Discover mounted KBs before generating MCP entries. A down server
	// deliberately falls back to the historical bare entry, so connection can
	// still be recorded and materialized later with `cartographer sync`.
	existing, err := clientconfig.Load(opts.Dir)
	if err != nil {
		existing = clientconfig.Default()
	}
	for _, pin := range opts.PinKeys {
		kbName, key, found := strings.Cut(pin, "=")
		if !found {
			return connectResult{}, fmt.Errorf("invalid --pin-key %q (want KB=PUBLIC_KEY)", pin)
		}
		if err := existing.AddSigningKey(kbName, key); err != nil {
			return connectResult{}, fmt.Errorf("invalid --pin-key %q: %w", pin, err)
		}
	}
	facts, healthErr := enumerateKBs(opts.ServerURL, opts.Auth, opts.TokenEnv)
	kbs := facts.Names

	// 1a. Resolve and persist the KB selection BEFORE the first MCP entry or
	// artifact is written (D190). `client bind` can only narrow a provider
	// after it is connected, so any ordering of the old commands delivered
	// every skill, agent, hook, instructions block and MCP descriptor of every
	// known KB first. The window is closed by making the choice part of
	// connect, not by moving bind earlier.
	selected, err := resolveConnectKBs(existing, opts, facts, healthErr)
	if err != nil {
		return connectResult{}, err
	}
	if selected != nil {
		// In memory in every mode, including --dry-run: the projection the run
		// reports must be the one the selection produces. The write itself is
		// already guarded at step 3.
		for _, provider := range opts.Providers {
			existing.ResetBinding(provider)
			for _, kb := range selected {
				if err := existing.Bind(provider, kb); err != nil {
					return connectResult{}, err
				}
			}
		}
	}
	// D193: --workspace turns the selection into a workspace binding instead of
	// a global one. It is decided here, before step 3 writes anything, because
	// a provider-global connect materializes every selected KB into $HOME and
	// undoing that afterwards is a migration, not a choice.
	if opts.Workspace != "" {
		for _, provider := range opts.Providers {
			p := configurator.Provider(provider)
			if !provisioning.SupportsProjectScope(p) {
				return connectResult{}, fmt.Errorf("%s cannot be scoped to a workspace: %s",
					provider, provisioning.ProjectScopeUnsupportedReason(p))
			}
			if err := existing.BindWorkspace(provider, opts.Workspace, selected); err != nil {
				return connectResult{}, err
			}
			if err := existing.SetWorkspaceScope(provider, clientconfig.ScopeWorkspace); err != nil {
				return connectResult{}, err
			}
		}
	} else if len(selected) > 0 && len(kbs) > 1 && !opts.DryRun {
		// Name the alternative once, where the operator is deciding. Silence
		// here is how a machine-wide catalogue becomes the default nobody chose.
		fmt.Println("note: these KBs will be readable from every directory on this machine.")
		fmt.Println("      pass --workspace <repo> to confine them to one repository instead (docs/sync.md §Workspace scope).")
	}

	// entryKBs stays the server's full mount list: it tells entriesForKBs that
	// the endpoint must be scoped with ?kb= at all. Which of them this provider
	// receives comes from the binding persisted just above — passing the
	// selection here instead would collapse a single-KB choice to the bare,
	// unscoped entry, which reaches every KB on the server.
	entryKBs := kbs
	if healthErr != nil || !facts.Listed {
		entryKBs = nil
	}
	// D187: a routed server collapses the entry set to one, whatever the
	// binding says — the KB travels in each tool call now, not in the URL. A
	// server that is unreachable keeps the historical shape: routing is only
	// ever asserted from evidence.
	routedPath := ""
	if healthErr == nil {
		routedPath = facts.RoutedPath
	}
	existing.ServerRoutedPath = routedPath
	existing.ServerMountMode = ""
	if routedPath != "" {
		existing.ServerMountMode = "routed"
	}
	entriesByProvider, err := entriesByProviderForKBs(existing, opts.Providers, opts.Name, opts.ServerURL, entryKBs, routedPath)
	if err != nil {
		return connectResult{}, err
	}
	if _, _, err := removeMCPEntries(opts.Name, existing.KnownKBs, opts.Providers, opts.Dir, opts.Auth, opts.TokenEnv, opts.DryRun); err != nil {
		return connectResult{}, err
	}
	configsWritten, configWarnings, err := applyMCPEntries(entriesByProvider, opts.Providers, opts.Dir, opts.Auth, opts.TokenEnv, opts.DryRun)
	if err != nil {
		return connectResult{}, err
	}
	// A routed server writes one entry per provider, and one entry cannot
	// collide with itself: the flat-namespace warning (D102) has nothing to
	// warn about and firing it would be noise.
	if routedPath == "" {
		if w := kiroFlatNamespaceWarning(opts.Providers, entriesByProvider, effectiveToolPrefixes(facts, healthErr), healthErr); w != "" {
			configWarnings = append(configWarnings, w)
		}
	}

	// 1b. Ensure the bootstrap hook (D60): purely local, independent of the
	// server manifest fetched in step 2 below — must be in place even when that
	// fetch is deferred (server down at connect time), since it's exactly what
	// lets a later session self-heal once the server comes back.
	if err := ensureBootstrapForProviders(opts.Providers, opts.Dir, opts.DryRun); err != nil {
		return connectResult{}, fmt.Errorf("ensure bootstrap hook: %w", err)
	}

	// 2. Materialize skills via sync_pull (best-effort: a deferred sync is not fatal).
	pullKBs := existing.KnownKBs
	if healthErr == nil {
		pullKBs = kbs
	}
	// Clients travels with it: connecting a provider must not pull or
	// materialize a KB it is not bound to (D170).
	pullCfg := &clientconfig.Config{ServerURL: opts.ServerURL, ServerName: opts.Name, Auth: opts.Auth, TokenEnv: opts.TokenEnv, KnownKBs: pullKBs, Clients: existing.Clients, SigningKeys: existing.SigningKeys}

	// The MCP-entry lines report what was emitted, not what was asked for:
	// entries is built from the client config alone, so keeping it when no
	// selected provider has an MCP emitter announces a write into a file the
	// output cannot even name (D147).
	mcpEntries := allEntryNames(entriesByProvider)
	if len(providersManagingMCP(opts.Providers)) == 0 {
		mcpEntries = nil
	}
	res := connectResult{Providers: opts.Providers, ConfigsWritten: configsWritten, MCPEntries: mcpEntries, Warnings: configWarnings}
	projections, projErr := allProjections(pullCfg, opts.Providers, opts.Dir)
	if projErr != nil {
		return connectResult{}, projErr
	}
	if manifests, err := manifestsForProjections(pullCfg, projections); err != nil {
		res.Deferred = true
		res.DeferredErr = err
	} else {
		applied, err := materializeForProviders(manifests, projections, opts.Dir, facts.Version, opts.Trust || opts.AutoTrust, opts.DryRun, false /* noHeal */, portabilityOptions{SearchRoots: existing.SearchRoots, SearchDepth: existing.SearchDepth, Paths: existing.Paths}, kbOrderForProviders(pullCfg, opts.Providers), existing.ApprovedMCPHashes())
		if err != nil {
			return connectResult{}, err
		}
		res.Applied = applied
	}

	// 3. Persist .cartographer.yaml.
	existing.ServerURL, existing.ServerName, existing.Auth, existing.TokenEnv, existing.Trust = opts.ServerURL, opts.Name, opts.Auth, opts.TokenEnv, opts.Trust
	if healthErr == nil {
		existing.KnownKBs = kbs
	}
	for _, p := range opts.Providers {
		existing.AddAgent(p)
	}
	if !opts.DryRun {
		if err := clientconfig.Save(opts.Dir, existing); err != nil {
			return connectResult{}, err
		}
	}

	return res, nil
}

// printMCPEntryLines reports the MCP entries emitted for providers, in the
// conditional under dryRun. Shared by connect and sync because both derive
// entries from the client config alone: without scoping to the providers that
// actually have an emitter, either would announce a write into a file it does
// not name and cannot have touched (D147).
func printMCPEntryLines(providers, entries []string, dryRun bool) {
	if len(providersManagingMCP(providers)) == 0 {
		return
	}
	for _, entry := range entries {
		if dryRun {
			fmt.Printf("[dry-run] would write MCP entry %s\n", entry)
		} else {
			fmt.Printf("wrote MCP entry %s\n", entry)
		}
	}
}

// printMCPEntryRemovals reports the MCP entries reconciliation removes.
// Only the ones NOT being rewritten are worth a line: an entry that is
// removed and immediately re-applied is an implementation detail of the
// rewrite, and printing it as a removal would make a no-op sync look
// destructive. Under --dry-run this is the half of the plan that was missing
// entirely (D172 WP4).
func printMCPEntryRemovals(providers []string, removed map[string][]string, applied []string, dryRun bool) {
	if len(providersManagingMCP(providers)) == 0 {
		return
	}
	keep := make(map[string]bool, len(applied))
	for _, name := range applied {
		keep[name] = true
	}
	seen := map[string]bool{}
	var gone []string
	for _, p := range providers {
		for _, name := range removed[p] {
			if keep[name] || seen[name] {
				continue
			}
			seen[name] = true
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		if dryRun {
			fmt.Printf("[dry-run] would remove MCP entry %s\n", name)
		} else {
			fmt.Printf("removed MCP entry %s\n", name)
		}
	}
}

// printKnownKBsChange reports the rewrite of .cartographer.yaml's known_kbs.
// A dry run that showed every artifact and every MCP entry but not this was
// an incomplete plan: the persisted KB list is what the next run reconciles
// against (D172 WP4).
func printKnownKBsChange(previous, current []string, dryRun bool) {
	if sameStringSet(previous, current) {
		return
	}
	verb := "updated"
	if dryRun {
		verb = "[dry-run] would update"
	}
	fmt.Printf("%s known_kbs: %s → %s\n", verb, kbListLabel(previous), kbListLabel(current))
}

// kbListLabel renders a KB list for a one-line diff, sorted so the two sides
// are comparable at a glance.
func kbListLabel(kbs []string) string {
	if len(kbs) == 0 {
		return "(none)"
	}
	out := append([]string(nil), kbs...)
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// sameStringSet compares two lists as sets: known_kbs order is not meaningful
// and reordering it is not a change worth reporting.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// providersManagingMCP filters providers down to those whose MCP configuration
// Cartographer actually writes. A provider configured outside Cartographer
// (D141, hermes) receives artifacts but no MCP entry, so every line that talks
// about MCP must be scoped to this subset rather than to the whole selection.
func providersManagingMCP(providers []string) []string {
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		if configurator.ManagesMCPConfig(configurator.Provider(p)) {
			out = append(out, p)
		}
	}
	return out
}

// resolveConnectKBs decides which KBs this connect binds the target providers
// to (D190).
//
// A provider that already carries an explicit binding is not a first connect:
// its recorded choice stands, and a `reconnect` or a re-run never silently
// re-opens a catalogue the operator narrowed. Only a provider with no binding
// yet must choose, and only when there is something to choose between.
func resolveConnectKBs(cfg *clientconfig.Config, opts connectOptions, facts serverFacts, healthErr error) ([]string, error) {
	if len(opts.KBs) == 0 && allProvidersBound(cfg, opts.Providers) {
		// Every target already declared its own binding: preserve it verbatim.
		return nil, nil
	}
	mounted := facts.Names
	listed := healthErr == nil && facts.Listed
	return resolveKBSelection(opts.KBs, mounted, listed)
}

// allProvidersBound reports whether every provider already has an explicit
// binding recorded — the difference between "these KBs" and "whatever is
// known", which BoundKBs is the only place allowed to resolve.
func allProvidersBound(cfg *clientconfig.Config, providers []string) bool {
	for _, p := range providers {
		if _, explicit := cfg.BoundKBs(p); !explicit {
			return false
		}
	}
	return true
}

// existingOrDefault loads the client config for dir, falling back to the
// defaults when there is none yet — a first connect has no file, and that is
// exactly the case the KB selection exists for.
func existingOrDefault(dir string) *clientconfig.Config {
	if c, err := clientconfig.Load(dir); err == nil {
		return c
	}
	return clientconfig.Default()
}
