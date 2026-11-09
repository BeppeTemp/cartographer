package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// The whole path with the example Kubernetes lister: preflight, listing, the
// poll and the KB. Skipped where there is no Python 3.
func TestSystemPollWithTheExampleKubernetesLister(t *testing.T) {
	var py string
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			if out, err := exec.Command(p, "-c", "import sys; print(sys.version_info[0])").Output(); err == nil && strings.TrimSpace(string(out)) == "3" {
				py = p
				break
			}
		}
	}
	if py == "" {
		t.Skip("no Python 3 interpreter in PATH: the example lister is not run")
	}
	example, _ := filepath.Abs(filepath.Join("..", "..", "examples", "system-recipes", "kubernetes"))
	fix := t.TempDir()
	entries, err := os.ReadDir(filepath.Join(example, "testdata", "view"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(example, "testdata", "view", e.Name()))
		os.WriteFile(filepath.Join(fix, e.Name()), b, 0o600)
	}
	t.Setenv("LISTER_FIXTURES_DIR", fix)
	s := newSysEnv(t)
	s.in = s.instance(Config{ExecRecipes: []recipe.ExecConfig{{Name: "k8s-dev", Shape: recipe.ShapeSystem, Command: py, Args: []string{filepath.Join(example, "lister.py")},
		Env: map[string]string{"LISTER_FIXTURES_DIR": "LISTER_FIXTURES_DIR"}, Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 64 << 10, Instructions: "x"}}})
	s.src().fm["recipe"] = "k8s-dev"

	s.mustPoll()
	if len(s.k.units()) != 2 || len(s.unitsOf("namespace/team-a")) != 1 || len(s.unitsOf("namespace/team-b")) != 1 {
		t.Fatalf("first run: %v / %v", s.k.units(), s.feed())
	}
	s.mustPoll()
	if len(s.k.units()) != 2 {
		t.Fatalf("second run created units: %v", s.k.units())
	}
	nsFile := "get_deployments.apps,statefulsets.apps,daemonsets.apps,cronjobs.batch,services,ingresses.networking.k8s.io,persistentvolumeclaims,configmaps_-n_team-a_-o_json.json"
	b, _ := os.ReadFile(filepath.Join(fix, nsFile))
	os.WriteFile(filepath.Join(fix, nsFile), []byte(strings.Replace(string(b), "api:1.4.2", "api:1.5.0", 1)), 0o600)
	s.mustPoll()
	ids := s.unitsOf("namespace/team-a")
	if len(ids) != 2 {
		t.Fatalf("after an edit: %v", ids)
	}
	var diffed bool
	for _, id := range ids {
		diffed = diffed || strings.Contains(s.k.pages[id].body, "-Deployment api replicas=3 containers=web:registry.example.com/team-a/api:1.4.2") && strings.Contains(s.k.pages[id].body, "+Deployment api replicas=3 containers=web:registry.example.com/team-a/api:1.5.0")
	}
	if !diffed {
		t.Error("no unit carries the diff of the image change")
	}
	// A namespace that disappears: one removal unit.
	ns := filepath.Join(fix, "get_namespaces_-o_json.json")
	b, _ = os.ReadFile(ns)
	os.WriteFile(ns, []byte(strings.Replace(string(b), `"name": "team-b"`, `"name": "team-c"`, 1)), 0o600)
	os.Remove(filepath.Join(fix, strings.Replace(nsFile, "team-a", "team-c", 1)))
	b, _ = os.ReadFile(filepath.Join(fix, strings.Replace(nsFile, "team-a", "team-b", 1)))
	os.WriteFile(filepath.Join(fix, strings.Replace(nsFile, "team-a", "team-c", 1)), b, 0o600)
	s.mustPoll()
	rm := 0
	for _, id := range s.unitsOf("namespace/team-b") {
		if s.k.pages[id].fm["observed_change"] == "removed" {
			rm++
		}
	}
	if rm != 1 || len(s.unitsOf("namespace/team-c")) != 1 {
		t.Errorf("removal units %d, team-c units %v (%v)", rm, s.unitsOf("namespace/team-c"), s.feed()["feed_error"])
	}
}
