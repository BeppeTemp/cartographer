package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

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
