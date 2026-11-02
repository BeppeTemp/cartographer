package main

import (
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// holdLockThenEdit holds the client lock, starts cmd in a goroutine, lets it
// run into the lock, makes an edit that only a loader-after-the-lock sees, and
// releases. It returns the config as saved at the end.
func holdLockThenEdit(t *testing.T, home string, cmd func()) *clientconfig.Config {
	t.Helper()
	release, err := provisioning.LockClientState(home, provisioning.DefaultClientLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	select {
	case <-done:
		t.Fatal("the command ran while the client lock was held")
	case <-time.After(300 * time.Millisecond):
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Paths = map[string]string{"repo:other": "/srv/other"}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not finish after the lock was released")
	}
	out, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// An edit made while a command waits for the lock must survive it (D172): the
// command loads after taking the lock, so it never saves a stale copy.
func TestPathsSetLoadsAfterTheLock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "")
	out := holdLockThenEdit(t, home, func() { cmdPaths([]string{"set", "repo:mine", "/srv/mine"}) })
	if out.Paths["repo:other"] != "/srv/other" && out.Paths["other"] != "/srv/other" {
		t.Errorf("the edit made during the wait was lost: %v", out.Paths)
	}
	if out.Paths["mine"] != "/srv/mine" {
		t.Errorf("paths set was not applied: %v", out.Paths)
	}
}

func TestWithClientConfigLoadsAfterTheLock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "")
	out := holdLockThenEdit(t, home, func() {
		_ = withClientConfig(home, func(c *clientconfig.Config) error {
			c.IgnoredPaths = append(c.IgnoredPaths, "repo:x")
			return nil
		})
	})
	if len(out.IgnoredPaths) != 1 || (out.Paths["repo:other"] == "" && out.Paths["other"] == "") {
		t.Errorf("lost an update: paths=%v ignored=%v", out.Paths, out.IgnoredPaths)
	}
}

func TestSyncWaitsForTheLock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "")
	// cmdSync with no agent connected still loads and may save: it must wait.
	holdLockThenEdit(t, home, func() { cmdSync(nil) })
}
