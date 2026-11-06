package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin"
)

// parseSetting reads a command-line value as the type the manifest declares.
func parseSetting(key string, st plugin.Setting, raw string) (any, error) {
	switch st.Type {
	case "bool":
		switch raw {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("%s is a bool: true or false, not %q", key, raw)
	case "int":
		n, err := strconv.ParseInt(raw, 10, strconv.IntSize)
		if err != nil {
			return nil, fmt.Errorf("%s is an int in base 10 that fits %d bits, not %q", key, strconv.IntSize, raw)
		}
		return int(n), nil
	}
	return raw, nil
}

func settingKeys(decl map[string]plugin.Setting) string {
	keys := make([]string, 0, len(decl))
	for k := range decl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "none (the manifest declares no settings)"
	}
	return strings.Join(keys, ", ")
}

// printPluginSettings lists every declared setting with its value and where the
// value comes from: `set` by the user, or the manifest's `default`.
func printPluginSettings(w io.Writer, p plugin.Plugin, e clientconfig.PluginEntry) {
	keys := make([]string, 0, len(p.Manifest.Settings))
	for k := range p.Manifest.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, src := p.Manifest.Settings[k].Default, "default"
		if sv, ok := e.Settings[k]; ok {
			v, src = sv, "set"
		}
		shown := "(unset)"
		if v != nil {
			shown = fmt.Sprint(v)
		}
		fmt.Fprintf(w, "setting %s = %s  (%s)\n", escapeControl(k), escapeControl(shown), src)
	}
}

// pluginConfig is `plugin config <name> [key=value ...] [--unset key ...]`. It
// writes only plugins.<name>.settings: never the digest, the approval or the
// permissions, so it asks for no new approval and a setting never widens a grant.
func pluginConfig(args []string) int {
	var name string
	sets := map[string]string{}
	var unset []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--unset" && i+1 < len(args):
			i++
			unset = append(unset, args[i])
		case strings.HasPrefix(a, "-"):
			return pluginFail(2, "%s", pluginUsage)
		case name == "":
			name = a
		default:
			k, v, ok := strings.Cut(a, "=")
			if !ok || k == "" {
				return pluginFail(2, "%q is not key=value", escapeControl(a))
			}
			sets[k] = v
		}
	}
	if name == "" {
		return pluginFail(2, "%s", pluginUsage)
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return pluginFail(2, "%s", escapeText(err.Error()))
	}
	if len(sets) == 0 && len(unset) == 0 {
		cfg, store, code := pluginLoad()
		if code >= 0 {
			return code
		}
		e, ok := cfg.Plugin(name)
		if !ok {
			return unknownPlugin(name)
		}
		_, p, tree := pluginView(store, name, e)
		if tree == "" {
			return pluginFail(1, "the stored tree of %s cannot be read", escapeControl(name))
		}
		printPluginSettings(os.Stdout, p, e)
		return 0
	}
	store, err := pluginStoreFn()
	if err != nil {
		return pluginFail(1, "%s", escapeText(err.Error()))
	}
	// The lock spans the load to the save, as every writer of the config does (D172).
	release, err := lockClient(dir)
	if err != nil {
		return pluginFail(1, "%s", escapeText(err.Error()))
	}
	defer release()
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return pluginFail(1, "%s", escapeText(err.Error()))
	}
	e, ok := cfg.Plugin(name)
	if !ok {
		return unknownPlugin(name)
	}
	_, p, tree := pluginView(store, name, e)
	if tree == "" {
		return pluginFail(1, "the stored tree of %s cannot be read", escapeControl(name))
	}
	decl := p.Manifest.Settings
	next := map[string]any{}
	for k, v := range e.Settings {
		next[k] = v
	}
	for _, k := range unset {
		if _, ok := decl[k]; !ok {
			return pluginFail(2, "%s declares no setting %q; its settings: %s", escapeControl(name), escapeControl(k), escapeControl(settingKeys(decl)))
		}
		delete(next, k)
	}
	keys := make([]string, 0, len(sets))
	for k := range sets {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		st, ok := decl[k]
		if !ok {
			return pluginFail(2, "%s declares no setting %q; its settings: %s", escapeControl(name), escapeControl(k), escapeControl(settingKeys(decl)))
		}
		v, err := parseSetting(k, st, sets[k])
		if err != nil {
			return pluginFail(2, "%s: %s", escapeControl(name), escapeControl(err.Error()))
		}
		next[k] = v
	}
	if len(next) == 0 {
		next = nil
	}
	e.Settings = next
	if err := cfg.SetPlugin(name, e); err != nil {
		return pluginFail(1, "%s", escapeText(err.Error()))
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		return pluginFail(1, "%s", escapeText(err.Error()))
	}
	printPluginSettings(os.Stdout, p, e)
	return 0
}
