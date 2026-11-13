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

// needBash skips where the script cannot run: Windows (bash there may be WSL) and
// an image without bash, such as the alpine one the docker job tests in.
func needBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
}

// The seed table of the fast tier (make check) and of the Windows leg of make
// signoff. A miss is not a hole, make gate runs everything, but it makes the fast
// tier lie: every row here is a reader the table must keep.
func TestAffectedPackagesSeedTable(t *testing.T) {
	needBash(t)
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"go file also selects the tree-scanning guards", []string{"internal/kb/kb.go"}, "./internal/kb ./internal/procattr ./internal/repodocs ./internal/boundaries"},
		{"test file", []string{"internal/audit/audit_test.go"}, "./internal/audit ./internal/procattr ./internal/repodocs ./internal/boundaries"},
		{"testdata belongs to its package", []string{"internal/kb/testdata/x/y.md"}, "./internal/kb"},
		{"embedded skill belongs to the embedding package", []string{"internal/skillbundle/bundled/cartographer-ops/SKILL.md"}, "./internal/skillbundle"},
		{"nested command", []string{"internal/webui/gen/main.go"}, "./internal/webui/gen ./internal/procattr ./internal/repodocs ./internal/boundaries"},
		{"frontend is hashed by webui", []string{"web/src/lib/health.ts"}, "./internal/webui"},
		{"markdown is read by repodocs, the tool-citation check and the docs KB gate", []string{"docs/testing.md"}, "./internal/repodocs ./internal/mcpserver ./internal/docskb"},
		{"scripts and workflows are read by repodocs", []string{"scripts/signoff.sh", ".github/workflows/ci.yml", "Makefile"}, "./internal/repodocs"},
		{"the module selects everything", []string{"docs/testing.md", "go.sum"}, "./..."},
		{"duplicates collapse", []string{"internal/kb/a.go", "internal/kb/b.go"}, "./internal/kb ./internal/procattr ./internal/repodocs ./internal/boundaries"},
		{"a deleted package still selects the guards", []string{"internal/gone-for-good/x.go"}, "./internal/procattr ./internal/repodocs ./internal/boundaries"},
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
	needBash(t)
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

// affected runs scripts/affected-pkgs.sh with args on paths and returns its packages.
func affected(t *testing.T, paths []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"scripts/affected-pkgs.sh"}, args...)...)
	cmd.Dir = repoRoot(t)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("affected-pkgs.sh %v: %v\n%s", args, err, out)
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// A reader seed selects only itself: a changed document changes what its test reads,
// not what the importers of its package run (a docs-only change used to pull every
// importer of internal/mcpserver into the Windows leg). --code, the Windows leg's
// selection, leaves the readers out.
func TestAffectedPackagesDoesNotExpandReaders(t *testing.T) {
	needBash(t)
	if got := affected(t, []string{"docs/testing.md", "scripts/land.sh"}); got != "./internal/docskb ./internal/mcpserver ./internal/repodocs" {
		t.Errorf("docs and scripts select %q, want only their readers", got)
	}
	if got := affected(t, []string{"docs/testing.md", "web/src/lib/health.ts"}, "--code"); got != "" {
		t.Errorf("--code on docs and frontend selects %q, want nothing", got)
	}
	got := " " + affected(t, []string{"docs/testing.md", "internal/execbit/execbit.go"}, "--code") + " "
	for _, want := range []string{"./internal/execbit", "./internal/provisioning", "./cmd/cartographer"} {
		if !strings.Contains(got, " "+want+" ") {
			t.Errorf("--code on a code change must keep %s, got%s", want, got)
		}
	}
	if strings.Contains(got, " ./internal/repodocs ") {
		t.Errorf("--code must leave the docs reader out, got%s", got)
	}
}

// TestEveryChildProcessIsHidden (internal/procattr), the repodocs checks and the
// import graph checks of internal/boundaries scan the source of every package: a Go
// file changed anywhere must select them, in the fast tier and, for procattr
// (Windows console windows), in the Windows leg too. A new import of a server-side
// package that crosses a boundary once passed make check (#1926). A new
// exec.Command without procattr.Hide in internal/gitx once passed make check and
// failed only the train gate.
func TestAffectedPackagesAlwaysRunsTheTreeScanningGuards(t *testing.T) {
	needBash(t)
	for _, args := range [][]string{nil, {"--seeds"}} {
		got := " " + affected(t, []string{"internal/gitx/gitx.go"}, args...) + " "
		for _, want := range []string{"./internal/gitx", "./internal/procattr", "./internal/repodocs", "./internal/boundaries"} {
			if !strings.Contains(got, " "+want+" ") {
				t.Errorf("%v: a Go change must select %s, got%s", args, want, got)
			}
		}
	}
	got := " " + affected(t, []string{"internal/gitx/gitx.go"}, "--code") + " "
	if !strings.Contains(got, " ./internal/procattr ") {
		t.Errorf("--code must keep the procattr guard, got%s", got)
	}
	for _, not := range []string{"./internal/repodocs", "./internal/boundaries"} {
		if strings.Contains(got, " "+not+" ") {
			t.Errorf("--code must leave %s out, got%s", not, got)
		}
	}
	if got := affected(t, []string{"docs/testing.md"}); strings.Contains(got, "procattr") || strings.Contains(got, "boundaries") {
		t.Errorf("a markdown change does not select procattr, got %q", got)
	}
}

// A checkout without <remote>/main (a copy synced to a build host, a shallow clone)
// cannot tell what it changed: --changed and make check must fail, not report "no Go
// package affected" and pass having tested nothing (#1748).
func TestCheckFailsWithoutTheBaseRef(t *testing.T) {
	needBash(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x")
	for _, f := range []string{"scripts/affected-pkgs.sh", "Makefile"} {
		c := exec.Command("sh", "-c", "mkdir -p "+dir+"/scripts && cp "+root+"/"+f+" "+dir+"/"+f)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("copy %s: %v\n%s", f, err, out)
		}
	}
	for _, args := range [][]string{{"bash", "scripts/affected-pkgs.sh", "--changed"}, {"make", "check"}} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err == nil {
			t.Fatalf("%v succeeded without origin/main:\n%s", args, out)
		}
		if !strings.Contains(string(out), "origin/main") && args[0] == "bash" {
			t.Errorf("%v does not name the missing base:\n%s", args, out)
		}
		if strings.Contains(string(out), "no Go package affected") {
			t.Errorf("%v claims nothing is affected:\n%s", args, out)
		}
	}
}
