package config

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// OriginKB says a per-KB value comes from the KB's own settings.yaml (D746).
// The KB is authoritative: it beats the server's kbs[] entry, env and flags.
const OriginKB Origin = "kb"

// KBSettingsSchema is the schema of a settings.yaml: KBKeys, the keys only a
// KB has. `kb create` renders the commented file from it.
func KBSettingsSchema() settingschema.Schema {
	return settingschema.Schema{Keys: append([]settingschema.Key(nil), KBKeys...)}
}

// kbPolicyStore is the effective per-KB specs and the keys that came from a
// KB file, replaced whenever a KB's settings are re-resolved. A separate
// object, behind a pointer, because the settings view reads it from the HTTP
// handlers while a pull refreshes it.
type kbPolicyStore struct {
	mu     sync.RWMutex
	eff    map[string]KBSpec
	fromKB map[string]bool // origin key -> set by the KB file
}

// policyInit guards the lazy creation of Config.kbPolicy: Config is passed
// around by value in places, so it cannot hold a lock of its own.
var policyInit sync.Mutex

func (c *Config) policy() *kbPolicyStore {
	policyInit.Lock()
	defer policyInit.Unlock()
	if c.kbPolicy == nil {
		c.kbPolicy = &kbPolicyStore{eff: map[string]KBSpec{}, fromKB: map[string]bool{}}
	}
	return c.kbPolicy
}

// RecordKBSettings keeps the effective spec of the KB mounted under name and
// the keys that come from its settings.yaml, for the settings view. A
// discovered KB, which has no kbs[] entry, is shown the same way.
func (c *Config) RecordKBSettings(name string, eff KBSpec, fromKB []string) {
	p := c.policy()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eff[name] = eff
	prefix := KBOriginKey(name, "")
	for k := range p.fromKB {
		if strings.HasPrefix(k, prefix) {
			delete(p.fromKB, k)
		}
	}
	for _, key := range fromKB {
		p.fromKB[KBOriginKey(name, key)] = true
	}
}

func (c *Config) kbSpecFor(name string) (KBSpec, bool) {
	p := c.policy()
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, ok := p.eff[name]
	return s, ok
}

func (c *Config) originFromKB(originKey string) bool {
	p := c.policy()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.fromKB[originKey]
}

// ResolveKBSettings reads a KB's policy from its settings.yaml (D746), the one
// place that carries it (D906): the server file names a KB, it does not
// configure it. server supplies the machine's side of the KB (its name, remote
// and path), which the result keeps; every policy field starts at its default.
// A usage_stale_days under kb.MinUsageStaleDays is raised to it, with a finding.
func ResolveKBSettings(server KBSpec, st kb.SettingsState) (KBSpec, kb.SettingsInfo) {
	info := kb.SettingsInfo{Present: st.Present}
	for _, m := range st.Malformed {
		info.Findings = append(info.Findings, m.String())
	}
	eff := KBSpec{Path: server.Path, Remote: server.Remote, Name: server.Name, BranchOverride: server.BranchOverride}
	if !st.Present || st.Unparseable {
		return eff, info
	}
	var part KBSpec
	if err := st.Settings.Decode(&part); err != nil {
		info.Findings = append(info.Findings, fmt.Sprintf("%s: %v", kb.SettingsFile, err))
		return eff, info
	}
	// branch has no kbs[] counterpart, so no yaml tag to decode it through.
	part.Branch, _ = st.Settings.String("branch")
	for _, key := range st.Settings.Keys() {
		if !setKBField(&eff, part, key) {
			info.Findings = append(info.Findings, key+": not applied (no such field)")
			continue
		}
		info.FromKB = append(info.FromKB, key)
	}
	if n := eff.UsageStaleDays; n != nil && *n < kb.MinUsageStaleDays {
		info.Findings = append(info.Findings, fmt.Sprintf("usage_stale_days: %d is under the minimum, %d is used", *n, kb.MinUsageStaleDays))
	}
	sort.Strings(info.FromKB)
	return eff, info
}

// setKBField copies one KB-scope field from part onto dst. Copying field by
// field, and not decoding over dst, keeps dst's pointers from being written
// through (yaml decodes into a non-nil pointer in place). The default case
// returns false; TestKBSettingsKeysMatchSchema fails when a key has no case.
func setKBField(dst *KBSpec, part KBSpec, key string) bool {
	switch key {
	case "usage_stale_days":
		dst.UsageStaleDays = part.UsageStaleDays
	case "branch":
		dst.Branch = part.Branch
	default:
		return false
	}
	return true
}
