package main

import (
	"context"
	"fmt"
	"io"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// Events run AFTER the work they follow, OUTSIDE the client lock (a handler is
// arbitrary code with a deadline of seconds, and every concurrent session-start
// sync waits for that lock, #1619), in plugin-name order. A failure is a warning
// that names plugin and event; it never changes an exit code or a returned error.

const (
	eventSyncAfter    = "sync.after"
	eventConnectAfter = "connect.after"
)

// lockedKeys are the artifacts the lockfile records, as the sync event tells
// installed from updated: an artifact written that was not in it is installed.
func lockedKeys(dir string) map[string]bool {
	keys := map[string]bool{}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return keys
	}
	add := func(l provisioning.Lock) {
		for _, m := range l.Managed {
			keys[m.Kind+"\x00"+m.Name+"\x00"+m.Path] = true
		}
	}
	for _, l := range lf.Providers {
		add(l)
	}
	for _, w := range lf.Workspaces {
		for _, l := range w.Providers {
			add(l)
		}
	}
	return keys
}

// eventFields is the summary a handler receives: counts and names derived from
// what the run already has; no paths, no token, no KB content. before is nil for
// connect, which has no snapshot: everything written there counts as installed.
func eventFields(event string, providers []string, before map[string]bool, results map[string]provisioning.AppliedResult, errs []string) map[string]any {
	var installed, updated, removed, pending int
	for _, r := range results {
		for _, m := range r.Written {
			if before[m.Kind+"\x00"+m.Name+"\x00"+m.Path] {
				updated++
			} else {
				installed++
			}
		}
		removed += len(r.Pruned)
		pending += len(r.NeedsApproval)
	}
	if errs == nil {
		errs = []string{}
	}
	return map[string]any{
		"event": event, "providers": append([]string{}, providers...),
		"installed": installed, "updated": updated, "removed": removed, "pending": pending, "errors": errs,
	}
}

// firePluginEvent runs every handler of event of the active plugins, sequentially.
// The caller has released the client lock.
func firePluginEvent(cfg *clientconfig.Config, event string, fields map[string]any, errw io.Writer) {
	if !pluginsEnabled() || cfg == nil || len(cfg.Plugins) == 0 {
		return
	}
	for _, s := range scriptedPlugins(cfg, errw) {
		for _, r := range s.prog.Registrations {
			if r.Kind != star.KindEvent || r.Name != event {
				continue
			}
			req := s.request(star.KindEvent, event, s.kbFor(cfg))
			req.Event, req.Log = fields, errw
			if _, err := callProgram(s.prog, context.Background(), req); err != nil {
				fmt.Fprintf(errw, "warning: plugin %s: %s failed: %s\n", escapeControl(s.name), event, escapeText(err.Error()))
			}
		}
	}
}
