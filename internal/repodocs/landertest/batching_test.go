package landertest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every picked commit that touches web/ gets its bundle built again, not only one that conflicts:
// the bundle that lands is built from the sources that land. A commit off web/ builds nothing, and
// a build that fails keeps the committed bundle (the gate's TestBundleMatchesSource judges it).
func TestLanderRebuildsTheBundleOfEveryWebChange(t *testing.T) {
	e := newLandEnv(t)
	e.queue("ui", "fix(atlas): ui\n", "2026-01-01T10:00:00Z", map[string]string{
		"web/src/a.ts": "// new source\n", "internal/webui/dist/provenance.json": "stale\n"})
	e.queue("kb", "fix(kb): kb\n", "2026-01-01T10:01:00Z", map[string]string{"k.txt": "k\n"})
	build, count := countingBuild(t, e)
	out, ok := e.land("LAND_BUNDLE_BUILD=" + build)
	if !ok || len(e.mainSubjects()) != 2 {
		t.Fatalf("both land (ok=%v)\n%s", ok, out)
	}
	if got := e.upGit("show", "main~1:internal/webui/dist/provenance.json"); got != "// new source" {
		t.Errorf("the web change lands with the rebuilt bundle, got %q", got)
	}
	if n, _ := os.ReadFile(count); strings.Count(string(n), "run") != 1 {
		t.Errorf("one build, for the web change only, got %q", n)
	}

	e = newLandEnv(t)
	e.queue("ui", "fix(atlas): ui\n", "2026-01-01T10:00:00Z", map[string]string{
		"web/src/a.ts": "// new source\n", "internal/webui/dist/provenance.json": "committed\n"})
	out, ok = e.land("LAND_BUNDLE_BUILD=" + bundleBuildScript(t, e, filepath.Join(t.TempDir(), "m"), false))
	if !ok || e.refs("failed/") != "" || !strings.Contains(out, "ui: it touches web/: building it again failed") {
		t.Fatalf("a failed build rejects nothing (ok=%v failed=%q)\n%s", ok, e.refs("failed/"), out)
	}
	if got := e.upGit("show", "main:internal/webui/dist/provenance.json"); got != "committed" {
		t.Errorf("the committed bundle lands, got %q", got)
	}
}

// make web refuses untracked files under web/: the bundle's source hash counts tracked files
// only, so it would name other sources than the ones that land.
func TestMakeWebRefusesUntrackedSources(t *testing.T) {
	needBash(t)
	mk, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "new.ts"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmd := exec.Command(mk, "-f", filepath.Join(repoRoot(t), "Makefile"), "web")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "untracked files under web/") || !strings.Contains(string(out), "web/new.ts") {
		t.Errorf("make web must refuse and name the file (err=%v):\n%s", err, out)
	}
}
