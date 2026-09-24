package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// `cartographer paths` (D262): the placeholder keys the last sync met, and the
// one place to record where they live on this machine. Before it, mapping a
// key meant knowing that `.cartographer.yaml` had a `paths:` section, which
// nothing ever mentioned, so every {{path:…}} in a KB stayed unresolved.

// placeholderRow is one key as the lockfile records it, merged across every
// projection (provider and workspace) that met it.
type placeholderRow struct {
	Key    string   `json:"key"`
	KBs    []string `json:"kbs,omitempty"`
	Path   string   `json:"path,omitempty"`
	Reason string   `json:"reason,omitempty"`
	// Description and Default are what the citing KB's paths.yaml declares
	// for the key (D263), empty when no bound KB declares it.
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

// Resolved reports whether some projection resolved the key.
func (r placeholderRow) Resolved() bool { return r.Path != "" }

// placeholderRows collects every key the lockfile records, sorted by key. A
// key resolved by one projection and not another (a config change between two
// partial syncs) is reported resolved: the path is what the operator needs to
// see, and the next sync makes them agree.
func placeholderRows(lf provisioning.LockFile) []placeholderRow {
	byKey := map[string]*placeholderRow{}
	row := func(id string) *placeholderRow {
		r, ok := byKey[id]
		if !ok {
			r = &placeholderRow{Key: id}
			byKey[id] = r
		}
		return r
	}
	visit := func(l provisioning.Lock) {
		for id, kbs := range l.PlaceholderSources {
			r := row(id)
			for _, kb := range kbs {
				if !containsString(r.KBs, kb) {
					r.KBs = append(r.KBs, kb)
				}
			}
		}
		for id, p := range l.ResolvedPlaceholders {
			row(id).Path = p
		}
		for id, reason := range l.UnresolvedPlaceholders {
			if r := row(id); r.Path == "" {
				r.Reason = reason
			}
		}
		for id, d := range l.PlaceholderDecls {
			if r := row(id); r.Description == "" {
				r.Description, r.Default = d.Description, d.Default
			}
		}
	}
	for _, l := range lf.Providers {
		visit(l)
	}
	for _, ws := range lf.Workspaces {
		for _, l := range ws.Providers {
			visit(l)
		}
	}
	out := make([]placeholderRow, 0, len(byKey))
	for _, r := range byKey {
		if r.Resolved() {
			r.Reason = ""
		}
		sort.Strings(r.KBs)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// unresolvedRowsOf is unresolvedRows over the new locks of one
// materialization pass, before they are persisted (connect's placeholder
// step reads them straight from the results).
func unresolvedRowsOf(results map[string]provisioning.AppliedResult) []placeholderRow {
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{}}
	for key, r := range results {
		lf.Providers[key] = r.NewLock
	}
	return unresolvedRows(lf)
}

// unresolvedRows is placeholderRows narrowed to the keys nothing resolved.
func unresolvedRows(lf provisioning.LockFile) []placeholderRow {
	var out []placeholderRow
	for _, r := range placeholderRows(lf) {
		if !r.Resolved() && r.Reason != "" {
			out = append(out, r)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pathsConfigKey turns what the operator typed into the key `paths:` stores:
// the "repo:"/"path:" prefix is accepted and dropped, because Resolve looks a
// key up without it — for both kinds (repoindex consults the manual map first
// for a repo key too). kind is "repo", "path" or "" when no prefix was given.
func pathsConfigKey(arg string) (key, kind string) {
	for _, k := range []string{"repo", "path"} {
		if rest, ok := strings.CutPrefix(arg, k+":"); ok {
			return rest, k
		}
	}
	return arg, ""
}

func cmdPaths(args []string) int {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		return cmdPathsList(args)
	case "set":
		return cmdPathsSet(args)
	case "unset":
		return cmdPathsUnset(args)
	default:
		fmt.Fprintln(os.Stderr, "Usage: cartographer paths [list [--json]] | set <key> <path> | unset <key>")
		return 2
	}
}

// cmdPathsList prints every key the lockfile records — no network call: it
// shows what the last sync found, which is what `status` summarizes.
func cmdPathsList(args []string) int {
	fs := flag.NewFlagSet("paths list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "Print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer paths list [--json]")
		return 2
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	rows := placeholderRows(lf)

	if *asJSON {
		out, _ := json.MarshalIndent(struct {
			Placeholders []placeholderRow `json:"placeholders"`
		}{rows}, "", "  ")
		fmt.Println(string(out))
		return 0
	}
	if len(rows) == 0 {
		fmt.Println("no placeholder recorded — run `cartographer sync` first, or the connected KBs cite none")
		return 0
	}
	unresolved := 0
	for _, r := range rows {
		kbs := strings.Join(r.KBs, ",")
		if kbs == "" {
			kbs = "-"
		}
		if r.Resolved() {
			fmt.Printf("%s\t%s\t%s\n", r.Key, kbs, r.Path)
		} else {
			unresolved++
			fmt.Printf("%s\t%s\tUNRESOLVED: %s\n", r.Key, kbs, r.Reason)
		}
		// What the KB says the key points at (D263), on its own line so the
		// tab-separated first line stays what scripts already read.
		if r.Description != "" {
			line := "\t" + r.Description
			if r.Default != "" {
				line += " (default " + r.Default + ")"
			}
			fmt.Println(line)
		}
	}
	if unresolved > 0 {
		fmt.Printf("\n%d unresolved — record each with `cartographer paths set <key> <path>`, then run `cartographer sync`\n", unresolved)
	}
	return 0
}

func cmdPathsSet(args []string) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer paths set <key> <path>")
		return 2
	}
	key, kind := pathsConfigKey(args[0])
	if key == "" {
		fmt.Fprintln(os.Stderr, "Error: empty key")
		return 2
	}
	value := args[1]
	dir, cfg, code := loadConfigForPaths()
	if code != 0 {
		return code
	}
	if kind == "" {
		kind = recordedKind(dir, key)
	}
	for _, w := range pathWarnings(kind, value) {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if cfg.Paths == nil {
		cfg.Paths = map[string]string{}
	}
	cfg.Paths[key] = value
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Printf("paths: %s -> %s\n", key, value)
	fmt.Println("apply it with: cartographer sync")
	return 0
}

func cmdPathsUnset(args []string) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer paths unset <key>")
		return 2
	}
	key, _ := pathsConfigKey(args[0])
	dir, cfg, code := loadConfigForPaths()
	if code != 0 {
		return code
	}
	if _, ok := cfg.Paths[key]; !ok {
		fmt.Fprintf(os.Stderr, "Error: no %q entry under paths:\n", key)
		return 1
	}
	delete(cfg.Paths, key)
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Printf("paths: %s removed\n", key)
	fmt.Println("apply it with: cartographer sync")
	return 0
}

// loadConfigForPaths loads the client config for a write. A missing file is
// an error, not a default: writing a fresh `.cartographer.yaml` holding only
// `paths:` would make every other command believe the client is connected.
func loadConfigForPaths() (string, *clientconfig.Config, int) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return "", nil, 2
	}
	if _, err := os.Stat(clientconfig.Path(dir)); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s not found — run `cartographer connect` first\n", clientconfig.Path(dir))
		return "", nil, 2
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return "", nil, 1
	}
	return dir, cfg, 0
}

// recordedKind names the kind the lockfile knows key under, so an unprefixed
// `paths set` of a repo key still gets the git-clone check. "" when the
// lockfile does not know it, or knows it under both kinds.
func recordedKind(dir, key string) string {
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return ""
	}
	var kinds []string
	for _, r := range placeholderRows(lf) {
		if r.Key == "repo:"+key || r.Key == "path:"+key {
			kinds = append(kinds, strings.SplitN(r.Key, ":", 2)[0])
		}
	}
	if len(kinds) == 1 {
		return kinds[0]
	}
	return ""
}

// pathWarnings reports what is odd about a path being recorded, without
// refusing it: the operator may be about to create or clone it, and a
// `paths:` entry that stops a setup step is worse than one flagged loudly.
func pathWarnings(kind, value string) []string {
	expanded := repoindex.ExpandHome(value)
	info, err := os.Stat(expanded)
	if err != nil {
		return []string{fmt.Sprintf("%s does not exist yet — recorded anyway", value)}
	}
	if kind == "repo" {
		if !info.IsDir() {
			return []string{fmt.Sprintf("%s is not a directory, so it is not a git clone — recorded anyway", value)}
		}
		if _, err := os.Stat(filepath.Join(expanded, ".git")); err != nil {
			return []string{fmt.Sprintf("%s is not a git clone (no .git) — recorded anyway", value)}
		}
	}
	return nil
}
