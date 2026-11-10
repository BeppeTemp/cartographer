package provisioning

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// Pi hooks. Pi has no declarative hook file: it loads
// $HOME/.pi/agent/extensions/*.ts without trust and runs them through jiti.
// Cartographer therefore generates one deterministic extension per KB hook,
// owned whole and returned as a managed file so a prune deletes it (the way
// OpenCode's plugin and Copilot's hook file are).
//
// Trap: Pi also loads extensions/*/index.ts, and a KB hook directory may carry
// an index.ts. The hook's own files therefore live OUTSIDE extensions/, under
// piHookFilesPrefix (the destinationMatrix hook cell); only the generated
// registration sits in extensions/.
const (
	piExtensionsPrefix = ".pi/agent/extensions/"
	piHookFilesPrefix  = ".pi/agent/cartographer-hooks/"
)

// piHookEvents maps the hook.json vocabulary to Pi's extension events. Only
// the three events probed to fire (Pi 1.5.0) are claimed (HookEventReach).
var piHookEvents = map[string]string{
	"SessionStart": "session_start",
	"PreToolUse":   "tool_call",
	"PostToolUse":  "tool_result",
}

// piExtensionRelPath is the generated extension, relative to the base dir.
// Slash form: it is recorded in the lockfile beside every other destination.
func piExtensionRelPath(hookName string) string {
	return piExtensionsPrefix + "cartographer-" + hookName + ".ts"
}

// registerPiHook writes hookName's extension and returns its relative path
// (empty when nothing was written) and a non-fatal warning.
func registerPiHook(baseDir, hookName, fullDestDir string) (relPath, warning string, registered bool, err error) {
	spec, specErr := loadHookSpec(fullDestDir)
	if specErr != nil {
		return "", hookSpecWarning(hookName, configurator.ProviderPi, specErr), false, nil
	}
	if w, skip := hookEventWarning(hookName, configurator.ProviderPi, spec.Event); skip {
		return "", w, false, nil
	}
	piEvent, ok := piHookEvents[spec.Event]
	if !ok {
		return "", fmt.Sprintf("hook %q: pi: event %q has no Pi equivalent; files were installed but the hook was not registered", hookName, spec.Event), false, nil
	}
	if m := strings.TrimSpace(spec.Matcher); m != "" && spec.Event != "SessionStart" {
		if _, rerr := regexp.Compile(m); rerr != nil {
			return "", fmt.Sprintf("hook %q: pi: matcher %q is not a valid regular expression (%v); files were installed but the hook was not registered", hookName, spec.Matcher, rerr), false, nil
		}
		if what := piMatcherRE2Only(m); what != "" {
			return "", fmt.Sprintf("hook %q: pi: matcher %q uses %s, which only Go's regular expressions understand and Pi would run as JavaScript (it would never match, silently disabling the hook); files were installed but the hook was not registered", hookName, spec.Matcher, what), false, nil
		}
	}

	command := posixShellHookCommand(resolveHookCommand(spec.Command, fullDestDir), hookHostWindows)
	content := generatePiExtension(hookName, spec, piEvent, command)

	rel := piExtensionRelPath(hookName)
	fullPath := filepath.Join(baseDir, filepath.FromSlash(rel))
	if err := mkdirAllNoFollow(baseDir, filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", false, fmt.Errorf("provisioning: mkdir %s: %w", filepath.Dir(fullPath), err)
	}
	if err := writeFileNoFollow(fullPath, []byte(content), 0o644); err != nil {
		return "", "", false, fmt.Errorf("provisioning: write pi extension %s: %w", fullPath, err)
	}
	return rel, "", true, nil
}

// piMatcherRE2Only names the first construct of a matcher that Go's RE2 accepts
// and a JavaScript RegExp does not read the same way, or "". The matcher is
// validated with regexp.Compile but run by Pi as a JS RegExp: a PreToolUse
// block must never silently stop blocking because of a dialect difference.
// The scan skips escaped characters, so `\\(?i)` is not flagged.
func piMatcherRE2Only(m string) string {
	inClass := false
	for i := 0; i < len(m); i++ {
		switch c := m[i]; {
		case c == '\\' && i+1 < len(m):
			switch m[i+1] {
			case 'A', 'z', 'Z', 'Q', 'E':
				return `\` + string(m[i+1])
			}
			i++
		case c == '[' && !inClass:
			inClass = true
			if strings.HasPrefix(m[i:], "[[:") || strings.HasPrefix(m[i:], "[^[:") {
				return "a POSIX class"
			}
		case c == ']':
			inClass = false
		case !inClass && strings.HasPrefix(m[i:], "(?"):
			rest := m[i+2:]
			if strings.HasPrefix(rest, "P<") {
				return "(?P<name>"
			}
			if len(rest) > 0 && (rest[0] == '-' || rest[0] >= 'a' && rest[0] <= 'z' || rest[0] >= 'A' && rest[0] <= 'Z') {
				return "an inline flag group"
			}
		}
	}
	return ""
}

// removePiHook deletes hookName's extension; absent is fine (an unregistered
// event or matcher never wrote one).
func removePiHook(baseDir, hookName string) error {
	fullPath := filepath.Join(baseDir, filepath.FromSlash(piExtensionRelPath(hookName)))
	if err := safewrite.Remove(baseDir, fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// generatePiExtension renders the extension: plain JavaScript that is also
// valid TypeScript (no annotations, so a test can run it with Node).
// Deterministic for equal input. Every interpolated value goes through
// mustJSONString.
//
// Contract (probed on Pi 1.5.0, see docs/harnesses.md ## pi): the command runs
// as `sh -c`, payload on stdin, 60 s timeout, stdout ignored.
//   - PreToolUse, exit 2: {block:true, reason: stderr}.
//   - PostToolUse, exit 2 with stderr: the text is appended to the tool result
//     content (structuredContent returned too: replacing content alone drops
//     it). Trap (the D362 trap): the handler NEVER throws and NEVER sets
//     isError, because a failed tool result makes the agent retry the write.
//   - anything else (other status, spawn error, timeout, exception): no effect.
func generatePiExtension(hookName string, spec contract.HookSpec, piEvent, command string) string {
	matcher := strings.TrimSpace(spec.Matcher)
	var sb strings.Builder
	fmt.Fprintf(&sb, "// cartographer:hook:%s — generated by Cartographer, do not edit.\n", hookName)
	fmt.Fprintf(&sb, "// KB event: %s", spec.Event)
	if matcher != "" && spec.Event != "SessionStart" {
		fmt.Fprintf(&sb, " (matcher: %s)", mustJSONString(matcher))
	}
	fmt.Fprintf(&sb, " → Pi: %s\n\n", piEvent)
	sb.WriteString("import { spawnSync } from \"node:child_process\"\n\n")
	fmt.Fprintf(&sb, "const HOOK_NAME = %s\n", mustJSONString(hookName))
	fmt.Fprintf(&sb, "const HOOK_EVENT = %s\n", mustJSONString(spec.Event))
	fmt.Fprintf(&sb, "const COMMAND = %s\n", mustJSONString(command))
	if spec.Event != "SessionStart" {
		fmt.Fprintf(&sb, "const MATCHER = %s\n", mustJSONString(matcher))
	}
	sb.WriteString("\n")
	if spec.Event != "SessionStart" {
		sb.WriteString("// Unanchored and case-insensitive: Pi's built-ins are lowercase, MCP tools are\n")
		sb.WriteString("// mcp__<server>__<tool>, the KB's matchers are Claude's. A pattern the JS engine\n")
		sb.WriteString("// rejects makes the hook inert.\n")
		sb.WriteString("function matches(toolName) {\n")
		sb.WriteString("  if (!MATCHER) return true\n")
		sb.WriteString("  try { return new RegExp(MATCHER, \"i\").test(String(toolName ?? \"\")) } catch { return false }\n")
		sb.WriteString("}\n\n")
	}
	sb.WriteString("// Returns { status, stderr }, or null on a spawn error or a timeout.\n")
	sb.WriteString("function run(payload, cwd) {\n")
	sb.WriteString("  try {\n")
	sb.WriteString("    const r = spawnSync(\"sh\", [\"-c\", COMMAND], { input: JSON.stringify(payload), encoding: \"utf8\", cwd: cwd || undefined, timeout: 60000 })\n")
	sb.WriteString("    if (r.error || r.status === null || r.status === undefined) return null\n")
	sb.WriteString("    return { status: r.status, stderr: String(r.stderr || \"\").trim() }\n")
	sb.WriteString("  } catch { return null }\n")
	sb.WriteString("}\n\n")

	sb.WriteString("export default function (pi) {\n")
	fmt.Fprintf(&sb, "  pi.on(%s, async (event, ctx) => {\n", mustJSONString(piEvent))
	sb.WriteString("    try {\n")
	switch spec.Event {
	case "SessionStart":
		sb.WriteString("      const sources = { startup: \"startup\", reload: \"startup\", new: \"clear\", resume: \"resume\", fork: \"resume\" }\n")
		sb.WriteString("      run({ hook_event_name: HOOK_EVENT, cwd: ctx?.cwd, source: sources[event?.reason] || \"startup\" }, ctx?.cwd)\n")
	case "PreToolUse":
		sb.WriteString("      if (!matches(event.toolName)) return\n")
		sb.WriteString("      const r = run({ hook_event_name: HOOK_EVENT, cwd: ctx?.cwd, tool_name: event.toolName, tool_input: event.input, tool_use_id: event.toolCallId }, ctx?.cwd)\n")
		sb.WriteString("      if (r && r.status === 2) return { block: true, reason: r.stderr || \"blocked by hook \" + HOOK_NAME }\n")
	case "PostToolUse":
		sb.WriteString("      // Claude fires PostToolUse on success only.\n")
		sb.WriteString("      if (event.isError || !matches(event.toolName)) return\n")
		sb.WriteString("      const r = run({ hook_event_name: HOOK_EVENT, cwd: ctx?.cwd, tool_name: event.toolName, tool_input: event.input, tool_use_id: event.toolCallId, tool_response: event.content }, ctx?.cwd)\n")
		sb.WriteString("      // Never throw and never set isError here: a failed result makes the agent retry the write.\n")
		sb.WriteString("      if (r && r.status === 2 && r.stderr) {\n")
		sb.WriteString("        const content = Array.isArray(event.content) ? event.content : []\n")
		sb.WriteString("        return { content: [...content, { type: \"text\", text: r.stderr }], structuredContent: event.structuredContent }\n")
		sb.WriteString("      }\n")
	}
	sb.WriteString("    } catch {}\n")
	sb.WriteString("  })\n")
	sb.WriteString("}\n")
	return sb.String()
}
