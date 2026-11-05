package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// privacy-guard.py --redact applied spans computed on text.lower() to text:
// a character lower() lengthens (U+0130) shifted every later span and left part
// of the term in what is PATCHed on the issue. A throwaway key and term.
func TestPrivacyGuardRedactKeepsOffsets(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	// On Windows without Python, LookPath finds the App Execution Alias in
	// WindowsApps, which exits 9009 instead of running anything (#1669).
	if err := exec.Command(py, "-I", "-c", "").Run(); err != nil {
		t.Skipf("python3 at %s does not run (a Microsoft Store alias?): %v", py, err)
	}
	root := repoRoot(t)
	guard := filepath.Join(root, ".github", "privacy-guard.py")
	dir := t.TempDir()
	digests := filepath.Join(dir, "digests")
	env := append(os.Environ(), "PRIVACY_GUARD_KEY=00ff", "PRIVACY_GUARD_DIGESTS="+digests)

	add := exec.Command(py, "-I", guard, "--add")
	add.Env = env
	add.Stdin = strings.NewReader("zebratestterm\nzebratestterk\n")
	out, err := add.Output()
	if err != nil {
		t.Fatalf("--add: %v", err)
	}
	if err := os.WriteFile(digests, out, 0o600); err != nil {
		t.Fatal(err)
	}

	f := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(f, []byte("İİİ ZebraTestTerm ok, İ zebratestterm end, Zebratestter\u212a"), 0o600); err != nil {
		t.Fatal(err)
	}
	red := exec.Command(py, "-I", guard, "--redact", f)
	red.Env = env
	if b, err := red.CombinedOutput(); err != nil {
		t.Fatalf("--redact: %v\n%s", err, b)
	}
	got, _ := os.ReadFile(f)
	if want := "İİİ [redacted] ok, İ [redacted] end, [redacted]"; string(got) != want {
		t.Errorf("redacted text %q, want %q", got, want)
	}
}

// The final stage runs as a numeric user: a pod with runAsNonRoot: true and no
// runAsUser is refused for a name the kubelet cannot map to a uid.
func TestDockerfileFinalStageUserIsNumeric(t *testing.T) {
	var user string
	for _, l := range strings.Split(readFile(t, repoRoot(t), "Dockerfile"), "\n") {
		if f := strings.Fields(l); len(f) == 2 && strings.EqualFold(f[0], "USER") {
			user = f[1]
		}
		if f := strings.Fields(l); len(f) > 0 && strings.EqualFold(f[0], "FROM") {
			user = "" // the last stage's USER wins
		}
	}
	if !regexp.MustCompile(`^[0-9]+(:[0-9]+)?$`).MatchString(user) {
		t.Errorf("Dockerfile: final stage USER %q, want a numeric uid[:gid]", user)
	}
}

// One explicit `npm ci` per CI leg: the Node targets repeat it unless NPM_CI is empty.
func TestNpmCIIsOverridable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("make is not part of the Windows leg")
	}
	mk, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make not on PATH")
	}
	for _, target := range []string{"web-test", "web", "e2e-web", "hero"} {
		run := func(args ...string) string {
			cmd := exec.Command(mk, append([]string{"-n", target}, args...)...)
			cmd.Dir = repoRoot(t)
			b, err := cmd.Output()
			if err != nil {
				t.Fatalf("make -n %s: %v", target, err)
			}
			return string(b)
		}
		if !strings.Contains(run(), "npm ci") {
			t.Errorf("make %s: no npm ci by default", target)
		}
		if strings.Contains(run("NPM_CI="), "npm ci") {
			t.Errorf("make %s NPM_CI=: still runs npm ci", target)
		}
	}
}
