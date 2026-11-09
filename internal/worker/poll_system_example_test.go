package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

func python3(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			if out, err := exec.Command(p, "-c", "import sys; print(sys.version_info[0])").Output(); err == nil && strings.TrimSpace(string(out)) == "3" {
				return p
			}
		}
	}
	t.Skip("no Python 3 interpreter in PATH: the example lister is not run")
	return ""
}

// The verdict lister end to end: a value that moves inside its band is no unit and
// no model call, one that crosses an edge is one unit with the words in its diff.
func TestSystemPollWithTheExampleVerdictLister(t *testing.T) {
	py := python3(t)
	var mu sync.Mutex
	ratio := "1.25"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/status/flags":
			fmt.Fprint(w, `{"status":"success","data":{"web.enable-admin-api":"false"}}`)
		case "/api/v1/rules":
			fmt.Fprint(w, `{"status":"success","data":{"groups":[]}}`)
		default:
			value := ratio
			if strings.Contains(r.URL.Query().Get("query"), "node_filesystem_avail_bytes") {
				value = "90.5"
			}
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1900000000,%q]}]}}`, value)
		}
	}))
	defer srv.Close()
	example, _ := filepath.Abs(filepath.Join("..", "..", "examples", "system-recipes", "prometheus-verdicts"))
	t.Setenv("METRICS_URL", srv.URL)
	s := newSysEnv(t)
	s.in = s.instance(Config{ExecRecipes: []recipe.ExecConfig{{Name: "metrics-verdicts", Shape: recipe.ShapeSystem, Command: py,
		Args: []string{filepath.Join(example, "lister.py"), filepath.Join(example, "checks.json")}, Env: map[string]string{"METRICS_URL": "METRICS_URL"},
		Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 8 << 10, Instructions: "A group is one service."}}})
	s.src().fm["recipe"] = "metrics-verdicts"

	s.mustPoll()
	if len(s.k.units()) != 2 {
		t.Fatalf("first run: %v / %v", s.k.units(), s.feed())
	}
	calls := len(s.model.reqs)
	setRatio := func(v string) {
		mu.Lock()
		defer mu.Unlock()
		ratio = v
	}
	setRatio("0.5")
	s.mustPoll()
	if len(s.k.units()) != 2 || len(s.model.reqs) != calls {
		t.Fatalf("a move inside the band: units %v, model calls %d, was %d", s.k.units(), len(s.model.reqs), calls)
	}
	setRatio("7.5")
	s.mustPoll()
	ids := s.unitsOf("service/api")
	if len(ids) != 2 || len(s.unitsOf("storage/data")) != 1 {
		t.Fatalf("after an edge: %v / %v", ids, s.unitsOf("storage/data"))
	}
	var diffed bool
	for _, id := range ids {
		body := s.k.pages[id].body
		diffed = diffed || (strings.Contains(body, "-error-ratio: ok") && strings.Contains(body, "+error-ratio: bad"))
		if strings.Contains(body, "7.5") || strings.Contains(body, "90.5") {
			t.Errorf("unit %s holds a value:\n%s", id, body)
		}
	}
	if !diffed {
		t.Error("no unit carries the diff of the verdict")
	}
}

// The example's blocks load as they stand, with the instructions they name.
func TestExampleVerdictRecipeAndReviewTaskLoad(t *testing.T) {
	example := filepath.Join("..", "..", "examples", "system-recipes", "prometheus-verdicts")
	block, err := os.ReadFile(filepath.Join(example, "recipe.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prometheus-verdicts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"instructions.md", "system-review.md"} {
		b, err := os.ReadFile(filepath.Join(example, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "prometheus-verdicts", f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("WORKER_TOKEN", "tok")
	t.Setenv("MODEL_API_KEY", "key")
	head := strings.TrimSuffix(promptYAML, "tasks:\n")
	config := filepath.Join(dir, "worker.yaml")
	if err := os.WriteFile(config, []byte(head+strings.ReplaceAll(string(block), "model: cloud-small", "model: cloud")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(config, DefaultKinds())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ExecRecipes) != 1 || !strings.Contains(cfg.ExecRecipes[0].Instructions, "gaps/verdict-") || len(cfg.Tasks) != 1 {
		t.Errorf("recipes %+v, tasks %+v", cfg.ExecRecipes, cfg.Tasks)
	}
}

// The whole path with the example Kubernetes lister: preflight, listing, the
// poll and the KB. Skipped where there is no Python 3.
func TestSystemPollWithTheExampleKubernetesLister(t *testing.T) {
	py := python3(t)
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
