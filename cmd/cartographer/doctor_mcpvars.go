package main

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpprobe"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// doctorLookupEnv is the environment doctor checks descriptors against; a test
// replaces it.
var (
	doctorLookupEnv = os.LookupEnv
	doctorDial      = mcpprobe.Dial
)

// checkMCPVariables is the always-on, cheap half of `mcp probe`: an approved MCP
// descriptor whose ${VAR} references are unset in this environment starts in a
// client as "Connection closed" with no explanation. One warning per descriptor,
// names only; a ${secret:...} form is the launcher's and never listed. Nothing
// is reported when the server cannot be asked for the descriptors (checkServerAt
// already says it is down) or when nothing is approved, so no fetch is made then.
func checkMCPVariables(cfg *clientconfig.Config, fetch func(*clientconfig.Config) (contract.Manifest, error)) []doctorFinding {
	type needing struct{ ref, label, url string }
	var needs []needing
	approved := cfg.ApprovedMCPHashes()
	if len(approved) == 0 {
		return nil
	}
	m, err := fetch(cfg)
	if err != nil {
		return nil
	}
	var out []doctorFinding
	for _, a := range m.Artifacts {
		if a.Kind != "mcp" || a.BuiltIn || approved[a.Source+"\x00"+provisioning.ApprovalName(a)] != a.ContentHash {
			continue
		}
		files, err := contract.ReadArtifactFiles(a, nil, nil)
		if err != nil || len(files) != 1 {
			continue
		}
		spec, err := contract.ParseMCPServerSpec(a.Name, files[0].Content)
		if err != nil {
			continue
		}
		if spec.Requires != "" {
			needs = append(needs, needing{escapeControl(strings.TrimPrefix(a.Source, "kb:")) + "/" + escapeControl(a.Name), escapeControl(spec.Requires), spec.URL})
		}
		missing := mcpprobe.Unresolved(contract.MCPEnvRefs(spec), doctorLookupEnv)
		if len(missing) == 0 {
			continue
		}
		sort.Strings(missing)
		ref := escapeControl(strings.TrimPrefix(a.Source, "kb:")) + "/" + escapeControl(a.Name)
		refs := make([]string, len(missing))
		for i, n := range missing {
			refs[i] = "${" + escapeControl(n) + "}"
		}
		out = append(out, doctorFinding{
			Check: "mcp-variables", Severity: doctorWarning,
			Message: "mcp " + ref + ": " + strings.Join(refs, ", ") + " not set in this environment; a client started from a shell that has them will work, others show \"Connection closed\"",
			Fix:     "cartographer mcp probe " + ref,
		})
	}
	// A descriptor that declares the network it needs gets a bounded dial, all in
	// parallel: being off the VPN is a state, not a fault, so it is an info.
	if len(needs) > 0 {
		urls := make([]string, len(needs))
		for i, n := range needs {
			urls[i] = n.url
		}
		for i, blocked := range mcpprobe.BlockedAll(context.Background(), urls, mcpprobe.ReachTimeout, doctorDial) {
			if blocked {
				out = append(out, doctorFinding{Check: "mcp-network", Severity: doctorInfo,
					Message: "mcp " + needs[i].ref + " needs " + needs[i].label + ": unreachable now",
					Fix:     "cartographer mcp probe " + needs[i].ref})
			}
		}
	}
	return out
}
