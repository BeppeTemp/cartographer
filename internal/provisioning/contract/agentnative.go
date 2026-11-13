package contract

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"gopkg.in/yaml.v3"
)

// nativeProviders are the clients that take a `providers:` entry: the ones whose
// agent file Cartographer translates. Claude receives the source verbatim, so its
// own fields go at the top level of the frontmatter, not here.
var nativeProviders = []configurator.Provider{
	configurator.ProviderOpenCode,
	configurator.ProviderCodex,
	configurator.ProviderKiro,
	configurator.ProviderAntigravity,
	configurator.ProviderCopilot,
	configurator.ProviderCline,
}

// reservedNativeKeys are the fields Cartographer writes itself for each client.
// An entry may not redefine them: it would silently change the agent's identity
// or prompt, which is not what a restriction block is for.
var reservedNativeKeys = map[configurator.Provider][]string{
	configurator.ProviderOpenCode:    {"description", "mode"},
	configurator.ProviderCodex:       {"name", "description", "developer_instructions"},
	configurator.ProviderKiro:        {"name", "description", "prompt"},
	configurator.ProviderAntigravity: {"name", "description", "mainAgent", "subagent"},
	configurator.ProviderCopilot:     {"name", "description"},
	configurator.ProviderCline:       {"name", "description"},
}

// NativeField is one key of a `providers.<client>` entry, with its YAML node kept
// so a rendering can preserve key order and nested structure.
type NativeField struct {
	Key   string
	Value *yaml.Node
}

// agentProviderFields parses the `providers:` block of an agent frontmatter into,
// per client, the ordered list of fields to copy. A missing block yields nil.
// An unknown client, a non-map entry or a reserved key is an error, so a typo
// fails loudly (at artifact_write and at sync) instead of dropping a restriction.
func agentProviderFields(fm *okf.Frontmatter) (map[configurator.Provider][]NativeField, error) {
	fields, _, err := agentProviderBlocks(fm)
	return fields, err
}

// agentProviderBlocks is agentProviderFields plus the `prompt_file` directive of
// each client. `prompt_file` is Cartographer's own instruction about the entry,
// never a native field: it is validated here and kept out of the returned
// fields, so no renderer can copy it into a client's file (PromptFileKey).
func agentProviderBlocks(fm *okf.Frontmatter) (map[configurator.Provider][]NativeField, map[configurator.Provider]string, error) {
	v, ok := fm.Get("providers")
	if !ok || v == nil {
		return nil, nil, nil
	}
	var text string
	switch t := v.(type) {
	case okf.Block:
		text = string(t)
	case string:
		text = t // a one-line flow map: providers: { codex: { ... } }
	default:
		return nil, nil, fmt.Errorf("frontmatter 'providers' must be a map keyed by client")
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, nil, fmt.Errorf("frontmatter 'providers': %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("frontmatter 'providers' must be a map keyed by client")
	}
	known := map[string]configurator.Provider{}
	for _, p := range nativeProviders {
		known[string(p)] = p
	}
	out := map[configurator.Provider][]NativeField{}
	files := map[configurator.Provider]string{}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		name, entry := top.Content[i].Value, top.Content[i+1]
		p, ok := known[name]
		if !ok {
			if name == string(configurator.ProviderClaudeCode) {
				return nil, nil, fmt.Errorf("frontmatter 'providers.%s': Claude Code receives the agent as written, so put its fields at the top level of the frontmatter", name)
			}
			return nil, nil, fmt.Errorf("frontmatter 'providers.%s': unknown client (supported: %s)", name, nativeProviderNames())
		}
		if entry.Kind == yaml.AliasNode {
			entry = entry.Alias
		}
		if entry.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("frontmatter 'providers.%s' must be a map of that client's native fields", name)
		}
		reserved := reservedNativeKeys[p]
		for j := 0; j+1 < len(entry.Content); j += 2 {
			k := entry.Content[j].Value
			for _, r := range reserved {
				if k == r {
					return nil, nil, fmt.Errorf("frontmatter 'providers.%s.%s': Cartographer writes %q itself on %s", name, k, k, name)
				}
			}
			val := entry.Content[j+1]
			if val.Kind == yaml.AliasNode {
				val = val.Alias
			}
			if k == PromptFileKey {
				if err := validatePromptFile(val); err != nil {
					return nil, nil, fmt.Errorf("frontmatter 'providers.%s.%s': %w", name, k, err)
				}
				files[p] = val.Value
				continue
			}
			out[p] = append(out[p], NativeField{Key: k, Value: val})
		}
	}
	return out, files, nil
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

// ClientsKey is the frontmatter key of an agent's allow-list of clients.
const ClientsKey = "clients"

// agentClientNames are the ids `clients:` accepts: Claude Code, which receives
// the agent as written, and the native providers. A plugin provider is matched
// by its base id (NativeProvider), so it has no name of its own here.
func agentClientNames() string {
	return string(configurator.ProviderClaudeCode) + ", " + nativeProviderNames()
}

func validAgentClient(id string) bool {
	if id == string(configurator.ProviderClaudeCode) {
		return true
	}
	for _, p := range nativeProviders {
		if id == string(p) {
			return true
		}
	}
	return false
}

// ParseAgentClients reads the `clients` allow-list of an agent's frontmatter:
// nil when the key is absent (every client), otherwise the lower-cased,
// de-duplicated names in order. A scalar, an empty list or an unknown name is an
// error naming the key and the supported clients.
func ParseAgentClients(fm *okf.Frontmatter) ([]string, error) {
	v, ok := fm.Get(ClientsKey)
	if !ok {
		return nil, nil
	}
	list, isList := v.([]string)
	if !isList {
		return nil, fmt.Errorf("frontmatter 'clients' must be a list of clients, e.g. [claude, kiro] (supported: %s)", agentClientNames())
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("frontmatter 'clients' is empty: list at least one client (supported: %s), or drop the key to project the agent everywhere", agentClientNames())
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range list {
		id := strings.ToLower(strings.TrimSpace(e))
		if !validAgentClient(id) {
			return nil, fmt.Errorf("frontmatter 'clients': unknown client %q (supported: %s)", e, agentClientNames())
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// ValidateAgentClients is the check artifact_write applies to `clients`.
func ValidateAgentClients(fm *okf.Frontmatter) error {
	_, err := ParseAgentClients(fm)
	return err
}

// AgentClientsOf reads the allow-list from an agent's source for the client
// side. An invalid or missing list yields nil (every client): a file that came
// by git skipped artifact_write, and BuildManifest reports the reason.
func AgentClientsOf(content []byte) []string {
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(content))
	if !hasFM {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return nil
	}
	list, err := ParseAgentClients(fm)
	if err != nil {
		return nil
	}
	return list
}

// kiroV2OnlyKeys are the keys of a `providers.kiro` entry that only Kiro's V2
// engine reads (#1717). Kiro CLI 3.x ignores `toolsSettings`, reads a shell
// `allowedCommands` regex as a glob and refuses `autoAllowReadonly` together with
// `denyByDefault`, and its session-start upgrade rewrites such a file in place.
// The V3 replacement is `permissions`, copied verbatim like every native key
// (D291); nothing is translated.
var kiroV2OnlyKeys = []string{"allowedTools", "toolsSettings"}

// KiroV2OnlyKeyWarnings names, for an agent that artifact_write accepted, the
// V2-only keys its `providers.kiro` entry carries and the V3 key to write
// instead. Advisory: V2 still runs them, so it informs and never rejects.
func KiroV2OnlyKeyWarnings(fm *okf.Frontmatter) []string {
	fields, err := NativeFieldsFor(fm, configurator.ProviderKiro)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range fields {
		for _, k := range kiroV2OnlyKeys {
			if f.Key == k {
				out = append(out, fmt.Sprintf("providers.kiro.%s is read by Kiro's V2 engine only: Kiro CLI 3.x ignores it or changes its meaning when it upgrades the agent; write the V3 `permissions` key instead (Cartographer copies it verbatim and translates nothing)", k))
			}
		}
	}
	return out
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

// NativeFieldsFor returns the fields to copy for one client (nil when none).
func NativeFieldsFor(fm *okf.Frontmatter, provider configurator.Provider) ([]NativeField, error) {
	m, err := agentProviderFields(fm)
	if err != nil {
		return nil, err
	}
	return m[provider], nil
}

// PromptFileKey is the `providers.<client>` directive that points a client's
// agent prompt at a file of the workspace instead of copying the body. It is
// not a native field of any client.
const PromptFileKey = "prompt_file"

// PromptFileClients are the clients that read their agent prompt from a file
// reference (`file://`), so a `prompt_file` is rendered for them. For any other
// client the key is a documented no-op: the body is used. Only Kiro is verified
// (docs/harnesses.md); a plugin provider is not listed, because it translates by
// its declared agent format and never as the built-in.
var PromptFileClients = []configurator.Provider{configurator.ProviderKiro}

// PromptFileCapable reports whether provider renders a `prompt_file`.
func PromptFileCapable(provider configurator.Provider) bool {
	for _, p := range PromptFileClients {
		if p == provider {
			return true
		}
	}
	return false
}

const maxPromptFileBytes = 512

func validatePromptFile(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return fmt.Errorf("must be a string: a path relative to the workspace root")
	}
	return checkPromptFilePath(n.Value)
}

// checkPromptFilePath is the one syntactic rule of a `prompt_file`: a non-empty,
// lexically local, slash-separated path relative to the workspace. Whether the
// file is inside the workspace once links are followed is the client's check
// (provisioning.resolvePromptFile).
func checkPromptFilePath(s string) error {
	switch {
	case s == "" || strings.TrimSpace(s) == "":
		return fmt.Errorf("must not be empty")
	case len(s) > maxPromptFileBytes:
		return fmt.Errorf("is longer than %d bytes", maxPromptFileBytes)
	case strings.ContainsRune(s, 0):
		return fmt.Errorf("must not contain a NUL byte")
	case strings.Contains(s, `\`):
		return fmt.Errorf("must use / as separator, not a backslash")
	case strings.HasPrefix(s, "/") || filepath.IsAbs(s) || (len(s) >= 2 && s[1] == ':' && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z')):
		return fmt.Errorf("must be relative to the workspace, not absolute")
	case !filepath.IsLocal(filepath.FromSlash(s)):
		return fmt.Errorf("must stay inside the workspace (no '..' that leaves it)")
	}
	return nil
}

// PromptFileFor returns the validated `providers.<provider>.prompt_file` of an
// agent's frontmatter, "" when it declares none. The error is the one
// artifact_write reports.
func PromptFileFor(fm *okf.Frontmatter, provider configurator.Provider) (string, error) {
	_, files, err := agentProviderBlocks(fm)
	if err != nil {
		return "", err
	}
	return files[provider], nil
}
