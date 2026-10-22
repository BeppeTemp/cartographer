package repodocs

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubGH answers `gh api repos/<slug> --jq <filter>` the way the REST endpoint
// does for a fork of example/upstream, and fails every other gh command: in
// particular `gh repo view`, which is GraphQL and refused where only REST works.
const stubGH = `#!/usr/bin/env bash
if [ "$1" = api ] && [ "$2" = repos/user/fork ]; then echo example/upstream; exit 0; fi
if [ "$1" = api ] && [ "$2" = repos/example/upstream ]; then exit 0; fi
echo "stub gh: unexpected call: $*" >&2
exit 1
`

// runUpstreamRepo runs scripts/upstream-repo.sh in a fresh clone whose origin
// is originURL, with the stub gh first on PATH.
func runUpstreamRepo(t *testing.T, originURL string, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on POSIX shells; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stubGH), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for _, c := range [][]string{{"init", "-q"}, {"remote", "add", "origin", originURL}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, c...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", c, err, out)
		}
	}
	cmd := exec.Command(bash, append([]string{filepath.Join(repoRoot(t), "scripts", "upstream-repo.sh")}, args...)...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if err != nil {
		return strings.TrimSpace(errb.String()), err
	}
	return strings.TrimSpace(out.String()), nil
}

// The fork check uses the REST API: a cloud agent session refuses GraphQL, and
// `make worktree-add` then failed on a clone of the upstream itself.
func TestUpstreamRepoUsesRESTForTheForkCheck(t *testing.T) {
	if got, err := runUpstreamRepo(t, "https://github.com/user/fork.git"); err != nil || got != "example/upstream" {
		t.Errorf("fork origin: got %q, err %v; want example/upstream", got, err)
	}
	if got, err := runUpstreamRepo(t, "https://github.com/example/upstream"); err != nil || got != "example/upstream" {
		t.Errorf("upstream origin: got %q, err %v; want example/upstream", got, err)
	}
	if _, err := runUpstreamRepo(t, "https://github.com/example/upstream", "--require-remote"); err != nil {
		t.Errorf("--require-remote on the upstream itself must pass: %v", err)
	}
	if msg, err := runUpstreamRepo(t, "git@github.com:user/fork.git", "--require-remote"); err == nil || !strings.Contains(msg, "git remote add upstream https://github.com/example/upstream.git") {
		t.Errorf("--require-remote on a fork must fail naming the remote to add: %q, err %v", msg, err)
	}
}
