package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// healthCheckTimeout bounds a single GET of /health (kb create's post-create
// guidance, service install's no-KB hint) — generous enough for a same-
// machine loopback call, short enough not to hang the CLI if the local
// service is down.
const healthCheckTimeout = 2 * time.Second

// healthPollInterval/restartWaitTimeout/noKBHintWaitTimeout bound the
// best-effort polling loops in waitHealthy/printNoKBHintIfEmpty: none of
// them gate the command's success (KB creation / service install already
// happened), they only affect what guidance gets printed afterwards.
const (
	healthPollInterval  = 200 * time.Millisecond
	restartWaitTimeout  = 10 * time.Second
	noKBHintWaitTimeout = 3 * time.Second
)

// printPostCreateGuidanceFn indirects printPostCreateGuidance so tests can
// stub it out: it otherwise reaches out over the network (real
// ~/.cartographer.yaml / service config on the machine running the test),
// which a unit test for the scaffold itself has no business doing.
var printPostCreateGuidanceFn = printPostCreateGuidance

// cmdKB dispatches `cartographer kb <subcommand>`.
func cmdKB(args []string) int {
	target, rest := splitPositional(args, "")
	switch target {
	case "create":
		return cmdKBCreate(rest)
	case "clone":
		return cmdKBClone(rest)
	case "list":
		return cmdKBList(rest)
	case "rename":
		return cmdKBRename(rest)
	default:
		fmt.Fprintln(os.Stderr, "Error: usage: cartographer kb create <name> (--remote <url> | --no-remote) [--data <dir>] [--restart]\n       cartographer kb clone <remote> [name] [--data <dir>] [--timeout <d>] [--restart]\n       cartographer kb list [--data <dir>] [--config <path>]\n       cartographer kb rename <old> <new> [--data <dir>] [--config <path>] [--restart]")
		return 2
	}
}

// kbNameRe validates a KB name as directory-safe: letters, digits, '-', '_'
// only — no '/', '.', or whitespace, so the name can never escape
// <dataDir>/<name> (no ".." is even expressible) and matches the kebab-case
// convention already documented for KB names (D53, skillbundle kb-create
// SKILL.md). No stricter/pre-existing validator exists elsewhere in the
// codebase (config.KBSpec.Name is an unvalidated free string) — this is it.
var kbNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateKBName returns an error describing why name is not a usable KB
// name, or nil if it is.
func validateKBName(name string) error {
	if name == "" {
		return fmt.Errorf("KB name must not be empty")
	}
	if !kbNameRe.MatchString(name) {
		return fmt.Errorf("invalid KB name %q: only letters, digits, '-', and '_' are allowed", name)
	}
	return nil
}

// cmdKBCreate implements `cartographer kb create <name> (--remote <url> |
// --no-remote) [--data <dir>] [--restart]`: scaffolds a new KB at
// <data>/<name> via the same kb.Init bootstrap used by `serve --kb <path>
// --init` (git init + OKF layout), attaches its git remote, then prints
// guidance on how to get the server to pick it up (WP2, D85). The remote is
// not optional (D134): a KB without an origin is neither durable nor
// syncable, and nothing downstream surfaces that state — `--no-remote` is the
// explicit opt-out for a throwaway local KB. The data dir resolution mirrors
// `service install`'s: the running service's config YAML `data:` field,
// falling back to defaultDataDir() (~/cartographer-data); --data overrides
// both.
func cmdKBCreate(args []string) int {
	// <name> is a leading positional argument, before the flags (see usage:
	// "kb create <name> (--remote <url> | --no-remote) …") — flag.Parse stops at
	// the first non-flag token, so it must be pulled out first (same
	// splitPositional dance as `service <target>` and `sync <provider>`).
	name, rest := splitPositional(args, "")

	fs := flag.NewFlagSet("kb create", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to read data: from (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	remoteFlag := fs.String("remote", "", "Git remote URL of an empty repository: attached as origin and pushed to")
	noRemoteFlag := fs.Bool("no-remote", false, "Create a local-only KB with no origin (not durable, never synced)")
	restartFlag := fs.Bool("restart", false, "Restart the local service and wait until healthy after creating the KB")
	fs.Parse(rest)

	if name == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb create <name> (--remote <url> | --no-remote) [--data <dir>] [--restart]")
		return 2
	}
	if err := validateKBName(name); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	remote := strings.TrimSpace(*remoteFlag)
	if code := checkRemoteChoice(remote, *noRemoteFlag); code != 0 {
		return code
	}

	if code := checkLocalTarget(*dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}

	path := filepath.Join(dataDir, name)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "Error: %s already exists\n", path)
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	// The identity has to be right before the initial commit exists: it is the
	// commit a forge with an author-membership push rule rejects (D156).
	authorName, authorEmail := resolveCreateIdentity(path)
	if remote != "" && authorEmail == kb.DefaultGitAuthorEmail {
		fmt.Fprintf(os.Stderr, "Warning: commits will be authored as %s; forges with author push rules will reject the push.\n", authorEmail)
		fmt.Fprintln(os.Stderr, "  Set git.author_name/git.author_email in the server config, or git config --global user.email, then retry.")
	}

	// kb.Init creates <path> (and its parents, i.e. dataDir too) itself —
	// no separate MkdirAll(dataDir) needed.
	if _, err := kb.InitWithIdentity(path, authorName, authorEmail); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if remote != "" {
		if code := attachOrigin(path, remote); code != 0 {
			// The scaffold is complete and valid; only the push failed, so
			// deleting it turns a recoverable state into no KB at all (D156).
			printUnpushedScaffoldGuidance(path, authorName, authorEmail, dataDir)
			return code
		}
	}
	fmt.Printf("KB %q created at %s\n", name, path)
	if remote != "" {
		fmt.Printf("origin: %s\n", remote)
	} else {
		printNoRemoteWarning(path)
	}

	printPostCreateGuidanceFn(*configFlag, *restartFlag)
	return 0
}

// resolveCreateIdentity resolves the author of a new KB's initial commit, in
// the same order the server uses when it mounts one (serve.go): the server
// config's git.author_name/git.author_email, then git's own resolution for that
// path, then the product default. Before D156 the CLI path ignored the first two
// and always committed as the product default, which a forge with an
// author-membership push rule rejects.
func resolveCreateIdentity(path string) (name, email string) {
	if cfgPath, err := service.NewManager().EffectiveConfigPath(""); err == nil {
		if cfg, cfgErr := config.Load(cfgPath); cfgErr == nil {
			if cfg.Git.AuthorName != "" && cfg.Git.AuthorEmail != "" {
				return cfg.Git.AuthorName, cfg.Git.AuthorEmail
			}
		}
	}
	if n, e, err := gitx.AuthorIdent(path); err == nil && n != "" && e != "" {
		return n, e
	}
	return kb.DefaultGitAuthorName, kb.DefaultGitAuthorEmail
}

// printUnpushedScaffoldGuidance explains what to do with a KB whose scaffold is
// valid but whose first push failed. The scaffold is deliberately kept (D156):
// the local work is correct and re-doing it by hand was the reported cost.
func printUnpushedScaffoldGuidance(path, authorName, authorEmail, dataDir string) {
	fmt.Fprintf(os.Stderr, "\nThe KB at %s is complete and valid; only the push failed. Finish it with:\n", path)
	fmt.Fprintf(os.Stderr, "  git -C %s commit --amend --author %q   # if the forge rejected the author\n",
		path, fmt.Sprintf("%s <%s>", authorName, authorEmail))
	fmt.Fprintf(os.Stderr, "  git -C %s push -u origin %s\n", path, gitx.DefaultBranch)
	fmt.Fprintf(os.Stderr, "Or remove it: rm -rf %s   (a server started with data: %s would otherwise mount it)\n", path, dataDir)
}

// checkRemoteChoice validates the --remote/--no-remote pair, which is a
// deliberate choice the caller has to make (D134): it returns 0 when exactly
// one of them is set, and the usage exit code (2) otherwise, after printing
// the three ways to get a KB.
func checkRemoteChoice(remote string, noRemote bool) int {
	if remote != "" && noRemote {
		fmt.Fprintln(os.Stderr, "Error: --remote and --no-remote are mutually exclusive")
		return 2
	}
	if remote == "" && !noRemote {
		fmt.Fprintln(os.Stderr, "Error: a KB needs a git remote — that is what makes it durable and syncable.")
		fmt.Fprintln(os.Stderr, "  cartographer kb create <name> --remote <url>   empty repository, becomes this KB's origin")
		fmt.Fprintln(os.Stderr, "  cartographer kb clone <remote>                 repository that already holds a KB")
		fmt.Fprintln(os.Stderr, "  cartographer kb create <name> --no-remote      local-only KB (not durable, never synced)")
		return 2
	}
	return 0
}

// attachOrigin wires the freshly scaffolded KB at path to remote as "origin"
// and pushes its initial commit with upstream tracking, so the KB is
// reconstructible from its repository the moment it exists. Returns the exit
// code to use (0 on success); on failure the caller removes the scaffold.
func attachOrigin(path, remote string) int {
	if err := gitx.AddRemote(path, "origin", remote); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	branch, err := gitx.Branch(path)
	if err != nil || branch == "" {
		branch = gitx.DefaultBranch
	}
	if err := gitx.PushSetUpstream(path, "origin", branch); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintln(os.Stderr, "Hint: authenticate git with your SSH agent or credential helper, then retry.")
		fmt.Fprintf(os.Stderr, "Hint: if the remote already holds a KB, mount it instead: cartographer kb clone %s\n", remote)
		return 1
	}
	return 0
}

// printNoRemoteWarning states what --no-remote costs, on stderr: the KB is a
// purely local git repository, so it has no backup and every sync path is
// inert for it (kb.hasRemote, CARTOGRAPHER_GIT_SYNC).
func printNoRemoteWarning(path string) {
	fmt.Fprintln(os.Stderr, "Warning: this KB has no origin — it is not backed up and will never sync.")
	fmt.Fprintf(os.Stderr, "  Attach one later with: git -C %s remote add origin <url> && git -C %s push -u origin %s\n",
		path, path, gitx.DefaultBranch)
}

// cmdKBClone implements `cartographer kb clone <remote> [name] [--data <dir>]
// [--restart]`: mounts a remote KB in the local service data directory. A
// failed clone or a clone that is not an OKF KB is removed, so auto-discovery
// never mistakes an unusable directory for a mountable KB.
func cmdKBClone(args []string) int {
	remote, rest := splitPositional(args, "")
	name, rest := splitPositional(rest, "")

	fs := flag.NewFlagSet("kb clone", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to read data: from (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	timeoutFlag := fs.Duration("timeout", defaultCloneTimeout, "Time budget for the whole clone")
	restartFlag := fs.Bool("restart", false, "Restart the local service and wait until healthy after mounting the KB")
	fs.Parse(rest)

	if remote == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb clone <remote> [name] [--data <dir>] [--restart]")
		return 2
	}
	if name == "" {
		name = remoteKBName(remote)
	}
	if err := validateKBName(name); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	if code := checkLocalTarget(*dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}
	path := filepath.Join(dataDir, name)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "Error: %s already exists\n", path)
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	// path did not exist a moment ago (checked above), so anything under it
	// now is this command's doing: the cleanup below removes only what we
	// created, never a directory that was already there.
	cloned := false
	defer func() {
		if !cloned {
			_ = os.RemoveAll(path)
		}
	}()

	// An interrupt must reach git, not just this process: cancelling the
	// context kills the child, and Clone returns only once it has exited —
	// removing a tree a running git is still writing produces a second,
	// more confusing failure.
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()
	stop := onInterrupt(cancel)
	defer stop()

	if err := gitx.Clone(ctx, remote, path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		if errors.Is(err, gitx.ErrCloneTimeout) {
			fmt.Fprintf(os.Stderr, "Hint: the clone exceeded %s — raise the budget with --timeout, or check the remote is reachable.\n", *timeoutFlag)
		}
		return 1
	}
	if _, err := kb.Open(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error: not an OKF KB — import it with the kb-import skill, push, then retry")
		return 1
	}

	cloned = true
	fmt.Printf("KB %q mounted at %s\n", name, path)
	printPostCreateGuidanceFn(*configFlag, *restartFlag)
	return 0
}

// kbRow is one direct subdirectory of the data dir, as `kb list` sees it.
// Mounted is a pointer because "the server said no" and "the server could not
// be asked" are different answers, and printing the second as the first would
// be a lie about what is running.
type kbRow struct {
	Name    string
	IsRepo  bool
	IsKB    bool
	Origin  string
	Mounted *bool
}

// cmdKBList implements `cartographer kb list [--data <dir>] [--config
// <path>]`: what is on disk, and which of it the server actually serves.
// Those are different questions and nothing else on the CLI answers either.
//
// The command writes NOTHING — no directory creation (unlike `serve`, a
// missing data dir is reported, not created) and no kb.Open, which
// self-migrates the git-exclude entry of every repository it touches. A
// listing command that mutates what it lists is not a listing command (D173).
func cmdKBList(args []string) int {
	fs := flag.NewFlagSet("kb list", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to read data: and http: from (default: the standard path)")
	fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb list [--data <dir>] [--config <path>]")
		return 2
	}

	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}
	rows, err := scanDataDir(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	mounted, mountErr := mountedKBNames(serverBaseURL(*configFlag))
	if mountErr == nil {
		for i := range rows {
			m := mounted[rows[i].Name]
			rows[i].Mounted = &m
		}
	}

	printKBRows(os.Stdout, dataDir, rows, mountErr)
	return 0
}

// scanDataDir reads one level of dataDir, describing each subdirectory
// without touching it. A KB is recognised by data/index.md, which is exactly
// what kb.Open checks — read directly here so the listing stays read-only.
func scanDataDir(dataDir string) ([]kbRow, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, fmt.Errorf("read data dir %q: %w", dataDir, err)
	}
	var rows []kbRow
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dataDir, e.Name())
		row := kbRow{Name: e.Name(), IsRepo: gitx.IsRepo(path)}
		if _, err := os.Stat(filepath.Join(path, "data", "index.md")); err == nil {
			row.IsKB = true
		}
		if row.IsRepo {
			if url, err := gitx.RemoteURL(path, "origin"); err == nil {
				row.Origin = strings.TrimSpace(url)
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// mountedKBNames asks the server which KBs it serves. An unreachable server
// is an error the caller reports as "could not ask", never as "nothing is
// mounted": absence of the signal is not evidence.
func mountedKBNames(baseURL string) (map[string]bool, error) {
	h, err := fetchHealth(baseURL)
	if err != nil {
		return nil, err
	}
	if h.KBs == nil {
		return nil, fmt.Errorf("server at %s does not report mounted KBs (single-KB server)", baseURL)
	}
	names := map[string]bool{}
	for _, kb := range *h.KBs {
		names[kb.Name] = true
	}
	return names, nil
}

// printKBRows renders the listing. The MOUNTED column exists only when the
// server answered.
func printKBRows(w io.Writer, dataDir string, rows []kbRow, mountErr error) {
	fmt.Fprintf(w, "data dir: %s\n", dataDir)
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no KB directories)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if mountErr == nil {
		fmt.Fprintln(tw, "NAME\tOKF\tGIT\tMOUNTED\tORIGIN")
	} else {
		fmt.Fprintln(tw, "NAME\tOKF\tGIT\tORIGIN")
	}
	for _, r := range rows {
		origin := r.Origin
		if origin == "" {
			origin = "-"
		}
		if mountErr == nil {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, yesNo(r.IsKB), yesNo(r.IsRepo), yesNo(r.Mounted != nil && *r.Mounted), origin)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, yesNo(r.IsKB), yesNo(r.IsRepo), origin)
	}
	tw.Flush()
	if mountErr != nil {
		fmt.Fprintf(w, "\nthe server could not be asked which of these are mounted: %v\n", mountErr)
	}
}

// resolveServerDataDir mirrors the data-dir precedence of `service install`
// (service.go:defaultDataDir, config.Load's Data field): the local service's
// config YAML `data:` field if the config exists and sets one, otherwise
// defaultDataDir() (~/cartographer-data).
//
// configPath selects which server config to read, mirroring `service
// install --config` / `service status --config`; empty means the standard
// path. Without it, a service installed at a custom config path was
// invisible here, and `kb create`/`kb clone` reported "mounted" about a
// directory no running server reads (D173).
func resolveServerDataDir(configPath string) string {
	if configPath == "" {
		p, err := service.ConfigPath()
		if err != nil {
			return defaultDataDir()
		}
		configPath = p
	}
	if cfg, err := config.Load(configPath); err == nil && cfg.Data != "" {
		return cfg.Data
	}
	return defaultDataDir()
}

// defaultCloneTimeout bounds `kb clone` end to end. Generous for a KB-sized
// repository over a slow link, short enough that an unreachable forge fails
// while the operator is still watching. --timeout overrides it; unbounded is
// not an option, since the failure it produces is a silent hang (D173).
const defaultCloneTimeout = 120 * time.Second

// checkLocalTarget refuses a `kb create`/`kb clone` that would act on the
// local data dir while this machine's client points at a remote server.
//
// These commands mount a KB on the LOCAL server, which on such a machine is
// not the server anyone is talking to: the command would report `KB "x"
// mounted at ...` about a directory nothing reads. That is worse than an
// error, so it is an error — with --local as the explicit opt-out, and no
// opinion at all when --data was passed (the caller named the target) or when
// there is no client config to contradict.
func checkLocalTarget(dataFlag string, local bool) int {
	if local || dataFlag != "" {
		return 0
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return 0
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || cfg.ServerURL == "" || isLoopbackURL(cfg.ServerURL) {
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: `kb create`/`kb clone` act on the LOCAL server's data dir, but this machine's client points at %s\n", cfg.ServerURL)
	fmt.Fprintln(os.Stderr, "  Mounting a KB on that server is an operation on its deployment, not something this command can do.")
	fmt.Fprintln(os.Stderr, "  Use --local to act on the local data dir anyway, or --data <dir> to name the target explicitly.")
	return 2
}

// onInterrupt runs fn on SIGINT/SIGTERM and returns a stop function that
// releases the handler. Registering it replaces Go's default of exiting
// immediately, which is what left a half-written clone on disk: the deferred
// cleanup never ran.
func onInterrupt(fn func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		if _, ok := <-ch; ok {
			fn()
		}
	}()
	return func() { signal.Stop(ch); close(ch) }
}

// healthInfo is the subset of the /health JSON body kb create's guidance
// and service install's no-KB hint care about. KBs is a pointer so hasNoKBs
// can tell "field absent" (pre-D84 shape, or a single-KB server, which never
// has a kbs field at all) from "present and empty" (MultiKB server, no
// subdir mounted) — the two cases need different fallbacks. The "ready"
// field (D84) is intentionally not decoded here: both a pre-D84 and a
// post-D84 server answer this struct correctly, since a missing field just
// leaves KBs nil.
type healthInfo struct {
	KBs *[]healthKB `json:"kbs"`
}

// healthKB is one entry of /health's kbs array. Only the name is decoded:
// `kb list` needs it to say which directories the server actually serves.
type healthKB struct {
	Name string `json:"name"`
}

// hasNoKBs reports whether the health response indicates zero KBs mounted.
// If the kbs field is present, its length decides. If it's absent
// (single-KB server shape, or a pre-D84 server that predates the field),
// fall back to checking dataDir directly — this is the "absent+data-dir
// empty" half of the WP2 spec.
func (h *healthInfo) hasNoKBs(dataDir string) bool {
	if h.KBs != nil {
		return len(*h.KBs) == 0
	}
	entries, err := discoverKBPaths(dataDir)
	return err == nil && len(entries) == 0
}

// fetchHealth GETs <baseURL>/health and decodes the fields this package
// cares about (hasNoKBs). Returns an error if the server is unreachable or
// does not respond 200 — every caller here treats that as "can't tell, stay
// silent" rather than a hard failure: this guidance is always best-effort
// and never blocks `kb create`/`service install`'s own success.
func fetchHealth(baseURL string) (*healthInfo, error) {
	client := http.Client{Timeout: healthCheckTimeout}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s/health: status %d", baseURL, resp.StatusCode)
	}
	var h healthInfo
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

// serverBaseURL resolves the local server's base HTTP URL (no /mcp or
// /health suffix). The authoritative source is the service config YAML's
// `http:` field (service.ConfigPath, D85) — this is what `serve` actually
// binds to, and is known even before `cartographer connect` has ever run.
// If that config doesn't exist or has no http: set (stdio-only, or not
// installed as a service yet), fall back to deriving it the way the client
// does: .cartographer.yaml server_url, defaulting to
// the local client default (clientconfig.Default), with the /mcp path
// stripped.
func serverBaseURL(configPath string) string {
	if configPath == "" {
		if p, err := service.ConfigPath(); err == nil {
			configPath = p
		}
	}
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil && cfg.HTTP != "" {
			if url := httpAddrToBaseURL(cfg.HTTP); url != "" {
				return url
			}
		}
	}

	serverURL := clientconfig.Default().ServerURL
	if dir, err := clientconfig.TargetDir(); err == nil {
		if cfg, err := clientconfig.Load(dir); err == nil {
			serverURL = cfg.ServerURL
		}
	}
	return strings.TrimSuffix(serverURL, "/mcp")
}

// httpAddrToBaseURL turns a server http listen address (e.g. ":39273" or
// "127.0.0.1:39273") into a base URL ("http://127.0.0.1:39273"), normalizing a
// bare port to loopback the same way internal/service's healthURL does.
// Returns "" if addr does not parse as host:port.
func httpAddrToBaseURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// printPostCreateGuidance is WP2's post-create half: if a local server
// answers /health, tell the user how to get the new KB mounted — either the
// plain hint (`cartographer service restart`) or, with restart=true
// (--restart), actually restart it and wait for it to become healthy again.
// If no server answers at all, stay silent: there is nothing running to
// restart, and the user is presumably still mid-setup (e.g. using the KB
// with `serve --kb <path>` directly, no service involved).
func printPostCreateGuidance(configPath string, restart bool) {
	base := serverBaseURL(configPath)
	if _, err := fetchHealth(base); err != nil {
		return
	}

	if !restart {
		fmt.Println("Restart the service to mount it: cartographer service restart")
		fmt.Println("On connected client machines, run: cartographer sync")
		return
	}

	fmt.Println("Restarting the service...")
	if err := service.NewManager().Restart(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: restart failed:", err)
		return
	}
	if waitHealthy(base) {
		fmt.Println("service healthy")
	} else {
		fmt.Fprintf(os.Stderr, "Warning: service did not report healthy within %s\n", restartWaitTimeout)
	}
}

// waitHealthy polls <baseURL>/health until it responds or
// restartWaitTimeout elapses, returning whether it became healthy in time.
func waitHealthy(baseURL string) bool {
	deadline := time.Now().Add(restartWaitTimeout)
	for {
		if _, err := fetchHealth(baseURL); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(healthPollInterval)
	}
}

// printNoKBHintIfEmpty is WP2's post-install half, called by
// cmdServiceInstall after a successful `service install`: it waits (briefly)
// for the just-(re)started service to answer /health, and if it reports
// zero KBs mounted (hasNoKBs), prints a hint pointing at `kb create`.
// dataDir is the data directory Install resolved (opts.DataDir as passed,
// or the pre-existing config's data:) — used by hasNoKBs's fallback when
// the kbs field itself is absent. Best-effort throughout: never returns an
// error, never affects service install's own exit code.
func printNoKBHintIfEmpty(configPath, dataDir string) {
	base := serverBaseURL(configPath)
	deadline := time.Now().Add(noKBHintWaitTimeout)
	var h *healthInfo
	var err error
	for {
		h, err = fetchHealth(base)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(healthPollInterval)
	}
	if h.hasNoKBs(dataDir) {
		fmt.Println("no KB mounted yet — create one with: cartographer kb create <name>")
	}
}
