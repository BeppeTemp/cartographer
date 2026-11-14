package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// pluginSchema versions the JSON of the plugin commands, like statusSchema.
const pluginSchema = "cartographer.plugin/v1"

const pluginUsage = `Usage: cartographer plugin new <name> [<dir>]
       cartographer plugin validate <dir> [--output table|json]
       cartographer plugin test <dir> [--recipe <name> | --connector <name>] [--output table|json]
       cartographer plugin install <git-url>[@<ref>] | <folder> [--yes]
       cartographer plugin vendor <git-url>[@<ref>] | <folder> --to <dir> [--side client|surveyor|server|atlas]
       cartographer plugin list [--output table|json]
       cartographer plugin show <name> [--files] [--output table|json]
       cartographer plugin approve <name> [--yes] [--output table|json]
       cartographer plugin update [<name>] [--yes] [--output table|json]
       cartographer plugin config <name> [<key>=<value> ...] [--unset <key> ...]
       cartographer plugin enable <name> [--output table|json]
       cartographer plugin disable <name> [--output table|json]
       cartographer plugin remove <name> [--output table|json]`

// Seams for the tests: the store lives in the user cache dir and the prompt needs a terminal.
var (
	pluginStoreFn     = core.DefaultStore
	pluginInteractive = func() bool { return isTerminal(os.Stdin.Fd()) }
)

// pluginOpts is what the manifest check needs from this binary. A plugin may not
// take the name of a command: the table plus the commands dispatched but not listed.
func pluginOpts() core.Options {
	return core.Options{RunningVersion: version, Side: core.SideClient, CheckScripts: star.Check, ReservedName: func(n string) bool {
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
	case "test":
		return pluginTest(rest)
	case "install":
		return pluginInstall(rest)
	case "vendor":
		return pluginVendor(rest)
	case "list":
		return pluginList(rest)
	case "show":
		return pluginShow(rest)
	case "approve":
		return pluginApprove(rest)
	case "update":
		return pluginUpdate(rest)
	case "config":
		return pluginConfig(rest)
	case "enable":
		return pluginEnable(rest)
	case "disable":
		return pluginDisable(rest)
	case "remove":
		return pluginRemove(rest)
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

// scaffold is the tree `plugin new` writes. Not every point gets a folder: git
// does not keep an empty one, and a `.gitkeep` inside a point is a file the
// tree reader refuses, so the README names the points instead.
func scaffold(name string) []core.TreeFile {
	rng := core.Floor(version)
	file := func(p, c string) core.TreeFile { return core.TreeFile{Path: p, Content: []byte(c)} }
	return []core.TreeFile{
		file(core.ManifestFile, fmt.Sprintf("schema: 2\nname: %s\nversion: 0.1.0\ndisplay_name: %s\ndescription: A Cartographer plugin.\ncartographer: %q\ncomponents:\n  client:\n    sdk:\n      artifacts: 1\n", name, name, rng)),
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
	// The running-version range is not checked: the scaffold declares a floor of
	// at least 0.29.0, which a snapshot build of an older core cannot meet, and
	// it must not fail on its own output.
	opts := pluginOpts()
	opts.RunningVersion = ""
	p, probs := core.ReadTree(files, opts)
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
func pluginRead(src plugin.Source) (files []core.TreeFile, sha string, p core.Plugin, probs []string, err error) {
	return pluginReadWith(src, pluginOpts())
}

// pluginReadWith is pluginRead with the options given, so a caller can read a
// tree without running its scripts.
func pluginReadWith(src plugin.Source, opts core.Options) (files []core.TreeFile, sha string, p core.Plugin, probs []string, err error) {
	tmp, err := os.MkdirTemp("", "cartographer-plugin-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmp)
	if files, sha, err = plugin.Fetch(context.Background(), src, tmp); err == nil {
		p, probs = core.ReadTree(files, opts)
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
	// Validation reads every side: Side stays empty.
	every := pluginOpts()
	every.Side = ""
	_, _, p, probs, err := pluginReadWith(src, every)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	// A tree with problems is not loaded again here: ReadTree already ran the
	// scripts and put their load error among the problems.
	var prog *star.Program
	if len(probs) == 0 {
		if prog, err = pluginProgram(p); err != nil {
			return pluginFail(1, "%s", escapeControl(err.Error()))
		}
	}
	if output == "json" {
		doc := map[string]any{"schema": pluginSchema, "valid": len(probs) == 0, "name": p.Manifest.Name, "digest": p.Digest, "problems": append([]string{}, probs...)}
		if prog != nil {
			doc["scripts"] = prog.Files
			doc["registers"] = registrationLines(p.Manifest.Name, prog)
		}
		if p.Manifest.Schema != nil && *p.Manifest.Schema == core.SchemaVersion {
			// Additive, schema 2 only: a schema 1 document stays what 0.28 printed.
			doc["sides"], doc["unsupported"], doc["unknown"] = append([]core.Side{}, p.Sides...), p.Unsupported, append([]string{}, p.Unknown...)
		}
		printJSON(doc)
	} else if len(probs) == 0 {
		fmt.Printf("%s %s is valid (digest %.12s)\n", escapeControl(p.Manifest.Name), escapeControl(p.Manifest.Version), p.Digest)
		if prog != nil {
			previewScripts(os.Stdout, p)
		}
		printSides(os.Stdout, p)
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
	if msg := clientSideProblem(p); msg != "" {
		return pluginFail(1, "%s; nothing was installed", msg)
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
	// A new entry is enabled but unapproved: core.State reads that as pending approval, not disabled.
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
	registerPluginProviders(os.Stderr)
	pluginPreview(os.Stdout, p, entry, cfg)
	// The consent covers what runs: the hook scripts, hook.json and any frontmatter that declares commands.
	for _, a := range p.Artifacts {
		fmt.Printf("%s %q:\n", a.Kind, a.Name)
		printApprovalContent(os.Stdout, a, a.Files)
	}
	printScriptContent(os.Stdout, p)
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
	entry.Tools = pluginToolNames(p) // every site that sets Approved sets the transport hint with it
	if err := save(cfg); err != nil {
		return pluginFail(1, "%v", err)
	}
	registerPluginProviders(os.Stderr)
	fmt.Printf("Enabled %s %s\n", name, entry.Version)
	return 0
}

func pluginPreview(w io.Writer, p core.Plugin, e clientconfig.PluginEntry, cfg *clientconfig.Config) {
	have := func(n string) (string, bool) { o, ok := cfg.Plugin(n); return o.Version, ok }
	plugin.Preview(w, p, plugin.PreviewInfo{Source: gitx.RedactRemoteURL(e.Source), Ref: e.Ref, SHA: e.SHA, Side: core.SideClient, Have: have}, pluginOpts())
	previewScripts(w, p)
}

// clientSideProblem is why the client cannot take a plugin, "" when it can: a
// schema 2 plugin that declares no client side, or one this build cannot load.
func clientSideProblem(p core.Plugin) string {
	if p.Manifest.Schema == nil || *p.Manifest.Schema != core.SchemaVersion {
		return ""
	}
	name := escapeControl(p.Manifest.Name)
	if !slices.Contains(p.Sides, core.SideClient) {
		var to []string
		for _, s := range p.Sides {
			to = append(to, string(s))
		}
		return fmt.Sprintf("plugin %s has no client side (it contributes to: %s)", name, strings.Join(to, ", "))
	}
	if why := p.Unsupported[core.SideClient]; why != "" {
		return fmt.Sprintf("the client side of plugin %s cannot load here: %s", name, escapeControl(why))
	}
	return ""
}

// printSides names what a schema 2 plugin declares: its sides, the ones this
// build cannot load and the entries it holds that no family of this build owns.
func printSides(w io.Writer, p core.Plugin) {
	if p.Manifest.Schema == nil || *p.Manifest.Schema != core.SchemaVersion {
		return
	}
	var sides []string
	for _, s := range p.Sides {
		sides = append(sides, string(s))
	}
	fmt.Fprintf(w, "sides: %s\n", escapeControl(strings.Join(sides, ", ")))
	for _, s := range p.Sides {
		if why := p.Unsupported[s]; why != "" {
			fmt.Fprintf(w, "%s side not loadable by this build: %s\n", s, escapeControl(why))
		}
	}
	if len(p.Unknown) > 0 {
		fmt.Fprintf(w, "not known to this build, stored but never loaded: %s\n", escapeControl(strings.Join(p.Unknown, ", ")))
	}
}

// pluginView is the tree a command shows for an entry: the approved one when the
// plugin is active, else the installed one; tree says which, "" when neither can be read.
func pluginView(store core.Store, name string, e clientconfig.PluginEntry) (state core.Status, p core.Plugin, tree string) {
	return pluginViewWith(store, name, e, false)
}

// pluginViewWith is pluginView that, with ignoreRange, also reads a tree the
// running version is outside the range of (#2089): the state stays invalid, the
// tree is shown so the user can decide between `plugin update` and `plugin remove`.
func pluginViewWith(store core.Store, name string, e clientconfig.PluginEntry, ignoreRange bool) (state core.Status, p core.Plugin, tree string) {
	st, active := core.State(name, e, store, pluginOpts())
	if active != nil {
		return st, *active, "approved"
	}
	o := pluginOpts()
	o.IgnoreRange = ignoreRange && st == core.StatusInvalid
	if o.IgnoreRange && e.Approved != "" {
		if ap, err := store.Open(name, e.Approved, o); err == nil {
			return st, ap, "approved"
		}
	}
	if e.Installed != "" {
		if ip, err := store.Open(name, e.Installed, o); err == nil {
			return st, ip, "installed"
		}
	}
	return st, core.Plugin{}, ""
}

func pluginLoad() (*clientconfig.Config, core.Store, int) {
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

func newPluginRow(name string, e clientconfig.PluginEntry, st core.Status, p core.Plugin) pluginRow {
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
	st, p, tree := pluginViewWith(store, pos[0], e, true)
	if tree == "" {
		if st == core.StatusInvalid {
			// Read past the range, the tree is still invalid: say for what.
			o := pluginOpts()
			o.IgnoreRange = true
			digest := e.Approved
			if digest == "" {
				digest = e.Installed
			}
			if _, err := store.Open(pos[0], digest, o); errors.Is(err, core.ErrInvalid) {
				return pluginFail(1, "%s is invalid (%s); fix it with `cartographer plugin update %s`, or drop it with `cartographer plugin remove %s`", pos[0], escapeControl(err.Error()), pos[0], pos[0])
			}
		}
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
	printPluginSettings(os.Stdout, p, e)
	if flags["--files"] {
		var fs []contract.ArtifactFile
		for _, f := range p.Files {
			fs = append(fs, contract.ArtifactFile{Path: f.Path, Content: f.Content, Executable: f.Executable})
		}
		printApprovalContent(os.Stdout, contract.Artifact{Kind: "plugin", Name: pos[0]}, fs)
		printScriptContent(os.Stdout, p)
	}
	return 0
}
