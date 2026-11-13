package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/mcpexec"
	"github.com/BeppeTemp/cartographer/internal/mcpprobe"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// Seams for the tests: the process environment and the network.
var (
	probeLookupEnv = os.LookupEnv
	probeDial      = mcpprobe.Dial
	// probeResolve is the launch path of `mcp exec`: the descriptor read from the
	// server, checked against the approval, its variables and secrets expanded.
	probeResolve = func(ctx context.Context, ref string) (*mcpexec.Launch, error) {
		deps, err := mcpExecDeps()
		if err != nil {
			return nil, err
		}
		return mcpexec.Resolve(ctx, deps, ref)
	}
	probeSession = mcpprobe.Run
)

const mcpProbeUsage = `usage: cartographer mcp probe [--json] [--timeout <duration>] <kb>/<slug>
       cartographer mcp probe [--json] [--timeout <duration>] <slug>

Diagnoses one approved MCP descriptor without a client: the ${VAR} references
unset in this shell, and for an http descriptor whether DNS, TCP and TLS reach
the server and what a failure is. When those pass it starts the descriptor the
way a client would (resolving ${VAR} and ${secret:...} as mcp exec does), runs
initialize and tools/list and prints the server and the tools. --timeout bounds
the session (default 20s). Values are never printed.

Exit codes:
  0  ok
  1  the server answered but is not usable
  2  usage error, descriptor not found, ambiguous or not approved
  3  unresolved ${VAR} (nothing was launched)
  4  network: DNS, connection refused or timeout
  5  TLS: unknown authority, expired or wrong name`

// cmdMCPProbe is `cartographer mcp probe`: the static checks, then, when nothing
// above stopped it, an MCP session (initialize, tools/list) started through the
// same resolution `mcp exec` uses.
func cmdMCPProbe(args []string) int {
	if hasHelpFlag(args) {
		fmt.Fprintln(os.Stdout, mcpProbeUsage)
		return 0
	}
	asJSON, target := false, ""
	timeout := mcpprobe.SessionTimeout
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "--timeout" && i+1 < len(args):
			d, err := time.ParseDuration(args[i+1])
			if err != nil || d <= 0 {
				fmt.Fprintln(os.Stderr, mcpProbeUsage)
				return mcpprobe.ExitUsage
			}
			timeout = d
			i++
		case strings.HasPrefix(a, "-") || target != "":
			fmt.Fprintln(os.Stderr, mcpProbeUsage)
			return mcpprobe.ExitUsage
		default:
			target = a
		}
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, mcpProbeUsage)
		return mcpprobe.ExitUsage
	}
	kbName, slug := "", target
	if i := strings.Index(target, "/"); i >= 0 {
		kbName, slug = target[:i], target[i+1:]
	}
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return mcpprobe.ExitUsage
	}
	m, err := approveManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return mcpprobe.ExitUsage
	}
	var matches []contract.Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" && !a.BuiltIn && a.Name == slug && (kbName == "" || a.Source == "kb:"+kbName) {
			matches = append(matches, a)
		}
	}
	switch {
	case len(matches) == 0:
		fmt.Fprintf(os.Stderr, "Error: no MCP descriptor %s is advertised by the server\n", escapeControl(target))
		return mcpprobe.ExitUsage
	case len(matches) > 1:
		var names []string
		for _, a := range matches {
			names = append(names, escapeControl(strings.TrimPrefix(a.Source, "kb:"))+"/"+escapeControl(a.Name))
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "Error: %s is ambiguous: %s\n", escapeControl(target), strings.Join(names, ", "))
		return mcpprobe.ExitUsage
	}
	a := matches[0]
	kb := strings.TrimPrefix(a.Source, "kb:")
	if cfg.ApprovedMCPHashes()[a.Source+"\x00"+provisioning.ApprovalName(a)] != a.ContentHash {
		fmt.Fprintf(os.Stderr, "Error: %s/%s is not approved: run cartographer approve mcp %s --kb %s\n", escapeControl(kb), escapeControl(a.Name), escapeControl(a.Name), escapeControl(kb))
		return mcpprobe.ExitUsage
	}
	files, err := contract.ReadArtifactFiles(a, nil, nil)
	if err != nil || len(files) != 1 {
		fmt.Fprintln(os.Stderr, "Error: invalid MCP descriptor")
		return mcpprobe.ExitUsage
	}
	spec, err := contract.ParseMCPServerSpec(a.Name, files[0].Content)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return mcpprobe.ExitUsage
	}
	dest, err := provisioning.MCPDescriptorTarget(spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return mcpprobe.ExitUsage
	}
	res := mcpprobe.Result{Target: dest, Transport: string(spec.Type)}
	res.Unresolved = mcpprobe.Unresolved(contract.MCPEnvRefs(spec), probeLookupEnv)
	if spec.Type == "http" {
		if derr := probeDial(context.Background(), spec.URL, 0); derr != nil {
			res.Net = mcpprobe.Classify(derr)
			res.Detail = derr.Error()
			res.Hint = mcpprobe.Hint(res.Net)
		}
	}
	if len(res.Unresolved) == 0 && res.Net == "" {
		probeRunSession(&res, kb, a.Name, timeout)
	}
	if spec.Requires != "" && (res.Net == mcpprobe.KindDNS || res.Net == mcpprobe.KindRefused || res.Net == mcpprobe.KindTimeout) {
		res.Needs = spec.Requires
	}
	code := mcpprobe.ExitCode(res)
	if asJSON {
		out, _ := json.Marshal(res)
		fmt.Println(string(out))
	} else {
		printProbe(os.Stdout, kb, a.Name, res)
	}
	return code
}

// printProbe writes one line per finding. Everything the server chose goes
// through escapeControl; no variable value is ever read here.
func printProbe(w io.Writer, kb, name string, r mcpprobe.Result) {
	fmt.Fprintf(w, "probe %s/%s\ntransport: %s\ntarget: %s\n", escapeControl(kb), escapeControl(name), escapeControl(r.Transport), escapeControl(r.Target))
	if len(r.Unresolved) > 0 {
		fmt.Fprintf(w, "unresolved: %s (unset in this shell; a client started from another shell or the dock may differ)\n", escapeControl(strings.Join(r.Unresolved, ", ")))
	}
	if r.Needs != "" {
		fmt.Fprintf(w, "needs %s: %s did not answer (%s)\n", escapeControl(r.Needs), escapeControl(hostOf(r.Target)), r.Net)
	}
	if r.Net != "" {
		fmt.Fprintf(w, "network: %s: %s\n", r.Net, escapeControl(r.Detail))
		if r.Hint != "" {
			fmt.Fprintf(w, "hint: %s\n", r.Hint)
		}
	} else if r.Transport == "http" {
		fmt.Fprintln(w, "network: reachable (DNS, TCP and TLS)")
	}
	if r.Via != "" {
		fmt.Fprintf(w, "via: %s\n", escapeControl(r.Via))
	}
	if r.Error != "" {
		fmt.Fprintf(w, "session: failed: %s\n", escapeControl(r.Error))
	} else if r.Protocol != "" {
		fmt.Fprintf(w, "session: ok, protocol %s, server %s\ntools: %d\n", escapeControl(r.Protocol), escapeControl(r.Server), r.Tools)
		for _, n := range r.ToolNames {
			fmt.Fprintf(w, "  %s\n", escapeControl(n))
		}
	}
}

// probeRunSession resolves the descriptor as mcp exec does and runs one MCP
// session with it, filling r. A fault that is a network or TLS class is
// reported as such; anything else is a server that is not usable (exit 1).
func probeRunSession(r *mcpprobe.Result, kb, slug string, timeout time.Duration) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	l, err := probeResolve(ctx, kb+"/"+slug)
	if err != nil {
		r.Error = err.Error()
		return
	}
	t := mcpprobe.Target{Command: l.Command, Args: l.Args, Env: l.Env, URL: l.URL, Headers: l.Headers}
	r.Via = "cartographer mcp exec resolution (in process)"
	// Scrub every value the launch resolved: each substituted piece alone (the
	// bare token of "Bearer <token>" shows up in a child's stderr or a response
	// body) and the composed env and header values.
	secrets := l.Pieces()
	for k := range l.Spec.Env {
		for _, e := range l.Env {
			if n, v, ok := strings.Cut(e, "="); ok && n == k {
				secrets = append(secrets, v)
			}
		}
	}
	for k, v := range l.Headers {
		if strings.Contains(l.Spec.Headers[k], "${") {
			secrets = append(secrets, v)
		}
	}
	scrub := mcpprobe.NewScrub(secrets)
	info, err := probeSession(ctx, t, timeout, scrub)
	if err != nil {
		if k := mcpprobe.SessionKind(err); k == mcpprobe.KindDNS || k == mcpprobe.KindRefused || k == mcpprobe.KindTimeout || k == mcpprobe.KindTLSUnknownAuthority || k == mcpprobe.KindTLSOther {
			r.Net, r.Hint = k, mcpprobe.Hint(k)
			r.Detail = err.Error()
			return
		}
		r.Error = err.Error()
		return
	}
	r.Protocol, r.Server, r.Tools = info.Protocol, info.Server, len(info.Tools)
	if info.Version != "" {
		r.Server += " " + info.Version
	}
	r.ToolNames = info.Tools[:min(len(info.Tools), mcpprobe.MaxToolNames)]
}

// hostOf is the host of an approved endpoint, for a line that names it.
func hostOf(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return u.Host
	}
	return target
}
