package provisioning

import (
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Crush hooks (D363). Probed on Crush v0.98.0: `hooks.<Event>[]` in
// ~/.config/crush/crush.json and in a project .crush.json fire; of six events
// tried only PreToolUse did. An entry is {name?, matcher?, command, timeout?}
// and `name` is the ownership key here: "cartographer-<hook>" is upserted and
// removed by it, and every other entry and key of the file is preserved
// (D23/D57 partial JSON edit).

const (
	crushGlobalHooksPrefix  = ".config/crush/hooks/"
	crushProjectHooksPrefix = ".crush/hooks/"
	crushProjectConfigRel   = ".crush.json"
)

func crushHookName(hookName string) string { return "cartographer-" + hookName }

// crushSettingsRel returns the config file, relative to the base dir, that
// registers a hook whose files live under hookRel (slash form): the project
// .crush.json for a project hook directory, the global crush.json otherwise.
func crushSettingsRel(hookRel string) string {
	if strings.HasPrefix(filepath.ToSlash(hookRel), crushProjectHooksPrefix) {
		return crushProjectConfigRel
	}
	return configurator.CrushConfigPath
}

func crushSettingsPath(baseDir, hookRel string) string {
	return filepath.Join(baseDir, filepath.FromSlash(crushSettingsRel(hookRel)))
}

// crushHookRel is fullDestDir relative to baseDir, in slash form.
func crushHookRel(baseDir, fullDestDir string) string {
	rel, err := filepath.Rel(baseDir, fullDestDir)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// crushMatcher turns the KB matcher (Claude's vocabulary, `Edit|Write`) into
// one Crush accepts. Trap: Crush's tool names are lowercase (`bash`, `edit`),
// so the case-sensitive Claude names would never match; `(?i)` (Go RE2, Crush
// is Go) makes the same alternation match both. Empty stays empty: all tools.
func crushMatcher(matcher string) string {
	m := strings.TrimSpace(matcher)
	if m == "" || strings.HasPrefix(m, "(?i)") {
		return m
	}
	return "(?i)" + m
}

// registerCrushHook upserts hookName's PreToolUse entry. Only PreToolUse
// fires on Crush, so any other event installs the files and warns.
func registerCrushHook(baseDir, hookName, fullDestDir string) (string, error) {
	spec, specErr := loadHookSpec(fullDestDir)
	if specErr != nil {
		return hookSpecWarning(hookName, configurator.ProviderCrush, specErr), nil
	}
	if warning, skip := hookEventWarning(hookName, configurator.ProviderCrush, spec.Event); skip {
		return warning, nil
	}

	path := crushSettingsPath(baseDir, crushHookRel(baseDir, fullDestDir))
	settings, err := loadJSONObject(path)
	if err != nil {
		return "", err
	}
	entry := map[string]interface{}{
		"name":    crushHookName(hookName),
		"command": resolveHookCommand(spec.Command, fullDestDir),
	}
	if m := crushMatcher(spec.Matcher); m != "" {
		entry["matcher"] = m
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	list, _ := hooks[spec.Event].([]interface{})
	list = withoutCrushHook(list, crushHookName(hookName))
	hooks[spec.Event] = append(list, entry)
	settings["hooks"] = hooks
	return "", saveJSONObject(path, settings)
}

// removeCrushHook strips hookName's entry from the config file that matches
// hookRel (the managed path of one of its files), dropping a list and a hooks
// map it empties. The file is never deleted: it carries the user's own
// configuration.
func removeCrushHook(baseDir, hookName, hookRel string) error {
	path := crushSettingsPath(baseDir, hookRel)
	settings, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	changed := false
	for event, raw := range hooks {
		list, ok := raw.([]interface{})
		if !ok {
			continue
		}
		kept := withoutCrushHook(list, crushHookName(hookName))
		if len(kept) == len(list) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if !changed {
		return nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return saveJSONObject(path, settings)
}

func withoutCrushHook(list []interface{}, name string) []interface{} {
	out := make([]interface{}, 0, len(list))
	for _, raw := range list {
		if e, ok := raw.(map[string]interface{}); ok && e["name"] == name {
			continue
		}
		out = append(out, raw)
	}
	return out
}
