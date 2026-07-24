package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	xterm "github.com/charmbracelet/x/term"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

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
		return "server is up but no KB is mounted — create one with: cartographer kb create <name>, then: cartographer service restart"
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

// installServiceAndWaitHealthy installs+starts the local cartographer
// service with default options (mirroring `cartographer service install`
// with no flags) and polls its /health endpoint until it responds or
// timeout elapses. Returns the first error encountered (install failure, or
// "still unhealthy after timeout").
func installServiceAndWaitHealthy(mgr *service.Manager, timeout time.Duration) error {
	if _, err := mgr.Install(service.InstallOptions{DataDir: defaultDataDir(), HTTPAddr: "127.0.0.1:8080"}); err != nil {
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
// to http://localhost:8080 / auth:false. existing is nil on a first-ever
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
// decisions.md) for the requested agent provider(s) — default "all" = every agent
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
	serverURL := fs.String("server-url", "http://localhost:8080/mcp", "Cartographer server URL")
	auth := fs.Bool("auth", false, "Enable bearer-token auth in generated configs")
	tokenEnv := fs.String("token-env", "CARTOGRAPHER_TOKENS", "Env var holding the bearer token")
	dryRun := fs.Bool("dry-run", false, "Print what would be written without writing")
	autoTrust := fs.Bool("auto-trust", false, "Trust KB-sourced skills without explicit signature")
	noInput := fs.Bool("no-input", false, "Never open the interactive form, even in a TTY")
	agentsCSV := fs.String("agents", "", "Comma-separated agent subset: claude,codex")
	fs.Parse(rest)

	interactive := wantsConnectForm(fs, *noInput)

	providers, err := resolveTargetProviders(target, *agentsCSV)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(providers) == 0 {
		fmt.Fprintln(os.Stderr, "No agent detected on this machine; nothing to connect (pass an explicit provider name to force it: claude|opencode|codex|kiro).")
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
	for _, p := range res.ConfigsWritten {
		if opts.DryRun {
			fmt.Printf("[dry-run] would write %s\n", p)
		} else {
			fmt.Printf("wrote %s\n", p)
		}
	}

	if res.Deferred {
		fmt.Fprintf(os.Stderr, "Warning: skill sync deferred, server unreachable: %v\n", res.DeferredErr)
	} else {
		printApplySummary(dir, res.Applied)
	}

	for _, p := range providers {
		fmt.Printf("connected: %s\n", p)
	}
	if !opts.DryRun {
		fmt.Printf("restart the %s sessions to load the MCP tools\n", strings.Join(providers, ", "))
	}
	if res.Deferred {
		fmt.Println("warning: skill sync deferred (server unreachable); run `cartographer sync` once the server is up")
	}
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
	Trust bool
}

// connectResult is the outcome of doConnect: which providers were connected, the
// per-provider materialization result (nil when Deferred), the deferral error
// if the server was unreachable during skill materialization, and the
// absolute MCP config paths written (or that would be written, in DryRun) by
// configurator.Apply — callers render their own "wrote <path>"
// output from this (cmdConnect for the CLI; the TUI stays silent on stdout).
type connectResult struct {
	Providers      []string
	Applied        map[string]provisioning.AppliedResult
	Deferred       bool
	DeferredErr    error
	ConfigsWritten []string
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

	// 1. Generate + apply MCP configs (HTTP only).
	scfg := &configurator.ServerConfig{Name: opts.Name, URL: opts.ServerURL, AuthEnabled: opts.Auth, TokenEnv: opts.TokenEnv}
	var configsWritten []string
	for _, p := range opts.Providers {
		r, err := configurator.Emit(scfg, configurator.Provider(p))
		if err != nil {
			return connectResult{}, fmt.Errorf("emit %s: %w", p, err)
		}
		written, err := configurator.Apply([]*configurator.EmitResult{r}, opts.Dir, opts.DryRun)
		if err != nil {
			return connectResult{}, fmt.Errorf("write config for %s: %w", p, err)
		}
		configsWritten = append(configsWritten, written...)
	}

	// 1b. Ensure the bootstrap hook (D60): purely local, independent of the
	// server manifest fetched in step 2 below — must be in place even when that
	// fetch is deferred (server down at connect time), since it's exactly what
	// lets a later session self-heal once the server comes back.
	if err := ensureBootstrapForProviders(opts.Providers, opts.Dir, opts.DryRun); err != nil {
		return connectResult{}, fmt.Errorf("ensure bootstrap hook: %w", err)
	}

	// 2. Materialize skills via sync_pull (best-effort: a deferred sync is not fatal).
	existing, err := clientconfig.Load(opts.Dir)
	if err != nil {
		existing = clientconfig.Default()
	}
	pullCfg := &clientconfig.Config{ServerURL: opts.ServerURL, ServerName: opts.Name, Auth: opts.Auth, TokenEnv: opts.TokenEnv, KBs: existing.KBs}

	res := connectResult{Providers: opts.Providers, ConfigsWritten: configsWritten}
	if m, err := fetchMergedManifest(pullCfg); err != nil {
		res.Deferred = true
		res.DeferredErr = err
	} else {
		applied, err := materializeForProviders(m, opts.Providers, opts.Dir, opts.Trust || opts.AutoTrust, opts.DryRun, existing.SearchRoots, existing.Paths)
		if err != nil {
			return connectResult{}, err
		}
		res.Applied = applied
	}

	// 3. Persist .cartographer.yaml.
	existing.ServerURL, existing.ServerName, existing.Auth, existing.TokenEnv, existing.Trust = opts.ServerURL, opts.Name, opts.Auth, opts.TokenEnv, opts.Trust
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
