package provisioning

// kirohooks.go (D300) — the Kiro registration of a materialized hook. Kiro's
// agent engine (KAS) loads standalone hook files, `{"version": "v1", "hooks":
// [...]}`, from every *.json directly inside ~/.kiro/hooks/. Cartographer owns
// exactly one of them, cartographer.json: one entry per provisioned hook,
// regenerated from the set of hooks registered, removed when the last one is
// pruned (the schema requires at least one entry, so an empty file would be
// reported as invalid on every session). No file of the user's is ever opened
// for writing, and no agent config is touched: a hook declared inside an agent
// fires only for that agent, and shadowing the built-in default agent is the
// intrusion D140 refused.
//
// The hook files themselves sit in ~/.kiro/hooks/cartographer/<name>/ (the
// destinationMatrix cell): the loader does not recurse, so they are never read
// as hook files of their own.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// kiroHooksRelPath is the Cartographer-owned standalone hook file, relative to
// the client base dir.
var kiroHooksRelPath = []string{".kiro", "hooks", "cartographer.json"}

// kiroHookTimeoutSeconds is written explicitly rather than left to the
// engine's default (60s in KAS 2.27): the bootstrap's `cartographer sync` is
// bounded by its own HTTP client timeout, and a hook killed earlier than that
// would leave the sync half-applied from Kiro's side.
const kiroHookTimeoutSeconds = 60

func kiroHooksPath(baseDir string) string {
	return filepath.Join(append([]string{baseDir}, kiroHooksRelPath...)...)
}

// registerKiroHook upserts hookName's entry in <baseDir>/.kiro/hooks/
// cartographer.json, keyed by the entry's name. Best-effort on hook.json like
// every registrar: a malformed one, or an event Kiro does not fire, is a
// warning and nothing is registered.
//
// The command keeps the host's spelling (resolveHookCommand only, no
// posixShellHookCommand): KAS runs a command hook through $SHELL -c on unix and
// through PowerShell, or cmd.exe, on Windows — not through a POSIX shell there.
func registerKiroHook(baseDir, hookName, fullDestDir string) (string, error) {
	spec, err := loadHookSpec(fullDestDir)
	if err != nil {
		return hookSpecWarning(hookName, configurator.ProviderKiro, err), nil
	}
	warning, skip := hookEventWarning(hookName, configurator.ProviderKiro, spec.Event)
	if skip {
		return warning, nil
	}
	entry := map[string]interface{}{
		"name":        hookName,
		"description": "Cartographer hook " + hookName + " — generated, do not edit",
		"trigger":     spec.Event,
		"action": map[string]interface{}{
			"type":    "command",
			"command": resolveHookCommand(spec.Command, fullDestDir),
		},
		"timeout": kiroHookTimeoutSeconds,
		"enabled": true,
	}
	if spec.Matcher != "" {
		entry["matcher"] = spec.Matcher
	}

	path := kiroHooksPath(baseDir)
	doc, err := loadJSONObject(path)
	if err != nil {
		return "", err
	}
	hooks, _ := stripKiroHookEntries(doc, hookName)
	hooks = append(hooks, entry)
	sortKiroHookEntries(hooks)
	doc["version"] = "v1"
	doc["hooks"] = hooks
	if err := mkdirAllNoFollow(baseDir, filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("provisioning: mkdir %s: %w", filepath.Dir(path), err)
	}
	return warning, saveJSONObject(path, doc)
}

// removeKiroHook strips hookName's entry from cartographer.json — the inverse of
// registerKiroHook, called from PruneManaged. The file goes with its last entry.
// No-op, with no write, when the file or the entry is absent.
func removeKiroHook(baseDir, hookName string) error {
	path := kiroHooksPath(baseDir)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	doc, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	hooks, changed := stripKiroHookEntries(doc, hookName)
	if !changed {
		return nil
	}
	if len(hooks) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("provisioning: remove %s: %w", path, err)
		}
		pruneEmptyDirs(baseDir, filepath.Join(kiroHooksRelPath...))
		return nil
	}
	doc["hooks"] = hooks
	return saveJSONObject(path, doc)
}

// stripKiroHookEntries returns doc's hooks list without the entries named
// hookName, and whether any was dropped. An entry of any other shape is kept
// verbatim: the file is Cartographer's, but nothing it did not write is
// deleted on a guess.
func stripKiroHookEntries(doc map[string]interface{}, hookName string) ([]interface{}, bool) {
	existing, _ := doc["hooks"].([]interface{})
	kept := make([]interface{}, 0, len(existing))
	changed := false
	for _, raw := range existing {
		if e, ok := raw.(map[string]interface{}); ok && e["name"] == hookName {
			changed = true
			continue
		}
		kept = append(kept, raw)
	}
	return kept, changed
}

// sortKiroHookEntries orders the entries by name so a re-sync in any order
// rewrites the same bytes.
func sortKiroHookEntries(hooks []interface{}) {
	name := func(raw interface{}) string {
		if e, ok := raw.(map[string]interface{}); ok {
			n, _ := e["name"].(string)
			return n
		}
		return ""
	}
	sort.SliceStable(hooks, func(i, j int) bool { return name(hooks[i]) < name(hooks[j]) })
}

// countKiroHookEntries counts the cartographer.json entries named hookName.
func countKiroHookEntries(baseDir, hookName string) (int, error) {
	doc, err := loadJSONObject(kiroHooksPath(baseDir))
	if err != nil {
		return 0, err
	}
	hooks, _ := doc["hooks"].([]interface{})
	n := 0
	for _, raw := range hooks {
		if e, ok := raw.(map[string]interface{}); ok && e["name"] == hookName {
			n++
		}
	}
	return n, nil
}
