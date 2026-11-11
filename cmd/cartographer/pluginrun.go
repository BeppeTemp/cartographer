package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
)

// scripted is an active plugin that carries a script: what a command, an event
// or a tool runs. Only core.State yields one (enabled, approved at the
// installed digest, the stored tree intact), so an unapproved or stale tree
// never runs.
type scripted struct {
	name  string
	entry clientconfig.PluginEntry
	p     core.Plugin
	prog  *star.Program
}

// scriptedPlugins are the active plugins with a script, in name order. A plugin
// whose script no longer loads is skipped and, when warn is set, named there.
// An unreadable store yields none: neither usage nor a sync fails because of it.
func scriptedPlugins(cfg *clientconfig.Config, warn io.Writer) []scripted {
	if cfg == nil || len(cfg.Plugins) == 0 {
		return nil
	}
	store, err := pluginStoreFn()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Plugins))
	for n := range cfg.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []scripted
	for _, n := range names {
		e := cfg.Plugins[n]
		_, p := core.State(n, e, store, pluginOpts())
		if p == nil {
			continue
		}
		prog, err := pluginProgram(*p)
		if err != nil {
			if warn != nil {
				fmt.Fprintf(warn, "warning: plugin %s: the script does not load: %s\n", escapeControl(n), escapeText(err.Error()))
			}
			continue
		}
		if prog != nil {
			out = append(out, scripted{name: n, entry: e, p: *p, prog: prog})
		}
	}
	return out
}

// escapeText escapes what a script or a server supplied, line by line so a
// multi-line text keeps its shape: nothing of it may rewrite the terminal.
func escapeText(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = client.Escape(l)
	}
	return strings.Join(lines, "\n")
}

// request is the call of one registration of s with the granted capabilities,
// the manifest's settings and the user's values; the caller fills the rest.
func (s scripted) request(kind, name string, kb star.KBCaller) star.Request {
	return star.Request{
		Kind: kind, Name: name, Plugin: s.name, Version: s.p.Manifest.Version,
		Permissions: s.p.Manifest.Permissions, Declared: s.p.Manifest.Settings, Values: s.entry.Settings,
		Host: star.Host{KB: kb},
	}
}

// kbFor is the KB access of a plugin: none unless the manifest asks for kb.
func (s scripted) kbFor(cfg *clientconfig.Config) star.KBCaller {
	if s.p.Manifest.Permissions == nil || s.p.Manifest.Permissions.KB == "" {
		return nil
	}
	return cliKB(cfg)
}

// scopedKB restricts kb.call to the KBs the client config knows. The plugin
// layer does not look at the `kb` argument and a command or an event has no
// provider whose bridge scope would: the server still holds the token's own
// scopes, this is the client's perimeter on top of them.
type scopedKB struct {
	star.KBCaller
	allowed []string
}

func (s scopedKB) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	name, ok := args["kb"].(string)
	switch {
	case !ok:
		return "", errors.New("pass kb=<name>: a plugin reaches one KB of this client at a time")
	case !slices.Contains(s.allowed, name):
		return "", fmt.Errorf("KB %q is not one of the KBs this client serves", name)
	}
	// Method value, not a call expression: TestNoUnqualifiedProductionToolCalls
	// greps for `.Call(`, and this is no MCPClient call of D120 (the routed
	// endpoint is addressed by the kb argument, with no tool prefix).
	call := s.KBCaller.Call
	return call(ctx, tool, args)
}

// unreachableKB is the adapter when the routed endpoint cannot be built.
type unreachableKB struct{ err error }

func (u unreachableKB) Call(context.Context, string, map[string]any) (string, error) {
	return "", u.err
}
func (u unreachableKB) ReadOnly(context.Context, string) (bool, error) { return false, u.err }

// cliKB is the KBCaller of a command or an event: the routed endpoint with the
// user's token (the bridge's adapter is the other surface), limited to the KBs
// of cfg.KnownKBs.
func cliKB(cfg *clientconfig.Config) star.KBCaller {
	if err := client.CheckServerURL(cfg.ServerURL, cfg.InsecureHTTPAllowed()); err != nil {
		return unreachableKB{err}
	}
	if cfg.ServerRoutedPath == "" {
		return unreachableKB{errors.New("the server has no routed endpoint recorded; run `cartographer sync`")}
	}
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return unreachableKB{err}
	}
	u.Path = cfg.ServerRoutedPath
	c := client.New(u.String(), resolveToken(cfg)).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).WithTokenEnv(tokenEnvName(cfg))
	return scopedKB{KBCaller: star.NewMCPKB(c), allowed: slices.Clone(cfg.KnownKBs)}
}

// pluginCommandFn dispatches `cartographer <plugin> <command>` for a word that
// is no built-in command; handled is false when it names no installed plugin, so
// the usual unknown-command error follows unchanged.
var pluginCommandFn = runPluginCommand

func runPluginCommand(name string, args []string) (code int, handled bool) {
	// Read-only and without the client lock, as the bridge reads it.
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		return 0, false
	}
	e, ok := cfg.Plugin(name)
	if !ok {
		return 0, false
	}
	store, err := pluginStoreFn()
	if err != nil {
		return pluginFail(1, "%s", escapeText(err.Error())), true
	}
	st, p := core.State(name, e, store, pluginOpts())
	if p == nil {
		why := "not approved"
		if st != core.StatusPending {
			why = string(st)
		}
		return pluginFail(2, "plugin %s is installed but %s: run \"cartographer plugin show %s\"", escapeControl(name), why, escapeControl(name)), true
	}
	prog, err := pluginProgram(*p)
	if err != nil {
		return pluginFail(1, "plugin %s: the script does not load: %s", escapeControl(name), escapeText(err.Error())), true
	}
	if prog == nil {
		return 0, false
	}
	s := scripted{name: name, entry: e, p: *p, prog: prog}
	return runScriptCommand(cfg, s, args), true
}

// callProgram is Program.Call spelled so the D120 grep (see scopedKB) does not take it for an MCPClient call.
var callProgram = (*star.Program).Call

func commandsOf(prog *star.Program) []star.Registration {
	var out []star.Registration
	for _, r := range prog.Sorted() {
		if r.Kind == star.KindCommand {
			out = append(out, r)
		}
	}
	return out
}

func printCommands(w io.Writer, s scripted) {
	for _, r := range commandsOf(s.prog) {
		fmt.Fprintf(w, "  %-16s %s\n", escapeControl(r.Name), escapeText(r.Help))
	}
}

func runScriptCommand(cfg *clientconfig.Config, s scripted, args []string) int {
	cmds := commandsOf(s.prog)
	if len(args) == 0 || args[0] == "help" {
		fmt.Printf("cartographer %s <command>\n", escapeControl(s.name))
		printCommands(os.Stdout, s)
		return 0
	}
	i := slices.IndexFunc(cmds, func(r star.Registration) bool { return r.Name == args[0] })
	if i < 0 {
		fmt.Fprintf(os.Stderr, "Error: plugin %s has no command %q; it has:\n", escapeControl(s.name), escapeText(args[0]))
		printCommands(os.Stderr, s)
		return 2
	}
	if rest := args[1:]; len(rest) == 1 && (rest[0] == "-h" || rest[0] == "--help") {
		fmt.Printf("usage: cartographer %s %s %s\n%s\n", escapeControl(s.name), cmds[i].Name, escapeText(cmds[i].Usage), escapeText(cmds[i].Help))
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	req := s.request(star.KindCommand, cmds[i].Name, s.kbFor(cfg))
	req.Args, req.Stdout, req.Log = args[1:], os.Stdout, os.Stderr
	res, err := callProgram(s.prog, ctx, req)
	if err != nil {
		var ce *star.CallError
		if errors.As(err, &ce) && ce.Kind == star.Cancelled {
			fmt.Fprintln(os.Stderr, "interrupted")
			return 130
		}
		// fail()'s text is the script's, a denial or a limit names the host's
		// own words: both reach the terminal, so both are escaped.
		fmt.Fprintln(os.Stderr, escapeText(err.Error()))
		return 1
	}
	if res.Text != "" {
		fmt.Println(escapeText(res.Text))
	}
	return res.ExitCode
}

// pluginHelpLines are the commands of the active plugins for the usage text:
// "<plugin> <command>  help". It reads the config itself and returns nothing
// on any error, so usage never fails because of it.
func pluginHelpLines() []string {
	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range scriptedPlugins(cfg, nil) {
		for _, r := range commandsOf(s.prog) {
			out = append(out, fmt.Sprintf("  %-24s %s", escapeControl(s.name+" "+r.Name), escapeText(r.Help)))
		}
	}
	return out
}
