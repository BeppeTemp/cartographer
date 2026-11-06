package clientconfig

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// The providers:, knowledge_bases: and defaults: groups of the file
// (D1731). Config keeps its per-subject maps (Clients,
// Scopes, UnboundModes, Workspaces, KBOptions, Identities, SigningKeys): the
// groups are how they are written, and the two functions below are the only
// place that knows both shapes.

// defaultsGroup holds what applies to every provider.
type defaultsGroup struct {
	UnboundAccess string `yaml:"unbound_access,omitempty"`
}

// providerEntry is one provider's settings: what was spread over clients,
// scopes, unbound_access and workspaces. Access and Tools are the two halves of
// the old clients.<p>.access: read-min.
type providerEntry struct {
	KBs           *[]string          `yaml:"kbs,omitempty"`
	Scope         string             `yaml:"scope,omitempty"`
	UnboundAccess string             `yaml:"unbound_access,omitempty"`
	ConfigDirs    []string           `yaml:"config_dirs,omitempty"`
	Access        string             `yaml:"access,omitempty"`
	Tools         string             `yaml:"tools,omitempty"`
	AllowSymlinks []string           `yaml:"allow_symlinks,omitempty"`
	Workspaces    []WorkspaceBinding `yaml:"workspaces,omitempty"`
}

// UnmarshalYAML tells an absent kbs key from an empty or a null one: the first
// leaves the provider on the default KB set, the others bind it to none.
func (e *providerEntry) UnmarshalYAML(node *yaml.Node) error {
	type plain providerEntry
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*e = providerEntry(p)
	if node.Kind == yaml.MappingNode && e.KBs == nil && pairIndex(node, "kbs") >= 0 {
		e.KBs = &[]string{}
	}
	return nil
}

// kbEntry is one KB's settings: what was spread over kb_options, identities and
// signing_keys. The KB named default holds the identity of every other KB.
type kbEntry struct {
	Access      string   `yaml:"access,omitempty"`
	Artifacts   *bool    `yaml:"artifacts,omitempty"`
	Identity    *string  `yaml:"identity,omitempty"`
	SigningKeys []string `yaml:"signing_keys,omitempty"`
}

// groups is cfg as the three groups of the file.
func (c *Config) groups() (defaultsGroup, map[string]providerEntry, map[string]kbEntry) {
	d := defaultsGroup{UnboundAccess: c.UnboundModes[unboundDefault]}
	prov := map[string]providerEntry{}
	for p, b := range c.Clients {
		e := providerEntry{ConfigDirs: b.ConfigDirs, AllowSymlinks: b.AllowSymlinks}
		if b.Explicit() {
			kbs := b.KBs
			if kbs == nil {
				kbs = []string{}
			}
			e.KBs = &kbs
		}
		if b.Access == AccessReadMin {
			e.Access, e.Tools = accessRead, toolsMinimal
		} else {
			e.Access = b.Access
		}
		prov[p] = e
	}
	for p, v := range c.Scopes {
		e := prov[p]
		e.Scope = v
		prov[p] = e
	}
	for p, v := range c.UnboundModes {
		if p != unboundDefault {
			e := prov[p]
			e.UnboundAccess = v
			prov[p] = e
		}
	}
	for p, w := range c.Workspaces {
		if len(w) > 0 {
			e := prov[p]
			e.Workspaces = w
			prov[p] = e
		}
	}
	kbs := map[string]kbEntry{}
	for kb, o := range c.KBOptions {
		kbs[kb] = kbEntry{Access: o.Access, Artifacts: o.Artifacts}
	}
	for kb, v := range c.Identities {
		e := kbs[kb]
		e.Identity = &v
		kbs[kb] = e
	}
	for kb, keys := range c.SigningKeys {
		if len(keys) > 0 {
			e := kbs[kb]
			e.SigningKeys = keys
			kbs[kb] = e
		}
	}
	return d, prov, kbs
}

const (
	unboundDefault = UnboundDefault
	accessRead     = "read"
	toolsMinimal   = "minimal"
)

// applyGroups sets in c what the groups of a file hold, over what c already has
// from the flat layout (the grouped key wins; a different value at both is
// refused before this runs). path names the file in an error.
func (c *Config) applyGroups(y *yamlConfig, path string) error {
	if v := y.Defaults.UnboundAccess; v != "" {
		c.setMode(unboundDefault, v)
	}
	for _, p := range sortedNames(y.Providers) {
		e := y.Providers[p]
		if len(e.ConfigDirs) > 0 && p != "claude" {
			return fmt.Errorf("clientconfig: %s: providers.%s.config_dirs is only valid under providers.claude", path, p)
		}
		if (e.Access != "" || e.Tools != "") && (e.Access != accessRead || e.Tools != toolsMinimal) {
			return fmt.Errorf("clientconfig: %s: providers.%s: access %q with tools %q is not valid: the only combination is access: %s with tools: %s (remove both for the normal access)", path, p, e.Access, e.Tools, accessRead, toolsMinimal)
		}
		b, had := c.Clients[p]
		set := e.KBs != nil || len(e.ConfigDirs) > 0 || e.Access != "" || len(e.AllowSymlinks) > 0
		if e.KBs != nil {
			b.KBs, b.KBsSet = *e.KBs, true
		}
		if len(e.ConfigDirs) > 0 {
			b.ConfigDirs = e.ConfigDirs
		}
		if e.Access != "" {
			b.Access = AccessReadMin
		}
		if len(e.AllowSymlinks) > 0 {
			b.AllowSymlinks = e.AllowSymlinks
		}
		// An entry that sets nothing at all is an empty binding, as clients: {p: {}} was.
		if set || had || (e.Scope == "" && e.UnboundAccess == "" && len(e.Workspaces) == 0) {
			if c.Clients == nil {
				c.Clients = map[string]ClientBinding{}
			}
			c.Clients[p] = b
		}
		if e.Scope != "" {
			if c.Scopes == nil {
				c.Scopes = map[string]string{}
			}
			c.Scopes[p] = e.Scope
		}
		if e.UnboundAccess != "" {
			c.setMode(p, e.UnboundAccess)
		}
		if len(e.Workspaces) > 0 {
			if c.Workspaces == nil {
				c.Workspaces = map[string][]WorkspaceBinding{}
			}
			c.Workspaces[p] = e.Workspaces
		}
	}
	for _, kb := range sortedNames(y.KnowledgeBases) {
		e := y.KnowledgeBases[kb]
		if e.Access != "" || e.Artifacts != nil {
			o := c.KBOptions[kb]
			if e.Access != "" {
				o.Access = e.Access
			}
			if e.Artifacts != nil {
				o.Artifacts = e.Artifacts
			}
			if c.KBOptions == nil {
				c.KBOptions = map[string]KBOption{}
			}
			c.KBOptions[kb] = o
		}
		if e.Identity != nil {
			if c.Identities == nil {
				c.Identities = map[string]string{}
			}
			c.Identities[kb] = *e.Identity
		}
		if len(e.SigningKeys) > 0 {
			if c.SigningKeys == nil {
				c.SigningKeys = map[string][]string{}
			}
			c.SigningKeys[kb] = e.SigningKeys
		}
	}
	return nil
}

func (c *Config) setMode(provider, mode string) {
	if c.UnboundModes == nil {
		c.UnboundModes = map[string]string{}
	}
	c.UnboundModes[provider] = mode
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
