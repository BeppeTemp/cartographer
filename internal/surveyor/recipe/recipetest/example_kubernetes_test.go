package recipetest

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// The reference system lister of examples/system-recipes/kubernetes, run through
// the kit on its own fixtures (kubectl is replaced by files): every check that
// applies passes. Skipped where no Python 3 is found, as the lister's own tests
// are (CI also runs on Windows).
func TestTheKubernetesListerPassesTheKit(t *testing.T) {
	py := ""
	for _, name := range []string{"python3", "python"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if out, err := exec.Command(p, "-c", "import sys; print(sys.version_info[0])").Output(); err == nil && strings.TrimSpace(string(out)) == "3" {
			py = p
			break
		}
	}
	if py == "" {
		t.Skip("no Python 3 interpreter in PATH: the example lister is not run")
	}
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "examples", "system-recipes", "kubernetes"))
	if err != nil {
		t.Fatal(err)
	}
	sc := scenario(
		Step{Verb: "check", Expect: Expect{OK: new(true)}},
		Step{Verb: "list", Expect: Expect{Entries: []any{"namespace/team-a", "namespace/team-b"}, NextCursor: "empty"}},
	)
	sc.Source = Source{ID: "s/k", Title: "dev cluster", Locator: "kubernetes://dev-cluster"}
	sc.Env["LISTER_FIXTURES_DIR"] = filepath.Join(dir, "testdata", "view")
	res := Run(context.Background(), Input{Name: "k8s-dev", Scenario: sc, Exec: &recipe.ExecConfig{
		Name: "k8s-dev", Shape: recipe.ShapeSystem, Command: py, Args: []string{filepath.Join(dir, "lister.py")},
		Env: map[string]string{"LISTER_FIXTURES_DIR": "CONFORMANCE_TEST_LISTER_FIXTURES_DIR"}, Timeout: time.Minute,
		MaxOutput: 1 << 20, MaxContent: 64 << 10, Instructions: "x"}})
	for _, r := range res {
		if r.Status == Fail {
			t.Errorf("%+v", r)
		}
	}
	for _, c := range []string{"spec", "parser", "entries", "repeatable", "system", "protocol", "secrets"} {
		if got := status(res, c); got != Pass {
			t.Errorf("%s: %s; %+v", c, got, res)
		}
	}
}
