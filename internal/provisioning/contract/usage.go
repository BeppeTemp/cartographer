package contract

// UsageProviders reports which providers contribute what, for kb_status and
// the docs: full per-activation, partial (catalogue timestamp plus path
// reads), or none.
func UsageProviders() (supported, partial, unsupported []string) {
	supported = []string{"antigravity", "claude", "copilot", "kiro"}
	partial = []string{"codex"}
	unsupported = []string{"crush", "opencode", "pi", "cline"}
	return
}
