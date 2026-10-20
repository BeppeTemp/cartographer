package clientconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// SettingsReport is what a client tells its server about its own settings
// (D818), so the Atlas can show the viewer their own client beside the server
// and the KBs. It is built here, on the client, because only the client knows
// its file and the schema of the version it runs. Entries have the JSON shape
// of the server's own settings entries, so the Atlas reads both alike.
type SettingsReport struct {
	Machine   string        `json:"machine"`
	Version   string        `json:"version"`
	ServerURL string        `json:"server_url"`
	Entries   []ReportEntry `json:"entries"`
	// YAML is the .cartographer.yaml as it is on disk, commented by the schema
	// when Save wrote it, with the value of every secret key replaced by
	// SecretRedacted; "" when the machine has no file yet.
	YAML string `json:"yaml"`
	// Providers are the connected agent clients: where each receives its KBs
	// and whether a workspace binding really narrows what a session sees.
	// Filled by the caller, which knows the providers (cmd/cartographer).
	Providers []ReportProvider `json:"providers"`
	// Placeholders are the {{repo:…}} and {{path:…}} keys the connected KBs
	// cite, as the last sync resolved them on this machine.
	Placeholders []ReportPlaceholder `json:"placeholders"`
}

// ReportProvider is one connected agent client.
type ReportProvider struct {
	Name string `json:"name"`
	// Scope is "provider" (every KB, under the home directory) or
	// "workspace" (each bound folder receives its own KBs).
	Scope string `json:"scope"`
	// Entry is the kind of global MCP entry the client has: "bridge" (the
	// local stdio bridge, D737, written when the client has a workspace
	// binding) or "http" (the server URL).
	Entry string `json:"entry"`
	// Isolation says whether a session sees only the KBs of its folder:
	// "workspace" when the client has the bridge, "none" when the global entry
	// still exposes every KB the token allows. It is a scope filter, not a
	// security boundary (D737).
	Isolation  string            `json:"isolation"`
	Workspaces []ReportWorkspace `json:"workspaces,omitempty"`
}

// ReportWorkspace is one folder bound to KBs for a provider.
type ReportWorkspace struct {
	Path   string   `json:"path"`
	Remote string   `json:"remote,omitempty"`
	KBs    []string `json:"kbs"`
}

// ReportPlaceholder is one placeholder key and where it resolves here.
type ReportPlaceholder struct {
	Key string   `json:"key"`
	KBs []string `json:"kbs,omitempty"`
	// Path is where the key resolves on this machine, "" when it does not.
	Path        string `json:"path,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Description string `json:"description,omitempty"`
	Ignored     bool   `json:"ignored,omitempty"`
}

// ReportEntry is one client key with its effective value and origin: "file"
// when .cartographer.yaml sets it, "default" otherwise. The value of a secret
// key is SecretSet or SecretUnset, never the secret (D818).
type ReportEntry struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Type        string `json:"type,omitempty"`
	Values      string `json:"values,omitempty"`
}

// Report reads the client settings in dir. A missing file is no error: every
// key is then at its default.
func Report(dir, version string) (*SettingsReport, error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clientconfig: read %s: %w", Path(dir), err)
	}
	machine, _ := os.Hostname()
	return report(data, machine, version)
}

// Template is the client settings of a machine with no .cartographer.yaml,
// every key at its default. The Atlas shows it to a viewer none of whose
// clients has reported yet (D818), so what a client can set is readable
// before one connects.
func Template() *SettingsReport {
	r, _ := report(nil, "", "")
	return r
}

// What a report shows in place of a secret, the words the server's own settings
// view uses for its secrets: SecretSet or SecretUnset in an entry,
// SecretRedacted in the YAML (config.SecretSet, config.SecretUnset,
// config.Redacted).
const (
	SecretSet      = "(set)"
	SecretUnset    = "(unset)"
	SecretRedacted = "<redacted>"
)

// IsSecret reports whether key is a secret of the client schema (the token).
func IsSecret(key string) bool {
	k, ok := Schema.Lookup(key)
	return ok && k.Secret
}

// SecretValue is the value a report shows for a secret key given its origin and
// value: SecretSet when the file sets a non-empty value, SecretUnset otherwise
// (a key at its default holds the schema's default, not a value of the file).
// A value already masked keeps its mask: the server masks a client's report
// again, and an unset token must not come out set.
func SecretValue(origin string, value any) string {
	if value == SecretSet || value == SecretUnset {
		return value.(string)
	}
	if origin == "file" && value != nil && value != "" {
		return SecretSet
	}
	return SecretUnset
}

// RedactYAML replaces the value of every secret key of a settings file with
// SecretRedacted (a `${NAME}` reference too: the server's own YAML view writes
// every secret that way), keeping the rest of the file, its comments included.
// A key with no value stays as it is: there is nothing to hide. The bytes are
// returned unchanged when no secret is set. A comment on a secret's own line
// goes with it, and a commented-out assignment (`# token: …`, an old token kept
// for later) has its value masked too, unless it is the schema's default, which
// is what the file lists for a key it does not set. The error is the file not
// being YAML, which cannot be checked for a secret.
func RedactYAML(data []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("clientconfig: parse settings: %w", err)
	}
	return redactDoc(&doc, data)
}

// redactDoc masks the secrets of an already parsed document; data is the
// file's bytes, returned when none is set.
func redactDoc(doc *yaml.Node, data []byte) ([]byte, error) {
	if opaque(doc, data) {
		return []byte(OpaqueYAML), nil
	}
	changed := false
	for _, k := range Schema.Keys {
		if !k.Secret {
			continue
		}
		for _, n := range lookupAll(doc, strings.Split(k.Path, ".")) {
			if n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "") {
				continue
			}
			// The anchor stays, so an alias to the value still resolves; the
			// comments on the value go, a trailing one is likely to quote it.
			*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: SecretRedacted, Anchor: n.Anchor}
			changed = true
		}
	}
	if redactComments(doc) {
		changed = true
	}
	if !changed {
		return data, nil
	}
	return settingschema.Encode(doc)
}

// OpaqueYAML stands for a settings file the mask cannot vouch for (opaque).
const OpaqueYAML = "# not shown: this file uses a YAML merge key or holds several documents, where a secret escapes the mask\n"

// opaque reports whether a settings file can carry a secret where the mask
// does not look: through a YAML merge key (`<<:`), which the client's decoder
// resolves, or in a second document. Such a file is replaced by OpaqueYAML.
func opaque(doc *yaml.Node, data []byte) bool {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for docs := 0; ; docs++ {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if docs > 1 {
				return true
			}
			break
		}
	}
	var merges func(n *yaml.Node) bool
	merges = func(n *yaml.Node) bool {
		if n == nil {
			return false
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if k := n.Content[i]; k.Tag == "!!merge" || k.Value == "<<" {
					return true
				}
			}
		}
		for _, c := range n.Content {
			if merges(c) {
				return true
			}
		}
		return false
	}
	return merges(doc)
}

// commentedSecret matches a commented-out assignment of one secret key
// ("# token: abc", indented or not): group 1 is what stays, group 2 the value;
// def is the schema's default, which a file lists commented out and is no secret.
type commentedSecret struct {
	re  *regexp.Regexp
	def string
}

var secretComments = func() []commentedSecret {
	var out []commentedSecret
	for _, k := range Schema.Keys {
		if k.Secret {
			leaf := k.Path[strings.LastIndex(k.Path, ".")+1:]
			out = append(out, commentedSecret{regexp.MustCompile(`^(\s*#\s*` + regexp.QuoteMeta(leaf) + `\s*:\s*)(\S.*)$`), k.Default})
		}
	}
	return out
}()

// redactComments masks the value of a commented-out assignment of a secret key
// in the comments of every node, and reports whether it changed any.
func redactComments(n *yaml.Node) bool {
	changed := false
	for _, c := range []*string{&n.HeadComment, &n.LineComment, &n.FootComment} {
		if *c == "" {
			continue
		}
		lines := strings.Split(*c, "\n")
		for i, l := range lines {
			for _, sc := range secretComments {
				if m := sc.re.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[2]) != sc.def {
					lines[i] = m[1] + SecretRedacted
					changed = true
				}
			}
		}
		*c = strings.Join(lines, "\n")
	}
	for _, child := range n.Content {
		if redactComments(child) {
			changed = true
		}
	}
	return changed
}

// report builds the report of one settings file's bytes; nil is no file. The
// secrets are masked here, on the client, so the token never leaves the machine
// (D818); the server masks them again for a client that predates this.
func report(data []byte, machine, version string) (*SettingsReport, error) {
	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("clientconfig: parse settings: %w", err)
		}
	}
	r := &SettingsReport{Machine: machine, Version: version, Entries: []ReportEntry{}}
	for _, k := range Schema.Keys {
		e := ReportEntry{Key: k.Path, Origin: "default", Description: k.Description, Default: k.Default, Type: k.Type, Values: k.Values}
		if n := lookup(&doc, k.Path); n != nil {
			e.Origin = "file"
			_ = n.Decode(&e.Value)
		} else {
			_ = yaml.Unmarshal([]byte(k.Default), &e.Value)
		}
		if k.Path == "server_url" {
			r.ServerURL, _ = e.Value.(string)
		}
		if k.Secret {
			e.Value = SecretValue(e.Origin, e.Value)
		}
		r.Entries = append(r.Entries, e)
	}
	masked, err := redactDoc(&doc, data)
	if err != nil {
		return nil, fmt.Errorf("clientconfig: mask settings: %w", err)
	}
	r.YAML = string(masked)
	return r, nil
}

// lookup returns the value node at a dotted path in a YAML document, or nil.
func lookup(doc *yaml.Node, path string) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range strings.Split(path, ".") {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}

// lookupAll returns every value node at a dotted path: a mapping that repeats
// a key has them all, where lookup stops at the first (a parse keeps the
// duplicates, and a secret hidden in the second must be masked too).
func lookupAll(doc *yaml.Node, segs []string) []*yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	if len(segs) == 0 {
		return []*yaml.Node{n}
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var out []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == segs[0] {
			out = append(out, lookupAll(n.Content[i+1], segs[1:])...)
		}
	}
	return out
}
