package main

import (
	"path/filepath"
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
	mine := filepath.Join(t.TempDir(), "mine") // absolute on every OS
	out := holdLockThenEdit(t, home, func() { cmdPaths([]string{"set", "repo:mine", mine}) })
	if out.Paths["repo:other"] != "/srv/other" && out.Paths["other"] != "/srv/other" {
		t.Errorf("the edit made during the wait was lost: %v", out.Paths)
	}
	if out.Paths["mine"] != mine {
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

// connect, disconnect and reconnect hold the client lock for their whole run
// (D172): a dry run is the only one that does not wait for it.
func TestConnectDisconnectReconnectWaitForTheLock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	conn := connectOptions{Providers: []string{"claude"}, Dir: home, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer"}
	calls := map[string]func() error{
		"connect": func() error { _, err := doConnect(conn); return err },
		"disconnect": func() error {
			_, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: home})
			return err
		},
		"reconnect": func() error { _, err := doReconnect(reconnectOptions{Dir: home, Target: "all"}); return err },
	}
	// connect first, so the other two have something to act on.
	if err := calls["connect"](); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"connect", "disconnect", "reconnect"} {
		release, err := provisioning.LockClientState(home, provisioning.DefaultClientLockTimeout)
		if err != nil {
			t.Fatal(err)
		}
		errc := make(chan error, 1)
		go func() { errc <- calls[name]() }()
		select {
		case <-errc:
			t.Fatalf("%s ran while the client lock was held", name)
		case <-time.After(300 * time.Millisecond):
		}
		release()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("%s after the release: %v", name, err)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%s did not finish after the lock was released", name)
		}
	}
	// A dry run takes no lock.
	release, err := provisioning.LockClientState(home, provisioning.DefaultClientLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	dry := conn
	dry.DryRun = true
	if _, err := doConnect(dry); err != nil {
		t.Errorf("dry-run connect under a held lock: %v", err)
	}
}
