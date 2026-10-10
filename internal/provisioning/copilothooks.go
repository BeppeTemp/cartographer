package provisioning

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Copilot CLI hooks (D676). Probed on copilot 1.0.94: a file
// ~/.copilot/hooks/<file>.json, {"version":1,"hooks":{"<event>":[{"type":
// "command","bash":…,"powershell":…}]}}, fires. Cartographer owns one such file
// per hook ("cartographer-<hook>.json") and never edits ~/.copilot/settings.json,
// which also accepts inline hooks but carries the user's own configuration.
//
// Copilot has no matcher: a hook fires for every tool. A KB hook that declares
// one is therefore not registered, because registering it would change what it
// means (the write-findings hook declares none for Copilot and filters by tool
// name in its command, cmd/cartographer/hook.go).

// Copilot parses every *.json below ~/.copilot/hooks/, subdirectories included,
// so only registrations live there; the hooks' own files are materialized under
// copilotFilesPrefix (the destinationMatrix hook cell).
const (
	copilotHooksPrefix = ".copilot/hooks/"
	copilotFilesPrefix = ".copilot/cartographer-hooks/"
)

// copilotHookEvents maps the hook.json vocabulary to Copilot's event names.
// Only the three events that were probed to fire are claimed (hookEventReach).
var copilotHookEvents = map[string]string{
	"SessionStart": "sessionStart",
	"PreToolUse":   "preToolUse",
	"PostToolUse":  "postToolUse",
}

// copilotHookRelPath is the registration file, relative to the base dir. Slash
// form: it is recorded in the lockfile beside every other destination.
func copilotHookRelPath(hookName string) string {
	return copilotHooksPrefix + "cartographer-" + hookName + ".json"
}

// registerCopilotHook writes hookName's registration file and returns its
// relative path (empty when nothing was written) and a non-fatal warning.
func registerCopilotHook(baseDir, hookName, fullDestDir string) (relPath, warning string, err error) {
	spec, specErr := loadHookSpec(fullDestDir)
	if specErr != nil {
		return "", hookSpecWarning(hookName, configurator.ProviderCopilot, specErr), nil
	}
	if w, skip := hookEventWarning(hookName, configurator.ProviderCopilot, spec.Event); skip {
		return "", w, nil
	}
	event, ok := copilotHookEvents[spec.Event]
	if !ok {
		return "", fmt.Sprintf("hook %q: copilot: event %q has no Copilot equivalent; files were installed but the hook was not registered", hookName, spec.Event), nil
	}
	if strings.TrimSpace(spec.Matcher) != "" {
		return "", fmt.Sprintf("hook %q: copilot: Copilot has no matcher, so a hook with matcher %q would fire for every tool; files were installed but the hook was not registered", hookName, spec.Matcher), nil
	}

	command := resolveHookCommand(spec.Command, fullDestDir)
	// bash runs through a POSIX shell on Windows too (backslashes would be
	// eaten, D267); powershell keeps the host's spelling and needs the call
	// operator in front of a quoted executable.
	powershell := command
	if strings.HasPrefix(powershell, `"`) {
		powershell = "& " + powershell
	}
	content, err := json.MarshalIndent(map[string]any{
		"version": 1,
		"hooks": map[string]any{
			event: []any{map[string]any{
				"type":       "command",
				"bash":       posixShellHookCommand(command, hookHostWindows),
				"powershell": powershell,
			}},
		},
	}, "", "  ")
	if err != nil {
		return "", "", err
	}

	rel := copilotHookRelPath(hookName)
	fullPath := filepath.Join(baseDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", fmt.Errorf("provisioning: mkdir %s: %w", filepath.Dir(fullPath), err)
	}
	if err := writeFileNoFollow(fullPath, append(content, '\n'), 0o644); err != nil {
		return "", "", fmt.Errorf("provisioning: write copilot hook file %s: %w", fullPath, err)
	}
	return rel, "", nil
}

// removeCopilotHook deletes hookName's registration file; absent is fine (an
// unregistered event or matcher never wrote one).
func removeCopilotHook(baseDir, hookName string) error {
	fullPath := filepath.Join(baseDir, filepath.FromSlash(copilotHookRelPath(hookName)))
	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
