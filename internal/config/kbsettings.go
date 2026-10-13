package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// OriginKB says a per-KB value comes from the KB's own settings.yaml (D746).
// The KB is authoritative: it beats the server's kbs[] entry, env and flags.
const OriginKB Origin = "kb"

// KBSettingsSchema is the kb scope of Schema as the keys of a settings.yaml:
// top-level names, with the kbs[]. prefix dropped. `kb create` renders the
// commented file from it.
func KBSettingsSchema() settingschema.Schema {
	var s settingschema.Schema
	for _, k := range Schema.Keys {
		name, ok := strings.CutPrefix(k.Path, "kbs[].")
		if !ok || k.Scope != settingschema.ScopeKB {
			continue
		}
		k.Path = name
		k.Env, k.Flag, k.Restart, k.Example = "", "", false, ""
		s.Keys = append(s.Keys, k)
	}
	return s
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

// ResolveKBSettings overlays a KB's settings.yaml on the server's spec for that
// KB (D746). Precedence: the KB file if it sets the key, else the server value,
// else the default. When both set a key and differ the server value is
// ignored and reported in the result; there is no local override. A key the
// file sets that fails a value check the parser cannot make (an unknown
// auto_repair check, a bad mcp_allowlist entry) is left out with a finding.
//
// serverSet says whether the server layers (kbs[], env, flag) set a key
// explicitly: a zero value is not "unset" (D743 origin map).
func ResolveKBSettings(server KBSpec, serverSet func(key string) bool, st kb.SettingsState) (KBSpec, kb.SettingsInfo) {
	info := kb.SettingsInfo{Present: st.Present, SecretsDeclared: server.Secrets}
	for _, m := range st.Malformed {
		info.Findings = append(info.Findings, m.String())
	}
	if !st.Present || st.Unparseable {
		return server, info
	}
	var part KBSpec
	if err := st.Settings.Decode(&part); err != nil {
		info.Findings = append(info.Findings, fmt.Sprintf("%s: %v", kb.SettingsFile, err))
		return server, info
	}
	eff := server
	for _, key := range st.Settings.Keys() {
		if reason := checkKBValue(key, part); reason != "" {
			info.Findings = append(info.Findings, key+": "+reason)
			continue
		}
		if !setKBField(&eff, part, key) {
			info.Findings = append(info.Findings, key+": not applied (no such field)")
			continue
		}
		info.FromKB = append(info.FromKB, key)
		sv, svSet := kbFieldValue(server, key)
		kv, _ := kbFieldValue(part, key)
		if svSet || serverSet != nil && serverSet(key) {
			if !reflect.DeepEqual(sv, kv) {
				info.Ignored = append(info.Ignored, kb.IgnoredSetting{Key: key, ServerValue: fmt.Sprint(sv), KBValue: fmt.Sprint(kv)})
			}
		}
	}
	info.SecretsDeclared = eff.Secrets
	sort.Strings(info.FromKB)
	return eff, info
}

// checkKBValue is the validation that needs config, lint or provisioning,
// which the kb package cannot import.
func checkKBValue(key string, part KBSpec) string {
	switch key {
	case "auto_repair":
		if err := ValidateAutoRepair(part.AutoRepair); err != nil {
			return err.Error()
		}
	case "mcp_allowlist":
		if err := provisioning.ValidateMCPAllowlist(part.MCPAllowlist); err != nil {
			return err.Error()
		}
	case "write_gate":
		if err := ValidateWriteGate(part.WriteGate); err != nil {
			return err.Error()
		}
	case "doctor_mode":
		if err := ValidateDoctorMode(part.DoctorMode); err != nil {
			return err.Error()
		}
	case "doctor_interval":
		if _, err := part.DoctorIntervalDays(); err != nil {
			return err.Error()
		}
	case "doctor_auto_interval":
		if _, err := part.DoctorAutoIntervalDays(); err != nil {
			return err.Error()
		}
	}
	return ""
}

// setKBField copies one KB-scope field from part onto dst. Copying field by
// field, and not decoding over dst, keeps dst's pointers from being written
// through (yaml decodes into a non-nil pointer in place). The default case
// returns false; TestKBSettingsKeysMatchSchema fails when a key has no case.
func setKBField(dst *KBSpec, part KBSpec, key string) bool {
	switch key {
	case "allow_artifact_write":
		dst.AllowArtifactWrite = part.AllowArtifactWrite
	case "allow_template_write":
		dst.AllowTemplateWrite = part.AllowTemplateWrite
	case "write_gate":
		dst.WriteGate = part.WriteGate
	case "auto_repair":
		dst.AutoRepair = part.AutoRepair
		if dst.AutoRepair == nil {
			dst.AutoRepair = []string{} // an explicit empty list means none
		}
	case "repair_on_write":
		dst.RepairOnWrite = part.RepairOnWrite
	case "doctor_mode":
		dst.DoctorMode = part.DoctorMode
	case "doctor_interval":
		dst.DoctorInterval = part.DoctorInterval
	case "doctor_auto_interval":
		dst.DoctorAutoInterval = part.DoctorAutoInterval
	case "doctor_budget":
		dst.DoctorBudget = part.DoctorBudget
	case "usage_stale_days":
		dst.UsageStaleDays = part.UsageStaleDays
	case "mcp_allowlist":
		dst.MCPAllowlist = part.MCPAllowlist
	case "git_profile":
		dst.GitProfile = part.GitProfile
	case "git_base_branch":
		dst.GitBaseBranch = part.GitBaseBranch
	case "git_forge":
		dst.GitForge = part.GitForge
	case "github_owner":
		dst.GitHubOwner = part.GitHubOwner
	case "github_repository":
		dst.GitHubRepository = part.GitHubRepository
	case "secrets":
		dst.Secrets = part.Secrets
	default:
		return false
	}
	return true
}

// ServerSetFunc answers, for the KB mounted under name, whether the server
// layers set a per-KB key explicitly (file, env or flag), from the origin map.
func (c *Config) ServerSetFunc(name string) func(key string) bool {
	return func(key string) bool {
		o, ok := c.Origins[KBOriginKey(name, key)]
		return ok && o != OriginDefault && o != OriginKB
	}
}

// kbFieldValue is the value of the KBSpec field with this YAML key, for
// comparing a server value with a KB's, and whether the spec sets it: a
// non-nil pointer or a non-zero value. A pointer is followed; an unset pointer
// and an empty slice are nil. Marshalling the spec would drop a false or a zero
// (omitempty) and make an explicit one indistinguishable from absent.
func kbFieldValue(s KBSpec, key string) (val any, set bool) {
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		tag := strings.Split(v.Type().Field(i).Tag.Get("yaml"), ",")[0]
		if tag != key {
			continue
		}
		f := v.Field(i)
		if f.Kind() == reflect.Ptr {
			if f.IsNil() {
				return nil, false
			}
			return f.Elem().Interface(), true
		}
		if f.Kind() == reflect.Slice {
			if f.Len() == 0 {
				return nil, !f.IsNil()
			}
			return f.Interface(), true
		}
		return f.Interface(), !f.IsZero()
	}
	return nil, false
}
