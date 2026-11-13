package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/mcpprobe"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// Seams for the tests: the process environment and the network.
var (
	probeLookupEnv = os.LookupEnv
	probeDial      = mcpprobe.Dial
)

const mcpProbeUsage = `usage: cartographer mcp probe [--json] <kb>/<slug>
       cartographer mcp probe [--json] <slug>

Diagnoses one approved MCP descriptor without a client: the ${VAR} references
unset in this shell, and for an http descriptor whether DNS, TCP and TLS reach
the server and what a failure is. Values are never printed.

Exit codes:
  0  ok
  1  the server answered but is not usable
  2  usage error, descriptor not found, ambiguous or not approved
  3  unresolved ${VAR} (nothing was launched)
  4  network: DNS, connection refused or timeout
  5  TLS: unknown authority, expired or wrong name`

// cmdMCPProbe is `cartographer mcp probe`. The MCP session itself (initialize,
// tools/list) is not run yet: this part ends after the static checks.
func cmdMCPProbe(args []string) int {
	if hasHelpFlag(args) {
		fmt.Fprintln(os.Stdout, mcpProbeUsage)
		return 0
	}
	asJSON, target := false, ""
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
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
	// The session part (initialize, tools/list) is the next change; until then a
	// descriptor that passes the static checks reports exit 0.
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
	if r.Net != "" {
		fmt.Fprintf(w, "network: %s: %s\n", r.Net, escapeControl(r.Detail))
		if r.Hint != "" {
			fmt.Fprintf(w, "hint: %s\n", r.Hint)
		}
	} else if r.Transport == "http" {
		fmt.Fprintln(w, "network: reachable (DNS, TCP and TLS)")
	}
}
