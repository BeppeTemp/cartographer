package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// cmdClient manages the per-provider KB binding (D169): which Knowledge Bases
// each connected agent client may receive. Every subcommand here works
// offline — it reads and writes .cartographer.yaml only and never contacts the
// server, so configuring a machine does not require the network.
func cmdClient(args []string) int {
	if len(args) == 0 {
		printClientUsage(os.Stderr)
		return 2
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no client config found in %s (run `cartographer connect` first): %v\n", dir, err)
		return 2
	}

	switch args[0] {
	case "list":
		return clientList(cfg, args[1:])
	case "show":
		return clientShow(cfg, args[1:])
	case "bind":
		return clientBind(dir, cfg, args[1:])
	case "unbind":
		return clientUnbind(dir, cfg, args[1:])
	case "reset":
		return clientReset(dir, cfg, args[1:])
	case "update":
		return clientUpdate(dir, cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown subcommand %q\n", args[0])
		printClientUsage(os.Stderr)
		return 2
	}
}

func printClientUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: cartographer client list")
	fmt.Fprintln(w, "       cartographer client show <provider>")
	fmt.Fprintln(w, "       cartographer client bind <provider> <kb>[,<kb>...]")
	fmt.Fprintln(w, "       cartographer client unbind <provider> <kb>[,<kb>...]")
	fmt.Fprintln(w, "       cartographer client reset <provider>")
	fmt.Fprintln(w, "       cartographer client update [--check=true|false] [--policy notify|auto-patch]")
}

// clientUpdate shows or sets the client-wide `update:` block (D254): whether
// the update check runs, and whether a patch release may install itself.
// Offline like every other `client` subcommand.
func clientUpdate(dir string, cfg *clientconfig.Config, args []string) int {
	fs := flag.NewFlagSet("client update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	check := fs.String("check", "", "Run the update check: true or false")
	policy := fs.String("policy", "", "notify (default) or auto-patch (patch releases through homebrew, install.sh or install.ps1 install themselves)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		printClientUsage(os.Stderr)
		return 2
	}
	changed := false
	if *check != "" {
		v, err := strconv.ParseBool(*check)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid --check %q (want true or false)\n", *check)
			return 2
		}
		cfg.Update.Check = &v
		changed = true
	}
	if *policy != "" {
		p, err := updatecheck.ParsePolicy(*policy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		cfg.Update.Policy = p
		changed = true
	}
	if changed {
		if code := saveClientConfig(dir, cfg); code != 0 {
			return code
		}
	}
	fmt.Printf("update check   %t\n", cfg.Update.CheckEnabled())
	fmt.Printf("update policy  %s\n", cfg.Update.EffectivePolicy())
	return 0
}

// bindingOrigin renders where a provider's KB list came from. It is the one
// piece of information that makes `client list` actionable: the same list of
// names means "declared" or "everything the server happens to mount today"
// depending on this column.
func bindingOrigin(explicit bool) string {
	if explicit {
		return "explicit"
	}
	return "default (all known)"
}

// formatKBList renders a resolved binding, distinguishing an explicitly empty
// one from an unset default. "none" is a configured state, not an error.
func formatKBList(kbs []string) string {
	if len(kbs) == 0 {
		return "none"
	}
	return strings.Join(kbs, ", ")
}

func clientList(cfg *clientconfig.Config, args []string) int {
	if len(args) != 0 {
		printClientUsage(os.Stderr)
		return 2
	}
	if len(cfg.Agents) == 0 {
		fmt.Println("no agent connected (run `cartographer connect`)")
		return 0
	}
	fmt.Printf("%-12s %-22s %s\n", "PROVIDER", "ORIGIN", "KBS")
	for _, p := range cfg.Agents {
		kbs, explicit := cfg.BoundKBs(p)
		fmt.Printf("%-12s %-22s %s\n", p, bindingOrigin(explicit), formatKBList(kbs))
	}
	return 0
}

func clientShow(cfg *clientconfig.Config, args []string) int {
	if len(args) != 1 {
		printClientUsage(os.Stderr)
		return 2
	}
	provider := args[0]
	if code := requireConnected(cfg, provider); code != 0 {
		return code
	}

	kbs, explicit := cfg.BoundKBs(provider)
	fmt.Printf("provider   %s\n", provider)
	fmt.Printf("origin     %s\n", bindingOrigin(explicit))
	fmt.Printf("bound      %s\n", formatKBList(kbs))

	// What a provider does NOT receive is the half a binding is declared for,
	// and it cannot be derived from the line above without the known set.
	bound := make(map[string]bool, len(kbs))
	for _, kb := range kbs {
		bound[kb] = true
	}
	var unbound []string
	for _, kb := range cfg.KnownKBs {
		if !bound[kb] {
			unbound = append(unbound, kb)
		}
	}
	sort.Strings(unbound)
	fmt.Printf("not bound  %s\n", formatKBList(unbound))
	return 0
}

func clientBind(dir string, cfg *clientconfig.Config, args []string) int {
	provider, kbs, code := parseBindArgs(cfg, args)
	if code != 0 {
		return code
	}

	_, wasExplicit := cfg.BoundKBs(provider)
	for _, kb := range kbs {
		if err := cfg.Bind(provider, kb); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}
	if code := saveClientConfig(dir, cfg); code != 0 {
		return code
	}

	// Creating the first binding flips the provider from "receives every known
	// KB" to "receives only these": that is a reduction in what it gets, and
	// stating it is the difference between a deliberate change and a surprise.
	if !wasExplicit {
		fmt.Printf("%s now receives only the KBs bound to it (it previously received every known KB)\n", provider)
	}
	// A KB absent from the cached set is not an error: it may be mounted later,
	// and this command must keep working with no server reachable.
	known := make(map[string]bool, len(cfg.KnownKBs))
	for _, kb := range cfg.KnownKBs {
		known[kb] = true
	}
	for _, kb := range kbs {
		if !known[kb] {
			fmt.Fprintf(os.Stderr, "warning: KB %q is not among the KBs the server last advertised\n", kb)
		}
	}

	bound, _ := cfg.BoundKBs(provider)
	fmt.Printf("%s bound to %s\n", provider, formatKBList(bound))
	warnProviderCollisions(cfg, provider, bound)
	fmt.Println("run `cartographer sync` to apply")
	return 0
}

// warnProviderCollisions reports, best-effort, a cross-KB collision the new
// binding just created (D171). Discovering it here is worth a round trip: the
// alternative is finding out at the next sync, which refuses to run.
//
// An unreachable server is not a failure. Configuring a machine must not
// require the network, so the command says the check was skipped and why, and
// the sync-time refusal remains the backstop.
func warnProviderCollisions(cfg *clientconfig.Config, provider string, bound []string) {
	if len(bound) < 2 {
		return // one KB cannot collide with itself
	}
	candidates, err := fetchCandidates(cfg, bound)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not check for cross-KB collisions (%v); `cartographer sync` will check again\n", err)
		return
	}
	for _, c := range collisionsForProvider(candidates.forKBs(bound), bound) {
		fmt.Fprintf(os.Stderr, "warning: %s/%s is claimed by %s — `cartographer sync` will refuse until one of them renames it\n",
			c.Kind, c.Name, strings.Join(c.Sources, ", "))
	}
}

func clientUnbind(dir string, cfg *clientconfig.Config, args []string) int {
	provider, kbs, code := parseBindArgs(cfg, args)
	if code != 0 {
		return code
	}

	for _, kb := range kbs {
		if err := cfg.Unbind(provider, kb); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}
	if code := saveClientConfig(dir, cfg); code != 0 {
		return code
	}

	bound, explicit := cfg.BoundKBs(provider)
	fmt.Printf("%s bound to %s\n", provider, formatKBList(bound))
	if explicit && len(bound) == 0 {
		// Deleting the entry here would silently restore "every known KB",
		// which is the opposite of what removing the last KB asks for.
		fmt.Printf("%s is now bound to no KBs; run `cartographer client reset %s` to return it to the default\n", provider, provider)
	}
	fmt.Println("run `cartographer sync` to apply")
	return 0
}

func clientReset(dir string, cfg *clientconfig.Config, args []string) int {
	if len(args) != 1 {
		printClientUsage(os.Stderr)
		return 2
	}
	provider := args[0]
	if code := requireConnected(cfg, provider); code != 0 {
		return code
	}

	cfg.ResetBinding(provider)
	if code := saveClientConfig(dir, cfg); code != 0 {
		return code
	}
	fmt.Printf("%s returned to the default: every known KB\n", provider)
	fmt.Println("run `cartographer sync` to apply")
	return 0
}

// parseBindArgs validates the "<provider> <kb>[,<kb>...]" shape shared by bind
// and unbind.
func parseBindArgs(cfg *clientconfig.Config, args []string) (provider string, kbs []string, code int) {
	if len(args) != 2 {
		printClientUsage(os.Stderr)
		return "", nil, 2
	}
	provider = args[0]
	if code := requireConnected(cfg, provider); code != 0 {
		return "", nil, code
	}
	for _, kb := range strings.Split(args[1], ",") {
		kb = strings.TrimSpace(kb)
		if kb == "" {
			fmt.Fprintf(os.Stderr, "Error: empty KB name in %q\n", args[1])
			return "", nil, 2
		}
		kbs = append(kbs, kb)
	}
	return provider, kbs, 0
}

// requireConnected rejects a provider that is unknown or not connected. A
// binding for a provider nobody connected would never be read, so recording it
// silently would be worse than refusing it.
func requireConnected(cfg *clientconfig.Config, provider string) int {
	if !isKnownProvider(provider) {
		fmt.Fprintf(os.Stderr, "Error: unknown provider %q (valid: %s)\n", provider, strings.Join(knownProviderNames(), ", "))
		return 2
	}
	if !cfg.HasAgent(provider) {
		fmt.Fprintf(os.Stderr, "Error: %s is not connected (connected: %s)\n", provider, formatKBList(cfg.Agents))
		return 2
	}
	return 0
}

func isKnownProvider(name string) bool {
	_, ok := configurator.Lookup(configurator.Provider(name))
	return ok
}

func knownProviderNames() []string {
	descriptors := configurator.Providers()
	names := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		names = append(names, string(d.Provider))
	}
	sort.Strings(names)
	return names
}

func saveClientConfig(dir string, cfg *clientconfig.Config) int {
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	return 0
}
