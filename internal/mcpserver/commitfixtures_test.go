package mcpserver

import (
	"os/exec"
	"testing"
)

// commitFixtures commits what a test wrote straight into a KB's working tree.
// The server provisions only files git tracks (#1325); a write through a tool
// commits on its own, a fixture written with os.WriteFile does not.
func commitFixtures(t testing.TB, root string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "--allow-empty", "-m", "fixtures"}} {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}
