package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	// Ignored is set when the operator marked the key as absent on this
	// machine (D282): it is still listed, but is not reported as a problem.
	Ignored bool `json:"ignored,omitempty"`
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
func unresolvedRowsOf(results map[string]provisioning.AppliedResult, ignored []string) []placeholderRow {
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{}}
	for key, r := range results {
		lf.Providers[key] = r.NewLock
	}
	return unresolvedRows(lf, ignored)
}

// unresolvedRows is placeholderRows narrowed to the keys nothing resolved and
// the operator did not mark absent on this machine (D282).
func unresolvedRows(lf provisioning.LockFile, ignored []string) []placeholderRow {
	var out []placeholderRow
	for _, r := range placeholderRows(lf) {
		if !r.Resolved() && r.Reason != "" && !containsString(ignored, r.Key) {
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
	// Help is answered before the dispatch: a leading flag otherwise belongs to
	// the default `list`, whose flag set would print only its own usage and
	// exit 2, never naming `set` — the fix the placeholder warning points at.
	if len(args) > 0 && isHelpArg(args[0]) {
		printPathsUsage(os.Stdout)
		return 0
	}
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	// `help` stays a word here: `paths unset help` names a key called help.
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		printPathsUsage(os.Stdout)
		return 0
	}
	switch sub {
	case "list":
		return cmdPathsList(args)
	case "set":
		return cmdPathsSet(args)
	case "ignore":
		return cmdPathsIgnore(args)
	case "unset":
		return cmdPathsUnset(args)
	case "suggest":
		return cmdPathsSuggest(args)
	default:
		printPathsUsage(os.Stderr)
		return 2
	}
}

func isHelpArg(a string) bool { return a == "-h" || a == "--help" || a == "help" }

func printPathsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cartographer paths [list [--json]] | suggest | set <key> <path> | ignore <key> | unset <key>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  list            every placeholder key the last sync met, its KBs, and its path or failure reason")
	fmt.Fprintln(w, "  suggest         propose a local path for every unresolved key (never writes)")
	fmt.Fprintln(w, "  set <key> <p>   record where <key> (repo:<name> or path:<name>) lives on this machine")
	fmt.Fprintln(w, "  ignore <key>    mark <key> as absent on this machine: it stops being reported (listed as ignored)")
	fmt.Fprintln(w, "  unset <key>     remove a recorded entry or an ignore, restoring the report")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "set, ignore and unset edit .cartographer.yaml; apply them with: cartographer sync")
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
	// A missing config is not an error here: nothing is ignored yet.
	var ignored []string
	if cfg, err := clientconfig.Load(dir); err == nil {
		ignored = cfg.IgnoredPaths
	}
	for i := range rows {
		rows[i].Ignored = !rows[i].Resolved() && containsString(ignored, rows[i].Key)
	}

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
		} else if r.Ignored {
			fmt.Printf("%s\t%s\tIGNORED (absent on this machine)\n", r.Key, kbs)
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
		fmt.Printf("\n%d unresolved — record each with `cartographer paths set <key> <path>` (or `cartographer paths ignore <key>` if it never exists here), then run `cartographer sync`\n", unresolved)
	}
	return 0
}

// cmdPathsSuggest proposes candidate paths for every unresolved, not ignored
// key (D320), so recording one is a confirmation instead of a lookup. It only
// reads — the lockfile, the client config and the disk — and prints the
// `paths set` line to run; nothing is written. Candidates, in order: the
// default the KB's paths.yaml declares, when it exists here; for a repo key,
// every clone under the search roots whose origin remote or directory names
// the key (sync's own resolution wanted an exact, unambiguous match, so this
// is where the near misses surface); for a path key, ~/<name>,
// ~/.config/<name> and ~/.<name>.
func cmdPathsSuggest(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer paths suggest")
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
	var ignored, roots []string
	depth := 0
	if cfg, err := clientconfig.Load(dir); err == nil {
		ignored, roots, depth = cfg.IgnoredPaths, cfg.SearchRoots, cfg.SearchDepth
	}
	rows := unresolvedRows(lf, ignored)
	if len(rows) == 0 {
		fmt.Println("no unresolved placeholder — nothing to suggest")
		return 0
	}
	home, _ := os.UserHomeDir()
	var idx *repoindex.Index
	for _, r := range rows {
		kind, name, _ := strings.Cut(r.Key, ":")
		var candidates []string
		add := func(p string) {
			if p != "" && !containsString(candidates, p) {
				candidates = append(candidates, p)
			}
		}
		if r.Default != "" {
			if p := repoindex.ExpandHome(r.Default); pathExists(p) {
				add(p)
			}
		}
		switch kind {
		case "repo":
			if idx == nil {
				idx, _, _ = repoindex.Scan(roots, depth)
			}
			for _, p := range repoCandidates(idx, name) {
				add(p)
			}
		case "path":
			if home != "" {
				last := filepath.Base(filepath.FromSlash(name))
				for _, p := range []string{filepath.Join(home, last), filepath.Join(home, ".config", last), filepath.Join(home, "."+last)} {
					if pathExists(p) {
						add(p)
					}
				}
			}
		}

		kbs := strings.Join(r.KBs, ",")
		if kbs == "" {
			kbs = "-"
		}
		fmt.Printf("%s\t%s\n", r.Key, kbs)
		if r.Description != "" {
			fmt.Printf("\t%s\n", r.Description)
		}
		if len(candidates) == 0 {
			fmt.Printf("\t(no suggestion — use cartographer paths set %s <path> or cartographer paths ignore %s)\n", r.Key, r.Key)
			continue
		}
		for _, c := range candidates {
			fmt.Printf("\tcartographer paths set %s %s\n", r.Key, c)
		}
	}
	fmt.Println("\nrun the line you confirm, then: cartographer sync")
	return 0
}

// repoCandidates returns, sorted, every clone in idx whose normalized origin
// remote contains name or whose directory is named name, case-insensitively.
func repoCandidates(idx *repoindex.Index, name string) []string {
	if idx == nil || name == "" {
		return nil
	}
	short := strings.ToLower(name)
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	var out []string
	for remote, paths := range idx.Repos {
		match := strings.Contains(strings.ToLower(string(remote)), short)
		for _, p := range paths {
			if match || strings.EqualFold(filepath.Base(p), short) {
				if !containsString(out, p) {
					out = append(out, p)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
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
	// An explicit mapping supersedes an earlier "absent here" (D282).
	cfg.IgnoredPaths = withoutIgnored(cfg.IgnoredPaths, key)
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
	_, hadPath := cfg.Paths[key]
	remaining := withoutIgnored(cfg.IgnoredPaths, key)
	hadIgnore := len(remaining) != len(cfg.IgnoredPaths)
	if !hadPath && !hadIgnore {
		fmt.Fprintf(os.Stderr, "Error: no %q entry under paths: and no ignored key of that name\n", key)
		return 1
	}
	delete(cfg.Paths, key)
	cfg.IgnoredPaths = remaining
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Printf("paths: %s removed\n", key)
	fmt.Println("apply it with: cartographer sync")
	return 0
}

// withoutIgnored returns ignored minus the entries key names: "repo:<key>",
// "path:<key>", or key itself (an unprefixed argument covers both kinds).
func withoutIgnored(ignored []string, key string) []string {
	var out []string
	for _, id := range ignored {
		if id == key || id == "repo:"+key || id == "path:"+key {
			continue
		}
		out = append(out, id)
	}
	return out
}

// cmdPathsIgnore records that a key is deliberately absent on this machine
// (D282). The stored id keeps its kind prefix, because the same slug can be a
// repo on one KB and a path in another; an unprefixed argument is qualified
// from the lockfile, and refused when that cannot be done unambiguously.
func cmdPathsIgnore(args []string) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer paths ignore <key>")
		return 2
	}
	key, kind := pathsConfigKey(args[0])
	if key == "" {
		fmt.Fprintln(os.Stderr, "Error: empty key")
		return 2
	}
	dir, cfg, code := loadConfigForPaths()
	if code != 0 {
		return code
	}
	if kind == "" {
		kind = recordedKind(dir, key)
	}
	if kind == "" {
		fmt.Fprintf(os.Stderr, "Error: cannot tell whether %q is a repo or a path key — write repo:%s or path:%s\n", key, key, key)
		return 2
	}
	if p, ok := cfg.Paths[key]; ok {
		fmt.Fprintf(os.Stderr, "Error: %q is mapped to %s under paths: — run `cartographer paths unset %s` first\n", key, p, key)
		return 1
	}
	id := kind + ":" + key
	if !containsString(cfg.IgnoredPaths, id) {
		cfg.IgnoredPaths = append(cfg.IgnoredPaths, id)
		sort.Strings(cfg.IgnoredPaths)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Printf("paths: %s ignored (absent on this machine; still left verbatim)\n", id)
	fmt.Println("restore the report with: cartographer paths unset " + id)
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
