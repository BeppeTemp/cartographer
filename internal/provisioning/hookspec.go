package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// loadHookSpec reads <hookDir>/hook.json and parses it, keeping the reason a
// hook cannot be registered so the registrars can report it.
func loadHookSpec(hookDir string) (contract.HookSpec, error) {
	data, err := os.ReadFile(filepath.Join(hookDir, "hook.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return contract.HookSpec{}, fmt.Errorf("hook.json not found")
		}
		return contract.HookSpec{}, fmt.Errorf("read hook.json: %v", err)
	}
	return contract.ParseHookSpec(data)
}

// hookSpecWarning is the sync-time report for a hook whose hook.json cannot be
// registered: the hook's files are materialized and counted, and nothing will
// fire it.
func hookSpecWarning(hookName string, provider configurator.Provider, reason error) string {
	return fmt.Sprintf("hook %q: %s: not registered (%v); its files were installed but it will never fire", hookName, provider, reason)
}

// hookEventWarning is the sync-time report for a well-formed hook.json whose
// event the given claude/codex-style registrar cannot honour honestly: skip is
// true when the event is known and the provider does not fire it (nothing is
// registered), false with a warning when the event is unknown to the
// vocabulary (registered as declared, since the client may know it; a typo
// would otherwise be silent).
func hookEventWarning(hookName string, provider configurator.Provider, event string) (warning string, skip bool) {
	if !contract.HookEventKnown(event) {
		return fmt.Sprintf("hook %q: %s: event %q is not a known hook event (known: %s); registered as declared, but a misspelled event never fires", hookName, provider, event, strings.Join(contract.HookEventNames(), ", ")), false
	}
	if !contract.HookEventReaches(event, provider) {
		return fmt.Sprintf("hook %q: %s: event %q is not fired by this client; files were installed but the hook was not registered", hookName, provider, event), true
	}
	return "", false
}
