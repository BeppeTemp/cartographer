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
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/defaults"
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
	if len(args) == 1 && isHelpArg(args[0]) {
		printKBUsage(os.Stdout)
		return 0
	}
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
	case "repair":
		return cmdKBRepair(rest)
	case "reauthor":
		return cmdKBReauthor(rest)
	case "unmount":
		return cmdKBUnmount(rest)
	default:
		fmt.Fprint(os.Stderr, "Error: ")
		printKBUsage(os.Stderr)
		return 2
	}
}

func printKBUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: cartographer kb create <name> (--remote <url> [--branch <name>] | --no-remote) [--readme generate|keep] [--data <dir>] [--restart]\n       cartographer kb clone <remote> [name] [--branch <name>] [--data <dir>] [--config <path>] [--timeout <d>] [--restart]\n       cartographer kb list [--data <dir>] [--config <path>] [--json]\n       cartographer kb rename <old> <new> [--data <dir>] [--config <path>] [--restart]\n       cartographer kb repair <kb> [--apply] [--json]\n       cartographer kb reauthor <kb> [--apply] [--json] [--data <dir>] [--config <path>]\n       cartographer kb unmount <name> [--delete-clone] [--force] [--dry-run] [--data <dir>] [--config <path>] [--restart]")
}

// validateKBName returns an error describing why name is not a usable KB
// name, or nil if it is. The rule lives in internal/config, which also checks
// the names of the server state file's aliases (D772, D906).
func validateKBName(name string) error { return config.ValidateKBName(name) }

// cmdKBCreate implements `cartographer kb create <name> (--remote <url> |
// --no-remote) [--data <dir>] [--restart]`: scaffolds a new KB at
// <data>/<name> via kb.Init (git init + OKF layout), attaches its git remote, then prints
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
	var name string

	fs := flag.NewFlagSet("kb create", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to read data: from (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	remoteFlag := fs.String("remote", "", "Git remote URL of an empty repository, or one holding only a forge's initial commit (README/LICENSE/.gitignore), whose files are kept (a recognised boilerplate README.md is replaced, see --readme): attached as origin and pushed to; a repository with history needs --branch, which puts the KB on an orphan branch of its own")
	noRemoteFlag := fs.Bool("no-remote", false, "Create a local-only KB with no origin (not durable, never synced)")
	readmeFlag := fs.String("readme", "", "What to do with the README.md of a forge's initial commit: generate (replace it with a short generated one) or keep (leave it); default replaces only a recognised forge boilerplate")
	branchFlag := fs.String("branch", "", "Branch the KB lives on, written as `branch` in its settings.yaml (the team's default, read from the remote's default branch) and pushed after it; on a repository with history and no such branch, the KB becomes an orphan branch X and only X is pushed (kbs[].branch is recorded in the server config, which must exist); needs --remote")
	restartFlag := fs.Bool("restart", false, "Restart the local service and wait until healthy after creating the KB")
	if code := bindPositionals("kb create", parseFlagsAnywhere(fs, args), &name); code != 0 {
		return code
	}

	if name == "" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb create <name> (--remote <url> [--branch <name>] | --no-remote) [--readme generate|keep] [--data <dir>] [--restart]")
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
	readme := strings.TrimSpace(*readmeFlag)
	if code := checkReadmeChoice(readme, *noRemoteFlag); code != 0 {
		return code
	}
	branch, code := checkBranchFlag(fs, *branchFlag)
	if code != 0 {
		return code
	}
	if branch != "" && *noRemoteFlag {
		fmt.Fprintln(os.Stderr, "Error: --branch needs --remote: a local-only KB never syncs, so the branch it writes to means nothing")
		return 2
	}
	// Refuse an option-like or transport-helper remote before the scaffold
	// exists, so nothing is left behind.
	if remote != "" {
		if err := gitx.ValidateRemote(remote); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}

	if code := checkLocalTarget("kb create", *dataFlag, *localFlag); code != 0 {
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

	// `branch` counts only in the settings.yaml committed on the remote's
	// default branch (D903), so the scaffold carries it and the default branch
	// is pushed first; the branch itself is created from it afterwards. A branch
	// that is the one the first push lands on needs no key at all.
	wantBranch := branch
	if branch != "" && branch == remoteTargetBranch(remote) {
		branch = ""
	}
	settingsFile := newKBSettingsFile(branch)

	// The author has to be right before the initial commit exists: it is the
	// commit a forge with an author-membership push rule rejects (D156). git
	// resolves an identity only inside an existing repository (a missing
	// directory fails `git -C`, and an includeIf gitdir: matches only there),
	// so the repository is created first (#1464).
	if err := os.MkdirAll(path, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	if err := gitx.Init(path); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		_ = os.RemoveAll(path)
		return 1
	}
	authorName, authorEmail, fallbackIdentity := resolveOperatorIdentity(path)
	if fallbackIdentity && remote != "" {
		writeFallbackIdentityWarning(os.Stderr, authorName, authorEmail, dataDir)
	}

	// kb.Init creates <path> (and its parents, i.e. dataDir too) itself —
	// no separate MkdirAll(dataDir) needed.
	var originBranch string
	var adopted []string
	var replacedReadme, orphan bool
	var orphanConfig string
	if _, err := kb.InitWithSettings(path, authorName, authorEmail, settingsFile); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		// A failed initial commit stops here, before the push: pushing an
		// unborn branch only yields "src refspec main does not match any",
		// which names neither the commit nor its cause (D265).
		if _, statErr := os.Stat(path); statErr == nil {
			fmt.Fprintf(os.Stderr, "Hint: git refused the KB's initial commit; the git message above names why. Fix it, then remove the partial scaffold (%s) and retry.\n", removeDirCommand(runtime.GOOS, path))
		}
		return 1
	}
	if remote != "" {
		res := attachOrigin(path, remote, name, readme, authorName, authorEmail, fallbackIdentity, wantBranch, *configFlag)
		if res.code != 0 {
			if res.discard {
				// Nothing was pushed and the next step is a rerun of this
				// command, which an existing directory would block (D710).
				_ = os.RemoveAll(path)
				return res.code
			}
			// The scaffold is complete and valid; only the push failed, so
			// deleting it turns a recoverable state into no KB at all (D156).
			printUnpushedScaffoldGuidance(path, res.branch, fallbackIdentity, dataDir)
			return res.code
		}
		originBranch, adopted, replacedReadme = res.branch, res.adopted, res.replacedReadme
		orphan, orphanConfig = res.orphan, res.configPath
		if !res.orphan && branch != "" && branch != originBranch {
			if err := pushKBBranch(path, branch); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				printUnpushedScaffoldGuidance(path, originBranch, fallbackIdentity, dataDir)
				fmt.Fprintf(os.Stderr, "The default branch %s is on the remote with settings.yaml; then create and push the KB's branch:\n  git -C %s checkout -b %s && git -C %s push -u origin %s\n", originBranch, hintQuote(runtime.GOOS, path), hintQuote(runtime.GOOS, branch), hintQuote(runtime.GOOS, path), hintQuote(runtime.GOOS, branch))
				return 1
			}
		}
	}
	fmt.Printf("KB %q created at %s\n", name, path)
	if remote != "" {
		if orphan && readme != "" {
			fmt.Printf("note: --readme %s has no effect: an orphan branch adopts nothing from the repository\n", readme)
		}
		if len(adopted) > 0 {
			replaced := ""
			if replacedReadme {
				replaced = "; replaced README.md"
			}
			fmt.Printf("origin: %s (adopted the forge's initial commit on %s: %s%s)\n", gitx.RedactRemoteURL(remote), originBranch, strings.Join(adopted, ", "), replaced)
		} else {
			fmt.Printf("origin: %s\n", gitx.RedactRemoteURL(remote))
		}
		if orphan {
			fmt.Printf("branch: %s (an orphan branch: it shares no history with the repository, whose other branches were not touched; kbs[].branch is recorded in %s)\n", originBranch, orphanConfig)
		} else if branch != "" {
			fmt.Printf("branch: %s (the KB's own setting; the default branch %s carries the settings.yaml that names it)\n", branch, originBranch)
		}
	} else {
		printNoRemoteWarning(path)
	}

	printPostCreateGuidanceFn(*configFlag, *restartFlag)
	return 0
}

// operatorIdentity is the author of a commit a command makes directly in a
// local KB at path (a new KB's initial commit, import --commit): the operator
// running it is its writer, so git's own resolution for that path (the
// per-folder identity included), then the server identity on a machine with
// none (D904, D156). It is the committer of those commits too (D950).
func operatorIdentity(path string) (name, email string) {
	name, email, _ = resolveOperatorIdentity(path)
	return name, email
}

// resolveOperatorIdentity is operatorIdentity that also says whether the
// server identity was the fallback. path has to be an existing repository.
func resolveOperatorIdentity(path string) (name, email string, fallback bool) {
	if n, e, err := gitx.AuthorIdent(path); err == nil && n != "" && e != "" {
		return n, e, false
	}
	n, e := kb.ServerIdentity()
	return n, e, true
}

// writeFallbackIdentityWarning tells the operator, before the initial commit,
// that git has no identity for the KB and which author it will carry (#1463).
// It warns and does not stop: both routes are named, the second being the
// three settings an operator who wants an identity for the KBs only needs.
func writeFallbackIdentityWarning(w io.Writer, name, email, dataDir string) {
	fmt.Fprintf(w, "Warning: git has no identity for this folder, so the initial commit is authored as %s <%s>.\n", name, email)
	fmt.Fprintln(w, "  A forge with an author-membership push rule refuses that author. Set one it knows first:")
	fmt.Fprintln(w, "  - everywhere: git config --global user.name \"<name>\" && git config --global user.email \"<an email of your forge account>\"")
	fmt.Fprintf(w, "  - for the KBs only, the same \"Name <email>\" in three places:\n")
	fmt.Fprintf(w, "      1. an includeIf \"gitdir/i:%s/\" in your global git config (commits made here by kb create and setup)\n", filepath.ToSlash(dataDir))
	fmt.Fprintln(w, "      2. git.identities.default in the server file (commits the server makes with no writer)")
	fmt.Fprintln(w, "      3. knowledge_bases.default.identity in .cartographer.yaml (writes through an HTTP entry)")
}

// printUnpushedScaffoldGuidance explains what to do with a KB whose scaffold is
// valid but whose first push failed. The scaffold is deliberately kept (D156):
// the local work is correct and re-doing it by hand was the reported cost.
func printUnpushedScaffoldGuidance(path, branch string, fallbackIdentity bool, dataDir string) {
	writeUnpushedScaffoldGuidance(os.Stderr, runtime.GOOS, path, branch, fallbackIdentity, dataDir)
}

// writeUnpushedScaffoldGuidance takes goos so the Windows wording is testable
// on any host.
//
// fallbackIdentity is true when the initial commit carries the server identity
// because git had none. Re-authoring is then the way out, once an identity the
// forge knows is set; with the operator's own identity amending to the same
// author never helps, so the line is left out (#1461).
func writeUnpushedScaffoldGuidance(w io.Writer, goos, path, branch string, fallbackIdentity bool, dataDir string) {
	fmt.Fprintf(w, "\nThe KB at %s is complete and valid; only the push failed. Finish it with:\n", path)
	if fallbackIdentity {
		fmt.Fprintln(w, "  git config --global user.email \"<an email of your forge account>\"   # or a per-folder identity")
		fmt.Fprintf(w, "  git -C %s commit --amend --reset-author --no-edit   # if the forge rejected the author\n", hintQuote(goos, path))
	}
	fmt.Fprintf(w, "  git -C %s push -u origin %s\n", hintQuote(goos, path), hintQuote(goos, branch))
	fmt.Fprintf(w, "Or remove it: %s   (a server started with data: %s would otherwise mount it)\n", removeDirCommand(goos, path), dataDir)
}

// removeDirCommand is the shell command that deletes dir recursively on goos:
// Windows users are in PowerShell, where `rm -rf` does not parse (#415).
func removeDirCommand(goos, dir string) string {
	if goos == "windows" {
		return "Remove-Item -Recurse -Force '" + strings.ReplaceAll(dir, "'", "''") + "'"
	}
	return "rm -rf " + hintQuote(goos, dir)
}

// hintQuote makes s one word of a paste-ready command on goos: untouched when
// it is made only of characters no shell treats specially, single-quoted
// otherwise (`'` becomes `'\”` in a POSIX shell, `”` in PowerShell). A data
// directory such as "$HOME/My KBs" otherwise made the printed `rm -rf` delete
// "My" and a relative "KBs/kb-a".
func hintQuote(goos, s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./:@%+=,~", r))
	}) {
		return s
	}
	if goos == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
		fmt.Fprintln(os.Stderr, "  cartographer kb create <name> --remote <url>   empty repository (or a forge's initial commit only), becomes this KB's origin")
		fmt.Fprintln(os.Stderr, "  cartographer kb clone <remote>                 repository that already holds a KB")
		fmt.Fprintln(os.Stderr, "  cartographer kb create <name> --no-remote      local-only KB (not durable, never synced)")
		return 2
	}
	return 0
}

// checkReadmeChoice validates --readme: empty (automatic), "generate" or
// "keep", and never together with --no-remote, which has no forge commit to
// adopt. It returns 0 when the choice is valid and the usage exit code (2)
// otherwise.
func checkReadmeChoice(readme string, noRemote bool) int {
	switch readme {
	case "":
		return 0
	case readmeGenerate, readmeKeep:
		if noRemote {
			fmt.Fprintln(os.Stderr, "Error: --readme applies to the README of a forge's initial commit, which --no-remote never adopts")
			return 2
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: --readme %q is not valid; use %s (replace the forge's README.md with a generated one), %s (leave it) or omit the flag (replace it only when it is a known forge boilerplate)\n", readme, readmeGenerate, readmeKeep)
	return 2
}

// originResult is what attachOrigin tells cmdKBCreate: the exit code, the
// branch the KB ended up on, and what to do with the local KB on failure.
type originResult struct {
	code int
	// discard: the failure happened before any push was attempted, so the
	// just-created local KB is deleted (a rerun of the same command would
	// otherwise stop on "already exists"). A push failure keeps it (D156).
	discard bool
	// branch is the branch the KB lives on (the remote's default when the
	// forge's initial commit was adopted, D264).
	branch string
	// adopted lists the files of the forge's initial commit that were kept.
	adopted []string
	// replacedReadme: the adopted README.md was replaced by a generated one.
	replacedReadme bool
	// orphan: the KB was pushed as an orphan branch of a repository with
	// history (D1412); configPath is the server file that must record it.
	orphan     bool
	configPath string
}

// attachOrigin wires the freshly scaffolded KB at path to remote as "origin"
// and pushes its initial commit with upstream tracking, so the KB is
// reconstructible from its repository the moment it exists. An empty remote
// gets the scaffold as is; a remote holding only a forge's initial commit
// (README/LICENSE/.gitignore, D710) gets the scaffold rebuilt on top of it, on
// the remote's own default branch (D264), by a fast-forward; any other
// non-empty remote is refused before the push. An unreachable remote falls
// through to the push, whose failure keeps the scaffold (D156). readme is the
// --readme choice for the adopted forge README.md (replaceForgeReadme).
//
// want is the --branch the operator gave ("" when none): on a repository with
// history it decides between the refusal and the orphan branch (D1412).
func attachOrigin(path, remote, kbName, readme, authorName, authorEmail string, fallbackIdentity bool, want, configFlag string) originResult {
	if err := gitx.AddRemote(path, "origin", remote); err != nil {
		// No origin exists, so "only the push failed" and its `git push` hint
		// would name a remote that is not there: discard the scaffold like any
		// other failure that precedes the first push, and say what failed.
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintf(os.Stderr, "Hint: git could not add %q as the origin of the new KB; check the URL and rerun the command (nothing was left behind).\n", gitx.RedactRemoteURL(remote))
		return originResult{code: 1, discard: true}
	}
	branch, err := gitx.Branch(path)
	if err != nil || branch == "" {
		branch = gitx.DefaultBranch
	}
	var adopted []string
	var replaced, orphan bool
	var configPath string
	if refs, lsErr := gitx.LsRemote(path, "origin"); lsErr == nil && refs.HasRefs {
		var res *originResult
		if branch, adopted, replaced, orphan, configPath, res = adoptForgeCommit(path, remote, kbName, readme, branch, want, configFlag, refs, authorName, authorEmail); res != nil {
			return *res
		}
	}
	// The server must follow X before it exists on the remote, or a discovered
	// KB would be switched to the customer's default branch by its first sync
	// (D1412). A failed push puts the server file back as it was.
	restore := func() {}
	if orphan {
		orig, _, rerr := readFileMode(configPath)
		if rerr == nil {
			rerr = appendBranchEntry(configPath, remote, kbName, branch)
		}
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "Error:", rerr)
			return originResult{code: 1, discard: true}
		}
		restore = func() { _ = config.WriteFileAtomic(configPath, orig) }
	}
	if err := gitx.PushSetUpstream(path, "origin", branch); err != nil {
		restore()
		fmt.Fprintln(os.Stderr, "Error:", err)
		if orphan {
			fmt.Fprintf(os.Stderr, "Hint: once the push succeeds, add this entry under kbs: in %s:\n  - remote: %s\n    branch: %s\n", configPath, gitx.RedactRemoteURL(remote), branch)
		}
		if strings.Contains(err.Error(), "src refspec") {
			// Not an authentication problem: the branch has no commit yet.
			fmt.Fprintf(os.Stderr, "Hint: the KB has no commit to push — git could not make the initial commit (often: committer identity unknown). Commit it, which also shows why it failed: git -C %s commit -m %q\n", hintQuote(runtime.GOOS, path), "init: KB initialized")
			return originResult{code: 1, branch: branch}
		}
		writePushRefusalHints(os.Stderr, err.Error(), remote, authorName, authorEmail, fallbackIdentity)
		return originResult{code: 1, branch: branch}
	}
	return originResult{branch: branch, adopted: adopted, replacedReadme: replaced, orphan: orphan, configPath: configPath}
}

// writePushRefusalHints names the likely cause of a refused first push the way
// the server's sync does (internal/kb/gitsync.go): the identity test first,
// because a membership rule and a branch protection both end with
// "pre-receive hook declined" (D1063); authentication only when neither fits.
func writePushRefusalHints(w io.Writer, gitOut, remote, authorName, authorEmail string, fallbackIdentity bool) {
	switch {
	case gitx.IsIdentityRejection(gitOut):
		fmt.Fprintf(w, "Hint: the remote refused the commit's author, %s <%s>; authentication worked.\n", authorName, authorEmail)
		if fallbackIdentity {
			fmt.Fprintln(w, "Hint: git has no identity of yours, so the server identity was used. Set a git identity the forge knows (git config --global user.email \"<an email of your forge account>\", or a per-folder identity), then re-author the commit as printed below.")
		} else {
			fmt.Fprintln(w, "Hint: that is the identity git resolves for this folder. Use an email of your forge account (git config --global user.email, or a per-folder identity), then re-author the commit: git commit --amend --reset-author --no-edit in the KB.")
		}
	case gitx.IsProtectedBranchRejection(gitOut):
		fmt.Fprintln(w, "Hint: the branch is protected for this account: the remote refuses a direct push to it. Push with an account allowed to, or ask for the protection to be lifted on this branch.")
	default:
		fmt.Fprintln(w, "Hint: authenticate git with your SSH agent or credential helper, then retry.")
		fmt.Fprintf(w, "Hint: if the remote already holds a KB, mount it instead: cartographer kb clone %s\n", gitx.RedactRemoteURL(remote))
	}
}

// adoptForgeCommit handles a remote that already has refs. It returns the
// branch to push and the adopted files, or a non-nil result when the create
// must stop: a remote that holds content, a path collision, or a failed fetch
// or rebase, or the README replacement — all before anything was pushed, so
// the local KB is discarded. The bool reports a replaced README.md.
func adoptForgeCommit(path, remote, kbName, readme, branch, want, configFlag string, refs gitx.RemoteRefs, authorName, authorEmail string) (string, []string, bool, bool, string, *originResult) {
	refuse := func() (string, []string, bool, bool, string, *originResult) {
		return branch, nil, false, false, "", &originResult{code: 1, discard: true}
	}
	if err := gitx.Fetch(path, "origin"); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return refuse()
	}
	target := refs.Default
	if target == "" && len(refs.Branches) == 1 {
		target = refs.Branches[0]
	}
	var forgePaths []string
	forgeInit := false
	if target != "" && len(refs.Branches) == 1 {
		var err error
		if forgePaths, forgeInit, err = gitx.ForgeInitialized(path, "origin/"+target); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return refuse()
		}
	}
	if want != "" && refs.HasBranch(want) && !(forgeInit && target == want) {
		fmt.Fprintf(os.Stderr, "Error: the remote %s already has a branch %s, so a new KB cannot be created on it.\n", gitx.RedactRemoteURL(remote), want)
		fmt.Fprintf(os.Stderr, "Hint: if it holds a KB, mount it: cartographer kb clone %s --branch %s\n", gitx.RedactRemoteURL(remote), want)
		fmt.Fprintln(os.Stderr, "Hint: otherwise pick another branch name.")
		return refuse()
	}
	if !forgeInit && want != "" {
		// Orphan mode (D1412): the scaffold is a fresh `git init`, so its
		// branch already shares no history with the repository. Every refusal
		// is decided here, before the push.
		configPath, err := serverConfigFile(configFlag)
		if err == nil {
			err = checkBranchEntryFree(configPath, remote, kbName)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return refuse()
		}
		if want != branch {
			if err := gitx.RenameBranch(path, want); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return refuse()
			}
		}
		return want, nil, false, true, configPath, nil
	}
	if !forgeInit {
		fmt.Fprintf(os.Stderr, "Error: the remote %s already has commits, so a new KB cannot be created in it.\n", gitx.RedactRemoteURL(remote))
		fmt.Fprintf(os.Stderr, "Hint: if it holds a KB, mount it: cartographer kb clone %s\n", gitx.RedactRemoteURL(remote))
		fmt.Fprintf(os.Stderr, "Hint: put the KB on a branch of its own, leaving the rest of the repository untouched: cartographer kb create %s --remote %s --branch <name>\n", kbName, gitx.RedactRemoteURL(remote))
		fmt.Fprintln(os.Stderr, "Hint: otherwise import its content with the cartographer-kb-import skill, or point --remote at an empty repository.")
		return refuse()
	}

	scaffold, err := gitx.TreePaths(path, "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return refuse()
	}
	taken := map[string]bool{}
	for _, p := range forgePaths {
		taken[strings.ToLower(p)] = true
	}
	var collisions []string
	for _, p := range scaffold {
		if taken[strings.ToLower(p)] {
			collisions = append(collisions, p)
		}
	}
	if len(collisions) > 0 {
		fmt.Fprintf(os.Stderr, "Error: the remote's initial commit already has %s, which the KB scaffold also writes; Cartographer will not merge them.\n", strings.Join(collisions, ", "))
		fmt.Fprintln(os.Stderr, "Hint: empty the repository, then retry — delete and re-create it without \"initialize with README\":")
		fmt.Fprintln(os.Stderr, "  gh repo delete <owner>/<repo> && gh repo create <owner>/<repo>")
		fmt.Fprintln(os.Stderr, "  glab repo delete <owner>/<repo> && glab repo create <owner>/<repo>")
		return refuse()
	}

	// The rebased commit is created by this git, so its committer must be the
	// operator who authors it (D950, D265), not whatever git would guess: a
	// forge with a membership rule checks the committer too.
	if err := gitx.RebaseOntoAutostash(path, "origin", target, kb.IdentityGitEnv(authorName, authorEmail)...); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return refuse()
	}
	if target != branch {
		if err := gitx.RenameBranch(path, target); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return refuse()
		}
	}
	replaced, err := replaceForgeReadme(path, "origin/"+target, remote, kbName, readme, forgePaths, authorName, authorEmail)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return refuse()
	}
	return target, forgePaths, replaced, false, "", nil
}

const (
	readmeGenerate = "generate"
	readmeKeep     = "keep"
)

// generatedReadme is the README of a KB that replaced its forge's README: the
// title, the project description the forge carried when there was one (the
// second %s, already followed by a blank line), and where the KB's rules and
// content start.
const generatedReadme = `# %s

%sThis is a Cartographer knowledge base: it is read and written through the Cartographer MCP server, not edited by hand.
The rules are in ` + "`instructions.md`" + ` and the content starts at ` + "`data/index.md`" + `.
`

// replaceForgeReadme applies the --readme choice to the README.md of the
// adopted forge commit (forgePaths), as one commit on top of the rebased
// scaffold so the forge's commit stays in the history. Automatic mode
// replaces only a recognised boilerplate (gitx.ForgeBoilerplateReadme) and
// tells the operator when it kept another README; "generate" replaces
// whatever the README holds; "keep" never does. It reports whether it did.
func replaceForgeReadme(path, forgeRef, remote, kbName, readme string, forgePaths []string, authorName, authorEmail string) (bool, error) {
	readmePath, single := gitx.ForgeReadmePath(forgePaths)
	if readme == readmeKeep {
		return false, nil
	}
	if readmePath == "" {
		if readme == readmeGenerate {
			fmt.Fprintln(os.Stderr, "Hint: the remote has no README.md to replace; --readme generate did nothing.")
		}
		return false, nil
	}
	// The adopted tree comes from the remote: a README that is a symlink
	// would make the write below land outside the KB, so only a regular
	// file is replaced.
	target := filepath.Join(path, readmePath)
	if fi, err := os.Lstat(target); err != nil {
		return false, err
	} else if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("the remote's %s is not a regular file; refusing to replace it", readmePath)
	}
	var boilerplate bool
	var description string
	if single {
		var err error
		if boilerplate, description, err = gitx.ForgeReadmeBoilerplate(path, forgeRef, readmePath, remote); err != nil {
			return false, err
		}
	}
	if readme == "" && !boilerplate {
		fmt.Fprintf(os.Stderr, "Hint: kept the remote's %s, which is not a known forge boilerplate; --readme %s replaces it with a generated one.\n", readmePath, readmeGenerate)
		return false, nil
	}
	if description != "" {
		description += "\n\n"
	}
	content := fmt.Sprintf(generatedReadme, kbName, description)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return false, err
	}
	subject := "docs: replace the forge's boilerplate README"
	if readme == readmeGenerate {
		subject = "docs: generate the README"
	}
	if err := gitx.CommitPaths(path, []string{readmePath}, subject, authorName, authorEmail, kb.IdentityGitEnv(authorName, authorEmail)...); err != nil {
		return false, err
	}
	return true, gitx.ResetPaths(path, readmePath)
}

// printNoRemoteWarning states what --no-remote costs, on stderr: the KB is a
// purely local git repository, so it has no backup and every sync path is
// inert for it (a KB syncs exactly when it has a remote, kb.hasRemote).
func printNoRemoteWarning(path string) {
	fmt.Fprintln(os.Stderr, "Warning: this KB has no origin — it is not backed up and will never sync.")
	fmt.Fprintf(os.Stderr, "  Attach one later with: git -C %s remote add origin <url> && git -C %s push -u origin %s\n",
		hintQuote(runtime.GOOS, path), hintQuote(runtime.GOOS, path), gitx.DefaultBranch)
}

// cmdKBClone implements `cartographer kb clone <remote> [name] [--data <dir>]
// [--restart]`: mounts a remote KB in the local service data directory. A
// failed clone or a clone that is not an OKF KB is removed, so auto-discovery
// never mistakes an unusable directory for a mountable KB.
func cmdKBClone(args []string) int {
	var remote, name string

	fs := flag.NewFlagSet("kb clone", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to read data: from (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	timeoutFlag := fs.Duration("timeout", defaultCloneTimeout, "Time budget for the whole clone")
	branchFlag := fs.String("branch", "", "Branch this server alone commits and pushes to: checked out in the clone and written as kbs[].branch in the server config (--config), which must exist")
	restartFlag := fs.Bool("restart", false, "Restart the local service and wait until healthy after mounting the KB")
	if code := bindPositionals("kb clone", parseFlagsAnywhere(fs, args), &remote, &name); code != 0 {
		return code
	}

	if remote == "" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb clone <remote> [name] [--branch <name>] [--data <dir>] [--config <path>] [--restart]")
		return 2
	}
	if name == "" {
		name = remoteKBName(remote)
	}
	if err := validateKBName(name); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	branch, code := checkBranchFlag(fs, *branchFlag)
	if code != 0 {
		return code
	}

	if code := checkLocalTarget("kb clone", *dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}
	// The server file is checked before anything is cloned: a --branch that
	// cannot be recorded would leave a clone the server then refuses to write.
	var configPath string
	if branch != "" {
		var err error
		if configPath, err = serverConfigFile(*configFlag); err == nil {
			err = checkBranchEntryFree(configPath, remote, name)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
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

	cloneErr := gitx.Clone(ctx, remote, path)
	// The handler exists to stop git: the phases after the clone (branch
	// checkout, the --restart wait) do not read ctx, so keeping it would turn
	// Ctrl-C into a no-op there. From here the default behaviour applies.
	stop()
	if err := cloneErr; err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		if errors.Is(err, gitx.ErrCloneTimeout) {
			fmt.Fprintf(os.Stderr, "Hint: the clone exceeded %s — raise the budget with --timeout, or check the remote is reachable.\n", *timeoutFlag)
		}
		return 1
	}
	// The layout is checked on the branch that will be mounted: a repository
	// whose default branch holds no KB can still carry one on --branch (D1412).
	teamBranch := ""
	if branch != "" {
		if !gitx.RemoteBranchExists(path, "origin", branch) && !hasKBLayout(path, "HEAD") {
			if forgeInitClone(path) {
				fmt.Fprintf(os.Stderr, "Error: %s holds only a forge's initial commit — create the KB in it: cartographer kb create <name> --remote %s --branch %s\n", gitx.RedactRemoteURL(remote), gitx.RedactRemoteURL(remote), branch)
				return 1
			}
			fmt.Fprintf(os.Stderr, "Error: branch %s does not exist on the remote and the default branch holds no KB\n", branch)
			return 1
		}
		teamBranch = committedBranchSetting(path)
		if err := checkoutConfiguredBranch(path, branch); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
	}
	if _, err := kb.Open(path); err != nil {
		// A forge's initial commit is not a KB to import but an empty project
		// to build one in: point at kb create, which adopts it (D710).
		if branch == "" && forgeInitClone(path) {
			fmt.Fprintf(os.Stderr, "Error: %s holds only a forge's initial commit — create the KB in it: cartographer kb create <name> --remote %s\n", gitx.RedactRemoteURL(remote), gitx.RedactRemoteURL(remote))
			return 1
		}
		fmt.Fprintln(os.Stderr, "Error: not an OKF KB — import it with the cartographer-kb-import skill, push, then retry")
		if branch == "" {
			printKBBranchHint(path, remote)
		}
		return 1
	}

	if branch != "" {
		if err := appendBranchEntry(configPath, remote, name, branch); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		cloned = true
		fmt.Printf("KB %q mounted at %s\n", name, path)
		fmt.Printf("branch: %s (kbs[].branch in %s; this server only)\n", branch, configPath)
		if teamBranch != "" && teamBranch != branch {
			fmt.Printf("note: the KB's own settings.yaml names branch %s; the server override wins, and the KB's setting is not read here\n", teamBranch)
		}
		printPostCreateGuidanceFn(*configFlag, *restartFlag)
		return 0
	}

	cloned = true
	fmt.Printf("KB %q mounted at %s\n", name, path)
	printPostCreateGuidanceFn(*configFlag, *restartFlag)
	return 0
}

// hasKBLayout reports whether ref of the clone at path holds data/index.md,
// the file kb.Open requires.
func hasKBLayout(path, ref string) bool {
	_, err := gitx.ShowFile(path, ref, "data/index.md")
	return err == nil
}

// maxBranchHints caps the candidate branches a failed `kb clone` lists.
const maxBranchHints = 5

var safeHintBranch = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// safeBranchHint admits only branch names that are safe to print inside a
// paste-ready shell command: a remote chooses its branch names.
func safeBranchHint(b string) bool {
	return safeHintBranch.MatchString(b) && kb.ValidateBranchName(b) == nil
}

// printKBBranchHint tells where a KB may live when the default branch holds
// none: the remote-tracking branches of the fresh clone (all fetched, so the
// check is local) that carry data/index.md, else the generic --branch hint.
func printKBBranchHint(path, remote string) {
	current, _ := gitx.Branch(path)
	var found []string
	if branches, err := gitx.RemoteTrackingBranches(path, "origin"); err == nil {
		for _, b := range branches {
			if b != current && safeBranchHint(b) && hasKBLayout(path, "origin/"+b) {
				found = append(found, b)
			}
		}
	}
	if len(found) == 0 {
		fmt.Fprintf(os.Stderr, "Hint: if the KB is on another branch: cartographer kb clone %s --branch <name>\n", gitx.RedactRemoteURL(remote))
		return
	}
	if len(found) > maxBranchHints {
		found = found[:maxBranchHints]
	}
	for _, b := range found {
		fmt.Fprintf(os.Stderr, "Hint: a KB lives on branch %s: cartographer kb clone %s --branch %s\n", b, gitx.RedactRemoteURL(remote), b)
	}
}

// forgeInitClone reports whether the fresh clone at path holds only a forge's
// initial commit on its single branch (gitx.ForgeInitialized).
func forgeInitClone(path string) bool {
	refs, err := gitx.LsRemote(path, "origin")
	if err != nil || len(refs.Branches) != 1 {
		return false
	}
	_, ok, err := gitx.ForgeInitialized(path, "HEAD")
	return err == nil && ok
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
// <path>] [--json]`: what is on disk, and which of it the server actually serves.
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
	jsonFlag := fs.Bool("json", false, "Print the listing as a JSON document on stdout")
	fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb list [--data <dir>] [--config <path>] [--json]")
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

	if *jsonFlag {
		if err := printKBRowsJSON(os.Stdout, dataDir, rows, mountErr); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		return 0
	}
	printKBRows(os.Stdout, dataDir, rows, mountErr)
	return 0
}

// kbListJSONRow and kbListJSON are the `kb list --json` document. Mounted is
// null when the server could not be asked, and MountedError then says why.
type kbListJSONRow struct {
	Name    string `json:"name"`
	OKF     bool   `json:"okf"`
	Git     bool   `json:"git"`
	Origin  string `json:"origin"`
	Mounted *bool  `json:"mounted"`
}

type kbListJSON struct {
	DataDir      string          `json:"data_dir"`
	KBs          []kbListJSONRow `json:"kbs"`
	MountedError string          `json:"mounted_error,omitempty"`
}

// printKBRowsJSON renders the same data as printKBRows as one indented JSON
// document, and nothing else.
func printKBRowsJSON(w io.Writer, dataDir string, rows []kbRow, mountErr error) error {
	doc := kbListJSON{DataDir: dataDir, KBs: make([]kbListJSONRow, 0, len(rows))}
	for _, r := range rows {
		doc.KBs = append(doc.KBs, kbListJSONRow{Name: r.Name, OKF: r.IsKB, Git: r.IsRepo, Origin: r.Origin, Mounted: r.Mounted})
	}
	if mountErr != nil {
		doc.MountedError = mountErr.Error()
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// scanDataDir reads one level of dataDir, describing each subdirectory
// without touching it. A KB is recognised by data/index.md, which is exactly
// what kb.Open checks — read directly here so the listing stays read-only.
func scanDataDir(dataDir string) ([]kbRow, error) {
	entries, err := os.ReadDir(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		// No data directory yet is "no KB yet", not a failure (#1304); the
		// listing still creates nothing.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read data dir %q: %w", dataDir, err)
	}
	var rows []kbRow
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dataDir, e.Name())
		if serverOwnDataDir(path) {
			continue
		}
		row := kbRow{Name: e.Name(), IsRepo: gitx.IsRepo(path)}
		if _, err := os.Stat(filepath.Join(path, "data", "index.md")); err == nil {
			row.IsKB = true
		}
		if row.IsRepo {
			if url, err := gitx.RemoteURL(path, "origin"); err == nil {
				row.Origin = gitx.RedactRemoteURL(strings.TrimSpace(url))
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
		return nil, fmt.Errorf("server at %s does not list its KBs to a request without a token (authentication on) or is a single-KB server", baseURL)
	}
	names := map[string]bool{}
	for _, kb := range *h.KBs {
		names[kb.Name] = true
	}
	return names, nil
}

// mountErrorLine says why the MOUNTED column is missing. A refused connection
// is the normal "no server" state, so it reads as that and names the command
// that starts one; any other failure keeps its own text.
func mountErrorLine(err error) string {
	// Windows reports a refusal as WSAECONNREFUSED (10061), which is not
	// syscall.ECONNREFUSED there; the number is harmless on the other systems.
	const wsaConnRefused = syscall.Errno(10061)
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaConnRefused) {
		return "the server is not running, so MOUNTED is not shown: start it with `cartographer service start`"
	}
	return fmt.Sprintf("the server could not be asked which of these are mounted: %v", err)
}

// printKBRows renders the listing. The MOUNTED column exists only when the
// server answered.
func printKBRows(w io.Writer, dataDir string, rows []kbRow, mountErr error) {
	fmt.Fprintf(w, "data dir: %s\n", dataDir)
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no KB yet: create one with `cartographer kb create <name>`)")
		if mountErr != nil {
			fmt.Fprintf(w, "\n%s\n", mountErrorLine(mountErr))
		}
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
		fmt.Fprintf(w, "\n%s\n", mountErrorLine(mountErr))
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

// checkLocalTarget refuses a `kb` subcommand (command, e.g. "kb rename") that would act on the
// local data dir while this machine's client points at a remote server.
//
// These commands mount a KB on the LOCAL server, which on such a machine is
// not the server anyone is talking to: the command would report `KB "x"
// mounted at ...` about a directory nothing reads. That is worse than an
// error, so it is an error — with --local as the explicit opt-out, and no
// opinion at all when --data was passed (the caller named the target) or when
// there is no client config to contradict.
func checkLocalTarget(command, dataFlag string, local bool) int {
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
	fmt.Fprintf(os.Stderr, "Error: `%s` acts on the LOCAL server's data dir, but this machine's client points at %s\n", command, cfg.ServerURL)
	fmt.Fprintln(os.Stderr, "  Mounting a KB on that server is an operation on its deployment, not something this command can do.")
	fmt.Fprintln(os.Stderr, "  Use --local to act on the local data dir anyway, or --data <dir> to name the target explicitly.")
	return 2
}

// onInterrupt runs fn on the first SIGINT/SIGTERM and returns a stop function
// that releases the handler; stop is idempotent. Registering it replaces Go's
// default of exiting immediately, which is what left a half-written clone on
// disk: the deferred cleanup never ran. After fn has run the handler is
// released too, so a second Ctrl-C ends the process instead of being swallowed
// by a channel nobody reads any more.
func onInterrupt(fn func()) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	var once sync.Once
	stop := func() { once.Do(func() { signal.Stop(ch); close(ch) }) }
	go func() {
		if _, ok := <-ch; ok {
			fn()
			signal.Stop(ch)
		}
	}()
	return stop
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
	// StartedAt identifies the answering process (D266); empty from a server
	// that predates the field.
	StartedAt string `json:"started_at"`
	// Bootstrapping is set while the server still clones and indexes its KBs
	// (D348); fetchHealth reports it as an error so no caller reads the empty
	// kbs list of that window as "no KB mounted".
	Bootstrapping bool `json:"bootstrapping"`
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
	entries, err := discoverKBPaths(dataDir, "")
	return err == nil && len(entries) == 0
}

// fetchHealth GETs <baseURL>/health and decodes the fields this package
// cares about (hasNoKBs). Returns an error if the server is unreachable or
// does not respond 200 — every caller here treats that as "can't tell, stay
// silent" rather than a hard failure: this guidance is always best-effort
// and never blocks `kb create`/`service install`'s own success.
func fetchHealth(baseURL string) (*healthInfo, error) {
	// Through the shared redirect policy (D1023): a redirect off a loopback
	// base must not take the request anywhere a plain client would follow.
	hc := client.HTTPClient(&http.Client{Timeout: healthCheckTimeout}, false)
	resp, err := hc.Get(strings.TrimRight(baseURL, "/") + "/health")
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
	if h.Bootstrapping {
		return nil, fmt.Errorf("%s/health: server is bootstrapping its KBs, retry shortly", baseURL)
	}
	return &h, nil
}

// serverBaseURL resolves the LOCAL server's base HTTP URL (no /mcp or /health
// suffix): the `http:` field of the service config (service.ConfigPath, D85),
// which is what `serve` actually binds to, else the local default listen
// address. It never reads the client's server.url: the `kb` subcommands act on
// this machine's data directory, and on a machine whose client points at a
// remote server that URL made `kb list` fill MOUNTED from the remote and
// `--restart` restart the local service while waiting on the remote's
// started_at.
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
	return httpAddrToBaseURL(defaults.DefaultListenAddress)
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

// Test seams of the --restart path: the real ones ask the installed service.
var (
	restartServiceFn   = func() error { return service.NewManager().Restart() }
	serviceInstalledFn = func(configPath string) bool {
		st, err := service.NewManager().Status(configPath)
		return err == nil && st.Installed
	}
)

// restartLocalService is what `--restart` does after a KB was created, cloned,
// renamed or unmounted: restart the local service and wait until a new process
// answers. It runs whatever /health says first. A server that does not answer,
// or still reports bootstrapping, used to make the command return before looking
// at the flag, with no restart and no message, though the KB stays unmounted
// until a manual one. Installed, the service is restarted anyway; not installed,
// there is nothing to restart and the operator is told so. ok is false when the
// caller should not go on to print the "run sync" guidance.
func restartLocalService(base, configPath string) (ok bool) {
	startedAt := ""
	if before, err := fetchHealth(base); err == nil {
		startedAt = before.StartedAt
		fmt.Println("Restarting the service...")
	} else {
		if !serviceInstalledFn(configPath) {
			fmt.Fprintf(os.Stderr, "Warning: --restart: nothing answers at %s and the service is not installed, so nothing was restarted. Start your server, or install the service (cartographer service install).\n", base)
			return false
		}
		fmt.Printf("The service does not answer normally (%v): restarting it anyway, as --restart asks...\n", err)
	}
	if err := restartServiceFn(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: restart failed:", err)
		return false
	}
	if waitHealthy(base, startedAt) {
		fmt.Println("service healthy")
	} else {
		fmt.Fprintf(os.Stderr, "Warning: service did not report healthy within %s\n", restartWaitTimeout)
	}
	return true
}

// printPostCreateGuidance is WP2's post-create half: if a local server
// answers /health, tell the user how to get the new KB mounted — either the
// plain hint (`cartographer service restart`) or, with restart=true
// (--restart), actually restart it and wait for it to become healthy again
// (restartLocalService, which also acts when the server does not answer).
// Without --restart and with no server answering, stay silent: there is
// nothing running to restart, and the user is presumably still mid-setup (e.g.
// using the KB with `serve --kb <path>` directly, no service involved).
func printPostCreateGuidance(configPath string, restart bool) {
	base := serverBaseURL(configPath)
	if restart {
		if !restartLocalService(base, configPath) {
			return
		}
		fmt.Println("On connected client machines, run: cartographer sync")
		fmt.Println(openSessionsLine)
		return
	}
	if _, err := fetchHealth(base); err != nil {
		return
	}
	fmt.Println("Restart the service to mount it: cartographer service restart")
	fmt.Println("On connected client machines, run: cartographer sync")
	fmt.Println(openSessionsLine)
}

// openSessionsLine tells the operator that agent sessions already open keep
// the tool list and instructions they started with (D712).
const openSessionsLine = "Agent sessions already open do not see the new KB: start a new session after cartographer sync."

// waitHealthy polls <baseURL>/health until a new process responds or
// restartWaitTimeout elapses, returning whether it became healthy in time.
// "New" is proved by a started_at different from oldStartedAt, the one the
// server reported before the restart (D266): the probe used to reach the old
// process before it exited and print "service healthy" over a server that was
// about to be gone. An empty oldStartedAt (a server predating the field)
// accepts any answer.
func waitHealthy(baseURL, oldStartedAt string) bool {
	return waitHealthyFor(baseURL, oldStartedAt, restartWaitTimeout)
}

// waitHealthyFor is waitHealthy with the budget as a parameter: a caller that
// waits for a large KB's first clone needs more than a restart does. A server
// still bootstrapping is not healthy (fetchHealth reports it as an error), so
// this is also the "ready" condition of a freshly started service.
func waitHealthyFor(baseURL, oldStartedAt string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if h, err := fetchHealth(baseURL); err == nil && (oldStartedAt == "" || h.StartedAt != oldStartedAt) {
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
		fmt.Println("no KB mounted yet — `cartographer setup` finishes the job, or: cartographer kb create <name> --remote <url>")
	}
}
