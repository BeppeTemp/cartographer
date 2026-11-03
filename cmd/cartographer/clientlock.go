package main

import (
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// withClientConfig is the one way a command read-modify-writes
// .cartographer.yaml (D172): it takes the client lock first, loads after taking
// it, runs fn on that fresh copy and saves it when fn returns nil. A copy
// loaded before the lock could be stale by the time it is saved, and the last
// writer would win silently.
//
// It must not wrap a caller that already holds the lock (realign, doctor,
// sync, the dashboard): a second descriptor on the same lock file in the same
// process blocks until the timeout. Such a caller uses clientconfig.Load and
// Save directly.
//
// A missing or unloadable file is an error here; connect, which may create the
// file, goes through loadConfigForWrite under its own lock.
func withClientConfig(dir string, fn func(cfg *clientconfig.Config) error) error {
	release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
	if err != nil {
		return err
	}
	defer release()
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return err
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return clientconfig.Save(dir, cfg)
}

// lockClient takes the client lock for a command that loads, works and saves in
// one go; the caller defers the returned release and loads after this call
// (D172). Same nesting rule as withClientConfig.
func lockClient(dir string) (func(), error) {
	release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
	if err != nil {
		return nil, err
	}
	return func() { _ = release() }, nil
}
