package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// pluginVendor fills a pinned store: the directory a headless component (the
// server, the Surveyor) reads its plugins from, read-only. It fetches like
// install, validates every side, writes the tree under <dir>/<name>/<digest>/
// and prints the preview and the config lines that pin it. It writes neither
// `.cartographer.yaml` nor any approval and takes no client lock: the pin the
// operator copies into the component's config is the consent.
func pluginVendor(args []string) int {
	var to, only string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--to" || a == "--side":
			if i+1 >= len(args) {
				return pluginFail(2, "%s needs a value\n%s", a, pluginUsage)
			}
			i++
			if a == "--to" {
				to = args[i]
			} else {
				only = args[i]
			}
		default:
			rest = append(rest, a)
		}
	}
	pos, _, ok := pluginArgs(rest)
	if !ok || len(pos) != 1 || to == "" {
		return pluginFail(2, "%s", pluginUsage)
	}
	if only != "" && !slices.Contains(core.Sides(), core.Side(only)) {
		return pluginFail(2, "--side %q is not one of client, surveyor, server, atlas", only)
	}
	dir, err := filepath.Abs(to)
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	if cache, err := pluginStoreFn(); err == nil && within(dir, cache.Root) {
		return pluginFail(2, "--to %s is the client's own plugin cache: it is written under the client lock by `plugin install`, a vendored store has one writer. Choose another directory", escapeControl(dir))
	}
	src, err := plugin.ParseSource(pos[0])
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	// Every side is validated: the store holds the whole tree whichever side is shown.
	opts := pluginOpts()
	opts.Side = ""
	files, sha, p, probs, err := pluginReadWith(src, opts)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	if len(probs) > 0 {
		for _, pr := range probs {
			fmt.Fprintln(os.Stderr, escapeControl(pr))
		}
		return pluginFail(1, "%s is not a valid plugin; nothing was stored", pos[0])
	}
	sides := p.Sides
	if only != "" {
		if !slices.Contains(sides, core.Side(only)) {
			return pluginFail(1, "plugin %s has no %s side", escapeControl(p.Manifest.Name), only)
		}
		sides = []core.Side{core.Side(only)}
	}
	// One process writes a vendored store; the temp-dir-and-rename write is race safe.
	if err := (core.Store{Root: filepath.Join(dir)}).Put(p.Manifest.Name, files, p.Digest, opts); err != nil {
		return pluginFail(1, "%v", err)
	}
	name := p.Manifest.Name
	recorded := src.Remote
	if !src.Git {
		recorded = src.Path
	}
	for _, s := range sides {
		plugin.Preview(os.Stdout, p, plugin.PreviewInfo{Source: gitx.RedactRemoteURL(recorded), Ref: src.Ref, SHA: sha, Side: s}, opts)
		fmt.Println()
	}
	fmt.Printf("Stored %s in %s\ndigest %s\n", escapeControl(name), escapeControl(dir), p.Digest)
	for _, s := range sides {
		switch {
		case s == core.SideClient:
			fmt.Println("client side: approve it with `cartographer plugin install`, a pin does not apply to it")
			continue
		case p.Unsupported[s] != "":
			fmt.Printf("\n%s side: not pinnable, this build cannot load it: %s\n", s, escapeControl(p.Unsupported[s]))
			continue
		case s == core.SideAtlas && slices.Contains(sides, core.SideServer):
			fmt.Println("\natlas side: loaded by the server, pinned with the server side above")
			continue
		case s == core.SideAtlas:
			s = core.SideServer
			fmt.Println("\natlas side: loaded by the server from its plugins pin")
		}
		fmt.Printf("\n# pin the %s side in the %s config:\nplugins_dir: %s\nplugins:\n  %s:\n    digest: %s\n", s, s, quoteYAML(dir), name, p.Digest)
	}
	return 0
}

// quoteYAML quotes a path for the config lines printed above.
func quoteYAML(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(escapeControl(s)) + `"`
}

// within reports whether dir is root or lies inside it, comparing the real
// paths when they exist so a link cannot hide the cache.
func within(dir, root string) bool {
	real := func(p string) string {
		p = filepath.Clean(p)
		for q := p; ; q = filepath.Dir(q) {
			if r, err := filepath.EvalSymlinks(q); err == nil {
				rel, _ := filepath.Rel(q, p)
				return filepath.Join(r, rel)
			}
			if filepath.Dir(q) == q {
				return p
			}
		}
	}
	rel, err := filepath.Rel(real(root), real(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
