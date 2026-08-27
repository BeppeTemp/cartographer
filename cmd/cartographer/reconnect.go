package main

// `cartographer reconnect` (D142): rebuild a provider's configuration from
// scratch after a server upgrade. `sync` is incremental by design — pruning is
// managed-only, so files and registrations left behind by an OLDER Cartographer
// version (a differently named generated plugin, a marker spelling that
// changed, a hook registered outside the managed block) are not in the current
// managed set and survive every sync. A full disconnect followed by a full
// connect removes them, because the connect half writes the current shape from
// nothing.
//
// It is deliberately NOT automatic (same reasoning as D121's upgrade-repair:
// automatic repair never invents an approval and never broadens trust), and it
// is a rebuild, not a reset: every setting in .cartographer.yaml — server URL
// and name, auth mode and token env, trust, pinned signing keys, MCP approvals,
// search roots and paths — survives the cycle, because doDisconnect preserves
// the file and doConnect reads it back.

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func cmdReconnect(args []string) int {
	target, rest := splitPositional(args, "")

	fs := flag.NewFlagSet("reconnect", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what would be rebuilt without writing")
	agentsCSV := fs.String("agents", "", "Comma-separated agent subset: claude,codex")
	fs.Parse(rest)

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if _, err := clientconfig.Load(dir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: no client config found in %s (run `cartographer connect` first): %v\n", dir, err)
		return 2
	}

	res, err := doReconnect(reconnectOptions{Target: target, AgentsCSV: *agentsCSV, Dir: dir, DryRun: *dryRun})
	for _, p := range res.Rebuilt {
		fmt.Printf("[%s] was not connected: building its configuration from scratch\n", p)
	}
	if res.Disconnected != nil {
		printDisconnectSummary(*res.Disconnected, *dryRun)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		// The disconnect half may already have run: saying only "error" here
		// would leave an agent without its MCP endpoint and no hint of it.
		if len(res.LeftDisconnected) > 0 {
			fmt.Fprintf(os.Stderr, "these providers are now DISCONNECTED: %s\n", strings.Join(res.LeftDisconnected, ", "))
			fmt.Fprintf(os.Stderr, "finish the rebuild with: %s\n", reconnectRecoveryCommand(res.LeftDisconnected, res.ConnectOptions))
		}
		return 2
	}
	if len(res.Providers) == 0 {
		fmt.Println("no connected provider matches; nothing to reconnect (name a provider explicitly to rebuild it anyway)")
		return 0
	}
	printConnectResult(dir, res.Providers, res.ConnectOptions, *res.Connected)
	return 0
}

// reconnectOptions bundles what a rebuild needs: the same provider selectors
// `connect`/`disconnect` accept (no third spelling), the client target
// directory, and dry-run.
type reconnectOptions struct {
	Target    string
	AgentsCSV string
	Dir       string
	DryRun    bool
}

// reconnectResult makes both halves of the cycle visible to the caller, and —
// when the connect half failed — exactly which providers the disconnect half
// left without a configuration.
type reconnectResult struct {
	Providers []string
	// Rebuilt are the selected providers that were not previously connected.
	// They are rebuilt all the same (this is a rebuild, not a repair), but the
	// difference is stated so an unexpected name in the output is explained.
	Rebuilt          []string
	Disconnected     *disconnectResult
	Connected        *connectResult
	ConnectOptions   connectOptions
	LeftDisconnected []string
}

// doReconnect is the business logic behind `reconnect`: a full doDisconnect
// followed by a full doConnect for the selected providers, reusing both rather
// than being a third implementation of either. Every setting is read from
// .cartographer.yaml and re-applied, so the cycle preserves the server URL and
// name, auth mode, token env and trust — and, because doDisconnect never
// deletes the file (D64) and doConnect reads it back, the pinned signing keys,
// MCP approvals, search roots and paths as well.
func doReconnect(opts reconnectOptions) (reconnectResult, error) {
	cfg, err := clientconfig.Load(opts.Dir)
	if err != nil {
		return reconnectResult{}, err
	}
	providers, err := resolveDisconnectProviders(opts.Target, opts.AgentsCSV, cfg.Agents)
	if err != nil {
		return reconnectResult{}, err
	}
	if len(providers) == 0 {
		return reconnectResult{}, nil
	}

	res := reconnectResult{Providers: providers}
	connected := make(map[string]bool, len(cfg.Agents))
	for _, p := range cfg.Agents {
		connected[p] = true
	}
	for _, p := range providers {
		if !connected[p] {
			res.Rebuilt = append(res.Rebuilt, p)
		}
	}

	disconnected, err := doDisconnect(disconnectOptions{Providers: providers, Dir: opts.Dir, DryRun: opts.DryRun})
	if err != nil {
		return res, err
	}
	res.Disconnected = &disconnected

	res.ConnectOptions = connectOptions{
		Providers: providers,
		Dir:       opts.Dir,
		ServerURL: cfg.ServerURL,
		Name:      cfg.ServerName,
		Auth:      cfg.Auth,
		TokenEnv:  cfg.TokenEnv,
		Trust:     cfg.Trust,
		DryRun:    opts.DryRun,
	}
	connectRes, err := doConnect(res.ConnectOptions)
	if err != nil {
		// A dry run wrote nothing, so nothing was left disconnected either.
		if !opts.DryRun {
			res.LeftDisconnected = providers
		}
		return res, err
	}
	res.Connected = &connectRes
	return res, nil
}

// reconnectRecoveryCommand renders the exact `cartographer connect` invocation
// that finishes a rebuild whose connect half failed — with the same settings
// reconnect was using, so recovering never means reconstructing them by hand.
func reconnectRecoveryCommand(providers []string, opts connectOptions) string {
	var sb strings.Builder
	sb.WriteString("cartographer connect --agents ")
	sb.WriteString(strings.Join(providers, ","))
	if opts.ServerURL != "" {
		fmt.Fprintf(&sb, " --server-url %s", opts.ServerURL)
	}
	if opts.Auth {
		sb.WriteString(" --auth")
		if opts.TokenEnv != "" {
			fmt.Fprintf(&sb, " --token-env %s", opts.TokenEnv)
		}
	}
	return sb.String()
}
