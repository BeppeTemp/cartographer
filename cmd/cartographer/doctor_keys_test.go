package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// A typo in the client file or in the server file is a warning that names the
// key, the file and the nearest known key; the clean tree stays silent
// (TestRunDoctor_HealthyTree).
func TestRunDoctor_ReportsUnknownKeysOfBothFiles(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")

	clientPath := clientconfig.Path(dir)
	raw, err := os.ReadFile(clientPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientPath, append(raw, []byte("update_chek: false\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(serverPath, []byte("http: \":39273\"\ncredentials_dri: /creds\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverConfigPathFn = func() string { return serverPath }

	report := runDoctor(dir, "")
	got := findingsFor(report, "unknown-keys")
	if len(got) != 2 {
		t.Fatalf("unknown-keys findings = %+v, want one per file", got)
	}
	for _, c := range []struct{ path, key, fix string }{
		{clientPath, "update_chek", "rename it to update_check"},
		{serverPath, "credentials_dri", "rename it to credentials_dir"},
	} {
		ok := false
		for _, f := range got {
			ok = ok || f.Path == c.path && f.Severity == doctorWarning && strings.Contains(f.Message, c.key) && f.Fix == c.fix
		}
		if !ok {
			t.Errorf("no warning for %s in %s, fix %q: %+v", c.key, c.path, c.fix, got)
		}
	}
}

// No service, no server file: nothing to read, nothing said.
func TestCheckUnknownKeys_NoServerFile(t *testing.T) {
	doctorStubs(t, nil, false)
	serverConfigPathFn = func() string { return filepath.Join(t.TempDir(), "absent.yaml") }
	if got := checkUnknownKeys(t.TempDir()); len(got) != 0 {
		t.Errorf("findings = %+v, want none", got)
	}
}
