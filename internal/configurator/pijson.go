package configurator

// Pi's MCP configuration (probed on Pi 1.5.0, @earendil-works/pi-coding-agent).
//
// Pi reads <agent-dir>/mcp.json, key "mcpServers". Three properties shape the
// entry:
//
//   - Pi's default exposure, "codemode", does not declare a server's tools to
//     the model. Cartographer's own entry keeps that default and pins the entry
//     tools with "toolExposure": {"<tool>": "direct"} (entrytools.go), so the
//     rest of the tools stay reachable from codemode and nothing is switched off
//     (D1724; D1603 had written "exposure": "direct" on the whole
//     entry). A third-party server of a KB "mcp" artifact still gets
//     "exposure": "direct": without it the entry would connect and be useless.
//     A test pins both.
//   - "${VAR}" in env and headers is Pi's own interpolation and the form
//     Cartographer writes itself (Authorization: Bearer ${TOKEN}): it is kept
//     verbatim, encoding it would break authentication.
//   - a value that is wholly "!command" is run by Pi at load. That is what a
//     KB-provided "mcp" artifact must not be able to smuggle in, so it is
//     refused, naming the server and the key.
//
// Pi skips, with a report, a server whose name has a character outside
// letters, digits, "_" and "-"; the emitter refuses it instead of writing an
// entry that silently does nothing.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// PiConfigPath is the MCP config file Cartographer writes for Pi, relative to
// the client base dir (the user's home), in slash form. PI_CODING_AGENT_DIR is
// not honoured (D676, D755).
const PiConfigPath = ".pi/agent/mcp.json"

var piServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// emitPiServer generates the ~/.pi/agent/mcp.json entry for name/spec.
func emitPiServer(name string, spec ServerSpec) (*EmitResult, error) {
	if !piServerNamePattern.MatchString(name) {
		return nil, fmt.Errorf("mcp %q: pi accepts only letters, digits, '_' and '-' in a server name and would skip this entry", name)
	}
	entry := map[string]any{}
	if spec.EntryTools {
		entry["toolExposure"] = piToolExposure()
	} else {
		entry["exposure"] = "direct"
	}
	switch spec.Type {
	case "http":
		entry["url"] = spec.URL
		if len(spec.Headers) > 0 {
			if err := piRefuseCommandValues(name, "header", spec.Headers); err != nil {
				return nil, err
			}
			entry["headers"] = spec.Headers
		}
	case "stdio":
		entry["command"] = spec.Command
		if len(spec.Args) > 0 {
			entry["args"] = spec.Args
		}
		if len(spec.Env) > 0 {
			if err := piRefuseCommandValues(name, "env", spec.Env); err != nil {
				return nil, err
			}
			entry["env"] = spec.Env
		}
	default:
		return nil, fmt.Errorf("mcp %q: unsupported transport %q", name, spec.Type)
	}
	content, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{name: entry}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return &EmitResult{Provider: ProviderPi, FilePath: PiConfigPath, Content: content}, nil
}

// piRefuseCommandValues refuses a map holding a value Pi would run as a
// command when it loads the file. kind names the field in the error.
func piRefuseCommandValues(server, kind string, in map[string]string) error {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.HasPrefix(in[key], "!") {
			return fmt.Errorf("mcp %q: %s %q is a '!command' value, which pi would execute when it loads mcp.json: remove it or pass the value through an environment variable", server, kind, key)
		}
	}
	return nil
}
