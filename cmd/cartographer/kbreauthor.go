package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// reauthorLockTimeout bounds the wait for the KB's advisory lock: an operator
// command that finds the server in the middle of a git section tries again
// shortly, it does not queue behind a long one.
var reauthorLockTimeout = 15 * time.Second

type reauthorCommit struct {
	SHA       string `json:"sha"`
	Author    string `json:"author"`
	Committer string `json:"committer"`
	Subject   string `json:"subject"`
	Rewrite   bool   `json:"rewrite"`
}

type reauthorReport struct {
	KB       string           `json:"kb"`
	Branch   string           `json:"branch"`
	Upstream string           `json:"upstream"`
	Identity string           `json:"identity"`
	Commits  []reauthorCommit `json:"commits"`
	Applied  bool             `json:"apply"`
	// Rewritten is how many commits --apply replaced; Pushed says whether the
	// push that followed succeeded, and Error is why not (both explicit, so
	// stdout alone tells a rewrite that worked from a push that did not).
	Rewritten int    `json:"rewritten"`
	Pushed    bool   `json:"pushed"`
	Error     string `json:"error,omitempty"`
}

// cmdKBReauthor implements `cartographer kb reauthor <kb> [--apply] [--json]
// [--data <dir>] [--config <path>] [--local]` (D1063): it re-authors, under the
// identity the server file configures for the KB, the commits that exist only
// in the local clone, then pushes. Like kb unmount it is offline and local
// (D177): it needs this machine's server config, the clone and operator
// authority, none of which an MCP token has. Exit codes: 0 done or nothing to
// do, 2 refusal or error.
func cmdKBReauthor(args []string) int {
	name, rest := splitPositional(args, "")
	fs := flag.NewFlagSet("kb reauthor", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	applyFlag := fs.Bool("apply", false, "Rewrite the commits and push (default: preview only)")
	jsonFlag := fs.Bool("json", false, "Print the report as JSON")
	fs.Parse(rest)
	if name == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb reauthor <kb> [--apply] [--json] [--data <dir>] [--config <path>]")
		return 2
	}
	if code := checkLocalTarget(*dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}
	return runKBReauthor(name, dataDir, *configFlag, *applyFlag, *jsonFlag, os.Stdout, os.Stderr)
}

func runKBReauthor(name, dataDir, configPath string, apply, asJSON bool, out, errOut io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errOut, "Error: "+format+"\n", a...)
		return 2
	}
	if err := validateKBName(name); err != nil {
		return fail("%v", err)
	}
	cfg, err := loadReauthorConfig(configPath)
	if err != nil {
		return fail("%v", err)
	}
	idName, idEmail, ok := cfg.IdentityFor(name)
	if !ok {
		return fail("no git identity is configured for %q: set `git.identity` (or `git.identities`) in the server file, "+
			"then run this again. Re-authoring with the fallback identity would reproduce the problem", name)
	}
	root := filepath.Join(dataDir, name)
	k, err := kb.Open(root)
	if err != nil {
		return fail("%s is not an OKF KB: %v", root, err)
	}
	k.GitEnv = gitEnvForKB(name, cfg.CredentialsPath())
	k.SetDefaultIdentity(idName, idEmail)

	release, err := k.AcquireProcessLock(reauthorLockTimeout, "cartographer kb reauthor")
	if err != nil {
		return fail("%v", err)
	}
	defer release()

	rep, code := planKBReauthor(k, name, idName, idEmail, errOut)
	if code != 0 {
		return code
	}
	rep.Applied = apply
	rewrites := 0
	for _, c := range rep.Commits {
		if c.Rewrite {
			rewrites++
		}
	}

	if apply && len(rep.Commits) > 0 {
		if rewrites > 0 {
			done, err := gitx.ReauthorCommits(root, rep.Upstream, idName, idEmail, k.GitEnv...)
			if err != nil {
				return fail("the rewrite changed nothing: %v", err)
			}
			rep.Rewritten = len(done)
			fmt.Fprintf(errOut, "re-authored %d commit(s) as %s (recorded in the branch reflog)\n", len(done), rep.Identity)
		}
		// Commits that already carry the identity are pushed too: the push
		// may be what still fails, and the next write would not retry it
		// before this command's answer is read.
		if err := k.SyncOut(); err != nil {
			rep.Error = err.Error()
			printReauthorReport(out, rep, asJSON)
			if rewrites > 0 {
				return fail("the commits were re-authored but the push failed: %v", err)
			}
			return fail("the push failed: %v", err)
		}
		rep.Pushed = true
	}
	printReauthorReport(out, rep, asJSON)
	if asJSON {
		return 0
	}
	switch {
	case len(rep.Commits) == 0:
		fmt.Fprintln(out, "nothing to re-author: no commit is ahead of the upstream")
	case !apply && rewrites == 0:
		fmt.Fprintf(out, "%s ahead already carry the configured identity: --apply will push them without rewriting anything\n", plural(len(rep.Commits), "commit"))
	case !apply:
		fmt.Fprintf(out, "%d commit(s) would be re-authored: run again with --apply to rewrite them and push\n", rewrites)
	default:
		fmt.Fprintln(out, "pushed. A running service recomputes its sync status at its next sync or write: no restart is needed")
	}
	return 0
}

// loadReauthorConfig reads the server file; a missing one is an empty config,
// which has no identity and so makes the command refuse with the instruction.
func loadReauthorConfig(configPath string) (*config.Config, error) {
	if configPath == "" {
		if resolved, err := service.ConfigPath(); err == nil {
			configPath = resolved
		}
	}
	if configPath == "" {
		return &config.Config{}, nil
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return &config.Config{}, nil
	} else if err != nil {
		return nil, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("server config %s cannot be read: %w", configPath, err)
	}
	return cfg, nil
}

// planKBReauthor proves the commits ahead exist nowhere else and lists them. A
// refusal is printed and returned as exit code 2. It fetches first: the "ahead"
// set is proven against the remote, never against a stale ref.
func planKBReauthor(k *kb.KB, name, idName, idEmail string, errOut io.Writer) (*reauthorReport, int) {
	refuse := func(format string, a ...any) (*reauthorReport, int) {
		fmt.Fprintf(errOut, "Error: refusing to re-author %q: %s\n", name, fmt.Sprintf(format, a...))
		return nil, 2
	}
	root := k.Root
	if !gitx.IsRepo(root) {
		return refuse("%s is not a git repository", root)
	}
	if origin, err := gitx.RemoteURL(root, "origin"); err != nil || strings.TrimSpace(origin) == "" {
		return refuse("the clone has no origin remote")
	}
	if kind, present := gitx.RebaseInProgress(root); present {
		return refuse("a git %s state is in progress in the clone: finish or abort it first", kind)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); err == nil {
		return refuse("a merge is in progress in the clone: finish or abort it first")
	}
	branch, _ := gitx.Branch(root)
	if branch == "" {
		return refuse("HEAD is detached")
	}
	if err := gitx.Fetch(root, "origin", k.GitEnv...); err != nil {
		return refuse("git fetch origin failed, so what is only local cannot be proven: %v", err)
	}
	if !gitx.RemoteBranchExists(root, "origin", branch) {
		return refuse("origin/%s does not exist: the branch was never pushed, there is no upstream to compare with", branch)
	}
	upstream := "origin/" + branch
	_, behind, _, err := gitx.AheadBehind(root, "origin", branch)
	if err != nil {
		return refuse("%v", err)
	}
	if behind > 0 {
		return refuse("the branch has diverged from %s (%d commit(s) behind): let the server sync first", upstream, behind)
	}
	if err := gitx.CheckLinear(root, upstream); err != nil {
		if errors.Is(err, gitx.ErrNotLinear) {
			return refuse("the commits ahead of %s hold a merge or a root commit, which cannot be rewritten", upstream)
		}
		return refuse("%v", err)
	}
	inRange, unpublished, err := gitx.UnpublishedCommitCount(root, upstream)
	if err != nil {
		return refuse("%v", err)
	}
	if inRange != unpublished {
		return refuse("%d of the commits ahead of %s already exist on another remote ref: published history is never rewritten", inRange-unpublished, upstream)
	}
	commits, err := gitx.LocalCommits(root, upstream)
	if err != nil {
		return refuse("%v", err)
	}
	rep := &reauthorReport{KB: name, Branch: branch, Upstream: upstream, Identity: idName + " <" + idEmail + ">", Commits: []reauthorCommit{}}
	for _, c := range commits {
		rep.Commits = append(rep.Commits, reauthorCommit{
			SHA:       shortSHA(c.SHA),
			Author:    c.Author + " <" + c.AuthorEmail + ">",
			Committer: c.Committer + " <" + c.CommitterEmail + ">",
			Subject:   c.Subject,
			Rewrite:   c.Author != idName || c.AuthorEmail != idEmail || c.Committer != idName || c.CommitterEmail != idEmail,
		})
	}
	return rep, 0
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func printReauthorReport(w io.Writer, rep *reauthorReport, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}
	fmt.Fprintf(w, "%s: %s ahead of %s, identity %s\n", rep.KB, plural(len(rep.Commits), "commit"), rep.Upstream, rep.Identity)
	for _, c := range rep.Commits {
		mark := " "
		if c.Rewrite {
			mark = "*"
		}
		fmt.Fprintf(w, "  %s %s  author %s  committer %s  %s\n", mark, c.SHA, c.Author, c.Committer, c.Subject)
	}
	fmt.Fprintln(w, "(* = the author or the committer changes)")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
