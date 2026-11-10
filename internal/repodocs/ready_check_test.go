package repodocs

import (
	"os/exec"
	"strings"
	"testing"
)

// readyCheckPlan runs scripts/ready-check-plan.sh on paths, from the repository root, and
// returns its name=value lines.
func readyCheckPlan(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH (affected-pkgs.sh runs go list)")
	}
	cmd := exec.Command("bash", "scripts/ready-check-plan.sh")
	cmd.Dir = repoRoot(t)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ready-check-plan.sh: %v\n%s", err, out)
	}
	got := map[string]string{}
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			t.Fatalf("not a name=value line: %q\n%s", l, out)
		}
		got[k] = v
	}
	for _, k := range []string{"pkgs", "count", "shards", "web"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("no %s line:\n%s", k, out)
		}
	}
	return got
}

// What the hosted pre-check of a queued change runs (ready-check.yml): the Windows tests
// of the packages a code change can break, in as many shards as their weight needs, and the
// frontend's unit tests when it touches the frontend. The shard list feeds a job matrix, so
// it is a JSON array that is never empty, and it always matches the count.
func TestReadyCheckPlan(t *testing.T) {
	needBash(t)
	cases := []struct {
		name               string
		paths              []string
		pkgs, count, shard string
		web                string
	}{
		{"documents have no Windows leg", []string{"docs/testing.md", "CONTRIBUTING.md"}, "", "1", "[0]", "false"},
		{"nothing in, nothing to run", nil, "", "1", "[0]", "false"},
		{"the module selects everything, in four shards", []string{"go.sum"}, "./...", "4", "[0,1,2,3]", "false"},
		{"the frontend runs its unit tests", []string{"web/src/lib/health.ts"}, "-", "-", "-", "true"},
		{"the bundle runs them too", []string{"internal/webui/dist/provenance.json"}, "-", "-", "-", "true"},
		{"the control plane is the slow package", []string{"internal/mcpserver/server.go"}, "-", "4", "[0,1,2,3]", "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readyCheckPlan(t, c.paths...)
			for k, want := range map[string]string{"pkgs": c.pkgs, "count": c.count, "shards": c.shard, "web": c.web} {
				if want != "-" && got[k] != want {
					t.Errorf("%s = %q, want %q (plan %v)", k, got[k], want, got)
				}
			}
			// The count follows the packages, whatever the change.
			n, slow := 0, false
			for p := range strings.FieldsSeq(got["pkgs"]) {
				n++
				slow = slow || p == "./..." || p == "./internal/mcpserver" || p == "./cmd/cartographer"
			}
			want := "1"
			if slow {
				want = "4"
			} else if n > 4 {
				want = "2"
			}
			if got["count"] != want {
				t.Errorf("count = %s for %d package(s) (slow: %v), want %s", got["count"], n, slow, want)
			}
			shards := map[string]string{"1": "[0]", "2": "[0,1]", "4": "[0,1,2,3]"}[got["count"]]
			if got["shards"] != shards {
				t.Errorf("shards = %s, want %s for count %s", got["shards"], shards, got["count"])
			}
		})
	}
}
