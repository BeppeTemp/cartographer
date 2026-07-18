// hooksettings.go (D57) — registration/removal of the Claude Code entries in
// <baseDir>/.claude/settings.json for the hooks materialized by Apply/PruneManaged.
// Before D57 Apply stopped at materializing the hook's files
// (.claude/hooks/<name>/) and left the user to paste the settings.json entry by
// hand (docs/decisions.md D48); here the hook becomes effective on its own,
// idempotently (no duplicates on re-apply) and prunably (the entry disappears
// when the hook is removed). See docs/decisions.md D57, docs/sync.md
// §Agents and hooks.
package provisioning

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// hookSpec is the hook.json format (KB, hooks/<name>/hook.json): the Claude
// Code event (e.g. "PostToolUse", "SessionStart"), an optional matcher and the
// command to run — relative to the hook's directory unless absolute (see
// resolveHookCommand).
type hookSpec struct {
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
}

// registerHookSettings reads the hook.json just materialized in fullDestDir and
// updates <baseDir>/.claude/settings.json (creating it if absent) with its entry
// in hooks.<Event>[] (D57). Best-effort on hook.json's content: a missing,
// unparseable file, or one lacking the required fields (event/command), does not
// fail Apply — registration is simply skipped, the hook stays materialized on
// disk anyway. Called by Apply only for kind "hook": destDir maps that kind only
// for the claude provider, so no provider parameter is needed here.
func registerHookSettings(baseDir, hookName, fullDestDir string) error {
	spec, ok := readHookSpec(fullDestDir)
	if !ok {
		return nil
	}

	command := resolveHookCommand(spec.Command, fullDestDir)
	// The D57 ownership criterion is the marker's presence in the command. A
	// command that doesn't reference the hook's dir (e.g. a "jq ..." one-liner,
	// resolved via PATH) would never contain it: without the marker the entry
	// would be neither idempotent nor prunable. Append it as an inert trailing
	// shell comment.
	if marker := hookOwnershipMarker(hookName); !strings.Contains(command, marker) {
		command += " # cartographer-hook: " + marker
	}

	settingsPath := claudeSettingsPath(baseDir)
	settings, err := loadJSONObject(settingsPath)
	if err != nil {
		return err
	}
	upsertHookEntry(settings, hookName, spec, command)
	return saveJSONObject(settingsPath, settings)
}

// removeHookEntries strips every settings.json hooks entry owned by hookName
// (§hookOwnershipMarker) from <baseDir>/.claude/settings.json. No-op — including no
// write to disk — if the file doesn't exist or has no entry for this hook: safe to
// call from PruneManaged/Apply's prune path regardless of whether registration ever
// succeeded for this hook (e.g. a malformed hook.json that registerHookSettings had
// silently skipped).
func removeHookEntries(baseDir, hookName string) error {
	settingsPath := claudeSettingsPath(baseDir)
	settings, err := loadJSONObject(settingsPath)
	if err != nil {
		return err
	}
	if !stripHookEntries(settings, hookName) {
		return nil
	}
	return saveJSONObject(settingsPath, settings)
}

// claudeSettingsPath returns the path to Claude Code's settings.json under baseDir.
func claudeSettingsPath(baseDir string) string {
	return filepath.Join(baseDir, ".claude", "settings.json")
}

// readHookSpec reads and parses <hookDir>/hook.json, returning ok=false (never an
// error) if the file is missing, unparseable, or missing a required field — the
// caller treats all of these as "nothing to register", not as a fatal condition.
func readHookSpec(hookDir string) (hookSpec, bool) {
	data, err := os.ReadFile(filepath.Join(hookDir, "hook.json"))
	if err != nil {
		return hookSpec{}, false
	}
	var spec hookSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return hookSpec{}, false
	}
	if spec.Event == "" || spec.Command == "" {
		return hookSpec{}, false
	}
	return spec, true
}

// resolveHookCommand resolves the leading token of command (the executable) against
// hookDirAbs when it is a relative path (e.g. "./notify.sh" or "scripts/run.sh" —
// anything containing a "/"), leaving absolute paths, $VAR-style references (e.g.
// "$HOME/...") and bare command names (e.g. "jq", resolved via PATH like in any
// shell) untouched. Any trailing arguments (separated by the first space) are passed
// through verbatim. hookDirAbs is the absolute materialized hook directory
// (<baseDir>/.claude/hooks/<nome>) — Claude Code runs hook commands as a shell
// command line, not necessarily from that directory, so a relative path in hook.json
// only makes sense resolved against it.
func resolveHookCommand(command, hookDirAbs string) string {
	fields := strings.SplitN(strings.TrimSpace(command), " ", 2)
	bin := fields[0]
	if !filepath.IsAbs(bin) && !strings.HasPrefix(bin, "$") && strings.ContainsRune(bin, '/') {
		bin = filepath.Join(hookDirAbs, bin)
	}
	if len(fields) == 2 {
		return bin + " " + fields[1]
	}
	return bin
}

// hookOwnershipMarker is the substring that marks a settings.json hook entry's
// command as owned by hookName: any command containing this path fragment was
// written by Cartographer for this hook (D57) and can be safely replaced/removed on
// re-apply/prune without touching hooks the user (or a differently-named
// Cartographer-managed hook) added by hand.
func hookOwnershipMarker(hookName string) string {
	return ".claude/hooks/" + hookName + "/"
}

// upsertHookEntry inserts spec's entry (matcher + command, type "command") into
// settings["hooks"][spec.Event], after first stripping any existing entry owned by
// hookName — idempotent: re-applying the same hook N times yields exactly one entry.
func upsertHookEntry(settings map[string]interface{}, hookName string, spec hookSpec, command string) {
	stripHookEntries(settings, hookName)

	hooksMap, _ := settings["hooks"].(map[string]interface{})
	if hooksMap == nil {
		hooksMap = map[string]interface{}{}
	}

	groups, _ := hooksMap[spec.Event].([]interface{})

	entry := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{"type": "command", "command": command},
		},
	}
	if spec.Matcher != "" {
		entry["matcher"] = spec.Matcher
	}

	hooksMap[spec.Event] = append(groups, entry)
	settings["hooks"] = hooksMap
}

// stripHookEntries removes, from settings["hooks"], every hook entry whose command
// contains hookName's ownership marker (§hookOwnershipMarker): dropped from its
// hooks[] list, the enclosing group entry dropped if that leaves it empty, the event
// key dropped if that empties the event's group list, and the "hooks" key itself
// dropped if that empties it. Returns whether anything changed. Anything that isn't
// ours — user-added hooks, hooks owned by a differently-named Cartographer hook,
// entries with an unexpected shape — is left untouched verbatim.
func stripHookEntries(settings map[string]interface{}, hookName string) bool {
	hooksMap, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return false
	}

	marker := hookOwnershipMarker(hookName)
	changed := false

	for event, groupsRaw := range hooksMap {
		groups, ok := groupsRaw.([]interface{})
		if !ok {
			continue
		}
		newGroups := make([]interface{}, 0, len(groups))
		for _, groupRaw := range groups {
			group, ok := groupRaw.(map[string]interface{})
			if !ok {
				newGroups = append(newGroups, groupRaw)
				continue
			}
			entries, ok := group["hooks"].([]interface{})
			if !ok {
				// Group without a hooks list (unexpected shape, not ours): don't touch it.
				newGroups = append(newGroups, groupRaw)
				continue
			}
			newEntries := make([]interface{}, 0, len(entries))
			for _, entryRaw := range entries {
				entry, ok := entryRaw.(map[string]interface{})
				if !ok {
					newEntries = append(newEntries, entryRaw)
					continue
				}
				cmd, _ := entry["command"].(string)
				if strings.Contains(cmd, marker) {
					changed = true
					continue
				}
				newEntries = append(newEntries, entryRaw)
			}
			if len(newEntries) == 0 {
				// The group held only our entries: drop it entirely, don't
				// leave a residual {"matcher": ..., "hooks": []}.
				changed = true
				continue
			}
			if len(newEntries) != len(entries) {
				group["hooks"] = newEntries
			}
			newGroups = append(newGroups, group)
		}
		if len(newGroups) == 0 {
			delete(hooksMap, event)
			changed = true
		} else if len(newGroups) != len(groups) {
			hooksMap[event] = newGroups
		}
	}

	if len(hooksMap) == 0 {
		delete(settings, "hooks")
	}
	return changed
}

// --- Codex (D58) ---
//
// Codex CLI has a stable hooks engine (since v0.124.0) whose event names largely
// mirror Claude Code's, and registers hooks as TOML array-of-tables inline in
// config.toml (see https://developers.openai.com/codex/hooks):
//
//	[[hooks.<Event>]]
//	matcher = "..."
//	[[hooks.<Event>.hooks]]
//	type = "command"
//	command = "..."
//
// Unlike Claude's settings.json (a single JSON object patched in place),
// config.toml is hand-curated and never parsed/re-serialized (D58): each hook
// gets its own marker-delimited block (codexHookMarkers), written via
// internal/blocktext into the same file configurator.emitCodex writes
// [mcp_servers.cartographer] into (under its own "cartographer:mcp:*"
// markers — distinct text, no collision). TOML array-of-tables don't need to
// be contiguous: several "[[hooks.X]]" headers for the same event, scattered
// across independently-managed blocks anywhere in the file, still merge into
// one array in file order — so per-hook blocks are safe even when multiple
// hooks share an event.

// codexHookEventNames maps Claude Code hook event names (as authored in a KB's
// hook.json) to Codex's own event names, for the rare case they diverge.
// Today the common events used in practice (SessionStart, PreToolUse,
// PostToolUse, UserPromptSubmit, Stop, SubagentStop, PreCompact) are named
// identically in both engines, so this map is empty — kept as an explicit seam
// for the day a KB uses an event whose Codex name differs from Claude's.
var codexHookEventNames = map[string]string{}

// codexHookEvent translates a Claude Code hook.json event name to its Codex
// equivalent via codexHookEventNames, passing it through unchanged if absent.
func codexHookEvent(claudeEvent string) string {
	if mapped, ok := codexHookEventNames[claudeEvent]; ok {
		return mapped
	}
	return claudeEvent
}

// codexConfigTOMLPath returns the path to Codex's config.toml under baseDir.
func codexConfigTOMLPath(baseDir string) string {
	return filepath.Join(baseDir, ".codex", "config.toml")
}

// codexHookMarkers returns the begin/end comment markers that delimit
// hookName's own managed block in config.toml — distinct per hook name, so
// registering/removing one hook never disturbs another's block or the
// [mcp_servers.cartographer] block written by configurator.
func codexHookMarkers(hookName string) (begin, end string) {
	return "# cartographer:hook:" + hookName + ":begin", "# cartographer:hook:" + hookName + ":end"
}

// registerHookConfigTOML mirrors registerHookSettings but for Codex (D58):
// reads hook.json from fullDestDir and upserts its registration as a
// marker-delimited TOML block in <baseDir>/.codex/config.toml. Best-effort on
// a missing/malformed hook.json (via readHookSpec) — Apply's materialization
// of the hook's files never fails because of it.
func registerHookConfigTOML(baseDir, hookName, fullDestDir string) error {
	spec, ok := readHookSpec(fullDestDir)
	if !ok {
		return nil
	}
	command := resolveHookCommand(spec.Command, fullDestDir)
	event := codexHookEvent(spec.Event)

	var sb strings.Builder
	fmt.Fprintf(&sb, "[[hooks.%s]]\n", event)
	if spec.Matcher != "" {
		fmt.Fprintf(&sb, "matcher = %s\n", configurator.QuoteTOMLString(spec.Matcher))
	}
	fmt.Fprintf(&sb, "[[hooks.%s.hooks]]\n", event)
	sb.WriteString("type = \"command\"\n")
	fmt.Fprintf(&sb, "command = %s\n", configurator.QuoteTOMLString(command))

	begin, end := codexHookMarkers(hookName)
	return blocktext.Write(codexConfigTOMLPath(baseDir), begin, end, sb.String())
}

// removeHookConfigTOML strips hookName's marker-delimited block (if present)
// from <baseDir>/.codex/config.toml — the inverse of registerHookConfigTOML,
// called from PruneManaged's prune path. No-op if the file or the block is
// absent (e.g. a malformed hook.json that registerHookConfigTOML had silently
// skipped registering in the first place).
func removeHookConfigTOML(baseDir, hookName string) error {
	begin, end := codexHookMarkers(hookName)
	_, err := blocktext.Remove(codexConfigTOMLPath(baseDir), begin, end, false)
	return err
}

// hookProviderFromPath infers which provider materialized a hook's ManagedFile
// from its Path prefix (destDir gives each provider its own hooks directory:
// ".claude/hooks/<name>/..." vs ".codex/hooks/<name>/..." vs
// ".opencode/hooks/<name>/..."), so PruneManaged can strip the matching
// provider-native registration without needing its own provider parameter.
// Returns "" for a path under none of the three (defensive; not expected in
// practice since kind "hook" only ever materializes under these providers,
// see destDir).
func hookProviderFromPath(path string) string {
	slash := filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(slash, ".claude/hooks/"):
		return "claude"
	case strings.HasPrefix(slash, ".codex/hooks/"):
		return "codex"
	case strings.HasPrefix(slash, ".opencode/hooks/"):
		return "opencode"
	default:
		return ""
	}
}

// --- OpenCode (D59) ---
//
// OpenCode has no declarative hook engine: it loads JS/TS plugin modules from
// a plugin directory (project ".opencode/plugins/", global
// "~/.config/opencode/plugins/" — see https://opencode.ai/docs/plugins/),
// each exporting an async function that receives { project, client, $,
// directory, worktree } and returns a hooks object. Files under the plugin
// directory autoload at startup — no entry in opencode.json is needed (unlike
// npm-published plugins, registered via the "plugin" array).
//
// Cartographer bridges the two models: for every hook whose KB event maps to
// an OpenCode hook (openCodeHookEvents below), Apply generates a whole,
// deterministic plugin file — <baseDir>/.config/opencode/plugins/
// cartographer-<name>.js — that runs the hook's materialized script when the
// mapped OpenCode hook/event fires. Ownership is per-FILE, not per-block (D57/
// D58 patch an existing shared file; this plugin file is entirely
// Cartographer's): the header comment is a marker for humans, not a merge
// boundary — the whole file is replaced on update and os.Remove'd on prune.
//
// Event mapping (Claude Code hook.json event → OpenCode):
//
//	PreToolUse    → "tool.execute.before" (dedicated hook, filtered by matcher)
//	PostToolUse   → "tool.execute.after"  (dedicated hook, filtered by matcher)
//	SessionStart  → generic "event" hook, filtered on event.type === "session.created"
//	Stop          → generic "event" hook, filtered on event.type === "session.idle"
//
// SessionStart/Stop have no dedicated hook key in OpenCode's plugin API (only
// tool.execute.before/after, shell.env and experimental.session.compacting do,
// per the docs' own examples) — session lifecycle is only observable through
// the generic pub/sub "event" hook, keyed on event.type. UserPromptSubmit (and
// any other event not in this map) has no OpenCode equivalent: no plugin is
// generated for it — the hook's files are still materialized, and Apply
// records a warning (see AppliedResult.Warnings) instead of failing.
type openCodeHookKind int

const (
	openCodeHookUnsupported openCodeHookKind = iota
	openCodeHookToolBefore
	openCodeHookToolAfter
	openCodeHookEvent
)

// openCodeMapping is the resolved translation of one Claude Code hook event to
// its OpenCode plugin-hook shape. eventType is only meaningful for
// openCodeHookEvent (the generic "event" hook's event.type to filter on).
type openCodeMapping struct {
	kind      openCodeHookKind
	eventType string
}

// openCodeHookEvents maps Claude Code hook.json event names to their OpenCode
// plugin-hook equivalent (see the D59 doc comment above). Events absent from
// this map have no OpenCode equivalent.
var openCodeHookEvents = map[string]openCodeMapping{
	"PreToolUse":   {kind: openCodeHookToolBefore},
	"PostToolUse":  {kind: openCodeHookToolAfter},
	"SessionStart": {kind: openCodeHookEvent, eventType: "session.created"},
	"Stop":         {kind: openCodeHookEvent, eventType: "session.idle"},
}

// openCodePluginRelPath returns hookName's generated plugin path, relative to
// baseDir — <baseDir>/.config/opencode/plugins/cartographer-<name>.js, the
// global OpenCode plugin directory (see the D59 doc comment above; baseDir is
// always the user's home for OpenCode, same as the "instructions" destDir
// case, ".config/opencode/AGENTS.md").
func openCodePluginRelPath(hookName string) string {
	return filepath.Join(".config", "opencode", "plugins", "cartographer-"+hookName+".js")
}

// registerOpenCodePlugin reads hook.json from fullDestDir and, if its event
// maps to an OpenCode hook (openCodeHookEvents), (re)writes the generated
// plugin wrapper at <baseDir>/<openCodePluginRelPath(hookName)>. Mirrors
// registerHookSettings/registerHookConfigTOML's best-effort policy on a
// missing/malformed hook.json (ok=false from readHookSpec: nothing to
// register, no error, no warning — Apply's materialization of the hook's own
// files never fails because of it).
//
// Returns:
//   - relPath: the plugin's path relative to baseDir, or "" if nothing was
//     written (malformed hook.json, or unmapped event).
//   - warning: non-empty only when the event was well-formed but has no
//     OpenCode equivalent — Apply surfaces this via AppliedResult.Warnings
//     instead of failing (the hook's files are still materialized).
func registerOpenCodePlugin(baseDir, hookName, fullDestDir string) (relPath, warning string, err error) {
	spec, ok := readHookSpec(fullDestDir)
	if !ok {
		return "", "", nil
	}

	mapping, ok := openCodeHookEvents[spec.Event]
	if !ok {
		return "", fmt.Sprintf("hook %q: event %q has no OpenCode equivalent, plugin not generated (files materialized anyway)", hookName, spec.Event), nil
	}

	command := resolveHookCommand(spec.Command, fullDestDir)
	content := generateOpenCodePlugin(hookName, spec, mapping, command)

	fullPath := filepath.Join(baseDir, openCodePluginRelPath(hookName))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", fmt.Errorf("provisioning: mkdir %s: %w", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return "", "", fmt.Errorf("provisioning: write opencode plugin %s: %w", fullPath, err)
	}
	return openCodePluginRelPath(hookName), "", nil
}

// removeOpenCodePlugin removes hookName's generated plugin file (if present)
// from <baseDir>/.config/opencode/plugins/ — the inverse of
// registerOpenCodePlugin, called from PruneManaged's prune path. No-op if the
// file is absent (e.g. an unmapped event that registerOpenCodePlugin never
// wrote a file for in the first place).
func removeOpenCodePlugin(baseDir, hookName string) error {
	fullPath := filepath.Join(baseDir, openCodePluginRelPath(hookName))
	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// openCodePluginIdentifier derives a valid, deterministic JS identifier from
// hookName for the plugin's exported const (e.g. "notify-log" →
// "CartographerHookNotifyLog"): any run of characters outside [A-Za-z0-9_] is
// dropped and the following character upper-cased, camel-case style. Distinct
// hook names always yield distinct identifiers as long as they differ after
// stripping non-identifier characters, which holds for the artifact names
// Apply already validates (single path segment, no "..", see Apply's own
// a.Name check) — collisions are harmless anyway since each hook lives in its
// own file/module scope.
func openCodePluginIdentifier(hookName string) string {
	var sb strings.Builder
	sb.WriteString("CartographerHook")
	upperNext := true
	for _, r := range hookName {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			if upperNext {
				sb.WriteString(strings.ToUpper(string(r)))
				upperNext = false
			} else {
				sb.WriteRune(r)
			}
		default:
			upperNext = true
		}
	}
	return sb.String()
}

// generateOpenCodePlugin renders the deterministic JS source of hookName's
// OpenCode plugin wrapper: same (spec, mapping, command) always produces
// byte-identical output, so re-apply on an unchanged hook never perturbs the
// file (idempotence — see docs/decisions.md D59) and PruneManaged/re-apply can
// tell "unchanged" from "updated" the same way every other artifact does
// (content hash of the *hook*, not of this generated file, drives that: this
// file is a side effect of materializing the hook, not itself hashed/diffed).
//
// command is already fully resolved (resolveHookCommand): an absolute path (or
// a "$VAR"-prefixed one) plus any verbatim trailing arguments, run via
// `sh -c <command>` — Bun's $ shell tag quotes the whole interpolated string
// as a single argument to `sh -c`, so the command line is interpreted by the
// shell exactly as Claude Code/Codex would run it natively, not word-split by
// $ itself.
//
// Matcher policy (PreToolUse/PostToolUse only): hook.json's matcher is
// Claude Code's own tool-name syntax (a literal name or "|"-separated
// alternatives, e.g. "Write|Edit") but OpenCode's tool names are a different,
// lowercase vocabulary ("bash", "write", "edit", ...) with no reliable 1:1
// mapping table — so the generated cartographerMatches() does a
// case-insensitive substring match (either direction) per "|" token instead of
// an exact-name comparison. An empty matcher always matches (same as Claude
// Code's own "no matcher" semantics).
func generateOpenCodePlugin(hookName string, spec hookSpec, mapping openCodeMapping, command string) string {
	ident := openCodePluginIdentifier(hookName)
	commandLit := mustJSONString(command)

	var sb strings.Builder
	fmt.Fprintf(&sb, "// cartographer:hook:%s — generated by Cartographer, do not edit.\n", hookName)
	fmt.Fprintf(&sb, "// KB event: %s", spec.Event)
	if spec.Matcher != "" {
		fmt.Fprintf(&sb, " (matcher: %s)", mustJSONString(spec.Matcher))
	}
	sb.WriteString(" → OpenCode: ")
	switch mapping.kind {
	case openCodeHookToolBefore:
		sb.WriteString("tool.execute.before\n")
	case openCodeHookToolAfter:
		sb.WriteString("tool.execute.after\n")
	case openCodeHookEvent:
		fmt.Fprintf(&sb, "event (event.type === %s)\n", mustJSONString(mapping.eventType))
	}
	sb.WriteString("\n")

	switch mapping.kind {
	case openCodeHookToolBefore, openCodeHookToolAfter:
		sb.WriteString("function cartographerMatches(toolName) {\n")
		fmt.Fprintf(&sb, "  const matcher = %s\n", mustJSONString(spec.Matcher))
		sb.WriteString("  if (!matcher) return true\n")
		sb.WriteString("  const tokens = matcher.split(\"|\").map((t) => t.trim().toLowerCase()).filter(Boolean)\n")
		sb.WriteString("  const name = String(toolName || \"\").toLowerCase()\n")
		sb.WriteString("  return tokens.some((t) => name.includes(t) || t.includes(name))\n")
		sb.WriteString("}\n\n")

		hookKey := "tool.execute.before"
		if mapping.kind == openCodeHookToolAfter {
			hookKey = "tool.execute.after"
		}
		fmt.Fprintf(&sb, "export const %s = async ({ $ }) => {\n", ident)
		sb.WriteString("  return {\n")
		fmt.Fprintf(&sb, "    %s: async (input, output) => {\n", mustJSONKey(hookKey))
		sb.WriteString("      if (!cartographerMatches(input.tool)) return\n")
		fmt.Fprintf(&sb, "      await $`sh -c %s`\n", commandLit)
		sb.WriteString("    },\n")
		sb.WriteString("  }\n")
		sb.WriteString("}\n")

	case openCodeHookEvent:
		fmt.Fprintf(&sb, "export const %s = async ({ $ }) => {\n", ident)
		sb.WriteString("  return {\n")
		sb.WriteString("    event: async ({ event }) => {\n")
		fmt.Fprintf(&sb, "      if (event.type !== %s) return\n", mustJSONString(mapping.eventType))
		fmt.Fprintf(&sb, "      await $`sh -c %s`\n", commandLit)
		sb.WriteString("    },\n")
		sb.WriteString("  }\n")
		sb.WriteString("}\n")
	}

	return sb.String()
}

// mustJSONString renders s as a JS double-quoted string literal (JSON string
// syntax is a valid subset of JS string syntax for every character this ever
// sees: hook commands/matchers are ordinary shell/text strings, never raw
// U+2028/U+2029 line separators). Used throughout generateOpenCodePlugin so
// every interpolated value is safely escaped.
func mustJSONString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		// s is always a plain Go string: Marshal cannot fail on it.
		panic(err)
	}
	return string(data)
}

// mustJSONKey renders a hook key (e.g. "tool.execute.before") as a quoted JS
// object key — always safe/needed since the key contains ".".
func mustJSONKey(s string) string {
	return mustJSONString(s)
}

// loadJSONObject reads and parses path as a generic JSON object, returning an empty
// (non-nil) map if the file doesn't exist. Used for settings.json, which Cartographer
// must only ever partially edit (D57) — decoding into map[string]interface{} instead
// of a fixed struct preserves every key it doesn't know about (e.g. "model", other
// hooks) across the read-modify-write round trip; only key ordering is not preserved
// (re-marshaled with json.MarshalIndent, which sorts map keys — acceptable, this is
// not meant to be byte-identical, only content-preserving).
func loadJSONObject(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("provisioning: parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m, nil
}

// saveJSONObject serializes m as indented JSON to path, creating parent directories
// as needed.
func saveJSONObject(path string, m map[string]interface{}) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("provisioning: serialize %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("provisioning: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("provisioning: write %s: %w", path, err)
	}
	return nil
}
