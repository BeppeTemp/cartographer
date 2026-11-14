package configurator

// Crush's MCP configuration (D225).
//
// Crush's current preferred configuration format is Bash — `crushrc`, evaluated
// at startup — with `crush.json` documented as deprecated but supported for the
// foreseeable future (https://github.com/charmbracelet/crush/tree/main/docs/config
// §Legacy JSON). Cartographer writes the JSON, for two reasons that both matter:
// merging into a client's own config file must be non-destructive (D23), which a
// JSON object allows key by key and a shell script does not; and anything written
// into `crushrc` would *run* in the user's session, which is a materially larger
// blast radius than a config key.
//
// The entries themselves are Crush's own vocabulary: the transport names
// (`stdio`, `http`) are identical to ServerSpec's, so unlike OpenCode there is
// nothing to translate there. Crush also accepts an `sse` transport, which
// ServerSpec has no way to express (validateServerSpec admits only http and
// stdio), so nothing here emits one. Two things do need translating, and both
// come from the same property of the file — Crush expands `$VAR` and `$(command)`
// in config values at load time (README §Environment Variables, §A note on
// security):
//
//   - a "${VAR}" reference becomes "$VAR", the documented form (the braced one
//     is not documented), unless a name character follows it (crushBracesStripped);
//   - a value that still contains "$(" afterwards is refused, not written: it
//     would execute on the next Crush start. Cartographer's own specs never
//     contain one, but a KB-provided "mcp" artifact can
//     (internal/provisioning/contract/mcpspec.go).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// crushSchemaURL is the schema Crush publishes for the JSON format.
const crushSchemaURL = "https://charm.land/crush.json"

// CrushConfigPath is the MCP config file Cartographer writes for Crush,
// relative to the client base dir, in slash form. It is also correct on Windows:
// Crush keeps user configuration under %USERPROFILE%\.config\crush, not under
// %APPDATA% (https://github.com/charmbracelet/crush#configuration).
const CrushConfigPath = ".config/crush/crush.json"

// emitCrushServer generates the ~/.config/crush/crush.json entry for name/spec.
func emitCrushServer(name string, spec ServerSpec, _ EmitOptions) (*EmitResult, error) {
	entry := map[string]any{}
	switch spec.Type {
	case "http":
		entry["type"] = "http"
		entry["url"] = spec.URL
	case "stdio":
		entry["type"] = "stdio"
		// A string command plus a separate args array — not OpenCode's single
		// array of command-and-arguments.
		entry["command"] = spec.Command
		if len(spec.Args) > 0 {
			entry["args"] = spec.Args
		}
		if len(spec.Env) > 0 {
			env, err := crushExpansionSafeMap(name, "env", spec.Env)
			if err != nil {
				return nil, err
			}
			entry["env"] = env
		}
	default:
		return nil, fmt.Errorf("mcp %q: unsupported transport %q", name, spec.Type)
	}
	if spec.Type != "stdio" && len(spec.Headers) > 0 {
		headers, err := crushExpansionSafeMap(name, "header", spec.Headers)
		if err != nil {
			return nil, err
		}
		entry["headers"] = headers
	}

	root := map[string]any{
		"$schema": crushSchemaURL,
		"mcp": map[string]any{
			name: entry,
		},
	}
	content, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return &EmitResult{
		Provider: ProviderCrush,
		FilePath: CrushConfigPath,
		Content:  content,
	}, nil
}

// crushBracesStripped rewrites "${VAR}" to the documented "$VAR" unless the
// next character would continue the name: "${PREFIX}_v2" is kept braced, as
// "$PREFIX_v2" names another variable. Done by hand rather than with a
// replacement template because "$" is the template's own escape character.
//
// A literal "$" in a value (for instance "cost $5") is not touched: how Crush
// escapes one is not documented, so it is left as written.
func crushBracesStripped(v string) string {
	var sb strings.Builder
	last := 0
	for _, loc := range envRefPattern.FindAllStringIndex(v, -1) {
		sb.WriteString(v[last:loc[0]])
		ref := v[loc[0]:loc[1]]
		if loc[1] < len(v) && isShellNameChar(v[loc[1]]) {
			sb.WriteString(ref)
		} else {
			sb.WriteString("$" + ref[2:len(ref)-1])
		}
		last = loc[1]
	}
	sb.WriteString(v[last:])
	return sb.String()
}

func isShellNameChar(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// crushExpansionSafeMap rewrites "${VAR}" to "$VAR" in every value and refuses
// the map if any value still carries a command substitution. kind names the
// field in the error ("header", "env") so the message says which one to fix.
func crushExpansionSafeMap(server, kind string, in map[string]string) (map[string]string, error) {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(in))
	for _, key := range keys {
		value := crushBracesStripped(in[key])
		if strings.Contains(value, "$(") {
			return nil, fmt.Errorf(
				"mcp %q: %s %q contains a command substitution, which crush would execute when it loads crush.json: remove it or pass the value through an environment variable",
				server, kind, key)
		}
		out[key] = value
	}
	return out, nil
}
