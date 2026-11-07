package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// registerAllowedSymlinks fills the safewrite allow set (D148 refined) with the
// union of every provider's providers.<provider>.allow_symlinks, read from the
// user's own client file in the home directory and from nowhere else: a
// .cartographer.yaml in a repository or the working directory must never decide
// which links are followed. An unreadable config registers nothing (fail closed).
func registerAllowedSymlinks() {
	// The light set registerPluginProviders filled is dropped first: a config
	// Load refuses registers nothing.
	safewrite.ResetAllowedLinks()
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return
	}
	var paths []string
	for _, b := range cfg.Clients {
		for _, e := range b.AllowSymlinks {
			paths = append(paths, clientconfig.ExpandSymlinkPath(dir, e))
		}
	}
	safewrite.SetAllowedLinks(paths)
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// clientSymlinks lists, adds or removes a provider's allow_symlinks entries.
func clientSymlinks(dir string, cfg *clientconfig.Config, args []string) int {
	fs := flag.NewFlagSet("client symlinks", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var add, remove multiFlag
	fs.Var(&add, "add", "Link to follow (repeatable)")
	fs.Var(&remove, "remove", "Entry to stop following (repeatable)")
	// The provider comes first, flags after it.
	if len(args) == 0 {
		printClientUsage(os.Stderr)
		return 2
	}
	provider := args[0]
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		printClientUsage(os.Stderr)
		return 2
	}
	if !isKnownProvider(provider) {
		fmt.Fprintf(os.Stderr, "Error: unknown provider %q (known: %s)\n", provider, strings.Join(knownProviderNames(), ", "))
		return 2
	}
	if len(add) == 0 && len(remove) == 0 {
		entries := cfg.AllowedSymlinks(provider)
		if len(entries) == 0 {
			fmt.Printf("%s: no symlink is followed\n", provider)
		}
		for _, e := range entries {
			p := clientconfig.ExpandSymlinkPath(dir, e)
			if t, err := os.Readlink(p); err == nil {
				fmt.Printf("%q -> %q\n", e, t)
			} else {
				fmt.Printf("%q (not a link)\n", e)
			}
		}
		return 0
	}
	for _, e := range add {
		if !clientconfig.ValidSymlinkEntry(dir, e) {
			fmt.Fprintf(os.Stderr, "Error: %q must be an absolute path, or start with ~/ or $HOME/, hold no control character and not be the home directory or /\n", e)
			return 2
		}
	}
	for _, e := range add {
		p := clientconfig.ExpandSymlinkPath(dir, e)
		if fi, err := os.Lstat(p); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %q does not exist yet\n", p)
		} else if fi.Mode()&os.ModeSymlink == 0 {
			fmt.Fprintf(os.Stderr, "warning: %q is not a symlink\n", p)
		}
		cfg.SetAllowSymlink(provider, e)
	}
	missing := false
	for _, e := range remove {
		if !cfg.UnsetAllowSymlink(provider, e) {
			fmt.Fprintf(os.Stderr, "Error: %q is not listed under providers.%s.allow_symlinks\n", e, provider)
			missing = true
		}
	}
	if missing {
		return 1
	}
	if code := saveClientConfig(dir, cfg); code != 0 {
		return code
	}
	fmt.Println("run cartographer sync to apply")
	return 0
}
