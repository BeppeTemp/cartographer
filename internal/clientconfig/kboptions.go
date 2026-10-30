package clientconfig

import (
	"fmt"
	"sort"
)

// KBOption is the per-KB settings entry of `kb_options` (#1408). Every field
// is optional and a pointer where the zero value would differ from "unset":
// an absent key is the default, an explicit false is a choice. A new option is
// a new field here plus its own accessor and a line in isZero, so the entry
// stays open for it.
type KBOption struct {
	// Artifacts false stops sync from provisioning the KB's skills, subagents,
	// hooks and MCP descriptors, and removes the ones already installed. Absent
	// is true: artifacts are synced. Read it through Config.KBArtifacts.
	Artifacts *bool `yaml:"artifacts,omitempty"`
}

// isZero reports whether the entry sets nothing, so it can be dropped.
func (o KBOption) isZero() bool { return o.Artifacts == nil }

// KBArtifacts reports whether kb's artifacts are synced. False only for an
// entry that says `artifacts: false`: a nil Config, an empty name, a missing
// entry and an absent key are all the default, true.
func (c *Config) KBArtifacts(kb string) bool {
	if c == nil || kb == "" {
		return true
	}
	o, ok := c.KBOptions[kb]
	if !ok || o.Artifacts == nil {
		return true
	}
	return *o.Artifacts
}

// SetKBArtifacts sets kb's artifacts option. Turning it on removes the key, the
// entry once it holds nothing else and the map once empty, so the file stays
// clean and a sibling option in the same entry survives.
func (c *Config) SetKBArtifacts(kb string, on bool) {
	o := c.KBOptions[kb]
	if on {
		o.Artifacts = nil
	} else {
		off := false
		o.Artifacts = &off
	}
	c.setKBOption(kb, o)
}

// setKBOption stores o under kb, deleting an empty entry and an empty map.
func (c *Config) setKBOption(kb string, o KBOption) {
	if o.isZero() {
		delete(c.KBOptions, kb)
		if len(c.KBOptions) == 0 {
			c.KBOptions = nil
		}
		return
	}
	if c.KBOptions == nil {
		c.KBOptions = map[string]KBOption{}
	}
	c.KBOptions[kb] = o
}

// validateKBOptions rejects an entry with no KB name. A name the server does
// not advertise is not an error: the KB may be mounted later (as Bind).
func (c *Config) validateKBOptions(path string) error {
	names := make([]string, 0, len(c.KBOptions))
	for k := range c.KBOptions {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if k == "" {
			return fmt.Errorf("clientconfig: %s: kb_options has an entry with an empty KB name", path)
		}
	}
	return nil
}
