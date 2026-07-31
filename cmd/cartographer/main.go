package main

import (
	"fmt"
	"io"
	"log"
	"os"
)

// version is the build version, normally overridden at link time via
// `-ldflags "-X main.version=..."` (see Makefile/Dockerfile). "dev" is used
// for local `go build`/`go run` without ldflags.
var version = "dev"

// subcommand describes one entry of `cartographer help`.
type subcommand struct {
	group string
	name  string
	desc  string
}

// subcommands lists every subcommand for the usage output.
var subcommands = []subcommand{
	{"Get started", "help", "Show this help"},
	{"Get started", "version", "Print the build version"},
	{"Client", "agents", "List local agent clients"},
	{"Client", "connect", "Connect an agent client"},
	{"Client", "disconnect", "Disconnect an agent client"},
	{"Client", "status", "Show client sync status"},
	{"Client", "sync", "Synchronize agent clients"},
	{"Client", "approve", "Approve or revoke a third-party MCP descriptor"},
	{"Server", "serve", "Run the MCP server"},
	{"Server", "service", "Manage the native server service"},
	{"Server", "upgrade-repair", "Repair the native service and provider sync after an upgrade"},
	{"Knowledge base", "kb", "Create or mount local knowledge bases"},
	{"Knowledge base", "import", "Import an external markdown corpus"},
	{"Knowledge base", "resolve", "Resolve a configured path placeholder"},
	{"Diagnostics", "reindex", "Reconcile search indexes"},
	{"Diagnostics", "audit", "Verify or export the compliance audit log"},
}

// serveFn/versionFn/agentsFn/connectFn/disconnectFn/statusFn/syncFn are
// indirected through package-level vars so tests can stub them out and
// exercise run()'s dispatch logic without actually starting the server or
// hitting the network.
var (
	serveFn         = cmdServe
	versionFn       = cmdVersion
	agentsFn        = cmdAgents
	connectFn       = cmdConnect
	disconnectFn    = cmdDisconnect
	statusFn        = cmdStatus
	syncFn          = cmdSync
	approveFn       = cmdApprove
	serviceFn       = cmdService
	upgradeRepairFn = cmdUpgradeRepair
	runTUIFn        = runTUI
	importFn        = cmdImport
	resolveFn       = cmdResolve
	kbFn            = cmdKB
	reindexFn       = cmdReindex
	auditFn         = cmdAudit
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lshortfile)
	os.Exit(run(os.Args[1:]))
}

// run dispatches to the subcommand named by args[0] and returns the process
// exit code. Kept separate from main so the dispatch logic is testable
// without invoking os.Exit or starting the server.
func run(args []string) int {
	if len(args) < 1 {
		if !isInteractive() {
			printUsage(os.Stdout)
			return 0
		}
		return runTUIFn()
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "serve":
		return serveFn(rest)
	case "kb":
		return kbFn(rest)
	case "version", "--version":
		return versionFn()
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	case "agents":
		return agentsFn(rest)
	case "connect":
		return connectFn(rest)
	case "disconnect":
		return disconnectFn(rest)
	case "status":
		return statusFn(rest)
	case "sync":
		return syncFn(rest)
	case "approve":
		return approveFn(rest)
	case "service":
		return serviceFn(rest)
	case "upgrade-repair":
		return upgradeRepairFn(rest)
	case "import":
		return importFn(rest)
	case "resolve":
		return resolveFn(rest)
	case "audit":
		return auditFn(rest)
	case "reindex":
		return reindexFn(rest)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q", cmd)
		if suggestion := commandSuggestion(cmd); suggestion != "" {
			fmt.Fprintf(os.Stderr, "; did you mean %q", suggestion)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr)
		printUsage(os.Stderr)
		return 2
	}
}

func cmdVersion() int {
	fmt.Println(version)
	return 0
}

func cmdNotImplemented(name string) int {
	fmt.Fprintf(os.Stderr, "%s: not yet implemented\n", name)
	return 1
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "cartographer — MCP server for the Agentic Wiki")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  cartographer <command> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Get started:")
	fmt.Fprintf(w, "  %-10s %s\n", "(none)", "Launch interactive dashboard (TTY only)")
	group := ""
	for _, sc := range subcommands {
		if sc.group != group {
			if group != "" {
				fmt.Fprintln(w)
			}
			if sc.group != "Get started" {
				fmt.Fprintf(w, "%s:\n", sc.group)
			}
			group = sc.group
		}
		fmt.Fprintf(w, "  %-10s %s\n", sc.name, sc.desc)
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run 'cartographer <command> -h' for flags of a specific command.")
}

// commandSuggestion returns the only close command. A distance of two keeps
// corrections useful ("agent" → "agents") without guessing unrelated input.
func commandSuggestion(input string) string {
	var match string
	for _, sc := range subcommands {
		if editDistance(input, sc.name) <= 2 {
			if match != "" {
				return ""
			}
			match = sc.name
		}
	}
	return match
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ra := range a {
		cur := make([]int, len(b)+1)
		cur[0] = i + 1
		for j, rb := range b {
			cost := 0
			if ra != rb {
				cost = 1
			}
			cur[j+1] = minInt(prev[j+1]+1, cur[j]+1, prev[j]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func minInt(values ...int) int {
	n := values[0]
	for _, v := range values[1:] {
		if v < n {
			n = v
		}
	}
	return n
}

// envFallback returns val if non-empty, else os.Getenv(envKey).
func envFallback(val, envKey string) string {
	if val != "" {
		return val
	}
	return os.Getenv(envKey)
}
