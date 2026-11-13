package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpexec"
)

const mcpExecUsage = `usage: cartographer mcp exec <kb>/<slug>

Starts the MCP server the approved descriptor mcp/<slug>.json of <kb> declares
(an http one is bridged from stdio, its headers sent to the server), with its ${VAR} and ${secret:...} references resolved into the child's
environment (stdio) only. Every start reads the secrets from the server and
nothing is cached. Written into a client's MCP configuration by sync, not meant
to be run by hand. stdout belongs to the server; diagnostics go to stderr.`

// mcpExecDeps and mcpExecRun are seams for tests: the production Deps and the
// call that replaces the process.
var (
	mcpExecDeps = productionMCPExecDeps
	mcpExecRun  = func(l *mcpexec.Launch) (int, error) { return l.Exec() }
	// mcpExecBridge serves an http descriptor over this process's stdio.
	mcpExecBridge = func(ctx context.Context, l *mcpexec.Launch) error {
		return l.Bridge(ctx, os.Stdin, os.Stdout, os.Stderr)
	}
)

// cmdMCPExec is `cartographer mcp exec <kb>/<slug>`.
func cmdMCPExec(args []string) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, mcpExecUsage)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, mcpExecUsage)
		return 2
	}
	if _, _, err := mcpexec.ParseRef(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	deps, err := mcpExecDeps()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", escapeText(err.Error()))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	l, err := mcpexec.Resolve(ctx, deps, args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if l.Spec.Type == "http" {
		// stdin and stdout are the client's protocol; the bridge keeps them.
		if err := mcpExecBridge(ctx, l); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", escapeText(err.Error()))
			return 1
		}
		return 0
	}
	stop()
	code, err := mcpExecRun(l)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot start %s: %s\n", escapeControl(l.Command), escapeText(err.Error()))
		return 1
	}
	return code
}

// cliMCPExecDeps is mcpexec.Deps over the routed endpoint with the user's token.
type cliMCPExecDeps struct {
	c   *client.MCPClient
	cfg *clientconfig.Config
}

func productionMCPExecDeps() (mcpexec.Deps, error) {
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		return nil, err
	}
	if err := client.CheckServerURL(cfg.ServerURL, cfg.InsecureHTTPAllowed()); err != nil {
		return nil, err
	}
	if cfg.ServerRoutedPath == "" {
		return nil, errors.New("the server has no routed endpoint recorded; run `cartographer sync`")
	}
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return nil, err
	}
	u.Path = cfg.ServerRoutedPath
	c := client.New(u.String(), resolveToken(cfg)).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnvName(cfg))
	return cliMCPExecDeps{c: c, cfg: cfg}, nil
}

// Call returns the first text block: the server may append a notice block, and
// the tool's answer is always the first.
func (d cliMCPExecDeps) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	raw, err := d.c.CallContext(ctx, tool, args)
	if err != nil {
		var re *client.RemoteError
		if errors.As(err, &re) && re.State == client.RemoteUnavailable && re.Code != client.CodeUnauthorized {
			host := d.cfg.ServerURL
			if u, perr := url.Parse(host); perr == nil {
				host = u.Host
			}
			return "", fmt.Errorf("cannot reach the server at %s: %v", host, err)
		}
		return "", err
	}
	// CallContext joins several text blocks as a JSON array of strings; one
	// block is the text itself.
	var blocks []string
	if json.Unmarshal(raw, &blocks) == nil && len(blocks) > 0 {
		return blocks[0], nil
	}
	return string(raw), nil
}

func (d cliMCPExecDeps) Approved(kb, slug string) (string, bool) {
	a, ok := d.cfg.MCPApprovals[kb][slug]
	return a.ContentHash, ok
}

func (cliMCPExecDeps) Environ() []string { return os.Environ() }
