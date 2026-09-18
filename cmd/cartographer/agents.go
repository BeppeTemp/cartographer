package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// cmdAgents lists the four supported agent providers: whether each is installed on
// this machine (internal/agents.Detect) and whether it is connected (listed in the
// machine-wide .cartographer.yaml, see clientconfig.TargetDir).
func cmdAgents(args []string) int {
	output, remaining, err := outputFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(remaining) != 0 {
		fmt.Fprintln(os.Stderr, "Error: usage: cartographer agents [--output table|json]")
		return 2
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	cfg, _ := clientconfig.Load(dir)
	s := emptySnapshot()
	s.Providers = providerStatuses(cfg)
	if cfg != nil {
		s.ServerURL = cfg.ServerURL
		s.State = "configured"
	}
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(s)
		return 0
	}

	connected := map[string]bool{}
	if cfg != nil {
		for _, a := range cfg.Agents {
			connected[a] = true
		}
	}
	writeAgentsTable(os.Stdout, agents.Detect(), connected)
	return 0
}

// writeAgentsTable prints the agents table with the PROVIDER and DETECTION
// columns sized on the widest value they actually print, header included: a
// fixed width silently loses the alignment of every following column as soon as
// a value reaches it (#303). INSTALLED and CONNECTED hold the fixed yes/no
// vocabulary, so their widths are constant.
//
// DETECTION qualifies EVIDENCE rather than replacing it (#305): a home
// directory left behind by an uninstalled client reads as a config directory,
// not as proof that the client is there. Both columns collapse to their header
// width on the common machine, which is why neither is fixed at its longest
// possible value.
func writeAgentsTable(w io.Writer, detected []agents.Agent, connected map[string]bool) {
	provider, detectedBy := len("PROVIDER"), len("DETECTION")
	for _, a := range detected {
		if n := len(a.Provider); n > provider {
			provider = n
		}
		if n := len(a.DetectedBy); n > detectedBy {
			detectedBy = n
		}
	}
	fmt.Fprintf(w, "%-*s %-10s %-10s %-*s %s\n", provider, "PROVIDER", "INSTALLED", "CONNECTED", detectedBy, "DETECTION", "EVIDENCE")
	for _, a := range detected {
		fmt.Fprintf(w, "%-*s %-10s %-10s %-*s %s\n", provider, a.Provider, yesNo(a.Installed), yesNo(connected[string(a.Provider)]), detectedBy, dashIfEmpty(string(a.DetectedBy)), dashIfEmpty(a.Evidence))
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
