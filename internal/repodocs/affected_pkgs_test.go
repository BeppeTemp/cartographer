package repodocs

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// affectedSeeds runs scripts/affected-pkgs.sh --seeds on paths, from the repository
// root (the seeds are the package directories, before go list expands them).
func affectedSeeds(t *testing.T, paths ...string) []string {
	t.Helper()
	cmd := exec.Command("bash", "scripts/affected-pkgs.sh", "--seeds")
	cmd.Dir = repoRoot(t)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("affected-pkgs.sh --seeds: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

// The seed table of the fast tier (make check) and of the Windows leg of make
// signoff. A miss is not a hole, make gate runs everything, but it makes the fast
// tier lie: every row here is a reader the table must keep.
func TestAffectedPackagesSeedTable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"go file", []string{"internal/kb/kb.go"}, "./internal/kb"},
		{"test file", []string{"internal/audit/audit_test.go"}, "./internal/audit"},
		{"testdata belongs to its package", []string{"internal/kb/testdata/x/y.md"}, "./internal/kb"},
		{"embedded skill belongs to the embedding package", []string{"internal/skillbundle/bundled/cartographer-ops/SKILL.md"}, "./internal/skillbundle"},
		{"nested command", []string{"internal/webui/gen/main.go"}, "./internal/webui/gen"},
		{"frontend is hashed by webui", []string{"web/src/lib/health.ts"}, "./internal/webui"},
		{"markdown is read by repodocs and the tool-citation check", []string{"docs/testing.md"}, "./internal/repodocs ./internal/mcpserver"},
		{"scripts and workflows are read by repodocs", []string{"scripts/signoff.sh", ".github/workflows/ci.yml", "Makefile"}, "./internal/repodocs"},
		{"the module selects everything", []string{"docs/x.md", "go.sum"}, "./..."},
		{"duplicates collapse", []string{"internal/kb/a.go", "internal/kb/b.go"}, "./internal/kb"},
		{"a deleted package selects nothing", []string{"internal/gone-for-good/x.go"}, ""},
		{"nothing in, nothing out", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(affectedSeeds(t, c.paths...), " "); got != c.want {
				t.Errorf("seeds of %v = %q, want %q", c.paths, got, c.want)
			}
		})
	}
}

// The expansion adds the packages whose tests depend on a seed, and only those.
func TestAffectedPackagesAddsReverseTestDependencies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	cmd := exec.Command("bash", "scripts/affected-pkgs.sh")
	cmd.Dir = repoRoot(t)
	cmd.Stdin = strings.NewReader("internal/execbit/execbit.go\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("affected-pkgs.sh: %v\n%s", err, out)
	}
	got := " " + strings.Join(strings.Fields(string(out)), " ") + " "
	for _, want := range []string{"./internal/execbit", "./internal/provisioning", "./cmd/cartographer"} {
		if !strings.Contains(got, " "+want+" ") {
			t.Errorf("a change to internal/execbit must select %s, got%s", want, got)
		}
	}
	for _, not := range []string{"./internal/okf", "./internal/graphalgo", "./..."} {
		if strings.Contains(got, " "+not+" ") {
			t.Errorf("internal/execbit does not reach %s, got%s", not, got)
		}
	}
}
