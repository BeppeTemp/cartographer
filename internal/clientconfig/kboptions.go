package clientconfig

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
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
	// Transversal true makes the KB's instructions and read access reach every
	// session of a provider in workspace scope, wherever the folder is bound;
	// its artifacts stay tied to a binding. Absent is false. Only the grouped
	// knowledge_bases layout carries it (yaml:"-": the flat kb_options layout is
	// a migration input and does not accept it). Read it through
	// Config.KBTransversal and Config.TransversalKBs.
	Transversal bool `yaml:"-"`
}

// The two values of KBOption.Access. Absent equals KBAccessWrite.
const (
	KBAccessRead  = "read"
	KBAccessWrite = "write"
)

// isZero reports whether the entry sets nothing, so it can be dropped.
func (o KBOption) isZero() bool {
	return o.Artifacts == nil && (o.Access == "" || o.Access == KBAccessWrite) && !o.Transversal
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

// KBTransversal reports whether kb is declared transversal. False for a nil
// Config, an empty name, a missing entry and an absent key. It says nothing
// about a provider: TransversalKBs applies the scope rule.
func (c *Config) KBTransversal(kb string) bool {
	if c == nil || kb == "" {
		return false
	}
	return c.KBOptions[kb].Transversal
}

// SetKBTransversal sets kb's transversal flag. Off removes the key, the entry
// once it holds nothing else and the map once empty, so the file stays clean.
func (c *Config) SetKBTransversal(kb string, on bool) {
	o := c.KBOptions[kb]
	o.Transversal = on
	c.setKBOption(kb, o)
}

// TransversalKBs returns the transversal KBs provider gets, sorted and
// deduplicated: none unless provider is in workspace scope (in provider scope
// the global catalogue already reaches every session, and providers.<p>.kbs is
// the person's own binding, which a flag must not widen), restricted to the KBs
// the server mounts when that list is known (RoutedBinding's rule). The single
// reader both the stdio bridge and sync use. A transversal KB is never added to
// a binding, and it is never an input of a lineage closure.
func (c *Config) TransversalKBs(provider string) []string {
	if c == nil || c.WorkspaceScope(provider) != ScopeWorkspace {
		return nil
	}
	var flagged []string
	for kb, o := range c.KBOptions {
		if kb != "" && o.Transversal {
			flagged = append(flagged, kb)
		}
	}
	return RoutedBinding(c.KnownKBs, flagged)
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
			return fmt.Errorf("clientconfig: %s: knowledge_bases has an entry with an empty KB name", path)
		}
		if a := c.KBOptions[k].Access; a != "" && a != KBAccessRead && a != KBAccessWrite {
			return fmt.Errorf("clientconfig: %s: knowledge_bases.%s.access %q is not valid: the values are %q and %q (remove the key for %q)", path, k, a, KBAccessRead, KBAccessWrite, KBAccessWrite)
		}
	}
	return nil
}

// legacyTransversalWarnings names each `kb_options.<kb>.transversal` of the old
// flat layout: that layout does not carry the flag, so it is ignored, and the
// realignment drops it. A person who wrote it has nothing else telling them so.
func legacyTransversalWarnings(data []byte) []string {
	var doc struct {
		KBOptions map[string]map[string]yaml.Node `yaml:"kb_options"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	var kbs []string
	for kb, o := range doc.KBOptions {
		if _, ok := o["transversal"]; ok {
			kbs = append(kbs, kb)
		}
	}
	sort.Strings(kbs)
	out := make([]string, len(kbs))
	for i, kb := range kbs {
		out[i] = fmt.Sprintf("kb_options.%s.transversal is ignored and the next sync drops it: the flag exists only under knowledge_bases.%s.transversal (set it with `cartographer client kb %s --transversal=true`)", kb, kb, kb)
	}
	return out
}

// legacyWarned remembers what Load already said in this process, so a command
// that loads the file several times says it once.
var legacyWarned sync.Map

// warnLegacyOnce prints the warnings of legacyTransversalWarnings to stderr, the
// one place every command that loads the client file passes through.
func warnLegacyOnce(path string, warnings []string) {
	for _, w := range warnings {
		line := "warning: " + path + ": " + strings.NewReplacer("\n", " ", "\r", " ").Replace(w)
		if _, dup := legacyWarned.LoadOrStore(line, true); !dup {
			fmt.Fprintln(os.Stderr, line)
		}
	}
}

// dropLegacyTransversal removes `transversal` from every entry of the flat
// `kb_options` map of root, in place, and returns the KB names it was on,
// sorted. The flat layout never accepted the flag; carrying it over with the
// move into knowledge_bases would activate it silently.
func dropLegacyTransversal(root *yaml.Node) []string {
	i := pairIndex(root, "kb_options")
	if i < 0 || root.Content[i+1].Kind != yaml.MappingNode {
		return nil
	}
	var done []string
	m := root.Content[i+1]
	for j := 0; j+1 < len(m.Content); j += 2 {
		e := m.Content[j+1]
		if e.Kind != yaml.MappingNode {
			continue
		}
		if k := pairIndex(e, "transversal"); k >= 0 {
			e.Content = append(e.Content[:k], e.Content[k+2:]...)
			done = append(done, m.Content[j].Value)
		}
	}
	sort.Strings(done)
	return done
}
