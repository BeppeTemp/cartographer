package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/BeppeTemp/cartographer/internal/mcpbridge"
)

// The streams are variables so tests can drive the bridge over their own pipes.
var (
	mcpStdin  io.Reader = os.Stdin
	mcpStdout io.Writer = os.Stdout
)

const mcpUsage = `usage: cartographer mcp --provider <name> [--dir <path>]

Runs the local stdio MCP bridge a client starts for a provider with a workspace
binding (D737). It serves the KBs bound to the folder it was started in and
proxies the session to the server. Not meant to be run by hand.`

// cmdMCP is `cartographer mcp`: the stdio bridge. Everything on stdout is
// protocol; diagnostics go to stderr.
func cmdMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	provider := fs.String("provider", "", "provider whose bindings apply (required)")
	dir := fs.String("dir", "", "folder to resolve (default: the working directory)")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, mcpUsage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *provider == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if *dir == "" {
		// The process working directory, never $PWD: OpenCode starts its MCP
		// children with $PWD set to $HOME.
		if *dir, err = os.Getwd(); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
	}
	b := &mcpbridge.Bridge{
		Cfg: cfg, Provider: *provider, Dir: *dir,
		Token: resolveToken(cfg), Version: version,
		In: mcpStdin, Out: mcpStdout, Log: os.Stderr,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := b.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}
