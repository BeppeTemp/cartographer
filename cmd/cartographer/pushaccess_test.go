package main

// Push access of a KB's remote (#1999): `kb clone` warns and `doctor` reports a
// credential that can read the remote and cannot push to it, against a remote
// that answers 403 to a push (gitxtest.ReadOnlyHTTPRemote). A pre-receive hook
// would not do: a push --dry-run never reaches the hooks, so a refusal there is
// seen only by the first real push (SyncOut reports it).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

const pushTestToken = "s3cret-token"

// isolateGit keeps the machine's git configuration (a keychain helper) out of a
// test; the process environment is what a clone and a probe without a KB
// credential run with.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

// tokenFile writes <name>.token into a new credentials directory.
func tokenFile(t *testing.T, name, content string) (credDir, path string) {
	t.Helper()
	credDir = filepath.Join(t.TempDir(), "credentials")
	if err := os.MkdirAll(credDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(credDir, name+".token")
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return credDir, path
}

// processToken gives the test process the credential helper of a token file: a
// `kb clone` clones with the process environment.
func processToken(t *testing.T, tokenPath string) {
	t.Helper()
	for _, kv := range gitxtest.TokenEnv(tokenPath) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}

// seedKB pushes a KB into the bare repository behind an HTTP remote.
func seedKB(t *testing.T, bare string) {
	t.Helper()
	src := kbtest.New(t).Root
	branch := gitOut(t, src, "symbolic-ref", "--short", "HEAD")
	mustRunGit(t, src, "push", bare, branch+":"+branch)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)
}

func refsOf(t *testing.T, dir string) string {
	t.Helper()
	return gitOut(t, dir, "for-each-ref", "--format=%(refname) %(objectname)")
}

// cloneWithToken clones url the way the server would, with the KB's token.
func cloneWithToken(t *testing.T, url, tokenPath string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "clone")
	if err := gitx.Clone(context.Background(), url, dest, append(gitxtest.IsolatedEnv(), gitxtest.TokenEnv(tokenPath)...)...); err != nil {
		t.Fatalf("clone: %v", err)
	}
	return dest
}

// closedPortURL is a remote nothing listens on: a probe of it meets a refused
// connection, the offline case.
func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr + "/kb.git"
}

func TestKBClone_WarnsWhenTheRemoteRefusesAPush(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	url, bare := gitxtest.ReadOnlyHTTPRemote(t, "token", pushTestToken)
	seedKB(t, bare)
	_, tok := tokenFile(t, "default", pushTestToken)
	processToken(t, tok)
	remoteBefore := refsOf(t, bare)
	data := filepath.Join(t.TempDir(), "data")

	var code int
	var out string
	errOut := withStderr(t, func() {
		out = withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBClone([]string{url, "kb-ro", "--data", data, "--config", filepath.Join(t.TempDir(), "absent.yaml")})
			})
		})
	})
	if code != 0 {
		t.Fatalf("cmdKBClone = %d, want 0: a reader may clone on purpose\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "Warning:") || !strings.Contains(errOut, "cannot push") {
		t.Errorf("stderr does not warn that the credential cannot push:\n%s", errOut)
	}
	if !strings.Contains(out, "mounted at") {
		t.Errorf("the KB was not mounted:\n%s", out)
	}
	clone := filepath.Join(data, "kb-ro")
	if got := refsOf(t, clone); strings.Contains(got, "cartographer-push-probe") {
		t.Errorf("the probe left a ref in the KB clone:\n%s", got)
	}
	if got := gitOut(t, clone, "status", "--porcelain"); got != "" {
		t.Errorf("the KB clone is dirty after the probe:\n%s", got)
	}
	if got := refsOf(t, bare); got != remoteBefore {
		t.Errorf("the remote changed: a dry run writes nothing\nbefore:\n%s\nafter:\n%s", remoteBefore, got)
	}
}

func TestKBClone_SaysNothingWhenThePushIsAccepted(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	url, bare := gitxtest.HTTPRemote(t, "token", pushTestToken)
	seedKB(t, bare)
	_, tok := tokenFile(t, "default", pushTestToken)
	processToken(t, tok)
	remoteBefore := refsOf(t, bare)

	var code int
	errOut := withStderr(t, func() {
		withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBClone([]string{url, "kb-rw", "--data", filepath.Join(t.TempDir(), "data"), "--config", filepath.Join(t.TempDir(), "absent.yaml")})
			})
		})
	})
	if code != 0 || strings.Contains(errOut, "Warning") || strings.Contains(errOut, "Note") {
		t.Errorf("code %d, stderr:\n%s\nwant a silent success on a writable remote", code, errOut)
	}
	if got := refsOf(t, bare); got != remoteBefore {
		t.Errorf("the probe pushed for real:\nbefore:\n%s\nafter:\n%s", remoteBefore, got)
	}
}

// The probe runs with the credentials the SERVER pushes the KB with: the KB's
// own token in the credentials directory, not the machine's.
func TestProbeKBPush_UsesTheServersCredentialForTheKB(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	url, bare := gitxtest.HTTPRemote(t, "token", pushTestToken)
	seedKB(t, bare)
	goodDir, goodTok := tokenFile(t, "kb-a", pushTestToken)
	clone := cloneWithToken(t, url, goodTok)
	ctx := context.Background()

	if r := probeKBPush(ctx, "kb-a", clone, goodDir); r.Verdict != pushWritable {
		t.Fatalf("with the KB's token: %+v, want writable", r)
	}
	badDir, _ := tokenFile(t, "kb-a", "wrong")
	if r := probeKBPush(ctx, "kb-a", clone, badDir); r.Verdict != pushRefused || !strings.Contains(r.Err.Error(), "credentials were rejected") {
		t.Fatalf("with a wrong token: %+v, want refused as rejected credentials", r)
	}
	// Another KB name has no token of its own here, and the process has none.
	if r := probeKBPush(ctx, "kb-b", clone, goodDir); r.Verdict != pushRefused {
		t.Fatalf("for a KB without a token: %+v, want refused", r)
	}
	if got := refsOf(t, clone); strings.Contains(got, "cartographer-push-probe") {
		t.Errorf("the probe left a ref in the KB clone:\n%s", got)
	}
}

func TestProbeKBPush_NoRemoteAndOffline(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	local := filepath.Join(t.TempDir(), "local")
	mustRunGit(t, "", "init", "-b", "main", local)
	if r := probeKBPush(context.Background(), "kb-local", local, ""); r.Verdict != pushNoRemote {
		t.Errorf("a clone without origin: %+v, want no remote", r)
	}
	if r := probeKBPush(context.Background(), "kb-absent", filepath.Join(t.TempDir(), "absent"), ""); r.Verdict != pushNoRemote {
		t.Errorf("a missing directory: %+v, want no remote", r)
	}
	off := filepath.Join(t.TempDir(), "off")
	mustRunGit(t, "", "init", "-b", "main", off)
	mustRunGit(t, off, "remote", "add", "origin", closedPortURL(t))
	if r := probeKBPush(context.Background(), "kb-off", off, ""); r.Verdict != pushOffline {
		t.Errorf("a remote nobody answers for: %+v, want offline", r)
	}
}

func TestCheckPushAccess(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	url, bare := gitxtest.ReadOnlyHTTPRemote(t, "token", pushTestToken)
	seedKB(t, bare)
	credDir, tok := tokenFile(t, "kb-ro", pushTestToken)

	dataDir := t.TempDir()
	cloneDest := cloneWithToken(t, url, tok)
	if err := os.Rename(cloneDest, filepath.Join(dataDir, "kb-ro")); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, "", "init", "-b", "main", filepath.Join(dataDir, "kb-local"))
	off := filepath.Join(dataDir, "kb-off")
	mustRunGit(t, "", "init", "-b", "main", off)
	mustRunGit(t, off, "remote", "add", "origin", closedPortURL(t))
	orig := doctorServerDirsFn
	doctorServerDirsFn = func() (string, string) { return dataDir, credDir }
	t.Cleanup(func() { doctorServerDirsFn = orig })

	local := &clientconfig.Config{ServerURL: "http://127.0.0.1:39273/mcp"}
	facts := serverFacts{Names: []string{"kb-ro", "kb-local", "kb-off", "kb-unmounted-dir", "../escape"}}

	got := checkPushAccess(local, facts, nil)
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want one for the read-only KB and one for the unreachable", got)
	}
	var refused, offline *doctorFinding
	for i := range got {
		if strings.Contains(got[i].Message, `"kb-ro"`) {
			refused = &got[i]
		}
		if strings.Contains(got[i].Message, `"kb-off"`) {
			offline = &got[i]
		}
	}
	if refused == nil || refused.Check != "push-access" || refused.Severity != doctorWarning || !strings.Contains(refused.Message, "cannot push") {
		t.Errorf("read-only KB finding = %+v, want a push-access warning", refused)
	}
	if offline == nil || offline.Severity != doctorInfo || !strings.Contains(offline.Message, "not checked") {
		t.Errorf("unreachable KB finding = %+v, want an info saying it was not checked", offline)
	}
	if doctorExitCode(doctorReport{Infos: 1}) != 0 {
		t.Error("an info finding must not fail the run")
	}
	if refs := refsOf(t, filepath.Join(dataDir, "kb-ro")); strings.Contains(refs, "cartographer-push-probe") {
		t.Errorf("the probe left a ref in the KB clone:\n%s", refs)
	}

	// A server on another machine has its clones elsewhere; one that did not
	// answer has told us nothing about its KBs.
	if got := checkPushAccess(&clientconfig.Config{ServerURL: "https://kb.example.com/mcp"}, facts, nil); len(got) != 0 {
		t.Errorf("remote server: %+v, want no findings", got)
	}
	if got := checkPushAccess(local, serverFacts{}, os.ErrNotExist); len(got) != 0 {
		t.Errorf("unreachable server: %+v, want no findings", got)
	}
}

// The check is part of the run: a local server's KB whose remote refuses the push
// shows up in `doctor` itself, not only in the check.
func TestRunDoctor_ReportsAKBThatCannotPush(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude") // a loopback server mounting "alpha"
	url, bare := gitxtest.ReadOnlyHTTPRemote(t, "token", pushTestToken)
	seedKB(t, bare)
	credDir, tok := tokenFile(t, "alpha", pushTestToken)
	dataDir := t.TempDir()
	if err := os.Rename(cloneWithToken(t, url, tok), filepath.Join(dataDir, "alpha")); err != nil {
		t.Fatal(err)
	}
	orig := doctorServerDirsFn
	doctorServerDirsFn = func() (string, string) { return dataDir, credDir }
	t.Cleanup(func() { doctorServerDirsFn = orig })

	found := findingsFor(runDoctor(dir, ""), "push-access")
	if len(found) != 1 || found[0].Severity != doctorWarning || !strings.Contains(found[0].Message, `"alpha"`) {
		t.Fatalf("push-access findings = %+v, want one warning for alpha", found)
	}
}

// Three verdicts, by an allow-list: the remote's own word on the credential is a
// refusal, a network failure is offline, and anything else (a TLS or host-key
// failure, a throttle, a server fault, a bare 403) is "could not check" and must
// never read as "the credential cannot push".
func TestClassifyPushError(t *testing.T) {
	const head = "git push --dry-run https://example.com/kb.git: "
	for _, tc := range []struct {
		name, out string
		want      pushVerdict
	}{
		{"read-only token", head + "this credential cannot push to the remote: use a token or key with write access\nremote: Permission to org/kb.git denied to user.\nfatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 403", pushRefused},
		{"write access", "remote: You do not have write access to this repository", pushRefused},
		{"github deploy key", "ERROR: The key you are authenticating with has been marked as read only.\nfatal: Could not read from remote repository.", pushRefused},
		{"gitlab ssh", "remote: GitLab: You are not allowed to push code to this project.\nfatal: Could not read from remote repository.", pushRefused},
		{"gitlab http", head + "this credential cannot push to the remote: use a token or key with write access\nremote: You are not allowed to push code to this project.\nfatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 403", pushRefused},
		{"gitlab http raw", "remote: You are not allowed to push code to this project.\nfatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 403", pushRefused},
		{"gitea", "remote: User permission denied for writing.\nremote: error: hook declined to update refs/heads/main", pushRefused},
		{"authentication failed", head + "the credentials were rejected: check the token\nfatal: Authentication failed for 'https://example.com/kb.git/'", pushRefused},
		{"401", "fatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 401", pushRefused},
		{"no credential", "fatal: could not read Username for 'https://example.com': terminal prompts disabled", pushRefused},
		{"rejected ssh key", head + "the forge rejected this machine's SSH key\ngit@example.com: Permission denied (publickey).", pushRefused},
		{"rule", "remote: error: GH013: Repository rule violations found", pushRefused},

		{"dns", "fatal: unable to access 'https://example.com/kb.git/': Could not resolve host: example.com", pushOffline},
		{"refused", "Failed to connect to 127.0.0.1 port 1 after 0 ms: Connection refused", pushOffline},
		{"network down", "ssh: connect to host example.com port 22: Network is down", pushOffline},
		{"reset", "read: Connection reset by peer", pushOffline},
		{"ssh banner", "kex_exchange_identification: Connection closed by remote host", pushOffline},
		{"closed", "Connection closed by remote host\nfatal: Could not read from remote repository.", pushOffline},
		{"interrupted", head + "interrupted: " + context.Canceled.Error(), pushInterrupted},
		{"deadline", fmt.Sprintf("%v for https://example.com/kb.git: context deadline exceeded", gitx.ErrProbeTimeout), pushOffline},

		{"untrusted tls", head + "the remote's TLS certificate is not trusted by this machine\nfatal: unable to access: SSL certificate problem: self-signed certificate", pushUnknown},
		{"host key", head + "the forge's host key is not in known_hosts\nHost key verification failed.", pushUnknown},
		{"429", "fatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 429", pushUnknown},
		{"503", "fatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 503", pushUnknown},
		{"502 rpc", "error: RPC failed; HTTP 502 curl 22 The requested URL returned error: 502", pushUnknown},
		{"bare 403", "fatal: unable to access 'https://example.com/kb.git/': The requested URL returned error: 403", pushUnknown},
		{"not found", "remote: Repository not found.\nfatal: repository 'https://example.com/kb.git/' not found", pushUnknown},
		{"gitlab protected", "remote: GitLab: You are not allowed to push code to protected branches on this project.", pushUnknown},
		{"unrecognised", head + "exit status 128: fatal: something git has never said", pushUnknown},
	} {
		var err error = errors.New(tc.out)
		if tc.name == "interrupted" {
			err = fmt.Errorf("push probe of https://example.com/kb.git interrupted: %w", context.Canceled)
		}
		if tc.name == "deadline" {
			err = fmt.Errorf("%w for https://example.com/kb.git: context deadline exceeded", gitx.ErrProbeTimeout)
		}
		if got := classifyPushError(err); got != tc.want {
			t.Errorf("%s: verdict %d, want %d for %q", tc.name, got, tc.want, tc.out)
		}
	}
}

// The server pushes through origin, which honours remote.origin.pushurl: that is
// the URL to probe, not the fetch URL.
func TestProbeKBPush_ProbesThePushURL(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	kb := filepath.Join(t.TempDir(), "kb")
	mustRunGit(t, "", "init", "-b", "main", kb)
	mustRunGit(t, kb, "remote", "add", "origin", "https://read.example.com/kb.git")
	var asked string
	orig := probePushAccessFn
	probePushAccessFn = func(_ context.Context, remote string, _ ...string) error { asked = remote; return nil }
	t.Cleanup(func() { probePushAccessFn = orig })

	if r := probeKBPush(context.Background(), "kb", kb, ""); r.Verdict != pushWritable || asked != "https://read.example.com/kb.git" {
		t.Fatalf("without a pushurl: %+v asked %q, want the fetch URL", r, asked)
	}
	mustRunGit(t, kb, "config", "remote.origin.pushurl", "https://write.example.com/kb.git")
	if r := probeKBPush(context.Background(), "kb", kb, ""); r.Verdict != pushWritable || asked != "https://write.example.com/kb.git" {
		t.Fatalf("with a pushurl: %+v asked %q, want the push URL", r, asked)
	}
}

// A probe failure that is neither a refusal nor silence is "could not check":
// an info finding that leaves the exit code alone, and a note from `kb clone`.
func TestProbeKBPush_UnknownIsNotARefusal(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	kb := filepath.Join(t.TempDir(), "kb")
	mustRunGit(t, "", "init", "-b", "main", kb)
	mustRunGit(t, kb, "remote", "add", "origin", "https://example.com/kb.git")
	orig := probePushAccessFn
	probePushAccessFn = func(context.Context, string, ...string) error {
		return errors.New("git push --dry-run https://example.com/kb.git: the remote's TLS certificate is not trusted by this machine\nSSL certificate problem")
	}
	t.Cleanup(func() { probePushAccessFn = orig })

	r := probeKBPush(context.Background(), "kb", kb, "")
	if r.Verdict != pushUnknown || r.Cause != "the remote's TLS certificate is not trusted by this machine" {
		t.Fatalf("result = %+v, want unknown with the cause and no URL", r)
	}
	got := pushFindings([]string{"kb"}, "/data", []pushResult{r})
	if len(got) != 1 || got[0].Severity != doctorInfo || !strings.Contains(got[0].Message, "could not be checked") {
		t.Fatalf("findings = %+v, want one info", got)
	}
	if doctorExitCode(doctorReport{Findings: got, Infos: 1}) != 0 {
		t.Error("a probe that could not check must not fail the run")
	}
	errOut := withStderr(t, func() { warnIfCannotPush("kb", kb, "", t.TempDir()) })
	if !strings.Contains(errOut, "Note:") || !strings.Contains(errOut, "could not be checked") || !strings.Contains(errOut, "TLS certificate") || strings.Contains(errOut, "Warning") {
		t.Errorf("stderr = %q, want a note with the cause and no warning", errOut)
	}
}

// Ctrl-C during the probe of `kb clone` is not a deadline: the note says it was
// interrupted, never "no answer within the probe's deadline".
func TestWarnIfCannotPush_InterruptedIsNotADeadline(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	kb := filepath.Join(t.TempDir(), "kb")
	mustRunGit(t, "", "init", "-b", "main", kb)
	mustRunGit(t, kb, "remote", "add", "origin", "https://example.com/kb.git")
	orig := probePushAccessFn
	probePushAccessFn = func(context.Context, string, ...string) error {
		return fmt.Errorf("push probe of https://example.com/kb.git interrupted: %w", context.Canceled)
	}
	t.Cleanup(func() { probePushAccessFn = orig })

	if r := probeKBPush(context.Background(), "kb", kb, ""); r.Verdict != pushInterrupted || r.Cause != "interrupted" {
		t.Fatalf("result = %+v, want interrupted", r)
	}
	errOut := withStderr(t, func() { warnIfCannotPush("kb", kb, "", t.TempDir()) })
	if !strings.Contains(errOut, "interrupted") || strings.Contains(errOut, "deadline") || strings.Contains(errOut, "Warning") {
		t.Errorf("stderr = %q, want a note that says interrupted", errOut)
	}
	if got := pushFindings([]string{"kb"}, "/data", []pushResult{{Verdict: pushInterrupted, Cause: "interrupted"}}); len(got) != 0 {
		t.Errorf("an interrupted probe is no finding: %+v", got)
	}
}

// 20 KBs behind one forge that is down are one line, not twenty; a different
// cause is its own line; a refusal stays one warning per KB.
func TestPushFindings_AggregatesByCause(t *testing.T) {
	down := pushResult{Verdict: pushOffline, Cause: "could not resolve host"}
	other := pushResult{Verdict: pushOffline, Cause: "connection refused"}
	unknown := pushResult{Verdict: pushUnknown, Cause: "could not resolve host"}
	refused := pushResult{Verdict: pushRefused, Cause: "no write access"}
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	got := pushFindings(names, "/data", []pushResult{down, refused, down, other, {Verdict: pushWritable}, unknown, {Verdict: pushNoRemote}})
	if len(got) != 4 {
		t.Fatalf("findings = %+v, want a warning, and one info per distinct verdict and cause", got)
	}
	if got[0].Severity != doctorWarning || !strings.Contains(got[0].Message, `"b"`) {
		t.Errorf("first = %+v, want the warning for b", got[0])
	}
	if got[1].Severity != doctorInfo || !strings.Contains(got[1].Message, `KBs "a", "c"`) || got[1].Path != "/data" {
		t.Errorf("down = %+v, want one finding naming a and c", got[1])
	}
	if !strings.Contains(got[2].Message, `KB "d"`) || !strings.Contains(got[3].Message, `KB "f"`) || !strings.Contains(got[3].Message, "could not be checked") {
		t.Errorf("rest = %+v / %+v", got[2], got[3])
	}
}

// doctor opens at most pushProbeParallel connections at once.
func TestCheckPushAccess_CapsConcurrency(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	isolateGit(t)
	dataDir := t.TempDir()
	var names []string
	for i := 0; i < 3*pushProbeParallel; i++ {
		name := fmt.Sprintf("kb-%d", i)
		names = append(names, name)
		mustRunGit(t, "", "init", "-b", "main", filepath.Join(dataDir, name))
		mustRunGit(t, filepath.Join(dataDir, name), "remote", "add", "origin", "https://example.com/"+name+".git")
	}
	var inFlight, peak atomic.Int32
	origProbe, origDirs := probePushAccessFn, doctorServerDirsFn
	probePushAccessFn = func(context.Context, string, ...string) error {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	doctorServerDirsFn = func() (string, string) { return dataDir, t.TempDir() }
	t.Cleanup(func() { probePushAccessFn, doctorServerDirsFn = origProbe, origDirs })

	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:39273/mcp"}
	if got := checkPushAccess(cfg, serverFacts{Names: names}, nil); len(got) != 0 {
		t.Fatalf("findings = %+v, want none", got)
	}
	if p := peak.Load(); p < 2 || p > pushProbeParallel {
		t.Errorf("peak concurrent probes = %d, want between 2 and %d", p, pushProbeParallel)
	}
}
