package main

import (
	"encoding/json"
	"fmt"
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

	fmt.Printf("%-10s %-10s %-10s %s\n", "PROVIDER", "INSTALLED", "CONNECTED", "EVIDENCE")
	connected := map[string]bool{}
	if cfg != nil {
		for _, a := range cfg.Agents {
			connected[a] = true
		}
	}
	for _, a := range agents.Detect() {
		fmt.Printf("%-10s %-10s %-10s %s\n", a.Provider, yesNo(a.Installed), yesNo(connected[string(a.Provider)]), dashIfEmpty(a.Evidence))
	}
	return 0
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
