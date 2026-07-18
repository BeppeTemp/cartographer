package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// cmdResolve resolves a single {{repo:<key>}}/{{path:<name>}} placeholder
// (D75 WP5) and prints the local path it resolves to. It is the runtime
// fallback for an agent that meets an unresolved placeholder in a concept
// (see the "Local paths" table's own pointer to this command, D75 WP4) and a
// standalone debug tool — the binary is already present on every connected
// machine, so this never depends on the server being reachable.
//
// Exit codes: 0 resolved (path printed on stdout), 1 resolution error
// (ambiguous/not found), 2 usage error (bad argument, no client config).
func cmdResolve(args []string) int {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	fs.Parse(args)

	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer resolve repo:<key>|path:<name>")
		return 2
	}

	kind, key, ok := strings.Cut(rest[0], ":")
	if !ok || key == "" || (kind != "repo" && kind != "path") {
		fmt.Fprintf(os.Stderr, "Error: expected repo:<key> or path:<name>, got %q\n", rest[0])
		return 2
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		// No .cartographer.yaml written yet (`connect` never run): the defaults
		// (search_roots: ~/Documents, paths: empty) remain a reasonable base
		// for resolving — resolve must not depend on connect.
		cfg = clientconfig.Default()
	}

	var resolved string
	switch kind {
	case "repo":
		var warnings []string
		resolved, warnings, err = repoindex.Resolve(key, cfg.Paths, cfg.SearchRoots)
		for _, w := range warnings {
			fmt.Fprintln(os.Stderr, w)
		}
	case "path":
		p, found := cfg.Paths[key]
		if !found {
			err = fmt.Errorf("no entry %q in paths: (.cartographer.yaml)", key)
		} else {
			resolved = resolveExpandHome(p)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	fmt.Println(resolved)
	return 0
}

// resolveExpandHome expands a leading "~" to the user's home directory —
// mirrors repoindex's/provisioning's unexported equivalents, duplicated here
// (a few lines) rather than exported purely for this one cross-package call.
func resolveExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
