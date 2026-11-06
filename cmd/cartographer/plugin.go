package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// pluginSchema versions the JSON of the plugin commands, like statusSchema.
const pluginSchema = "cartographer.plugin/v1"

const pluginUsage = `Usage: cartographer plugin new <name> [<dir>]
       cartographer plugin validate <dir> [--output table|json]
       cartographer plugin install <git-url>[@<ref>] | <folder> [--yes]
       cartographer plugin list [--output table|json]
       cartographer plugin show <name> [--files] [--output table|json]`

// Seams for the tests: the store lives in the user cache dir and the prompt needs a terminal.
var (
	pluginStoreFn     = plugin.DefaultStore
	pluginInteractive = func() bool { return isTerminal(os.Stdin.Fd()) }
)

// pluginOpts is what the manifest check needs from this binary. A plugin may not
// take the name of a command: the table plus the commands dispatched but not listed.
func pluginOpts() plugin.Options {
	return plugin.Options{RunningVersion: version, ReservedName: func(n string) bool {
		return n == "hook" || n == "plugin" || slices.ContainsFunc(subcommands, func(c subcommand) bool { return c.name == n })
	}}
}

// cmdPlugin is dispatched by run() but absent from the usage table until the
// turn-on change of the plugins plan (the test exception in main_test.go).
func cmdPlugin(args []string) int {
	if len(args) > 0 && hasHelpFlag(args[:1]) {
		fmt.Println(pluginUsage)
		return 0
	}
	if len(args) == 0 {
		return pluginFail(2, "%s", pluginUsage)
	}
	rest := args[1:]
	switch args[0] {
	case "new":
		return pluginNew(rest)
	case "validate":
		return pluginValidate(rest)
	case "install":
		return pluginInstall(rest)
	case "list":
		return pluginList(rest)
	case "show":
		return pluginShow(rest)
	}
	return pluginFail(2, "%s", pluginUsage)
}

func pluginFail(code int, format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	return code
}

// pluginArgs splits args into positionals and the boolean flags named in bools;
// ok is false for any other flag.
func pluginArgs(args []string, bools ...string) (pos []string, flags map[string]bool, ok bool) {
	flags = map[string]bool{}
	for _, a := range args {
		switch {
		case slices.Contains(bools, a):
			flags[a] = true
		case strings.HasPrefix(a, "-"):
			return nil, nil, false
		default:
			pos = append(pos, a)
		}
	}
	return pos, flags, true
}

func printJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

var coreVersionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)`)

// scaffold is the tree `plugin new` writes. Not every point gets a folder: git
// does not keep an empty one, and a `.gitkeep` inside a point is a file the
// tree reader refuses, so the README names the points instead.
func scaffold(name string) []plugin.TreeFile {
	rng := ">=0.28.0"
	if m := coreVersionRe.FindStringSubmatch(version); m != nil {
		rng = ">=" + m[1]
	}
	file := func(p, c string) plugin.TreeFile { return plugin.TreeFile{Path: p, Content: []byte(c)} }
	return []plugin.TreeFile{
		file(plugin.ManifestFile, fmt.Sprintf("schema: 1\nname: %s\nversion: 0.1.0\ndisplay_name: %s\ndescription: A Cartographer plugin.\ncartographer: %q\n", name, name, rng)),
		file("README.md", "# "+name+"\n\nA Cartographer plugin. Add skills/, agents/ or hooks/, then run `cartographer plugin validate .`.\n"),
		file("skills/"+name+"-example/SKILL.md", fmt.Sprintf("---\nname: %s-example\ndescription: Example skill of the %s plugin, replace it with your own.\n---\n\n# Example\n\nSay what this skill does.\n", name, name)),
	}
}

func pluginNew(args []string) int {
	pos, _, ok := pluginArgs(args)
	if !ok || len(pos) < 1 || len(pos) > 2 {
		return pluginFail(2, "%s", pluginUsage)
	}
	name, dir := pos[0], "./"+pos[0]
	if len(pos) == 2 {
		dir = pos[1]
	}
	files := scaffold(name)
	// Same check install runs: a refused name gets the same messages, nothing is written.
	p, probs := plugin.ReadTree(files, pluginOpts())
	if len(probs) == 0 && p.Manifest.Name != name {
		probs = []string{fmt.Sprintf("name: %q is not a plugin name", name)}
	}
	if len(probs) > 0 {
		return pluginFail(2, "cannot scaffold %q:\n  %s", name, strings.Join(probs, "\n  "))
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return pluginFail(2, "%s exists and is not empty", dir)
	}
	for _, f := range files {
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		if err == nil {
			err = os.WriteFile(full, f.Content, 0o644)
		}
		if err != nil {
			return pluginFail(1, "%v", err)
		}
	}
	fmt.Printf("Created plugin %s in %s\nNext: cartographer plugin validate %s\n", name, dir, dir)
	return 0
}

// pluginRead acquires a source into memory and validates it.
func pluginRead(src plugin.Source) (files []plugin.TreeFile, sha string, p plugin.Plugin, probs []string, err error) {
	tmp, err := os.MkdirTemp("", "cartographer-plugin-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	if files, sha, err = plugin.Fetch(context.Background(), src, tmp); err == nil {
		p, probs = plugin.ReadTree(files, pluginOpts())
	}
	return
}

func pluginValidate(args []string) int {
	output, args, err := outputFlag(args)
	pos, _, ok := pluginArgs(args)
	if err != nil || !ok || len(pos) != 1 {
		return pluginFail(2, "%s", pluginUsage)
	}
	if fi, err := os.Stat(pos[0]); err != nil || !fi.IsDir() {
		return pluginFail(2, "%s is not a folder", pos[0])
	}
	src, err := plugin.ParseSource(pos[0])
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	_, _, p, probs, err := pluginRead(src)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	if output == "json" {
		printJSON(map[string]any{"schema": pluginSchema, "valid": len(probs) == 0, "name": p.Manifest.Name, "digest": p.Digest, "problems": append([]string{}, probs...)})
	} else if len(probs) == 0 {
		fmt.Printf("%s %s is valid (digest %.12s)\n", escapeControl(p.Manifest.Name), escapeControl(p.Manifest.Version), p.Digest)
	} else {
		for _, pr := range probs {
			fmt.Println(escapeControl(pr))
		}
	}
	if len(probs) > 0 {
		return 1
	}
	return 0
}

func pluginInstall(args []string) int {
	pos, flags, ok := pluginArgs(args, "--yes")
	if !ok || len(pos) != 1 {
		return pluginFail(2, "%s", pluginUsage)
	}
	src, err := plugin.ParseSource(pos[0])
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	store, err := pluginStoreFn()
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	files, sha, p, probs, err := pluginRead(src)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	if len(probs) > 0 {
		for _, pr := range probs {
			fmt.Fprintln(os.Stderr, escapeControl(pr))
		}
		return pluginFail(1, "%s is not a valid plugin; nothing was installed", pos[0])
	}
	name := p.Manifest.Name
	recorded := src.Remote
	if !src.Git {
		recorded = src.Path
	}

	release, err := lockClient(dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	// The lock is not held while the user reads and answers the prompt.
	unlock := func() {
		if release != nil {
			release()
			release = nil
		}
	}
	defer unlock()
	cfg, err := loadConfigForWrite(dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	entry, had := cfg.Plugin(name)
	if had && entry.Source != recorded {
		return pluginFail(2, "%s is installed from %s: run `cartographer plugin remove %s` first", name, gitx.RedactRemoteURL(entry.Source), name)
	}
	if err := store.Put(name, files, p.Digest, pluginOpts()); err != nil {
		return pluginFail(1, "%v", err)
	}
	previous := entry.Approved
	entry.Source, entry.Ref, entry.SHA, entry.Version, entry.Installed = recorded, src.Ref, sha, p.Manifest.Version, p.Digest
	// A new entry is enabled but unapproved: plugin.State reads that as pending approval, not disabled.
	entry.Enabled = entry.Enabled || !had
	save := func(c *clientconfig.Config) error {
		if err := c.SetPlugin(name, entry); err != nil {
			return err
		}
		return clientconfig.Save(dir, c)
	}
	if err := save(cfg); err != nil {
		return pluginFail(1, "%v", err)
	}
	unlock()
	pluginPreview(os.Stdout, p, entry, cfg)
	// The consent covers what runs: the hook scripts, hook.json and any frontmatter that declares commands.
	for _, a := range p.Artifacts {
		fmt.Printf("%s %q:\n", a.Kind, a.Name)
		printApprovalContent(os.Stdout, a, a.Files)
	}
	if entry.Approved == p.Digest && entry.Enabled {
		fmt.Printf("%s %s is already approved and enabled\n", name, entry.Version)
		return 0
	}
	if !flags["--yes"] && !(pluginInteractive() && promptYesNo(fmt.Sprintf("Enable %s %s? [y/N] ", name, entry.Version))) {
		if previous != "" {
			fmt.Printf("not approved: the previously approved version stays active; run `cartographer plugin install %s` again to approve this one\n", pos[0])
		} else {
			fmt.Printf("pending approval: run `cartographer plugin install %s` again on a terminal, or with --yes\n", pos[0])
		}
		return 0
	}
	// Retake the lock and reload: the file may have changed while the prompt was open.
	if release, err = lockClient(dir); err != nil {
		return pluginFail(1, "%v", err)
	}
	if cfg, err = loadConfigForWrite(dir); err != nil {
		return pluginFail(1, "%v", err)
	}
	if cur, ok := cfg.Plugin(name); !ok || cur.Installed != p.Digest {
		return pluginFail(1, "%s changed while waiting for the answer; nothing was approved, run the install again", name)
	}
	entry, _ = cfg.Plugin(name)
	entry.Approved, entry.Enabled = p.Digest, true
	if err := save(cfg); err != nil {
		return pluginFail(1, "%v", err)
	}
	fmt.Printf("Enabled %s %s\n", name, entry.Version)
	return 0
}

func pluginPreview(w io.Writer, p plugin.Plugin, e clientconfig.PluginEntry, cfg *clientconfig.Config) {
	have := func(n string) (string, bool) { o, ok := cfg.Plugin(n); return o.Version, ok }
	plugin.Preview(w, p, plugin.PreviewInfo{Source: gitx.RedactRemoteURL(e.Source), Ref: e.Ref, SHA: e.SHA, Have: have}, pluginOpts())
}

// pluginView is the tree a command shows for an entry: the approved one when the
// plugin is active, else the installed one; tree says which, "" when neither can be read.
func pluginView(store plugin.Store, name string, e clientconfig.PluginEntry) (state plugin.Status, p plugin.Plugin, tree string) {
	st, active := plugin.State(name, e, store, pluginOpts())
	if active != nil {
		return st, *active, "approved"
	}
	if e.Installed != "" {
		if ip, err := store.Open(name, e.Installed, pluginOpts()); err == nil {
			return st, ip, "installed"
		}
	}
	return st, plugin.Plugin{}, ""
}

func pluginLoad() (*clientconfig.Config, plugin.Store, int) {
	store, err := pluginStoreFn()
	if err != nil {
		return nil, store, pluginFail(1, "%v", err)
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return nil, store, pluginFail(2, "%v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		cfg, err = clientconfig.Default(), nil
	}
	if err != nil {
		return nil, store, pluginFail(1, "%v", err)
	}
	return cfg, store, -1
}

type pluginRow struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Source          string         `json:"source"`
	SHA             string         `json:"sha,omitempty"`
	Installed       string         `json:"installed,omitempty"`
	Approved        string         `json:"approved,omitempty"`
	State           string         `json:"state"`
	Contributions   map[string]int `json:"contributions"`
	UpdateAvailable bool           `json:"update_available"`
}

func newPluginRow(name string, e clientconfig.PluginEntry, st plugin.Status, p plugin.Plugin) pluginRow {
	r := pluginRow{Name: name, Version: e.Version, Source: gitx.RedactRemoteURL(e.Source), SHA: e.SHA, Installed: e.Installed,
		Approved: e.Approved, State: string(st), Contributions: map[string]int{}, UpdateAvailable: e.Approved != "" && e.Installed != e.Approved}
	for pt, cs := range p.Contributions {
		r.Contributions[pt] = len(cs)
	}
	return r
}

func pluginList(args []string) int {
	output, args, err := outputFlag(args)
	if pos, _, ok := pluginArgs(args); err != nil || !ok || len(pos) != 0 {
		return pluginFail(2, "%s", pluginUsage)
	}
	cfg, store, code := pluginLoad()
	if code >= 0 {
		return code
	}
	names := make([]string, 0, len(cfg.Plugins))
	for n := range cfg.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	rows := []pluginRow{}
	for _, n := range names {
		st, p, _ := pluginView(store, n, cfg.Plugins[n])
		rows = append(rows, newPluginRow(n, cfg.Plugins[n], st, p))
	}
	if output == "json" {
		printJSON(map[string]any{"schema": pluginSchema, "plugins": rows})
		return 0
	}
	for _, r := range rows {
		var cs []string
		for pt, n := range r.Contributions {
			cs = append(cs, fmt.Sprintf("%s %d", pt, n))
		}
		sort.Strings(cs)
		line := fmt.Sprintf("%-20s %-8s %-18s %-12.12s %s", r.Name, r.Version, r.State, r.Installed, r.Source)
		if len(cs) > 0 {
			line += "  [" + strings.Join(cs, ", ") + "]"
		}
		if r.UpdateAvailable {
			line += "  update available"
		}
		fmt.Println(escapeControl(line))
	}
	return 0
}

func pluginShow(args []string) int {
	output, args, err := outputFlag(args)
	pos, flags, ok := pluginArgs(args, "--files")
	if err != nil || !ok || len(pos) != 1 {
		return pluginFail(2, "%s", pluginUsage)
	}
	cfg, store, code := pluginLoad()
	if code >= 0 {
		return code
	}
	e, found := cfg.Plugin(pos[0])
	if !found {
		return pluginFail(2, "no plugin named %q (see `cartographer plugin list`)", pos[0])
	}
	st, p, tree := pluginView(store, pos[0], e)
	if tree == "" {
		return pluginFail(1, "%s is %s: the stored tree cannot be read; reinstall it with `cartographer plugin install %s`", pos[0], st, gitx.RedactRemoteURL(e.Source))
	}
	if tree == "approved" && e.Approved != e.Installed {
		// Ref, sha and version describe the installed tree, not the one shown.
		e.Ref, e.SHA, e.Version = "", "", p.Manifest.Version
	}
	if output == "json" {
		doc := map[string]any{"schema": pluginSchema, "plugin": newPluginRow(pos[0], e, st, p), "tree": tree}
		printJSON(doc)
		return 0
	}
	fmt.Printf("state: %s; showing the %s tree\n", st, tree)
	pluginPreview(os.Stdout, p, e, cfg)
	if flags["--files"] {
		var fs []provisioning.ArtifactFile
		for _, f := range p.Files {
			fs = append(fs, provisioning.ArtifactFile{Path: f.Path, Content: f.Content, Executable: f.Executable})
		}
		printApprovalContent(os.Stdout, provisioning.Artifact{Kind: "plugin", Name: pos[0]}, fs)
	}
	return 0
}
