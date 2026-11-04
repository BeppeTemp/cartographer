package configurator

// Cline's MCP configuration (bundle of the VS Code extension 4.1.26).
//
// Cline reads <home>/.cline/data/settings/cline_mcp_settings.json, key
// "mcpServers", and hot-reloads it. An entry carries "type": "stdio",
// "streamableHttp" (or "sse", not emitted). Cline declares no MCP roots capability and starts
// a stdio server with the extension host's working directory unless the entry
// sets "cwd": no "cwd" is written, because it would pin one folder for a
// global entry. "disabled", "autoApprove" and "timeout" are the user's policy
// and are never written either.

import (
	"encoding/json"
	"fmt"
)

// ClineConfigPath is the MCP settings file Cartographer writes for Cline,
// relative to the client base dir (the user's home), in slash form. CLINE_DIR
// and CLINE_MCP_SETTINGS_PATH are not honoured (D676, D755).
const ClineConfigPath = ".cline/data/settings/cline_mcp_settings.json"

// emitClineServer generates the cline_mcp_settings.json entry for name/spec.
func emitClineServer(name string, spec ServerSpec) (*EmitResult, error) {
	entry := map[string]any{}
	switch spec.Type {
	case "http":
		// "sse" is also a Cline type, but no KB descriptor or ServerSpec carries
		// that transport (validateServerSpec refuses it): not emitted.
		entry["type"] = "streamableHttp"
		entry["url"] = spec.URL
		if len(spec.Headers) > 0 {
			entry["headers"] = spec.Headers
		}
	case "stdio":
		entry["type"] = "stdio"
		entry["command"] = spec.Command
		if len(spec.Args) > 0 {
			entry["args"] = spec.Args
		}
		if len(spec.Env) > 0 {
			entry["env"] = spec.Env
		}
	default:
		return nil, fmt.Errorf("mcp %q: unsupported transport %q", name, spec.Type)
	}
	content, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{name: entry}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return &EmitResult{Provider: ProviderCline, FilePath: ClineConfigPath, Content: content}, nil
}
