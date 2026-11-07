package main

import (
	"context"
	"io"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpbridge"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
)

// bridgePluginTools are the tools of the active plugins for the stdio bridge:
// the same plugin set a command or an event runs (scriptedPlugins), so an
// unapproved or stale tree never runs here either. A plugin whose script does
// not load logs one line to warn and contributes nothing; it never fails the
// bridge. Each plugin is loaded once, here.
//
// kb.call goes through the bridge's own adapter (the session's scope, token and
// author), not cliKB: the caller's ToolClient arrives with each call, and one
// star.MCPKB is shared by every tool so the server's read-only classification
// is fetched once per session.
func bridgePluginTools(cfg *clientconfig.Config, warn io.Writer) []mcpbridge.PluginTool {
	var (
		once sync.Once
		kb   star.KBCaller
	)
	sharedKB := func(c mcpbridge.ToolClient) star.KBCaller {
		once.Do(func() { kb = star.NewMCPKB(c) })
		return kb
	}
	var out []mcpbridge.PluginTool
	for _, s := range scriptedPlugins(cfg, warn) {
		for _, r := range s.prog.Sorted() {
			if r.Kind != star.KindTool {
				continue
			}
			s, r := s, r
			out = append(out, mcpbridge.PluginTool{
				Plugin: s.name, Name: r.Name, Description: r.Help, Params: r.Params, Writes: r.Writes,
				Run: func(ctx context.Context, c mcpbridge.PluginCall) (string, error) {
					var caller star.KBCaller
					if p := s.p.Manifest.Permissions; p != nil && p.KB != "" {
						caller = sharedKB(c.KB)
					}
					req := s.request(star.KindTool, r.Name, caller)
					req.Params, req.ReadOnly, req.Log = c.Params, c.ReadOnly, c.Log
					// The client sees writes=False as readOnlyHint and may auto-approve it.
					req.KBReadOnly = !r.Writes
					res, err := callProgram(s.prog, ctx, req)
					if err != nil {
						return "", err
					}
					return res.Text, nil
				},
			})
		}
	}
	return out
}
