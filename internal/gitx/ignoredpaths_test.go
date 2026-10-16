package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The paths travel on stdin, not in argv: a move of many files would otherwise
// fail with "argument list too long" (the limit is a few hundred KiB on macOS
// and 32 KiB on Windows), and a path with a newline or a non-ASCII character
// would come back quoted.
func TestIgnoredPathsDoesNotGrowArgv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 200)
	var paths, want []string
	for i := 0; i < 10000; i++ {
		ext := "md"
		if i%2 == 0 {
			ext = "sh"
		}
		p := fmt.Sprintf("skills/s/%s/f%05d.%s", long, i, ext)
		paths = append(paths, p)
		if ext == "sh" {
			want = append(want, p)
		}
	}
	got, err := IgnoredPaths(dir, paths)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("IgnoredPaths over %d paths: %d ignored, err %v; want %d", len(paths), len(got), err, len(want))
	}
}

func TestIgnoredPathsUnusualNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An accent and a quote would be C-quoted without -z; a newline would split the name.
	paths := []string{"skills/s/è.sh", "skills/s/it's.sh", "skills/s/a\nb.sh", "skills/s/ok.md"}
	got, err := IgnoredPaths(dir, paths)
	if want := paths[:3]; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("IgnoredPaths = %q, %v; want %q", got, err, want)
	}
}

func TestIgnoredPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := IgnoredPaths(dir, []string{"hooks/g/hook.json", "hooks/g/run.sh", "skills/s/SKILL.md"})
	if err != nil || !reflect.DeepEqual(got, []string{"hooks/g/run.sh"}) {
		t.Errorf("IgnoredPaths = %v, %v; want [hooks/g/run.sh] (paths need not exist)", got, err)
	}
	got, err = IgnoredPaths(dir, []string{"a.md"})
	if err != nil || len(got) != 0 {
		t.Errorf("nothing ignored: got %v, %v", got, err)
	}
	if got, err = IgnoredPaths(dir, nil); err != nil || got != nil {
		t.Errorf("no paths: got %v, %v", got, err)
	}
}
