package main

import (
	"fmt"
	"log"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/hooks"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// serverHooks is the registry of the hooks wired in the server config, built
// once at start. Empty when the config has no hooks: the points that consult it
// are then no-ops and the server behaves as without hooks.
var serverHooks *hooks.Registry

// buildServerHooks builds the registry or refuses the start, naming the hook.
// A hook is active only when the operator's entry AND an approved plugin exist:
// approved means pinned by digest in plugins and loaded for the server side.
// The manifest does not yet declare which hook points a plugin implements, so
// the point is not cross-checked against it here (hooks.Options.Approved is the
// one place that check lands).
func buildServerHooks(cfg *config.Config, plugins []*core.Plugin) (*hooks.Registry, error) {
	if len(cfg.Hooks) == 0 {
		return nil, nil
	}
	dir := cfg.CredentialsPath()
	if dir == "" {
		return nil, fmt.Errorf("config: hooks: a folder session has no credentials directory to read the hook credentials from (set credentials_dir)")
	}
	loaded := map[string]bool{}
	for _, p := range plugins {
		loaded[p.Manifest.Name] = true
	}
	return hooks.NewRegistry(cfg.Hooks, hooks.Options{
		CredentialsDir: dir,
		Approved:       func(plugin, _ string) bool { return loaded[plugin] },
	})
}

// logServerHooks says one line per hook, never a URL's query or a credential.
func logServerHooks(r *hooks.Registry) {
	for _, m := range r.Metrics() {
		h, _ := r.Hook(m.Name)
		log.Printf("hook %s: %s, %s, %s, timeout %s", h.Name, h.Point, h.Mode, h.FailurePolicy, h.Timeout)
	}
}
