package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// version is the build version, normally overridden at link time via
// `-ldflags "-X main.version=..."` (see Makefile/Dockerfile). "dev" is used
// for local `go build`/`go run` without ldflags.
var version = "dev"

// subcommand describes one entry of `cartographer help`.
type subcommand struct {
	name string
	desc string
}

// subcommands lists every subcommand for the usage output.
var subcommands = []subcommand{
	{"serve", "Run the MCP server (stdio or HTTP)"},
	{"kb", "Create and manage local KBs (kb create <name>)"},
	{"version", "Print the build version"},
	{"help", "Show this help message"},
	{"agents", "List detected/connected agent clients on this machine"},
	{"connect", "Connect this machine to a cartographer server"},
	{"disconnect", "Disconnect this machine from a cartographer server"},
	{"status", "Show sync status against the configured server"},
	{"sync", "Synchronize local agent clients with the configured server"},
	{"service", "Manage the MCP server as a native service (install|uninstall|start|stop|restart|status)"},
	{"import", "Mechanically import an external markdown corpus into a KB (D74 scaffold)"},
	{"resolve", "Resolve a {{repo:<key>}}/{{path:<name>}} placeholder to a local path (D75)"},
}

// serveFn/versionFn/agentsFn/connectFn/disconnectFn/statusFn/syncFn are
// indirected through package-level vars so tests can stub them out and
// exercise run()'s dispatch logic without actually starting the server or
// hitting the network.
var (
	serveFn      = cmdServe
	versionFn    = cmdVersion
	agentsFn     = cmdAgents
	connectFn    = cmdConnect
	disconnectFn = cmdDisconnect
	statusFn     = cmdStatus
	syncFn       = cmdSync
	serviceFn    = cmdService
	runTUIFn     = runTUI
	importFn     = cmdImport
	resolveFn    = cmdResolve
	kbFn         = cmdKB
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
	case "version":
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
	case "service":
		return serviceFn(rest)
	case "import":
		return importFn(rest)
	case "resolve":
		return resolveFn(rest)
	default:
		if strings.HasPrefix(cmd, "-") {
			fmt.Fprintf(os.Stderr, "Error: flags are not accepted at the root level; did you mean %q?\n\n", "cartographer serve ...")
		} else {
			fmt.Fprintf(os.Stderr, "Error: unknown command %q; did you mean %q?\n\n", cmd, "cartographer serve")
		}
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
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintf(w, "  %-10s %s\n", "(none)", "Launch interactive dashboard (TTY only)")
	for _, sc := range subcommands {
		fmt.Fprintf(w, "  %-10s %s\n", sc.name, sc.desc)
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run 'cartographer <command> -h' for flags of a specific command.")
}

// envFallback returns val if non-empty, else os.Getenv(envKey).
func envFallback(val, envKey string) string {
	if val != "" {
		return val
	}
	return os.Getenv(envKey)
}
