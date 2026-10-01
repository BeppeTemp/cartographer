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
//   - opencode: `permission` (edit/bash/webfetch: allow|ask|deny) and `tools`
//     (name: bool) in the agent Markdown frontmatter, per opencode.ai/docs/agents;
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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// nativeProviders are the clients that take a `providers:` entry: the ones whose
// agent file Cartographer translates. Claude receives the source verbatim, so its
// own fields go at the top level of the frontmatter, not here.
var nativeProviders = []configurator.Provider{
	configurator.ProviderOpenCode,
	configurator.ProviderCodex,
	configurator.ProviderKiro,
	configurator.ProviderAntigravity,
}

// reservedNativeKeys are the fields Cartographer writes itself for each client.
// An entry may not redefine them: it would silently change the agent's identity
// or prompt, which is not what a restriction block is for.
var reservedNativeKeys = map[configurator.Provider][]string{
	configurator.ProviderOpenCode:    {"description", "mode"},
	configurator.ProviderCodex:       {"name", "description", "developer_instructions"},
	configurator.ProviderKiro:        {"name", "description", "prompt"},
	configurator.ProviderAntigravity: {"name", "description", "mainAgent", "subagent"},
}

// nativeField is one key of a `providers.<client>` entry, with its YAML node kept
// so a rendering can preserve key order and nested structure.
type nativeField struct {
	Key   string
	Value *yaml.Node
}

// agentProviderFields parses the `providers:` block of an agent frontmatter into,
// per client, the ordered list of fields to copy. A missing block yields nil.
// An unknown client, a non-map entry or a reserved key is an error, so a typo
// fails loudly (at artifact_write and at sync) instead of dropping a restriction.
func agentProviderFields(fm *okf.Frontmatter) (map[configurator.Provider][]nativeField, error) {
	v, ok := fm.Get("providers")
	if !ok || v == nil {
		return nil, nil
	}
	var text string
	switch t := v.(type) {
	case okf.Block:
		text = string(t)
	case string:
		text = t // a one-line flow map: providers: { codex: { ... } }
	default:
		return nil, fmt.Errorf("frontmatter 'providers' must be a map keyed by client")
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("frontmatter 'providers': %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("frontmatter 'providers' must be a map keyed by client")
	}
	known := map[string]configurator.Provider{}
	for _, p := range nativeProviders {
		known[string(p)] = p
	}
	out := map[configurator.Provider][]nativeField{}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		name, entry := top.Content[i].Value, top.Content[i+1]
		p, ok := known[name]
		if !ok {
			if name == string(configurator.ProviderClaudeCode) {
				return nil, fmt.Errorf("frontmatter 'providers.%s': Claude Code receives the agent as written, so put its fields at the top level of the frontmatter", name)
			}
			return nil, fmt.Errorf("frontmatter 'providers.%s': unknown client (supported: %s)", name, nativeProviderNames())
		}
		if entry.Kind == yaml.AliasNode {
			entry = entry.Alias
		}
		if entry.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("frontmatter 'providers.%s' must be a map of that client's native fields", name)
		}
		reserved := reservedNativeKeys[p]
		for j := 0; j+1 < len(entry.Content); j += 2 {
			k := entry.Content[j].Value
			for _, r := range reserved {
				if k == r {
					return nil, fmt.Errorf("frontmatter 'providers.%s.%s': Cartographer writes %q itself on %s", name, k, k, name)
				}
			}
			val := entry.Content[j+1]
			if val.Kind == yaml.AliasNode {
				val = val.Alias
			}
			out[p] = append(out[p], nativeField{Key: k, Value: val})
		}
	}
	return out, nil
}

func nativeProviderNames() string {
	names := make([]string, len(nativeProviders))
	for i, p := range nativeProviders {
		names[i] = string(p)
	}
	return strings.Join(names, ", ")
}

// ValidateAgentProviders is the check artifact_write applies to an agent's
// `providers:` block, so a malformed one is rejected before it reaches a KB.
func ValidateAgentProviders(fm *okf.Frontmatter) error {
	_, err := agentProviderFields(fm)
	return err
}

// nativeProvidersDeclared lists, sorted, the clients that have a non-empty
// `providers:` entry; "" fields are skipped. Unparseable frontmatter yields nil
// (the translation reports it on its own).
func nativeProvidersDeclared(fm *okf.Frontmatter) []string {
	m, err := agentProviderFields(fm)
	if err != nil {
		return nil
	}
	var out []string
	for p, f := range m {
		if len(f) > 0 {
			out = append(out, string(p))
		}
	}
	sort.Strings(out)
	return out
}

// nativeFieldsFor returns the fields to copy for one client (nil when none).
func nativeFieldsFor(fm *okf.Frontmatter, provider configurator.Provider) ([]nativeField, error) {
	m, err := agentProviderFields(fm)
	if err != nil {
		return nil, err
	}
	return m[provider], nil
}

// renderNativeYAML renders the fields as top-level YAML lines, keeping the
// author's key order, nesting and flow/block style.
func renderNativeYAML(fields []nativeField) (string, error) {
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
func renderNativeTOML(fields []nativeField) (string, error) {
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
			return n.Value, nil
		case "!!null":
			return "", fmt.Errorf("TOML has no null")
		default:
			return configurator.QuoteTOMLString(n.Value), nil
		}
	}
	return "", fmt.Errorf("unsupported YAML node")
}

// renderNativeJSONPairs renders the fields as `"key": value` JSON members, in
// the author's order, for splicing into an object.
func renderNativeJSONPairs(fields []nativeField) ([]string, error) {
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
