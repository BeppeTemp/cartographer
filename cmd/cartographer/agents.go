package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// cmdAgents lists the four supported agent providers: whether each is installed on
// this machine (internal/agents.Detect) and whether it is connected (listed in the
// machine-wide .cartographer.yaml, see clientconfig.TargetDir).
func cmdAgents(args []string) int {
	fs := flag.NewFlagSet("agents", flag.ExitOnError)
	fs.Parse(args)

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	connected := map[string]bool{}
	if cfg, err := clientconfig.Load(dir); err == nil {
		for _, a := range cfg.Agents {
			connected[a] = true
		}
	}

	detected := agents.Detect()

	fmt.Printf("%-10s %-10s %-10s %s\n", "PROVIDER", "INSTALLED", "CONNECTED", "EVIDENCE")
	for _, a := range detected {
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
