package provisioning

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// hookEventReach is the one declared vocabulary of hook.json events (D284): every
// canonical event name, with the providers whose hook mechanism can fire it.
// Validation on write (ValidateHookJSON), lint, and the claude/codex registrars
// all read this table; the OpenCode and Antigravity registrars keep their own
// mapping data (openCodeHookEvents, antigravityHookEvents) because it carries
// more than a yes/no, and a test pins that the two agree with this table.
//
// The names are the ones this repository already encodes for a client (the
// Codex comment on codexHookEventNames, openCodeHookEvents,
// antigravityHookEvents). A client's event that is not encoded anywhere in the
// repo is not guessed at here: adding one is one line in this table, plus the
// mapping in the registrar if the client needs one.
var hookEventReach = map[string][]configurator.Provider{
	"SessionStart":     {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode},
	"UserPromptSubmit": {configurator.ProviderClaudeCode, configurator.ProviderCodex},
	"PreToolUse":       {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity},
	"PostToolUse":      {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity},
	"Stop":             {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity},
	"SubagentStop":     {configurator.ProviderClaudeCode, configurator.ProviderCodex},
	"PreCompact":       {configurator.ProviderClaudeCode, configurator.ProviderCodex},
	// Antigravity's own lifecycle events: not Claude Code events, so a hook
	// authored with them is never registered in Claude's settings.json.
	"PreInvocation":  {configurator.ProviderAntigravity},
	"PostInvocation": {configurator.ProviderAntigravity},
}

// hookCapableProviders are the providers with a hook mechanism (hookMechanisms),
// in the order a message names them.
var hookCapableProviders = []configurator.Provider{
	configurator.ProviderClaudeCode,
	configurator.ProviderCodex,
	configurator.ProviderOpenCode,
	configurator.ProviderAntigravity,
}

// HookEventNames returns the declared event vocabulary, sorted.
func HookEventNames() []string {
	names := make([]string, 0, len(hookEventReach))
	for n := range hookEventReach {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// hookEventKnown reports whether event is in the vocabulary.
func hookEventKnown(event string) bool {
	_, ok := hookEventReach[event]
	return ok
}

// hookEventReaches reports whether provider's mechanism can fire event.
func hookEventReaches(event string, provider configurator.Provider) bool {
	for _, p := range hookEventReach[event] {
		if p == provider {
			return true
		}
	}
	return false
}

// HookEventMisses returns the hook-capable providers that will not fire event,
// as their registry ids. Empty for an event outside the vocabulary (the caller
// rejects those separately).
func HookEventMisses(event string) []string {
	if !hookEventKnown(event) {
		return nil
	}
	var out []string
	for _, p := range hookCapableProviders {
		if !hookEventReaches(event, p) {
			out = append(out, string(p))
		}
	}
	return out
}

// parseHookSpec parses and checks hook.json bytes: valid JSON, a non-empty
// event and command. It says nothing about the event vocabulary (a sync stays
// tolerant of an event it does not know) — see ValidateHookJSON for the strict
// write-time check.
func parseHookSpec(data []byte) (hookSpec, error) {
	var spec hookSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return hookSpec{}, fmt.Errorf("invalid JSON: %v", err)
	}
	if strings.TrimSpace(spec.Event) == "" {
		return hookSpec{}, fmt.Errorf(`missing required field "event"`)
	}
	if strings.TrimSpace(spec.Command) == "" {
		return hookSpec{}, fmt.Errorf(`missing required field "command"`)
	}
	return spec, nil
}

// loadHookSpec reads <hookDir>/hook.json and parses it, keeping the reason a
// hook cannot be registered so the registrars can report it.
func loadHookSpec(hookDir string) (hookSpec, error) {
	data, err := os.ReadFile(filepath.Join(hookDir, "hook.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return hookSpec{}, fmt.Errorf("hook.json not found")
		}
		return hookSpec{}, fmt.Errorf("read hook.json: %v", err)
	}
	return parseHookSpec(data)
}

// hookCommandEscapes reports whether a relative command — a leading token that
// contains a separator — climbs out of the hook's directory. Absolute paths,
// $VAR references and bare names are not relative and never escape.
func hookCommandEscapes(command string) bool {
	bin, _, _ := splitHookCommand(strings.TrimSpace(command))
	bin = unquoteHookToken(bin)
	if isAbsCommandPath(bin) || strings.HasPrefix(bin, "$") || !strings.ContainsAny(bin, `/\`) {
		return false
	}
	clean := path.Clean(strings.ReplaceAll(bin, `\`, "/"))
	return clean == ".." || strings.HasPrefix(clean, "../")
}

// ValidateHookJSON is the strict check artifact_write applies to a hook's
// hook.json (D284). It rejects invalid JSON, a missing event or command and a
// relative command that leaves the hook's directory; an event outside the
// vocabulary is a warning (UnknownHookEvent), not an error. On success it returns the registry ids of the hook-capable
// clients that will not fire the event (the hook may target one client, so
// that is information, not an error).
func ValidateHookJSON(data []byte) (misses []string, err error) {
	spec, err := parseHookSpec(data)
	if err != nil {
		return nil, err
	}
	if hookCommandEscapes(spec.Command) {
		return nil, fmt.Errorf("command %q points outside the hook's directory", spec.Command)
	}
	return HookEventMisses(spec.Event), nil
}

// UnknownHookEvent returns a warning when hook.json declares an event outside
// the vocabulary, "" otherwise. It warns rather than rejects: the vocabulary
// holds only the events this repository encodes, and a client may fire more
// (Claude Code's Notification, for one), which claude/codex register as declared.
func UnknownHookEvent(data []byte) string {
	spec, err := parseHookSpec(data)
	if err != nil || hookEventKnown(spec.Event) {
		return ""
	}
	return fmt.Sprintf("event %q is not in the known vocabulary (%s): it is registered as declared on claude and codex, and never fires if misspelled", spec.Event, strings.Join(HookEventNames(), ", "))
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
	if !hookEventKnown(event) {
		return fmt.Sprintf("hook %q: %s: event %q is not a known hook event (known: %s); registered as declared, but a misspelled event never fires", hookName, provider, event, strings.Join(HookEventNames(), ", ")), false
	}
	if !hookEventReaches(event, provider) {
		return fmt.Sprintf("hook %q: %s: event %q is not fired by this client; files were installed but the hook was not registered", hookName, provider, event), true
	}
	return "", false
}
