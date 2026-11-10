package contract

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// HookEventReach is the one declared vocabulary of hook.json events (D284): every
// canonical event name, with the providers whose hook mechanism can fire it.
// Validation on write (ValidateHookJSON), lint, and the claude/codex/kiro/crush/copilot
// registrars all read this table; the OpenCode and Antigravity registrars keep their own
// mapping data (openCodeHookEvents, antigravityHookEvents) because it carries
// more than a yes/no, and a test pins that the two agree with this table.
//
// The names are the ones this repository already encodes for a client (the
// Codex comment on codexHookEventNames, openCodeHookEvents,
// antigravityHookEvents). A client's event that is not encoded anywhere in the
// repo is not guessed at here: adding one is one line in this table, plus the
// mapping in the registrar if the client needs one.
var HookEventReach = map[string][]configurator.Provider{
	// Kiro's KAS engine has a trigger of the same name for each of its three
	// cells, verified to fire under `kiro-cli chat --v3 --tui` (D300). Its
	// PreToolUse/PostToolUse were never exercised and its SessionEnd is not in
	// this vocabulary, so neither is claimed.
	"SessionStart":     {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderKiro, configurator.ProviderCopilot, configurator.ProviderPi, configurator.ProviderCline},
	"UserPromptSubmit": {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderKiro, configurator.ProviderCline},
	"PreToolUse":       {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderCrush, configurator.ProviderCopilot, configurator.ProviderPi, configurator.ProviderCline},
	"PostToolUse":      {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderCopilot, configurator.ProviderPi, configurator.ProviderCline},
	"Stop":             {configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderOpenCode, configurator.ProviderAntigravity, configurator.ProviderKiro},
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
	configurator.ProviderKiro,
	configurator.ProviderCrush,
	configurator.ProviderCopilot,
	configurator.ProviderPi,
	configurator.ProviderCline,
}

// HookEventNames returns the declared event vocabulary, sorted.
func HookEventNames() []string {
	names := make([]string, 0, len(HookEventReach))
	for n := range HookEventReach {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// HookEventKnown reports whether event is in the vocabulary.
func HookEventKnown(event string) bool {
	_, ok := HookEventReach[event]
	return ok
}

// HookEventReaches reports whether provider's mechanism can fire event.
func HookEventReaches(event string, provider configurator.Provider) bool {
	provider = NativeProvider(provider) // a plugin provider fires what its base's dispatcher fires
	for _, p := range HookEventReach[event] {
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
	if !HookEventKnown(event) {
		return nil
	}
	var out []string
	for _, p := range hookCapableProviders {
		if !HookEventReaches(event, p) {
			out = append(out, string(p))
		}
	}
	return out
}

// ParseHookSpec parses and checks hook.json bytes: valid JSON, a non-empty
// event and command. It says nothing about the event vocabulary (a sync stays
// tolerant of an event it does not know) — see ValidateHookJSON for the strict
// write-time check.
func ParseHookSpec(data []byte) (HookSpec, error) {
	var spec HookSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return HookSpec{}, fmt.Errorf("invalid JSON: %v", err)
	}
	if strings.TrimSpace(spec.Event) == "" {
		return HookSpec{}, fmt.Errorf(`missing required field "event"`)
	}
	if strings.TrimSpace(spec.Command) == "" {
		return HookSpec{}, fmt.Errorf(`missing required field "command"`)
	}
	return spec, nil
}

// hookCommandEscapes reports whether a relative command — a leading token that
// contains a separator — climbs out of the hook's directory. Absolute paths,
// $VAR references and bare names are not relative and never escape.
func hookCommandEscapes(command string) bool {
	bin, _, _ := SplitHookCommand(strings.TrimSpace(command))
	bin = UnquoteHookToken(bin)
	if IsAbsCommandPath(bin) || strings.HasPrefix(bin, "$") || !strings.ContainsAny(bin, `/\`) {
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
	spec, err := ParseHookSpec(data)
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
// (Claude Code's Notification, for one), which those registrars write as declared.
func UnknownHookEvent(data []byte) string {
	spec, err := ParseHookSpec(data)
	if err != nil || HookEventKnown(spec.Event) {
		return ""
	}
	return fmt.Sprintf("event %q is not in the known vocabulary (%s): it is registered as declared on claude, codex, copilot, crush, kiro and pi, and never fires if misspelled", spec.Event, strings.Join(HookEventNames(), ", "))
}
