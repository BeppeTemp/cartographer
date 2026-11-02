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
	// Access is "" or "write" (read-write, the default) or "read": the KB is
	// connected read-only on every provider that receives it (#1409). A scope
	// filter, not a security boundary. Read it through Config.KBAccess.
	Access string `yaml:"access,omitempty"`
}

// The two values of KBOption.Access. Absent equals KBAccessWrite.
const (
	KBAccessRead  = "read"
	KBAccessWrite = "write"
)

// isZero reports whether the entry sets nothing, so it can be dropped.
func (o KBOption) isZero() bool {
	return o.Artifacts == nil && (o.Access == "" || o.Access == KBAccessWrite)
}

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

// KBAccess returns kb's access, KBAccessRead or KBAccessWrite (the default for
// a nil Config, an empty name, a missing entry and an absent key).
func (c *Config) KBAccess(kb string) string {
	if c == nil || kb == "" || c.KBOptions[kb].Access != KBAccessRead {
		return KBAccessWrite
	}
	return KBAccessRead
}

// SetKBAccess sets kb's access ("read" or "write"). Write removes the key, the
// entry once it holds nothing else and the map once empty.
func (c *Config) SetKBAccess(kb, access string) error {
	if access != KBAccessRead && access != KBAccessWrite {
		return fmt.Errorf("clientconfig: invalid access %q: want %q or %q", access, KBAccessRead, KBAccessWrite)
	}
	o := c.KBOptions[kb]
	o.Access = access
	if access == KBAccessWrite {
		o.Access = ""
	}
	c.setKBOption(kb, o)
	return nil
}

// ReadOnlyKBs returns, sorted, the KBs whose access is "read".
func (c *Config) ReadOnlyKBs() []string {
	if c == nil {
		return nil
	}
	var out []string
	for k, o := range c.KBOptions {
		if k != "" && o.Access == KBAccessRead {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
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
		if a := c.KBOptions[k].Access; a != "" && a != KBAccessRead && a != KBAccessWrite {
			return fmt.Errorf("clientconfig: %s: kb_options.%s.access %q is not valid: the values are %q and %q (remove the key for %q)", path, k, a, KBAccessRead, KBAccessWrite, KBAccessWrite)
		}
	}
	return nil
}
