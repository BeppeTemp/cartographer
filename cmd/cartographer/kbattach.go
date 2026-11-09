package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// cmdKBAttach implements `cartographer kb attach <kb> --remote <url>`: it gives
// a KB created with --no-remote (D134) the origin it lacks. The remote of a KB
// lives only in the git config of its clone, so nothing is written to the server
// configuration and the running server needs no restart: it reads the remote
// from git at its next sync decision.
func cmdKBAttach(args []string) int {
	var name string
	fs := flag.NewFlagSet("kb attach", flag.ExitOnError)
	remoteFlag := fs.String("remote", "", "Git remote URL to attach as origin (required)")
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	if code := bindPositionals("kb attach", parseFlagsAnywhere(fs, args), &name); code != 0 {
		return code
	}
	if name == "" || *remoteFlag == "" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb attach <kb> --remote <url> [--data <dir>] [--config <path>]")
		return 2
	}
	if code := checkLocalTarget("kb attach", *dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}
	return runKBAttach(name, dataDir, *configFlag, *remoteFlag, os.Stdout, os.Stderr)
}

// remoteCredentialProblem says why a remote URL must not be stored in the
// KB's .git/config, or "" when it may. A secret in a URL would be written there
// in clear: a password in any URL, a token in the user slot of an http(s) URL
// (the usual PAT shape), and any URL-form remote that cannot be parsed, since a
// password with a "/", "#", "?" or space breaks the parse and would slip
// through. ssh:// keeps its user (ssh://git@host/x), and the scp-like forms
// (git@host:path) have no password slot. The message never echoes the URL.
func remoteCredentialProblem(remote string) string {
	if !strings.Contains(remote, "://") {
		return ""
	}
	u, err := url.Parse(remote)
	if err != nil {
		return "the remote URL cannot be parsed, so it cannot be checked for a credential"
	}
	if u.User == nil {
		return ""
	}
	if _, has := u.User.Password(); has {
		return "the remote URL carries a password"
	}
	if s := strings.ToLower(u.Scheme); s == "http" || s == "https" {
		return "the remote URL carries a user name, which for http(s) is where a token goes"
	}
	return ""
}

// runKBAttach is the testable body of cmdKBAttach. Exit codes: 2 for usage and
// validation, 1 for a refusal or a failure. Any exit other than success leaves
// the KB as it was: the origin it added is removed again, because a half-attached
// KB would make the server push by itself on its next write.
func runKBAttach(name, dataDir, configPath, remote string, out, errOut io.Writer) int {
	usage := func(format string, a ...any) int {
		fmt.Fprintf(errOut, "Error: "+format+"\n", a...)
		return 2
	}
	refuse := func(format string, a ...any) int {
		fmt.Fprintf(errOut, "Error: "+format+"\n", a...)
		return 1
	}
	if err := validateKBName(name); err != nil {
		return usage("%v", err)
	}
	if err := gitx.ValidateRemote(remote); err != nil {
		return usage("%s", gitx.RedactRemoteText(err.Error(), remote))
	}
	if problem := remoteCredentialProblem(remote); problem != "" {
		return usage(problem + ", which would be written to the KB's .git/config. " +
			"Use a credential helper or an SSH key, or put the key or token in the server's credentials directory (<kb>.ssh, <kb>.token or default.*), and give the URL without the password")
	}
	cfg, err := loadReauthorConfig(configPath)
	if err != nil {
		return usage("%v", err)
	}
	root := filepath.Join(dataDir, name)
	if _, err := os.Stat(root); err != nil {
		return usage("no KB %q at %s: see `cartographer kb list`", name, root)
	}
	k, err := kb.Open(root)
	if err != nil {
		return usage("%s is not an OKF KB (see `cartographer kb list`): %v", root, err)
	}
	if !gitx.IsRepo(root) {
		return refuse("%s is not a git repository", root)
	}
	release, err := k.AcquireProcessLock(reauthorLockTimeout, "cartographer kb attach")
	if err != nil {
		return refuse("%v", err)
	}
	defer release()

	// A KB that already has an origin: the same one is a no-op, another one is
	// a git operation done by hand.
	if cur, err := gitx.RemoteURL(root, "origin"); err == nil && strings.TrimSpace(cur) != "" {
		if sameRemote(cur, remote) {
			fmt.Fprintf(out, "KB %q already has origin %s: nothing to do\n", name, gitx.RedactRemoteURL(cur))
			return 0
		}
		return refuse("KB %q already has origin %s: changing or removing a remote is a `git remote` operation done by hand (git -C %s remote ...); attach is not needed",
			name, gitx.RedactRemoteURL(cur), hintQuote(runtime.GOOS, root))
	}
	if kind, present := gitx.RebaseInProgress(root); present {
		return refuse("a %s is in progress in %s: finish or abort it first", kind, root)
	}
	branch, err := gitx.Branch(root)
	if err != nil || branch == "" {
		return refuse("%s has a detached HEAD: check out the KB's branch first", root)
	}
	env := gitEnvForKB(name, cfg.CredentialsPath())

	if err := gitx.AddRemote(root, "origin", remote); err != nil {
		return refuse("%v", err)
	}
	attached := false
	defer func() {
		if !attached {
			_ = gitx.RemoveRemote(root, "origin")
		}
	}()
	shown := gitx.RedactRemoteURL(remote)

	refs, err := gitx.LsRemote(root, "origin", env...)
	if err != nil {
		fmt.Fprintln(errOut, "Error:", err)
		writePushRefusalHints(errOut, err.Error(), remote, "", "", false)
		return 1
	}
	if refs.HasRefs {
		switch {
		case len(refs.Branches) == 0:
			return refuse("the remote %s holds refs but no branch (tags only): attach needs an empty repository, or one whose only branch is %q and is part of this KB's history", shown, branch)
		case len(refs.Branches) > 1:
			return refuse("the remote %s has several branches (%s): attach needs an empty repository, or one whose only branch is %q and is part of this KB's history. If it holds a KB, mount it: cartographer kb clone %s",
				shown, strings.Join(refs.Branches, ", "), branch, shown)
		case refs.Branches[0] != branch:
			return refuse("the remote %s has the branch %q, this KB is on %q: attach needs an empty repository, or one holding only %q. If it holds a KB, mount it: cartographer kb clone %s --branch %s",
				shown, refs.Branches[0], branch, branch, shown, refs.Branches[0])
		}
		if err := gitx.FetchNoTags(root, "origin", env...); err != nil {
			fmt.Fprintln(errOut, "Error:", err)
			writePushRefusalHints(errOut, err.Error(), remote, "", "", false)
			return 1
		}
		ok, err := gitx.IsAncestor(root, "origin/"+branch, "HEAD", env...)
		if err != nil {
			return refuse("%v", err)
		}
		if !ok {
			if _, forgeInit, ferr := gitx.ForgeInitialized(root, "origin/"+branch); ferr == nil && forgeInit {
				return refuse("the remote %s holds only a forge initial commit (README, LICENSE or .gitignore), which is not part of this KB's history and cannot be adopted by a KB that already has commits. Empty the repository (or create a new one without initializing it), then run this again", shown)
			}
			return refuse("the remote's history is not part of this KB's: %s on %s is diverged from or ahead of the local %s. If the remote holds a KB, mount it instead: cartographer kb clone %s", branch, shown, branch, shown)
		}
	}

	if err := gitx.PushSetUpstream(root, "origin", branch, env...); err != nil {
		fmt.Fprintln(errOut, "Error:", err)
		authorName, authorEmail, fallback := resolveOperatorIdentity(root)
		writePushRefusalHints(errOut, err.Error(), remote, authorName, authorEmail, fallback)
		fmt.Fprintf(errOut, "Hint: the KB was left as it was, without an origin. If the commits carry an author the remote refuses, re-author them first: cartographer kb reauthor %s\n", name)
		return 1
	}
	attached = true

	fmt.Fprintf(out, "KB %q: origin set to %s, pushed %s\n", name, shown, branch)
	if refs, err := gitx.LsRemote(root, "origin", env...); err == nil && refs.Default != "" {
		fmt.Fprintf(out, "The remote's default branch is %s.\n", refs.Default)
	}
	fmt.Fprintln(out, "The running server needs no restart: it syncs on the next read or write.")
	return 0
}
