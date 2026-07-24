package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// reindexConfigPath is indirected for tests; normal use reads the local
// service's server config, which is the authority for local KB paths.
var reindexConfigPath = service.ConfigPath

type reindexTarget struct {
	Name string
	Path string
}

// cmdReindex reconciles every configured KB. A running server owns its SQLite
// connections, so the command calls its reindex MCP tool. Only when health is
// unavailable does it open the local index databases directly.
func cmdReindex(args []string) int {
	fs := flag.NewFlagSet("reindex", flag.ExitOnError)
	kbFlag := fs.String("kb", "", "Reindex only this configured KB name")
	fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer reindex [--kb <name>]")
		return 2
	}

	cfg := clientconfig.Default()
	if dir, err := clientconfig.TargetDir(); err == nil {
		if loaded, err := clientconfig.Load(dir); err == nil {
			cfg = loaded
		}
	}
	targetNames := kbTargets(cfg)
	if *kbFlag != "" {
		targetNames = []string{*kbFlag}
	}

	if _, err := fetchHealth(strings.TrimSuffix(cfg.ServerURL, "/mcp")); err == nil {
		for _, name := range targetNames {
			raw, err := client.New(cfg.ServerURL, resolveToken(cfg)).WithKB(name).Call("reindex", map[string]any{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "reindex %s: %v\n", displayKBName(name), err)
				return 1
			}
			fmt.Printf("%s: %s\n", displayKBName(name), strings.TrimSpace(string(raw)))
		}
		return 0
	}

	targets, err := localReindexTargets(*kbFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reindex:", err)
		return 1
	}
	for _, target := range targets {
		k, err := kb.Open(target.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reindex %s: open KB: %v\n", target.Name, err)
			return 1
		}
		ix, err := sqlindex.Open(filepath.Join(k.Root, ".cartographer", "index.db"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "reindex %s: open SQLite index: %v\n", target.Name, err)
			return 1
		}
		stats, reconcileErr := mcpserver.ReconcileIndex(k, nil, ix)
		closeErr := ix.Close()
		if reconcileErr != nil {
			fmt.Fprintf(os.Stderr, "reindex %s: %v\n", target.Name, reconcileErr)
			return 1
		}
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "reindex %s: close SQLite index: %v\n", target.Name, closeErr)
			return 1
		}
		fmt.Printf("%s: indexed=%d updated=%d removed=%d\n", target.Name, stats.Indexed, stats.Updated, stats.Removed)
	}
	return 0
}

func displayKBName(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// localReindexTargets reproduces the local mount resolution relevant to the
// administrative fallback. Remote specs point at their clone destination;
// auto-discovered data children are included after explicit specs.
func localReindexTargets(only string) ([]reindexTarget, error) {
	path, err := reindexConfigPath()
	if err != nil {
		return nil, fmt.Errorf("locate server config: %w", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("read server config %q: %w", path, err)
	}

	byName := make(map[string]reindexTarget)
	for _, spec := range cfg.KBs {
		name := resolveKBName(spec, spec.Path)
		localPath := spec.Path
		if spec.Remote != "" {
			if cfg.Data == "" {
				return nil, fmt.Errorf("KB %q is remote but server config has no data directory", name)
			}
			localPath = filepath.Join(cfg.Data, name)
		}
		if localPath != "" {
			byName[name] = reindexTarget{Name: name, Path: localPath}
		}
	}
	if cfg.Data != "" {
		paths, err := discoverKBPaths(cfg.Data)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			name := filepath.Base(path)
			if _, explicit := byName[name]; !explicit {
				byName[name] = reindexTarget{Name: name, Path: path}
			}
		}
	}
	if only != "" {
		target, ok := byName[only]
		if !ok {
			return nil, fmt.Errorf("configured KB %q not found", only)
		}
		return []reindexTarget{target}, nil
	}
	if len(byName) == 0 {
		return nil, fmt.Errorf("no local KBs configured")
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	targets := make([]reindexTarget, 0, len(names))
	for _, name := range names {
		targets = append(targets, byName[name])
	}
	return targets, nil
}
