package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// runBare is bare `cartographer` on a terminal: a short status of this
// machine, a blank line, then the usage. It is a help screen, not a check, so
// it always returns 0 (`cartographer status` is the command with meaningful
// exit codes), and it never writes to disk: the one remote read is the one
// `status` makes, bounded by statusHealthTimeout.
func runBare() int {
	printBareStatus(os.Stdout)
	fmt.Fprintln(os.Stdout)
	printUsage(os.Stdout)
	return 0
}

// printBareStatus writes the short status: the server, the next step when
// there is one, and one line per connected provider. Per-artifact lists,
// kinds, bindings and the plugin table are `cartographer status`.
func printBareStatus(w io.Writer) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintf(w, "client config cannot be located: %v\n", err)
		return
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(w, "next: %s\n", setupHintText)
			return
		}
		fmt.Fprintf(w, "client config cannot be read (fix or remove %s): %v\n", clientconfig.Path(dir), err)
		return
	}
	if len(cfg.Agents) == 0 {
		fmt.Fprintln(w, "no agent connected (run `cartographer connect`)")
		empty := emptySnapshot()
		empty.Plugins = snapshotPlugins(cfg)
		printPluginSection(w, empty)
		return
	}
	s := snapshotForConfig(dir, cfg, true)
	printServerLines(w, w, s)
	if hint := setupHint(&s); hint != "" {
		fmt.Fprintf(w, "next: %s\n", hint)
	}
	for _, p := range s.Providers {
		if !p.Connected {
			continue
		}
		line := fmt.Sprintf("  [%s] %s", escapeControl(p.Name), escapeControl(strings.ReplaceAll(p.State, "_", "-")))
		if p.State == "drift" {
			line += " - run `cartographer sync`"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, "details: cartographer status | cartographer doctor")
}
