package configurator

import "slices"

// Entry tools (D1724): the reading path the managed instructions
// name, and the only tools Cartographer asks a client to keep in view.
//
// Most clients now hide MCP tools behind a code mode or a tool search, and
// Cartographer works with that instead of switching it off (a `codemode: false`
// or an entry-wide `exposure: direct` opts out of a client feature). What it
// does is make these few tools reachable through each client's own pin:
//
//   - Claude Code and Antigravity read a per-tool `_meta` key from tools/list
//     (AlwaysLoadMeta, set by the server);
//   - Pi reads `toolExposure` in the config entry (piToolExposure, below);
//   - the other clients have no per-tool pin, and the server's initialize
//     instructions name the same tools.
//
// The list is one place: the server flags exactly these tools, a test pins the
// two together.
var entryTools = []string{"atlas_overview", "search", "concept_read", "graph_context"}

// EntryTools returns the names of the entry tools.
func EntryTools() []string { return slices.Clone(entryTools) }

// IsEntryTool reports whether name is an entry tool.
func IsEntryTool(name string) bool { return slices.Contains(entryTools, name) }

// AlwaysLoadMeta is the `_meta` an entry tool carries in tools/list: the keys
// Claude Code (`anthropic/alwaysLoad`, 2.1) and Antigravity
// (`antigravity/alwaysLoad`, 1.3.3) read to keep a tool out of their deferred
// set. A fresh map per call: the SDK keeps what it is given. Other clients
// ignore unknown `_meta` keys.
func AlwaysLoadMeta() map[string]any {
	return map[string]any{"anthropic/alwaysLoad": true, "antigravity/alwaysLoad": true}
}

// The keys are the server's bare tool names, not Pi's `mcp__<server>__<tool>`
// form: Pi 1.5.0 matches `toolExposure` against the tool name the server lists
// (core/mcp-servers.js getMcpToolExposure, called with tool.name), read from the
// installed package, not from its docs.
//
// piToolExposure is the `toolExposure` of a Pi entry: the entry tools direct
// (declared to the model like built-ins), every other tool of the server left
// in Pi's default exposure, codemode (Pi 1.5 MCP docs, "Control tool
// exposure").
func piToolExposure() map[string]any {
	out := make(map[string]any, len(entryTools))
	for _, n := range entryTools {
		out[n] = "direct"
	}
	return out
}
