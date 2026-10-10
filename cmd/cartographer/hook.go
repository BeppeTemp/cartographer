package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// `cartographer hook write-findings` (D353) is the logic of the client-generated
// cartographer-write-findings hook (internal/provisioning/writefindings.go): it
// reads a PostToolUse payload on stdin (Claude Code or Codex: same fields) and,
// when the write response carries findings, returns them to the agent as
// feedback. Internal: not listed in printUsage, like `update apply`.
//
// The feedback channel is chosen by --channel, never sniffed from the payload
// (D361): "stderr" (default, Claude Code) prints the message on stderr and
// exits 2; "context" (Codex) prints {"hookSpecificOutput":{"hookEventName":
// "PostToolUse","additionalContext":msg}} on stdout and exits 0, because Codex
// replaces the tool result with the stderr text of an exit-2 hook and the agent
// then repeats the write; "copilot" (GitHub Copilot CLI, D676) prints
// {"additionalContext":msg} at the top level of stdout and exits 0, which
// Copilot appends to the tool result as "Additional guidance from postToolUse
// hooks". Its payload is camelCase ({toolName, toolResult:{textResultForLlm}}),
// and an MCP tool is named <server>-<tool>.
//
// Every other outcome is silent exit 0 — no findings, unknown payload shape,
// parse error, bad flag, even a panic: a hook must never break a session. With
// the stderr channel stdout stays empty; with "context" exit 2 is never returned.

var (
	hookStdin  io.Reader = os.Stdin
	hookStderr io.Writer = os.Stderr
)

// maxHookPayload bounds what the hook reads: a batch response can be large, a
// payload past this is skipped, not buffered without limit.
const maxHookPayload = 8 << 20

// maxFeedbackFindings caps the lines in the message: the agent has the full
// response anyway.
const maxFeedbackFindings = 10

func cmdHook(args []string) (code int) {
	const usage = "usage: cartographer hook write-findings [--channel stderr|context|copilot]   (internal: reads a PostToolUse payload on stdin)"
	if len(args) == 0 || args[0] != "write-findings" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	defer func() {
		// A Go panic exits 2, which a PostToolUse hook reads as feedback.
		if recover() != nil {
			code = 0
		}
	}()
	channel := "stderr"
	switch rest := args[1:]; {
	case len(rest) == 0:
	case len(rest) == 2 && rest[0] == "--channel" && (rest[1] == "stderr" || rest[1] == "context" || rest[1] == "copilot"):
		channel = rest[1]
	default:
		// A hook must never break a session: a bad flag is exit 0, not 2.
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	raw, err := io.ReadAll(io.LimitReader(hookStdin, maxHookPayload))
	if err != nil {
		return 0
	}
	msg := writeFindingsFeedback(raw)
	if msg == "" {
		return 0
	}
	if channel == "copilot" {
		out, err := json.Marshal(map[string]string{"additionalContext": msg})
		if err != nil {
			return 0
		}
		fmt.Fprintln(os.Stdout, string(out))
		return 0
	}
	if channel == "context" {
		out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":     "PostToolUse",
			"additionalContext": msg,
		}})
		if err != nil {
			return 0
		}
		fmt.Fprintln(os.Stdout, string(out))
		return 0
	}
	fmt.Fprintln(hookStderr, msg)
	return 2
}

// hookFinding is the part of a write response's finding entry the message uses.
type hookFinding struct {
	Path    string `json:"path"`
	Check   string `json:"check"`
	Message string `json:"message"`
}

// writeFindingsFeedback returns the feedback message for a PostToolUse payload,
// or "" when there is nothing to say.
func writeFindingsFeedback(payload []byte) string {
	var p struct {
		ToolName     string          `json:"tool_name"`
		ToolResponse json.RawMessage `json:"tool_response"`
		// Copilot CLI's payload (D676).
		CopilotTool   string `json:"toolName"`
		CopilotResult struct {
			Text json.RawMessage `json:"textResultForLlm"`
		} `json:"toolResult"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return ""
	}
	if p.ToolName == "" && len(p.ToolResponse) == 0 {
		p.ToolName, p.ToolResponse = p.CopilotTool, p.CopilotResult.Text
	}
	if len(p.ToolResponse) == 0 {
		return ""
	}
	if p.ToolName != "" && !isWriteFindingsTool(p.ToolName) {
		return ""
	}
	findings := collectFindings(p.ToolResponse, 0)
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This write returned %d finding(s). A write is done when its response has none.\n", len(findings))
	for i, f := range findings {
		if i == maxFeedbackFindings {
			fmt.Fprintf(&b, "+%d more (see the write response)\n", len(findings)-maxFeedbackFindings)
			break
		}
		fmt.Fprintf(&b, "%s %s: %s\n", f.Check, f.Path, f.Message)
	}
	b.WriteString("Fix them in your next write, or record why with a `lint_ignore` entry (check + reason) on the concept.")
	return b.String()
}

func isWriteFindingsTool(name string) bool {
	for _, t := range provisioning.WriteFindingsTools {
		if name == t || strings.HasSuffix(name, "__"+t) || strings.HasSuffix(name, "_"+t) || strings.HasSuffix(name, "-"+t) {
			return true
		}
	}
	return false
}

// collectFindings walks a tool_response in any of the shapes Claude Code
// delivers an MCP result in: the result object, a content-block array, or a
// JSON (or "text\nfindings:\n[...]") string in a text block. It looks for a
// top-level `findings` array or results[].findings (concept_batch).
func collectFindings(raw json.RawMessage, depth int) []hookFinding {
	if depth > 4 {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return findingsFromText(s, depth)
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		var out []hookFinding
		for _, it := range items {
			out = append(out, collectFindings(it, depth+1)...)
		}
		return out
	case '{':
		var o map[string]json.RawMessage
		if json.Unmarshal(raw, &o) != nil {
			return nil
		}
		var out []hookFinding
		if f, ok := o["findings"]; ok {
			var fs []hookFinding
			if json.Unmarshal(f, &fs) == nil {
				out = append(out, fs...)
			}
		}
		if r, ok := o["results"]; ok {
			var rs []struct {
				Findings []hookFinding `json:"findings"`
			}
			if json.Unmarshal(r, &rs) == nil {
				for _, e := range rs {
					out = append(out, e.Findings...)
				}
			}
		}
		if len(out) > 0 {
			return out
		}
		for _, key := range []string{"content", "structuredContent", "text"} {
			if v, ok := o[key]; ok {
				out = append(out, collectFindings(v, depth+1)...)
			}
		}
		return out
	}
	return nil
}

// findingsFromText handles a text block: a JSON document, or the plain-text
// form supersede uses ("... \nfindings:\n<json array>").
func findingsFromText(s string, depth int) []hookFinding {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	if t[0] == '{' || t[0] == '[' {
		// A client that joins the result's content blocks into one string
		// (OpenCode 2.x, D362) delivers the write response followed by the
		// server's sync-state block: several JSON values, one per line.
		var out []hookFinding
		dec := json.NewDecoder(strings.NewReader(t))
		for {
			var v json.RawMessage
			if dec.Decode(&v) != nil {
				return out
			}
			out = append(out, collectFindings(v, depth+1)...)
		}
	}
	const marker = "\nfindings:\n"
	i := strings.Index(s, marker)
	if i < 0 {
		return nil
	}
	var fs []hookFinding
	if json.NewDecoder(strings.NewReader(s[i+len(marker):])).Decode(&fs) != nil {
		return nil
	}
	return fs
}
