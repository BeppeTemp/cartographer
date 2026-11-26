package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCISameTree pins scripts/ci-same-tree.sh: a push to main skips its heavy legs only
// when a green dispatched run tested the same tree, and every failed read runs them.
func TestCISameTree(t *testing.T) {
	needBash(t)
	const (
		pushed = "1111111111111111111111111111111111111111"
		other  = "2222222222222222222222222222222222222222"
		gated  = "3333333333333333333333333333333333333333"
		treeA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		treeB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	// FAKE_RUNS is what `gh run list` prints; FAKE_TREES maps a commit to its tree.
	gh := `#!/usr/bin/env bash
case "$1 $2" in
  "run list") [ "${FAKE_LIST_RC:-0}" = 0 ] || exit 1; printf '%b' "$FAKE_RUNS" ;;
  "api "*) c=${2##*/}; for kv in $FAKE_TREES; do [ "${kv%%=*}" = "$c" ] && echo "${kv#*=}" && exit 0; done; exit 1 ;;
  *) exit 1 ;;
esac
`
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, sha, runs, trees string
		listFails              bool
		want                   string
	}{
		{"a dispatched run on the same tree", pushed, "7 " + other + "\\n8 " + gated + "\\n", pushed + "=" + treeA + " " + other + "=" + treeB + " " + gated + "=" + treeA, false, "green=true\nrun=8"},
		{"no run on this tree", pushed, "7 " + other + "\\n", pushed + "=" + treeA + " " + other + "=" + treeB, false, "green=false"},
		{"the run list cannot be read", pushed, "", pushed + "=" + treeA, true, "green=false"},
		{"the pushed tree cannot be read", pushed, "8 " + gated + "\\n", gated + "=" + treeA, false, "green=false"},
		{"a malformed sha", "main", "8 " + gated + "\\n", gated + "=" + treeA, false, "green=false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "ci-same-tree.sh"))
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "REPO=owner/name", "SHA="+c.sha,
				"FAKE_RUNS="+c.runs, "FAKE_TREES="+c.trees)
			if c.listFails {
				cmd.Env = append(cmd.Env, "FAKE_LIST_RC=1")
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("ci-same-tree.sh exited %v: it must always exit 0", err)
			}
			if got := strings.TrimSpace(string(out)); got != c.want {
				t.Errorf("stdout = %q, want %q", got, c.want)
			}
		})
	}
}
