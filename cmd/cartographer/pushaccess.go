package main

// Push access of a KB's remote (#1999). The server commits every write and
// pushes it; a credential that can read the remote but not push it is found,
// without this, only at the first write, as an unpushed commit. `setup` already
// asks the question (gitx.ProbePushAccess: a push --dry-run of a throw-away
// commit, made in a temporary repository, that writes nothing to the remote and
// leaves nothing in the KB's clone). Here the same probe runs where a KB
// arrives (`kb clone`) and where a machine is diagnosed (`doctor`), with the
// credentials the SERVER has for that KB (its credentials directory): the
// machine's own may differ. The rest of the environment (GIT_SSH_COMMAND,
// SSH_AUTH_SOCK, proxies) is the one of the shell running `kb clone` or
// `doctor`, not the service's. The server start does not probe: it has no
// network to spend there.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// pushProbeTimeout bounds one probe: an unreachable remote costs this much, and
// doctor probes the KBs side by side, so it costs it once per batch of
// pushProbeParallel.
const pushProbeTimeout = 20 * time.Second

// pushProbeParallel caps how many probes (so ssh connections) doctor runs at
// once: a forge throttles a burst of logins from one machine.
const pushProbeParallel = 4

// probePushAccessFn is indirected so a test can play a remote without a forge.
var probePushAccessFn = gitx.ProbePushAccess

type pushVerdict int

const (
	// pushWritable: the remote accepted the dry run.
	pushWritable pushVerdict = iota
	// pushNoRemote: the clone has no origin, so there is nothing to push to.
	pushNoRemote
	// pushRefused: the remote answered with a verdict on the credential
	// (gitx.IsPushCredentialVerdict): it cannot push, or is not accepted, or
	// there is none; Err says why, with the remedy.
	pushRefused
	// pushOffline: the remote did not answer (gitx.IsRemoteUnreachable): no
	// verdict on the credential.
	pushOffline
	// pushUnknown: the probe failed for a reason that is neither: a TLS or
	// host-key failure, an HTTP 429 or 5xx, a connection cut mid-way. Nothing
	// is known about the credential; Err says what happened.
	pushUnknown
	// pushInterrupted: the caller cancelled the probe (Ctrl-C). Not the
	// deadline, and no finding of any kind.
	pushInterrupted
)

type pushResult struct {
	Verdict pushVerdict
	Err     error
	// Cause is Err's first line, without the remote's URL, safe to print and to
	// compare: KBs that fail the same way share it.
	Cause string
}

// classifyPushError sorts a failed probe in three: the remote did not answer, it
// answered with a verdict on the credential, or neither is known. Only an
// allow-list of verdicts is a refusal; every other failure is "could not check".
func classifyPushError(err error) pushVerdict {
	switch {
	case errors.Is(err, context.Canceled):
		return pushInterrupted
	case gitx.IsRemoteUnreachable(err):
		return pushOffline
	case gitx.IsPushCredentialVerdict(err):
		return pushRefused
	}
	return pushUnknown
}

// kbPushEnv is the git environment the server pushes the KB name with: the KB's
// own token and key (gitEnvForKB), and default.ssh through GIT_SSH_COMMAND
// unless the process environment or the KB already sets one (setupGitSSH).
func kbPushEnv(name, credDir string) []string {
	env := gitEnvForKB(name, credDir)
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			return env
		}
	}
	if _, set := os.LookupEnv("GIT_SSH_COMMAND"); set {
		return env
	}
	if key, ok := credentialFile(credDir, "", config.CredentialSSH); ok {
		env = append(env, "GIT_SSH_COMMAND="+sshCommandWithHosts(credDir, key))
	}
	return env
}

// serverCredentialsDir is the credentials directory of the server whose data
// directory is dataDir: credentials_dir of the server file at configPath, else
// <dataDir>/credentials. configPath may be "" or unreadable.
func serverCredentialsDir(configPath, dataDir string) string {
	c := &config.Config{Data: dataDir}
	if configPath != "" {
		if cfg, err := config.Load(configPath); err == nil {
			c.CredentialsDir = cfg.CredentialsDir
		}
	}
	return c.CredentialsPath()
}

// probeKBPush asks the origin of the KB clone at path whether the server's
// credentials for the KB name may push to it.
func probeKBPush(ctx context.Context, name, path, credDir string) pushResult {
	// The server pushes through origin, which honours remote.origin.pushurl.
	remote, err := gitx.RemotePushURL(path, "origin")
	if err != nil || remote == "" {
		return pushResult{Verdict: pushNoRemote}
	}
	err = probePushAccessFn(ctx, remote, kbPushEnv(name, credDir)...)
	if err == nil {
		return pushResult{Verdict: pushWritable}
	}
	return pushResult{Verdict: classifyPushError(err), Err: err, Cause: pushCause(err, remote)}
}

// pushCause is the first line of a probe error, printable, with the remote's URL
// (and git's "git push --dry-run <url>:" lead) replaced by "the remote" so that
// KBs failing the same way read the same.
func pushCause(err error, remote string) string {
	if errors.Is(err, gitx.ErrProbeTimeout) {
		return "no answer within the probe's deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	line := firstLine(err)
	if rest, ok := strings.CutPrefix(line, "git push --dry-run "); ok {
		if _, after, found := strings.Cut(rest, ": "); found {
			line = after
		}
	}
	return strings.ReplaceAll(line, gitx.RedactRemoteURL(remote), "the remote")
}

// firstLine is the headline of a multi-line git error, safe to print.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return escapeControl(strings.TrimSpace(line))
}

// warnIfCannotPush is the probe `kb clone` runs on the clone it just made. It
// only says what it found: a reader may clone on purpose, so the KB stays
// mounted and the exit code stays 0.
func warnIfCannotPush(name, path, configPath, dataDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), pushProbeTimeout)
	defer cancel()
	// git runs in its own process group: Ctrl-C reaches it only through ctx.
	stop := onInterrupt(cancel)
	defer stop()
	res := probeKBPush(ctx, name, path, serverCredentialsDir(configPath, dataDir))
	switch res.Verdict {
	case pushRefused:
		fmt.Fprintf(os.Stderr, "Warning: the credentials this server pushes %q with cannot push to its remote: %s\n", name, res.Cause)
		fmt.Fprintln(os.Stderr, "  Writes will be committed in the clone and never reach the remote. To write, give the KB a credential with write access (<kb>.token or <kb>.ssh in the server's credentials directory); to only read, ignore this. `cartographer doctor` checks it again.")
	case pushOffline:
		fmt.Fprintf(os.Stderr, "Note: push access of %q was not checked, the remote did not answer (%s); `cartographer doctor` checks it later\n", name, res.Cause)
	case pushInterrupted:
		fmt.Fprintf(os.Stderr, "Note: push access of %q not checked: interrupted; `cartographer doctor` checks it later\n", name)
	case pushUnknown:
		fmt.Fprintf(os.Stderr, "Note: push access of %q could not be checked (%s); `cartographer doctor` checks it again\n", name, res.Cause)
	}
}

// doctorServerDirsFn is the data and credentials directories of the local
// server; a variable so a doctor test does not read this machine's.
var doctorServerDirsFn = func() (dataDir, credDir string) {
	dataDir = resolveServerDataDir("")
	configPath, _ := serverConfigFile("")
	return dataDir, serverCredentialsDir(configPath, dataDir)
}

// checkPushAccess: a KB whose remote refuses the push of the credentials the
// server uses fills the clone with commits that never leave it, and nothing
// says so until the first write (#1999). It probes each KB of the LOCAL server
// that has an origin, side by side (at most pushProbeParallel at once); a
// remote that does not answer, or a failure that says nothing about the
// credential, is an info finding, not a verdict. It stays silent for a server
// on another machine (its clones are not here to read), and when the server did
// not answer. It writes nothing: the probe works in a temporary repository.
func checkPushAccess(cfg *clientconfig.Config, facts serverFacts, healthErr error) []doctorFinding {
	if healthErr != nil || !isLoopbackURL(cfg.ServerURL) || len(facts.Names) == 0 {
		return nil
	}
	dataDir, credDir := doctorServerDirsFn()
	results := make([]pushResult, len(facts.Names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, pushProbeParallel)
	for i, name := range facts.Names {
		if validateKBName(name) != nil {
			results[i].Verdict = pushNoRemote
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), pushProbeTimeout)
			defer cancel()
			results[i] = probeKBPush(ctx, name, filepath.Join(dataDir, name), credDir)
		}()
	}
	wg.Wait()
	return pushFindings(facts.Names, dataDir, results)
}

// pushFindings turns the probe results (one per name) into findings. A refusal
// is one warning per KB. A remote that did not answer, or a probe that could not
// be judged, is one info finding per distinct cause naming every KB it hit: 20
// KBs behind one forge that is down are one line, not twenty.
func pushFindings(names []string, dataDir string, results []pushResult) []doctorFinding {
	var out []doctorFinding
	type group struct {
		verdict pushVerdict
		cause   string
		names   []string
	}
	var groups []*group
	for i, name := range names {
		switch r := results[i]; r.Verdict {
		case pushRefused:
			out = append(out, doctorFinding{
				Check: "push-access", Severity: doctorWarning, Path: filepath.Join(dataDir, name),
				Message: fmt.Sprintf("KB %q: the credentials the server pushes it with cannot push to its remote: %s", escapeControl(name), r.Cause),
				Fix:     "give the KB a credential with write access (<kb>.token or <kb>.ssh in the credentials directory, then restart the service); ignore this if the KB is for reading",
			})
		case pushOffline, pushUnknown:
			var g *group
			for _, c := range groups {
				if c.verdict == r.Verdict && c.cause == r.Cause {
					g = c
				}
			}
			if g == nil {
				g = &group{verdict: r.Verdict, cause: r.Cause}
				groups = append(groups, g)
			}
			g.names = append(g.names, name)
		}
	}
	for _, g := range groups {
		quoted := make([]string, len(g.names))
		for i, n := range g.names {
			quoted[i] = fmt.Sprintf("%q", escapeControl(n))
		}
		subject := "KB " + quoted[0]
		path := filepath.Join(dataDir, g.names[0])
		if len(g.names) > 1 {
			subject = "KBs " + strings.Join(quoted, ", ")
			path = dataDir
		}
		f := doctorFinding{Check: "push-access", Severity: doctorInfo, Path: path}
		if g.verdict == pushOffline {
			f.Message = fmt.Sprintf("%s: push access not checked, the remote did not answer (%s)", subject, g.cause)
			f.Fix = "run `cartographer doctor` again when the remote is reachable"
		} else {
			f.Message = fmt.Sprintf("%s: push access could not be checked (%s)", subject, g.cause)
			f.Fix = "no verdict on the credential: fix the cause above, then run `cartographer doctor` again"
		}
		out = append(out, f)
	}
	return out
}
