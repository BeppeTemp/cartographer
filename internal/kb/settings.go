package kb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SettingsFile is the KB's own policy file at its root (D746), next to
// paths.yaml: how this KB behaves, whoever serves it. It travels with the KB
// through git, so every mount of the same KB gets the same behaviour. No MCP
// tool writes it (artifact_write's whitelist does not name it): it changes
// every mount, so it goes through git review like a Map contract.
const SettingsFile = "settings.yaml"

// settingKind is how a settings.yaml value is checked. The keys themselves are
// the schema's kb scope (config.Schema); this package cannot import config
// (config -> lint -> kb), so TestKBSettingsKeysMatchSchema in config pins this
// table to the schema in both directions.
type settingKind int

const (
	kindBool settingKind = iota
	kindString
	kindEnum
	kindDays      // "<n>d", "<n>" or an integer; 0 disables
	kindInt0      // integer >= 0
	kindInt1      // integer >= 1
	kindStringSeq // sequence of scalars; empty allowed
	kindMCPList   // sequence of {name, transport, target}
)

type settingSpec struct {
	kind   settingKind
	values []string // kindEnum
}

// settingsKeys are the keys a KB carries. auto_repair names are checked by
// whoever applies them (they need lint.FixableChecks); mcp_allowlist entries
// are checked by provisioning.ValidateMCPAllowlist at apply time.
var settingsKeys = map[string]settingSpec{
	"allow_artifact_write": {kind: kindBool},
	"allow_template_write": {kind: kindBool},
	"write_gate":           {kind: kindEnum, values: []string{"off", "error", "warning"}},
	"auto_repair":          {kind: kindStringSeq},
	"repair_on_write":      {kind: kindBool},
	"doctor_mode":          {kind: kindEnum, values: []string{"unattended", "assisted"}},
	"doctor_interval":      {kind: kindDays},
	"doctor_auto_interval": {kind: kindDays},
	"doctor_budget":        {kind: kindInt1},
	"usage_stale_days":     {kind: kindInt0},
	"mcp_allowlist":        {kind: kindMCPList},
	"secrets":              {kind: kindBool},
}

// settingsServerOnly are the kbs[] keys bound to one machine; a KB that names
// one gets a finding that says so rather than "unknown key".
var settingsServerOnly = map[string]bool{
	"path": true, "remote": true, "name": true, "ssh_key": true, "known_hosts": true,
	"author_name": true, "author_email": true, "committer_name": true, "committer_email": true,
	"sops_age_key_file": true,
	"git_branch":        true, "artifact_signing_seed": true,
}

// SettingsKeyNames returns the keys the KB file accepts, sorted.
func SettingsKeyNames() []string {
	out := make([]string, 0, len(settingsKeys))
	for k := range settingsKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SettingsServerOnlyNames returns the server-only keys the KB file refuses, sorted.
func SettingsServerOnlyNames() []string {
	out := make([]string, 0, len(settingsServerOnly))
	for k := range settingsServerOnly {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SettingsMalformed is one entry of settings.yaml that was left out.
type SettingsMalformed struct {
	Key    string
	Reason string
}

func (m SettingsMalformed) String() string {
	if m.Key == "" {
		return m.Reason
	}
	return fmt.Sprintf("%s: %s", m.Key, m.Reason)
}

// Settings is the accepted part of settings.yaml: only well-formed entries of
// known kb-scope keys.
type Settings struct {
	keys    []string
	mapping *yaml.Node
}

// Has reports whether the KB file sets key.
func (s Settings) Has(key string) bool {
	for _, k := range s.keys {
		if k == key {
			return true
		}
	}
	return false
}

// Keys lists the accepted keys in file order.
func (s Settings) Keys() []string { return append([]string(nil), s.keys...) }

// Decode decodes the accepted entries into v (a *config.KBSpec): only those
// keys are touched, every other field of v keeps its value.
func (s Settings) Decode(v any) error {
	if s.mapping == nil {
		return nil
	}
	return s.mapping.Decode(v)
}

// ParseSettings parses settings.yaml tolerantly, as ParsePathRegistry does:
// every well-formed entry is kept, every other one is left out and listed.
// err is non-nil only when the file is not a YAML mapping at all, in which
// case the whole file is ignored.
func ParseSettings(data []byte) (Settings, []SettingsMalformed, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Settings{}, nil, fmt.Errorf("%s: not valid YAML: %v", SettingsFile, err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return Settings{}, nil, nil
	}
	doc := root.Content[0]
	if doc.Kind == yaml.ScalarNode && doc.Tag == "!!null" {
		return Settings{}, nil, nil
	}
	if doc.Kind != yaml.MappingNode {
		return Settings{}, nil, fmt.Errorf("%s: top level must be a mapping of setting names to values", SettingsFile)
	}
	var bad []SettingsMalformed
	out := Settings{mapping: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}
	seen := map[string]bool{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k, v := doc.Content[i], doc.Content[i+1]
		key := k.Value
		spec, known := settingsKeys[key]
		switch {
		case settingsServerOnly[key]:
			bad = append(bad, SettingsMalformed{key, "server-only setting: it is bound to one machine and belongs in the server's kbs[] entry, not in the KB"})
			continue
		case !known:
			bad = append(bad, SettingsMalformed{key, "unknown setting"})
			continue
		case seen[key]:
			bad = append(bad, SettingsMalformed{key, "declared twice"})
			continue
		}
		seen[key] = true
		if reason := checkSetting(spec, v); reason != "" {
			bad = append(bad, SettingsMalformed{key, reason})
			continue
		}
		out.keys = append(out.keys, key)
		out.mapping.Content = append(out.mapping.Content, k, v)
	}
	return out, bad, nil
}

func checkSetting(spec settingSpec, v *yaml.Node) string {
	scalar := v.Kind == yaml.ScalarNode && v.Tag != "!!null"
	switch spec.kind {
	case kindBool:
		var b bool
		if v.Kind != yaml.ScalarNode || v.Decode(&b) != nil {
			return "must be true or false"
		}
	case kindString:
		if !scalar {
			return "must be a string"
		}
	case kindEnum:
		if !scalar || !inList(spec.values, v.Value) {
			return "must be one of " + strings.Join(spec.values, ", ")
		}
	case kindDays:
		if !scalar {
			return `must be a number of days ("14d", "14", or 0 to disable)`
		}
		if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v.Value), "d")); err != nil || n < 0 {
			return fmt.Sprintf(`%q is not a number of days ("14d", "14", or 0 to disable)`, v.Value)
		}
	case kindInt0, kindInt1:
		min := 0
		if spec.kind == kindInt1 {
			min = 1
		}
		n, err := strconv.Atoi(v.Value)
		if !scalar || err != nil || n < min {
			return fmt.Sprintf("must be an integer, %d or more", min)
		}
	case kindStringSeq:
		if v.Kind != yaml.SequenceNode {
			return "must be a list of names"
		}
		for _, it := range v.Content {
			if it.Kind != yaml.ScalarNode || it.Tag == "!!null" {
				return "must be a list of names"
			}
		}
	case kindMCPList:
		if v.Kind != yaml.SequenceNode {
			return "must be a list of {name, transport, target} entries"
		}
		for _, it := range v.Content {
			if it.Kind != yaml.MappingNode {
				return "must be a list of {name, transport, target} entries"
			}
			for j := 0; j+1 < len(it.Content); j += 2 {
				if f := it.Content[j].Value; f != "name" && f != "transport" && f != "target" {
					return fmt.Sprintf("entry field %q is not name, transport or target", f)
				}
			}
		}
	}
	return ""
}

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// SettingsState is what ReadSettings found at the KB root.
type SettingsState struct {
	// Present is false when the KB has no settings.yaml: it behaves as it did
	// before the file existed, on every server.
	Present bool
	// Unparseable is set when the file exists but is not a YAML mapping (its
	// reason is Malformed[0]): the whole file is ignored.
	Unparseable bool
	Settings    Settings
	Malformed   []SettingsMalformed
}

// ReadSettings reads the KB's settings.yaml tolerantly. A symlinked file is an
// error, as every KB-root artifact is (D148).
func (k *KB) ReadSettings() (SettingsState, error) {
	path := filepath.Join(k.Root, SettingsFile)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return SettingsState{}, nil
	}
	if err != nil {
		return SettingsState{}, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return SettingsState{}, fmt.Errorf("%s: symlink not allowed", SettingsFile)
	}
	if !fi.Mode().IsRegular() {
		return SettingsState{}, fmt.Errorf("%s: not a regular file", SettingsFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SettingsState{}, err
	}
	s, bad, perr := ParseSettings(data)
	if perr != nil {
		return SettingsState{Present: true, Unparseable: true, Malformed: []SettingsMalformed{{Reason: perr.Error()}}}, nil
	}
	return SettingsState{Present: true, Settings: s, Malformed: bad}, nil
}

// IgnoredSetting is a server value that a KB's settings.yaml beat while the two
// differed (D746): the KB is authoritative, the server value changes nothing.
type IgnoredSetting struct {
	Key         string `json:"key"`
	ServerValue string `json:"server_value"`
	KBValue     string `json:"kb_value"`
}

// SettingsInfo is what the last resolution of a KB's settings found, for
// kb_status and the startup log. The zero value is a KB with no settings.yaml.
type SettingsInfo struct {
	Present bool
	// FromKB are the keys whose effective value comes from settings.yaml.
	FromKB []string
	// Ignored are the server values the file beat.
	Ignored []IgnoredSetting
	// Findings are the entries of the file that were left out, and why.
	Findings []string
	// SecretsDeclared is the effective `secrets` value: the KB says it uses
	// secrets, so a mount without an age key is a defect and not a choice.
	SecretsDeclared bool
}

// FromKBHas reports whether key's effective value comes from settings.yaml.
func (i SettingsInfo) FromKBHas(key string) bool {
	for _, k := range i.FromKB {
		if k == key {
			return true
		}
	}
	return false
}

// IgnoredFor returns the ignored server value of key, if the file beat one.
func (i SettingsInfo) IgnoredFor(key string) (IgnoredSetting, bool) {
	for _, g := range i.Ignored {
		if g.Key == key {
			return g, true
		}
	}
	return IgnoredSetting{}, false
}

// SettingsInfo returns the last resolution's result.
func (k *KB) SettingsInfo() SettingsInfo {
	k.settingsMu.RLock()
	defer k.settingsMu.RUnlock()
	return k.settingsInfo
}

// SetSettingsInfo records a resolution's result.
func (k *KB) SetSettingsInfo(i SettingsInfo) {
	k.settingsMu.Lock()
	k.settingsInfo = i
	k.settingsMu.Unlock()
}

// RefreshSettings re-resolves the KB's effective settings when settings.yaml
// changed on disk since the last call (a pull, an edit committed as external
// changes): it costs one Lstat otherwise. It runs inside the git lock, after a
// pull, and does not take any lock of its own beyond the settings one, so a
// handler that holds the KB lock may call it. A no-op until the server sets
// SettingsReload.
func (k *KB) RefreshSettings() {
	if k.SettingsReload == nil {
		return
	}
	stamp := "absent"
	if fi, err := os.Lstat(filepath.Join(k.Root, SettingsFile)); err == nil {
		stamp = fmt.Sprintf("%d-%d-%v", fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
	}
	k.settingsMu.Lock()
	same := k.settingsStamp == stamp
	k.settingsStamp = stamp
	k.settingsMu.Unlock()
	if !same {
		k.SettingsReload(k)
	}
}

// MarkSettingsSeen records the file as it is now without reloading: the first
// resolution at mount time calls it so that RefreshSettings does not redo it.
func (k *KB) MarkSettingsSeen() {
	stamp := "absent"
	if fi, err := os.Lstat(filepath.Join(k.Root, SettingsFile)); err == nil {
		stamp = fmt.Sprintf("%d-%d-%v", fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
	}
	k.settingsMu.Lock()
	k.settingsStamp = stamp
	k.settingsMu.Unlock()
}
