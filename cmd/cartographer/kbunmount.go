package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/service"
	"gopkg.in/yaml.v3"
)

// unmountedDir is where `kb unmount` parks a clone that sits in the data dir
// (D761). A dot-dir: discoverKBPaths and scanDataDir skip it, so the server
// does not mount it again at the next start.
const unmountedDir = ".unmounted"

// unmountPlan is what the preflight determined, before anything changed.
type unmountPlan struct {
	Name    string
	KBPath  string
	DataDir string
	Origin  string // the clone's origin URL, "" when none

	// ConfigPath is the server config to edit, "" when none exists; EntryIndex
	// is the kbs[] entry to remove, -1 for a KB mounted by discovery.
	ConfigPath string
	EntryIndex int

	Scopes     []string
	SigningKey bool
	Approvals  bool
	Bindings   []string

	// ParkPath is where the clone moves without --delete-clone, "" when it
	// stays where it is (a kbs[].path outside the data dir).
	ParkPath string
	Delete   bool
	// Unsafe lists why the clone cannot be proven pushed; --force overrides.
	Unsafe []string
}

// cmdKBUnmount implements `cartographer kb unmount <name> [--delete-clone]
// [--restart] [--force] [--dry-run] [--data <dir>] [--config <path>]`.
//
// Like kb rename it is offline and local (D177): this machine's server config,
// data dir and client bindings. The remote stays the source of truth.
func cmdKBUnmount(args []string) int {
	name, rest := splitPositional(args, "")

	fs := flag.NewFlagSet("kb unmount", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to edit (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	deleteFlag := fs.Bool("delete-clone", false, "Delete the local clone instead of parking it in <data>/"+unmountedDir)
	forceFlag := fs.Bool("force", false, "Unmount even when the clone has uncommitted or unpushed work")
	dryRunFlag := fs.Bool("dry-run", false, "Print the plan and change nothing")
	restartFlag := fs.Bool("restart", false, "Restart the local service and wait until healthy afterwards")
	fs.Parse(rest)

	if name == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb unmount <name> [--delete-clone] [--force] [--dry-run] [--data <dir>] [--config <path>] [--restart]")
		return 2
	}
	if code := checkLocalTarget(*dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}

	plan, err := planKBUnmount(name, dataDir, *configFlag, *deleteFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	printUnmountPreflight(os.Stdout, plan)
	if len(plan.Unsafe) > 0 && !*forceFlag {
		fmt.Fprintf(os.Stderr, "Error: refusing to unmount %q: %s — push or commit first, or pass --force\n", name, strings.Join(plan.Unsafe, "; "))
		return 1
	}
	if *dryRunFlag {
		fmt.Println("dry run: nothing changed")
		return 0
	}

	backup, err := applyKBUnmount(plan)
	if backup != "" {
		fmt.Printf("server config backup: %s\n", backup)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	fmt.Printf("KB %q unmounted\n", name)
	switch {
	case plan.Delete:
		fmt.Printf("deleted the local clone %s\n", plan.KBPath)
	case plan.ParkPath != "":
		fmt.Printf("parked the local clone in %s\n", plan.ParkPath)
	default:
		fmt.Printf("left the local clone in %s (outside the data dir, never discovered)\n", plan.KBPath)
	}
	if plan.Origin != "" {
		fmt.Printf("the remote stays the source of truth: `cartographer kb clone %s %s` brings it back\n", plan.Origin, name)
	}
	fmt.Println("other machines' client configs are not reached: their stale URLs are served without this KB until they run `cartographer sync`.")
	printPostUnmountGuidanceFn(*configFlag, *restartFlag)
	return 0
}

// planKBUnmount validates the unmount and collects what it affects, writing
// nothing but a `git fetch` (to tell pushed from unpushed honestly).
func planKBUnmount(name, dataDir, configPath string, deleteClone bool) (*unmountPlan, error) {
	if err := validateKBName(name); err != nil {
		return nil, err
	}
	p := &unmountPlan{Name: name, DataDir: dataDir, KBPath: filepath.Join(dataDir, name), EntryIndex: -1, Delete: deleteClone}

	if configPath == "" {
		if resolved, err := service.ConfigPath(); err == nil {
			configPath = resolved
		}
	}
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			cfg, err := config.Load(configPath)
			if err != nil {
				return nil, fmt.Errorf("server config %s cannot be read, refusing to edit it: %w", configPath, err)
			}
			p.ConfigPath = configPath
			i, err := findKBEntry(name, p.KBPath, configPath, cfg)
			if err != nil {
				return nil, err
			}
			p.EntryIndex = i
			if i >= 0 && cfg.KBs[i].Path != "" {
				p.KBPath = cfg.KBs[i].Path
			}
			p.Scopes = scopeRefs(cfg, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(p.KBPath, "data", "index.md")); err != nil {
		return nil, fmt.Errorf("%s is not an OKF KB (no data/index.md): nothing to unmount", p.KBPath)
	}
	p.SigningKey, p.Approvals = clientRefs(name)
	p.Bindings = bindingRefs(name)

	// TRAP: a clone left in the data dir is mounted again by discovery at the
	// next start, kbs[] entry or not (serve.go discoverKBPaths): it must leave
	// the data dir, or the unmount is undone by the next restart.
	if !deleteClone && sameDir(filepath.Dir(filepath.Clean(p.KBPath)), dataDir) {
		p.ParkPath = filepath.Join(dataDir, unmountedDir, filepath.Base(p.KBPath))
		if _, err := os.Lstat(p.ParkPath); err == nil {
			return nil, fmt.Errorf("%s already exists: remove it, or pass --delete-clone", p.ParkPath)
		}
	}

	p.Origin, _ = gitx.RemoteURL(p.KBPath, "origin")
	p.Unsafe = unpushedReasons(p.KBPath, p.Origin)
	return p, nil
}

// unpushedReasons says why the clone cannot be proven fully pushed: a dirty
// tree, commits ahead of upstream, or an upstream that cannot be compared.
func unpushedReasons(dir, origin string) []string {
	var why []string
	if st, err := gitx.Status(dir); err != nil {
		why = append(why, "git status failed: "+err.Error())
	} else if strings.TrimSpace(st) != "" {
		why = append(why, "the working tree has uncommitted changes")
	}
	if origin == "" {
		return append(why, "the clone has no origin remote, so nothing proves its commits exist elsewhere")
	}
	if err := gitx.Fetch(dir, "origin"); err != nil {
		return append(why, "git fetch origin failed: "+err.Error())
	}
	branch, err := gitx.Branch(dir)
	if err != nil || branch == "" {
		return append(why, "the current branch cannot be determined")
	}
	ahead, known, err := gitx.AheadCount(dir, "origin", branch)
	switch {
	case err != nil:
		why = append(why, "comparing with origin/"+branch+" failed: "+err.Error())
	case !known:
		why = append(why, "origin/"+branch+" does not exist, so the branch was never pushed")
	case ahead > 0:
		why = append(why, fmt.Sprintf("%d commit(s) not pushed to origin/%s", ahead, branch))
	}
	return why
}

func printUnmountPreflight(w io.Writer, p *unmountPlan) {
	fmt.Fprintf(w, "unmounting %s (%s)\n", p.Name, p.KBPath)
	switch {
	case p.ConfigPath == "":
		fmt.Fprintln(w, "no server config found")
	case p.EntryIndex < 0:
		fmt.Fprintf(w, "%s has no kbs[] entry for this KB (mounted by discovery)\n", p.ConfigPath)
	default:
		fmt.Fprintf(w, "%s: removing kbs[%d] (a backup is written first)\n", p.ConfigPath, p.EntryIndex)
	}
	for _, b := range p.Bindings {
		fmt.Fprintf(w, "this machine's .cartographer.yaml: removing the KB from the %s binding\n", b)
	}
	switch {
	case p.Delete:
		fmt.Fprintf(w, "deleting the local clone %s\n", p.KBPath)
	case p.ParkPath != "":
		fmt.Fprintf(w, "parking the local clone in %s\n", p.ParkPath)
	}
	if len(p.Scopes) == 0 && !p.SigningKey && !p.Approvals {
		return
	}
	fmt.Fprintf(w, "\nNOT removed by this command — update them yourself:\n")
	for _, s := range p.Scopes {
		fmt.Fprintf(w, "  auth scope in %s → %s\n", p.ConfigPath, s)
	}
	if p.SigningKey {
		fmt.Fprintf(w, "  signing_keys[%q] in this machine's .cartographer.yaml\n", p.Name)
	}
	if p.Approvals {
		fmt.Fprintf(w, "  mcp_approvals[%q] in this machine's .cartographer.yaml\n", p.Name)
	}
}

// applyKBUnmount edits the server config (after a backup), the client
// bindings, then moves or deletes the clone. A failure after the config edit
// restores the config from the backup; the bindings removal is reported, not
// undone (a sync realigns a binding to a KB that is mounted again).
func applyKBUnmount(p *unmountPlan) (backup string, err error) {
	if p.EntryIndex >= 0 {
		backup, err = backupServerConfig(p.ConfigPath)
		if err != nil {
			return "", err
		}
		if err := removeKBEntry(p.ConfigPath, p.EntryIndex); err != nil {
			return backup, err
		}
	}
	restore := func(cause error) error {
		if backup == "" {
			return cause
		}
		if data, rerr := os.ReadFile(backup); rerr == nil {
			if werr := os.WriteFile(p.ConfigPath, data, 0o600); werr == nil {
				return fmt.Errorf("%w (the server config was restored from %s)", cause, backup)
			}
		}
		return fmt.Errorf("%w (and %s could not be restored: copy %s over it)", cause, p.ConfigPath, backup)
	}
	if len(p.Bindings) > 0 {
		if err := removeBindings(p.Name); err != nil {
			return backup, restore(fmt.Errorf("remove the client bindings: %w", err))
		}
	}
	switch {
	case p.Delete:
		if err := os.RemoveAll(p.KBPath); err != nil {
			return backup, restore(fmt.Errorf("delete %s: %w", p.KBPath, err))
		}
	case p.ParkPath != "":
		if err := os.MkdirAll(filepath.Dir(p.ParkPath), 0o755); err != nil {
			return backup, restore(err)
		}
		if err := os.Rename(p.KBPath, p.ParkPath); err != nil {
			return backup, restore(fmt.Errorf("park %s: %w", p.KBPath, err))
		}
	}
	return backup, nil
}

// backupServerConfig copies the config next to itself as
// <name>.bak-<UTC timestamp>, 0600 like the config.
func backupServerConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	backup := path + ".bak-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return "", fmt.Errorf("write the backup %s: %w", backup, err)
	}
	return backup, nil
}

// removeKBEntry deletes the index-th kbs[] item through a yaml.Node document,
// so comments, key order and unmodelled fields survive (as rewriteKBEntry).
func removeKBEntry(path string, index int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if _, err := kbEntryNode(&doc, index); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	kbs := mapValue(doc.Content[0], "kbs")
	kbs.Content = append(kbs.Content[:index], kbs.Content[index+1:]...)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// removeBindings drops name from every client and workspace binding of this
// machine's client config. A list it empties stays an explicit empty binding,
// as `client unbind` of the last KB does.
func removeBindings(name string) error {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return err
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return err
	}
	for p, b := range cfg.Clients {
		if containsString(b.KBs, name) {
			b.KBs = removeFromList(b.KBs, name)
			b.KBsSet = true
			cfg.Clients[p] = b
		}
	}
	for p, ws := range cfg.Workspaces {
		for i := range ws {
			ws[i].KBs = removeFromList(ws[i].KBs, name)
		}
		cfg.Workspaces[p] = ws
	}
	return clientconfig.Save(dir, cfg)
}

func removeFromList(list []string, name string) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

var printPostUnmountGuidanceFn = printPostUnmountGuidance

// printPostUnmountGuidance is printPostCreateGuidance worded for an unmount:
// the running server still serves the KB until it restarts.
func printPostUnmountGuidance(configPath string, restart bool) {
	base := serverBaseURL(configPath)
	before, err := fetchHealth(base)
	if err != nil {
		return
	}
	if !restart {
		fmt.Println("The running service still serves the KB until it restarts: cartographer service restart")
		fmt.Println("On connected client machines, run: cartographer sync")
		return
	}
	fmt.Println("Restarting the service...")
	if err := service.NewManager().Restart(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: restart failed:", err)
		return
	}
	if waitHealthy(base, before.StartedAt) {
		fmt.Println("service healthy")
	} else {
		fmt.Fprintf(os.Stderr, "Warning: service did not report healthy within %s\n", restartWaitTimeout)
	}
	fmt.Println("On connected client machines, run: cartographer sync")
}
