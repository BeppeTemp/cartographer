package provisioning

// Per-client native agent fields (D291).
//
// An agent's frontmatter may carry a `providers:` block keyed by client:
//
//	providers:
//	  opencode: { permission: { edit: deny, bash: deny } }
//	  codex: { sandbox_mode: read-only }
//	  kiro: { tools: ["read"] }
//
// Each entry is copied VERBATIM into that client's native agent file, after the
// fields Cartographer itself writes. Nothing is inferred, mapped or checked
// against what the client accepts: the author writes the client's own syntax and
// owns its correctness. That is the whole point — D55/D283 refuse to invent a
// mapping of Claude's `tools`, and this is the opt-in that does not need one.
//
// VERIFICATION STATUS (honest): the keys believed to restrict an agent are
//   - opencode: `permission` (edit/bash/webfetch/mcp: allow|ask|deny) in the
//     agent Markdown frontmatter, per opencode.ai/docs/agents. `tools`
//     (name: bool) is no longer shown in the OpenCode agents documentation as
//     of 1.18.x; prefer `permission` (D320);
//   - codex: `sandbox_mode` (read-only, workspace-write, ...) in the agent TOML,
//     per the Codex subagents documentation;
//   - kiro: `tools` and `allowedTools` in the agent JSON config;
//   - antigravity: no restriction key is known to us.
//
// None of this has been exercised against the real clients by this repository
// (D195 verified only Kiro's name/description/prompt). The mechanism therefore
// copies whatever the author writes and promises nothing about what a client
// does with it. Do not add a key here "because it is believed to work": the
// author supplies it, and a wrong key is theirs to find.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"gopkg.in/yaml.v3"
)

// nativeMCPServerRefs returns the MCP server names an entry's `tools` or
// `allowedTools` list cites as `@<server>` or `@<server>/<tool>` (Kiro's
// syntax), sorted and unique, `@builtin` excluded (D320). A heuristic over the
// author's values, not a parse of any client's grammar: D291 copies the entry
// verbatim and promises nothing, so what this feeds is advisory only.
func nativeMCPServerRefs(fields []contract.NativeField) []string {
	seen := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode {
			v := strings.TrimSpace(n.Value)
			if !strings.HasPrefix(v, "@") {
				return
			}
			server := strings.TrimPrefix(v, "@")
			if i := strings.Index(server, "/"); i >= 0 {
				server = server[:i]
			}
			if server != "" && server != "builtin" {
				seen[server] = true
			}
			return
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for _, f := range fields {
		if f.Key == "tools" || f.Key == "allowedTools" {
			walk(f.Value)
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// renderNativeYAML renders the fields as top-level YAML lines, keeping the
// author's key order, nesting and flow/block style.
func renderNativeYAML(fields []contract.NativeField) (string, error) {
	var buf bytes.Buffer
	for _, f := range fields {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		n := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: f.Key}, f.Value,
		}}
		if err := enc.Encode(n); err != nil {
			return "", fmt.Errorf("encode native field %q: %w", f.Key, err)
		}
		if err := enc.Close(); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

// renderNativeTOML renders the fields as top-level TOML key/value lines.
func renderNativeTOML(fields []contract.NativeField) (string, error) {
	var sb strings.Builder
	for _, f := range fields {
		v, err := tomlValue(f.Value)
		if err != nil {
			return "", fmt.Errorf("native field %q: %w", f.Key, err)
		}
		fmt.Fprintf(&sb, "%s = %s\n", tomlKey(f.Key), v)
	}
	return sb.String(), nil
}

func tomlKey(k string) string {
	if k != "" && strings.Trim(k, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") == "" {
		return k
	}
	return configurator.QuoteTOMLString(k)
}

func tomlValue(n *yaml.Node) (string, error) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	switch n.Kind {
	case yaml.MappingNode:
		parts := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := tomlValue(n.Content[i+1])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey(n.Content[i].Value)+" = "+v)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := tomlValue(c)
			if err != nil {
				return "", err
			}
			parts = append(parts, v)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!bool", "!!int", "!!float":
			// The decoded value, not the YAML spelling: True, .5, .inf and 017
			// are valid YAML and invalid TOML.
			return tomlScalar(n)
		case "!!null":
			return "", fmt.Errorf("TOML has no null")
		default:
			return configurator.QuoteTOMLString(n.Value), nil
		}
	}
	return "", fmt.Errorf("unsupported YAML node")
}

// tomlScalar renders a YAML bool, int or float scalar from its decoded value.
func tomlScalar(n *yaml.Node) (string, error) {
	switch n.Tag {
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return "", err
		}
		return strconv.FormatBool(b), nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err == nil {
			return strconv.FormatInt(i, 10), nil
		}
		var u uint64
		if err := n.Decode(&u); err != nil {
			return "", err
		}
		return strconv.FormatUint(u, 10), nil
	}
	var f float64
	if err := n.Decode(&f); err != nil {
		return "", err
	}
	switch {
	case math.IsInf(f, 1):
		return "inf", nil
	case math.IsInf(f, -1):
		return "-inf", nil
	case math.IsNaN(f):
		return "nan", nil
	}
	out := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(out, ".e") {
		out += ".0" // TOML wants a fraction or an exponent to tell a float from an integer
	}
	return out, nil
}

// renderNativeJSONPairs renders the fields as `"key": value` JSON members, in
// the author's order, for splicing into an object.
func renderNativeJSONPairs(fields []contract.NativeField) ([]string, error) {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		k, _ := json.Marshal(f.Key)
		v, err := jsonValue(f.Value)
		if err != nil {
			return nil, fmt.Errorf("native field %q: %w", f.Key, err)
		}
		out = append(out, string(k)+": "+v)
	}
	return out, nil
}

func jsonValue(n *yaml.Node) (string, error) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	switch n.Kind {
	case yaml.MappingNode:
		parts := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, _ := json.Marshal(n.Content[i].Value)
			v, err := jsonValue(n.Content[i+1])
			if err != nil {
				return "", err
			}
			parts = append(parts, string(k)+": "+v)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := jsonValue(c)
			if err != nil {
				return "", err
			}
			parts = append(parts, v)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return "", err
		}
		if _, isTime := v.(fmt.Stringer); isTime {
			v = n.Value
		}
		b, err := json.Marshal(v)
		return string(b), err
	}
	return "", fmt.Errorf("unsupported YAML node")
}
