package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/audit"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/hooks"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// serverHooks is the registry of the hooks wired in the server config, built
// once at start. Empty when the config has no hooks: the points that consult it
// are then no-ops and the server behaves as without hooks.
var serverHooks *hooks.Registry

// serverBlockingSinks are the audit_sink hooks in blocking mode, for the KB
// servers (nil: none).
var serverBlockingSinks []*hooks.Sink

// serverIdentityHook is the identity point built from serverHooks (nil: none).
var serverIdentityHook auth.IdentityHook

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
	n := 0
	for _, h := range cfg.Hooks {
		if h.Point == hooks.PointIdentity {
			n++
		}
	}
	if n > 1 {
		return nil, fmt.Errorf("config: hooks: %d identity hooks: at most one serves the point", n)
	}
	if n == 1 && cfg.Auth.ProxyIdentity == nil {
		return nil, fmt.Errorf("config: hooks: an identity hook needs auth.proxy_identity (it names the user behind a trusted proxy)")
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

// startAuditSinks starts one delivery goroutine per audit_sink hook, reading
// the audit chain from the cursor each keeps beside the log, and records the
// blocking ones for the KB servers. The returned stop ends them, each after one
// last delivery attempt bounded by hooks.SinkStopFlush. A sink without an
// audit log refuses the start: it would deliver nothing, silently.
func startAuditSinks(reg *hooks.Registry, l *audit.Log, logPath string) (stop func(), err error) {
	list := reg.ForPoint(hooks.PointAuditSink)
	if len(list) == 0 {
		return func() {}, nil
	}
	if l == nil {
		return nil, fmt.Errorf("config: hooks: %q is an audit_sink, but this server keeps no audit log", list[0].Name)
	}
	src := func(cursor string, limit int) ([]hooks.SinkEvent, bool, error) {
		es, gap, err := l.EventsAfter(cursor, limit)
		out := make([]hooks.SinkEvent, 0, len(es))
		for _, e := range es {
			b, mErr := json.Marshal(e)
			if mErr != nil {
				return nil, false, mErr
			}
			out = append(out, hooks.SinkEvent{Hash: e.Hash, Entry: b})
		}
		return out, gap, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	serverBlockingSinks = nil
	for _, h := range list {
		sk, err := reg.NewSink(h.Name, src, l.Wake(), filepath.Dir(logPath))
		if err != nil {
			cancel()
			return nil, err
		}
		if h.Blocking {
			serverBlockingSinks = append(serverBlockingSinks, sk)
			log.Printf("hook %s: blocking audit sink: every call waits up to %s for it", h.Name, h.Timeout)
		}
		wg.Add(1)
		go func() { defer wg.Done(); sk.Run(ctx) }()
	}
	return func() { cancel(); wg.Wait() }, nil
}
