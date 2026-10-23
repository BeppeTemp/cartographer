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
	"github.com/BeppeTemp/cartographer/internal/safewrite"
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
	if opts.Auth {
		token = tokenSpecValue(opts.Token)
	}
	health, err := client.New(opts.ServerURL, token).WithInsecureHTTP(opts.insecureHTTPAllowed()).Health(probeTimeout)
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

// isLoopbackURL reports whether rawURL's host is loopback (client.IsLoopbackHost) —
// used to decide whether an unreachable server is plausibly this machine's
// own cartographer service (worth offering to install), as opposed to a
// remote server the user doesn't control.
func isLoopbackURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return client.IsLoopbackHost(u.Hostname())
}

// shouldOfferServiceInstall decides whether cmdConnect should offer to
// install+start the local cartographer service after a failed probe: the
// server URL must be loopback, and no service may be installed yet: one that is
// installed and still unreachable is running with a wrong port or firewalled
// (installing again wouldn't help) or was stopped on purpose, which a reinstall
// would silently undo.
func shouldOfferServiceInstall(loopback bool, installed bool) bool {
	return loopback && !installed
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
// choice and skips the form even in a TTY. Behavior flags (--dry-run) are
// orthogonal to the form and don't suppress it.
var connectFormFlagNames = map[string]bool{
	"server-url":          true,
	"auth":                true,
	"allow-insecure-http": true,
	"token-env":           true,
	"agents":              true,
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
	// Token is the token spec (`token` in .cartographer.yaml): "${NAME}" or a
	// literal. Only a literal already in the file reaches here as a literal:
	// no flag takes one, so a token never lands in argv (D906).
	Token string
	Name  string
}

// resolveConnectSettings applies flag > config > default precedence for the
// settings persisted in .cartographer.yaml. A form flag NOT passed explicitly
// (absent from passed) inherits the value already in existing rather than the
// hard-coded flag default — so a bare `connect <agent>` on a machine already
// pointed at a remote server never silently rewrites server_url/auth/token
// to the local default / auth:false. existing is nil on a first-ever
// connect, where the flag defaults (and Name "cartographer") apply as-is. The
// interactive form, when opened, overrides ServerURL/Auth/Token afterwards with
// the user's input.
func resolveConnectSettings(passed map[string]bool, flagURL string, flagAuth bool, flagTokenEnv string, existing *clientconfig.Config) connectSettings {
	s := connectSettings{
		ServerURL: flagURL,
		Auth:      flagAuth,
		Token:     clientconfig.EnvRefToken(flagTokenEnv),
		Name:      "cartographer",
	}
	if existing == nil {
		return s
	}
	if existing.ServerName != "" {
		s.Name = existing.ServerName
	}
	if !passed["server-url"] && existing.ServerURL != "" {
		s.ServerURL = existing.ServerURL
	}
	if !passed["auth"] {
		s.Auth = existing.Auth
	}
	if !passed["token-env"] && existing.Token != "" {
		s.Token = existing.Token
	}
	return s
}

// cmdConnect generates the MCP client config (HTTP transport only, D37) for
// the requested agent provider(s) — default "all" = every agent
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
	autoTrust := fs.Bool("auto-trust", false, "Removed (D906): accepted and ignored")
	var pinKeys repeatedString
	fs.Var(&pinKeys, "pin-key", "Pin a KB Ed25519 public key as KB=PUBLIC_KEY (repeatable)")
	noInput := fs.Bool("no-input", false, "Never open the interactive form, even in a TTY")
	agentsCSV := fs.String("agents", "", "Comma-separated agent subset: claude,codex")
	var kbSel repeatedString
	fs.Var(&kbSel, "kb", "KB this client may receive (repeatable, or comma-separated; 'all' for every mounted KB)")
	allowInsecure := fs.Bool("allow-insecure-http", false, "Accept a plain http:// server URL that is not loopback (the bearer token then travels in clear)")
	workspaceFlag := fs.String("workspace", "", "Project the selected KBs into this repository instead of the global catalogue (D193)")
	fs.Parse(rest)
	warnRemovedAutoTrust(*autoTrust)

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
		Token:     settings.Token,
		DryRun:    *dryRun,
		// The key survives a re-run without the flag; the flag only adds it.
		AllowInsecureHTTP: *allowInsecure || (existing != nil && existing.AllowInsecureHTTP),
		PinKeys:           pinKeys,
		KBs:               splitCommaList(kbSel),
		Workspace:         *workspaceFlag,
		// The placeholder step is a question, so it follows the same rule as
		// every other connect question: a terminal on both ends, and no
		// --no-input. A dry run has nothing to re-materialize.
		PromptPaths: !*noInput && !*dryRun && isTerminal(os.Stdin.Fd()) && isTerminal(os.Stdout.Fd()),
	}

	if interactive {
		prefill, err := clientconfig.Load(dir)
		if err != nil {
			prefill = clientconfig.Default()
		}
		title := fmt.Sprintf("Connect %s", strings.Join(providers, ", "))
		formPrefill := connectOptions{Providers: providers, ServerURL: prefill.ServerURL, Name: prefill.ServerName, Auth: prefill.Auth, Token: prefill.Token}
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

			formOpts.AllowInsecureHTTP = opts.AllowInsecureHTTP
			if err := client.CheckServerURL(formOpts.ServerURL, formOpts.insecureHTTPAllowed()); err != nil {
				formPrefill, errMsg = formOpts, err.Error()
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
					if statusErr == nil && shouldOfferServiceInstall(true, st.Installed) {
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
			opts.Providers, opts.ServerURL, opts.Name, opts.Auth, opts.Token =
				providers, formOpts.ServerURL, formOpts.Name, formOpts.Auth, formOpts.Token

			// The KB choice (D190) comes after the probe, because that is when
			// the names exist, and before doConnect, because that is when the
			// first artifact would be written. Only asked when there is a
			// choice to make and the operator has not already made it.
			if len(opts.KBs) == 0 {
				facts, ferr := enumerateKBs(opts.ServerURL, opts.Auth, opts.Token, opts.insecureHTTPAllowed())
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
				fmt.Fprintln(os.Stderr, "hint: the local server isn't responding — run `cartographer setup` (server, first KB and agents), or `cartographer service install` for the server alone")
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
	if !opts.DryRun {
		fmt.Printf("restart the %s sessions to load the MCP tools\n", strings.Join(providers, ", "))
	}
	if res.Deferred {
		fmt.Println("warning: skill sync deferred (server unreachable); run `cartographer sync` once the server is up")
	}
	if !opts.DryRun {
		ensureSyncTimer(dir, providers)
		removeLegacyDoctorTimer()
	} else if hookless := hooklessProviders(providers); len(hookless) > 0 {
		fmt.Printf("[dry-run] would install the sync timer: %s\n", describeHookless(hookless))
	}
	if !opts.DryRun {
		ackSessionHookLimit(dir, providers)
	}
}

// ackSessionHookLimit records, for every provider whose session hook fires only
// in some sessions (SessionHookLimit, D300), that connect has just shown the
// operator that limit (D320): `status` then stops repeating it. Best effort —
// a lockfile that cannot be read or written only means the hint is shown
// again, which is the pre-D320 behaviour.
func ackSessionHookLimit(dir string, providers []string) {
	path := lockFilePath(dir)
	lf, err := provisioning.ReadLockFile(path)
	if err != nil {
		return
	}
	changed := false
	for _, p := range providers {
		if provisioning.SessionHookLimit(configurator.Provider(p)) == "" {
			continue
		}
		lock := lf.ForProvider(p)
		if lock.SessionHookLimitAcked || lock.Provider == "" {
			continue
		}
		lock.SessionHookLimitAcked = true
		lf.SetProvider(p, lock)
		changed = true
	}
	if changed {
		_ = provisioning.WriteLockFile(path, lf)
	}
}

// hooklessProviders returns the providers among those given that have no
// session-start hook, or one that fires only in some of their sessions
// (provisioning.SessionHookLimit — Kiro, D300): not covered by a trigger in
// every session. Pure — it never looks at the timer.
func hooklessProviders(providers []string) []string {
	var hookless []string
	for _, p := range providers {
		provider := configurator.Provider(p)
		if !provisioning.SupportsSessionHook(provider) || provisioning.SessionHookLimit(provider) != "" {
			hookless = append(hookless, p)
		}
	}
	return hookless
}

// providersNeedingSyncTimer returns the hookless providers (hooklessProviders)
// that the scheduled timer does not yet cover: nil when every provider has a
// hook, and also nil when the timer is installed, since that is what covers the
// hook-less ones (D140). The timer status is returned alongside so a caller can
// name its path.
//
// One predicate, three callers — ensureSyncTimer (connect), printSyncTimerHint
// (status) and doctor's checkTriggerCoverage — because two copies of it
// disagreed: the hint never consulted the timer, so it advised installing a
// trigger that was already installed and running. Disconnect must NOT use this
// one to decide whether the timer is still needed: an installed timer makes it
// return nil, which would read as "nobody needs it"; it uses hooklessProviders.
func providersNeedingSyncTimer(providers []string) ([]string, service.SyncTimerStatus) {
	hookless := hooklessProviders(providers)
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

// removeLegacyDoctorTimer removes the scheduled headless doctor job that 0.22
// and 0.23 installed (D369, D690): Cartographer no longer starts an agent by
// itself (D704), and the job would fire a `doctor run` that no longer exists.
// Called by connect, sync and upgrade-repair; best effort, it never fails either.
func removeLegacyDoctorTimer() {
	removed, err := removeLegacyDoctorTimerFn()
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "warning: removing the old scheduled doctor job failed: %v\n", err)
	case removed:
		fmt.Println("removed the old scheduled doctor job: cartographer-kb-doctor sessions now start only when you ask an agent for one")
	}
}

// ensureSyncTimer installs the scheduled trigger when a provider being
// configured has no reliable session-start hook and the timer is not already
// there (D325, revising D140's opt-in): left to a hint, that client drifts
// silently. An explicit `service sync-timer uninstall` is remembered
// (clientconfig.SyncTimerOptOut) and respected. The opt-out is read BEFORE
// anything is installed, and a config that cannot be read counts as opted out:
// a corrupt file must never be the reason a background job appears.
func ensureSyncTimer(dir string, providers []string) {
	hookless, _ := providersNeedingSyncTimer(providers)
	if len(hookless) == 0 {
		return // every provider has a hook, or the timer already covers them
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Printf("note: %s, but the client config cannot be read (%v) — not installing the sync timer\n", describeHookless(hookless), err)
		return
	}
	if cfg != nil && cfg.SyncTimerOptOut {
		fmt.Printf("note: %s, but the sync timer was explicitly uninstalled — not reinstalling (reset with `cartographer service sync-timer install`)\n", describeHookless(hookless))
		return
	}
	fmt.Printf("installing the sync timer: %s\n", describeHookless(hookless))
	if err := syncTimerInstallFn(service.DefaultSyncInterval); err != nil {
		fmt.Fprintf(os.Stderr, "warning: sync timer install failed: %v; install it manually with `cartographer service sync-timer install`\n", err)
		return
	}
	fmt.Printf("sync timer installed (every %s); opt out with `cartographer service sync-timer uninstall`\n", service.DefaultSyncInterval)
}

// printSyncTimerHint names the scheduled trigger when a connected provider has
// no reliable session hook and the timer is not installed. `status` only
// reports (D325): installing is connect's and setup's job, never a read's.
func printSyncTimerHint(providers []string) {
	hookless, _ := providersNeedingSyncTimer(providers)
	if len(hookless) == 0 {
		return
	}
	fmt.Printf("%s: it syncs only on demand — install the scheduled trigger with `cartographer service sync-timer install`\n",
		describeHookless(hookless))
}

// describeHookless names the providers providersNeedingSyncTimer returned, each
// with the reason it needs the timer: no session-start hook at all, or one that
// fires only in the sessions SessionHookLimit names. Saying "no hook" of a
// client whose hook is installed would be false; saying nothing would hide the
// sessions it never fires in.
func describeHookless(hookless []string) string {
	var none, partial []string
	for _, p := range hookless {
		if limit := provisioning.SessionHookLimit(configurator.Provider(p)); limit != "" {
			partial = append(partial, fmt.Sprintf("%s's session-start hook fires only in `%s`", p, limit))
		} else {
			none = append(none, p)
		}
	}
	var parts []string
	if len(none) > 0 {
		parts = append(parts, strings.Join(none, ", ")+" has no session-start hook")
	}
	return strings.Join(append(parts, partial...), "; ")
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
	// Token is the token spec persisted as `token` (see connectSettings.Token).
	Token   string
	DryRun  bool
	PinKeys []string
	// AllowInsecureHTTP is the persisted opt-in to a non-loopback http://
	// server URL (D1023); the env var also allows a run (insecureHTTPAllowed).
	AllowInsecureHTTP bool
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
	// PromptPaths opens the placeholder step (D262, pathsform.go) after the
	// first materialization when some key the bound KBs cite did not
	// resolve. Set by cmdConnect only on a TTY without --no-input; the TUI
	// leaves it off (it cannot run a second program inside its own), and a
	// non-interactive run reports the keys through printApplySummary instead.
	PromptPaths bool
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

// insecureHTTPAllowed reports whether this connect may reach a non-loopback
// http:// URL: the opt-in, or CARTOGRAPHER_ALLOW_INSECURE_HTTP.
func (o connectOptions) insecureHTTPAllowed() bool {
	return (&clientconfig.Config{AllowInsecureHTTP: o.AllowInsecureHTTP}).InsecureHTTPAllowed()
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
	// The pre-#329 default is migrated when passed explicitly too: it reaches
	// here from old scripts and docs, and on Windows it points at ::1, where
	// the local service does not listen (D231).
	opts.ServerURL = defaults.MigrateLegacyMCPURL(opts.ServerURL)
	// Refused before anything is read or written: a bearer token never leaves
	// for a cleartext non-loopback URL without the opt-in (D1023).
	if err := client.CheckServerURL(opts.ServerURL, opts.insecureHTTPAllowed()); err != nil {
		return connectResult{}, err
	}

	// 1. Discover mounted KBs before generating MCP entries. A down server
	// deliberately falls back to the historical bare entry, so connection can
	// still be recorded and materialized later with `cartographer sync`.
	existing, err := clientconfig.Load(opts.Dir)
	if err != nil {
		existing = clientconfig.Default()
	} else {
		// connect saves the config itself, which drops the name; the lockfile
		// section goes with the next sync (connect holds no client lock here).
		handleUnsupportedProviders(opts.Dir, existing, false)
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
	facts, healthErr := enumerateKBs(opts.ServerURL, opts.Auth, opts.Token, opts.insecureHTTPAllowed())
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
	claudeDirs, claudeDirWarnings := claudeConfigDirs(opts.Dir, existing)
	// A symlinked config file or directory is refused for its provider only
	// (D148): the refusal is a warning, the other providers proceed.
	var refusalNotes []string
	if _, _, err := removeMCPEntries(opts.Name, existing.KnownKBs, opts.Providers, opts.Dir, claudeDirs, opts.Auth, opts.tokenEnvName(), opts.DryRun); errors.Is(err, safewrite.ErrSymlinkDestination) {
		refusalNotes = refusalWarnings(err)
	} else if err != nil {
		return connectResult{}, err
	}
	authorWarnings := setEntryAuthors(existing, entriesByProvider, globalIdentFn)
	configsWritten, configWarnings, err := applyMCPEntries(entriesByProvider, opts.Providers, opts.Dir, claudeDirs, opts.Auth, opts.tokenEnvName(), opts.DryRun)
	if err != nil {
		return connectResult{}, err
	}
	configWarnings = appendUnique(append(configWarnings, authorWarnings...), refusalNotes...)
	// A routed server writes one entry per provider, and one entry cannot
	// collide with itself: the flat-namespace warning (D102) has nothing to
	// warn about and firing it would be noise.
	if routedPath == "" {
		if w := kiroFlatNamespaceWarning(opts.Providers, entriesByProvider, effectiveToolPrefixes(facts, healthErr), healthErr); w != "" {
			configWarnings = append(configWarnings, w)
		}
	}
	if w := toolIdentifierBudgetWarning(opts.Providers, entriesByProvider, facts, healthErr); w != "" {
		configWarnings = append(configWarnings, w)
	}
	if slices.Contains(opts.Providers, string(configurator.ProviderClaudeCode)) {
		configWarnings = append(configWarnings, claudeDirWarnings...)
	}
	if w := literalTokenWarning(opts.Auth, opts.Token); w != "" {
		configWarnings = append(configWarnings, w)
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
	pullCfg := &clientconfig.Config{ServerURL: opts.ServerURL, ServerName: opts.Name, Auth: opts.Auth, Token: opts.Token, AllowInsecureHTTP: opts.AllowInsecureHTTP, KnownKBs: pullKBs, Clients: existing.Clients, SigningKeys: existing.SigningKeys}

	mcpEntries := allEntryNames(entriesByProvider)
	res := connectResult{Providers: opts.Providers, ConfigsWritten: configsWritten, MCPEntries: mcpEntries, Warnings: configWarnings}
	projections, projErr := allProjections(pullCfg, opts.Providers, opts.Dir)
	if projErr != nil {
		return connectResult{}, projErr
	}
	if manifests, err := manifestsForProjections(pullCfg, projections); err != nil {
		res.Deferred = true
		res.DeferredErr = err
	} else {
		materialize := func() (map[string]provisioning.AppliedResult, error) {
			return materializeForProviders(manifests, projections, opts.Dir, facts.Version, opts.DryRun, false /* noHeal */, portabilityOptions{SearchRoots: existing.ScanRoots(), Paths: existing.Paths}, kbOrderForProviders(pullCfg, opts.Providers), existing.ApprovedMCPHashes())
		}
		applied, err := materialize()
		if err != nil {
			return connectResult{}, err
		}
		// The placeholder step (D262): the keys are only known now, after the
		// pull, so this is where they can be asked for. Answers go into
		// existing.Paths — saved with the rest of the config below — and the
		// instructions block is materialized once more so its table carries
		// them; everything else is already in place and is a no-op.
		if opts.PromptPaths && !opts.DryRun {
			if rows := unresolvedRowsOf(applied, existing.IgnoredPaths); len(rows) > 0 {
				answers, err := runPathsForm(rows)
				if err != nil {
					return connectResult{}, fmt.Errorf("placeholder step: %w", err)
				}
				if len(answers) > 0 {
					if existing.Paths == nil {
						existing.Paths = map[string]string{}
					}
					for id, path := range answers {
						key, kind := pathsConfigKey(id)
						for _, w := range pathWarnings(kind, path) {
							res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", id, w))
						}
						existing.Paths[key] = path
					}
					if applied, err = materialize(); err != nil {
						return connectResult{}, err
					}
				}
			}
		}
		res.Applied = applied
	}

	// 3. Persist .cartographer.yaml.
	existing.ServerURL, existing.ServerName, existing.Auth, existing.Token = opts.ServerURL, opts.Name, opts.Auth, opts.Token
	existing.AllowInsecureHTTP = opts.AllowInsecureHTTP
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
		if err := syncTokenFile(opts); err != nil {
			return connectResult{}, err
		}
	}

	return res, nil
}

// syncTokenFile keeps the private token file (D699) in step with connect: with
// auth on and the env var set, the file gets a copy for unattended runs; with
// auth off, a stale copy goes away. With auth on and the variable unset (a
// reconnect from a bare shell) the existing file is left alone.
func syncTokenFile(opts connectOptions) error {
	if !opts.Auth || strings.TrimSpace(opts.Token) == "" {
		return clientconfig.RemoveToken(opts.Dir)
	}
	// A literal token is in .cartographer.yaml (mode 0600) and wins over the
	// file: nothing to copy, and an old copy is left as it is (D906).
	if (&clientconfig.Config{Token: opts.Token}).LiteralToken() != "" {
		return nil
	}
	if tok := tokenSpecValue(opts.Token); tok != "" {
		return clientconfig.WriteToken(opts.Dir, tok)
	}
	return nil
}

// literalTokenWarning tells the person that a literal token does not reach the
// agents: their MCP entries can only name an environment variable, and nothing
// exports it for a literal. "" when auth is off or the token is a reference.
func literalTokenWarning(auth bool, token string) string {
	c := &clientconfig.Config{Token: token}
	if !auth || c.LiteralToken() == "" {
		return ""
	}
	return fmt.Sprintf("the token in %s is a literal: it reaches cartographer's own sync, but the agents' MCP entries read $%s, which nothing sets for them. Export $%[2]s in the environment that starts your agents, or use token: ${NAME} (the literal is never written into an agent's config)", clientconfig.FileName, c.TokenEnv())
}

// tokenEnvName is the environment variable the agents' MCP entries reference
// for this connect: the NAME of a `${NAME}` token, the default name for a
// literal one, which an entry cannot carry (D906).
func (o connectOptions) tokenEnvName() string {
	return (&clientconfig.Config{Token: o.Token}).TokenEnv()
}

// printMCPEntryLines reports the MCP entries emitted for providers, in the
// conditional under dryRun. Shared by connect and sync because both derive
// entries from the client config alone: without scoping to the providers that
// actually have an emitter, either would announce a write into a file it does
// not name and cannot have touched (D147).
func printMCPEntryLines(providers, entries []string, dryRun bool) {
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
