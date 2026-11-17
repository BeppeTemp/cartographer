package kb

import (
	"strings"
	"testing"
)

func TestFirstLinesBoundsRemoteOutput(t *testing.T) {
	out := "\x1b[31mremote: boom\x1b[0m\rspoof\n" + strings.Repeat("y", 5000) + "\n\n" + strings.Repeat("z\n", 100)
	got := firstLines(out, 3)
	if strings.ContainsAny(got, "\x1b\r") || len(got) > 1100 {
		t.Fatalf("unbounded: %d bytes %q", len(got), got)
	}
	if strings.Count(got, " | ") > 2 {
		t.Fatalf("more than 3 lines: %q", got)
	}
	e := &ErrPushRefused{Branch: "main", Remedy: "r", Git: out}
	if strings.ContainsAny(e.Error(), "\x1b\r") || len(e.Error()) > 1500 {
		t.Fatalf("ErrPushRefused unbounded: %d", len(e.Error()))
	}
}

// The stored remedy is decided from the whole output, not the bounded text.
func TestStoredRemedySurvivesTheBound(t *testing.T) {
	banner := strings.Repeat("remote: "+strings.Repeat("=", 80)+"\n", 30)
	k := &KB{Root: t.TempDir()}
	k.setGitStatus("failed", &ErrProtectedBranch{Branch: "main", Git: banner + "remote: error: GH006: Protected branch update failed\n"}, 1)
	if k.gitStatus.LastRemedy != Remedy("GH006") {
		t.Fatalf("remedy %q", k.gitStatus.LastRemedy)
	}
	if len(k.gitStatus.LastError) > 2000 {
		t.Fatalf("last_error not bounded: %d", len(k.gitStatus.LastError))
	}
}
