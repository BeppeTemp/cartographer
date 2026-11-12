package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A denied open is permissions, not contention (#1961): it fails at once with
// its own message and the sentinel the commands map to a distinct exit code,
// and any other open failure keeps the plain error.
func TestLockClientState_DeniedOpenHasItsOwnError(t *testing.T) {
	dir := t.TempDir()
	// A file where the directory should be: the open fails on every host.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	orig := openDenied
	t.Cleanup(func() { openDenied = orig })

	openDenied = func(error) bool { return true }
	start := time.Now()
	_, err := LockClientState(blocker, time.Minute)
	if !errors.Is(err, ErrClientDirDenied) {
		t.Fatalf("err = %v, want ErrClientDirDenied", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("a denied open was retried")
	}
	for _, want := range []string{"access denied", "not another cartographer operation", "normal terminal", "outside its sandbox", ClientLockFileName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}

	openDenied = func(error) bool { return false }
	if _, err := LockClientState(blocker, time.Second); err == nil || errors.Is(err, ErrClientDirDenied) {
		t.Fatalf("a plain open failure = %v, want an error that is not ErrClientDirDenied", err)
	}
}

func TestLockOpenDeniedError_NamesTheTokenKind(t *testing.T) {
	cause := errors.New("boom")
	restricted := lockOpenDeniedError(`C:\h\.lock`, `HOST\user`, true, cause).Error()
	plain := lockOpenDeniedError(`C:\h\.lock`, `HOST\user`, false, cause).Error()
	if !strings.Contains(restricted, `HOST\user`) || !strings.Contains(restricted, "a restricted token") {
		t.Errorf("restricted message = %q", restricted)
	}
	if strings.Contains(plain, "a restricted token") || !strings.Contains(plain, "unrestricted") {
		t.Errorf("plain message = %q", plain)
	}
}
