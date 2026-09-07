// Package clientconfig reads and writes the client-side `.cartographer.yaml`
// file: the record of which server this machine talks to and which agent
// providers have been connected via `cartographer connect`.
package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"gopkg.in/yaml.v3"
)

// FileName is the client config file name, written in the user's home
// directory (see TargetDir).
const FileName = ".cartographer.yaml"

// Config is the on-disk client configuration.
type Config struct {
	ServerURL  string   `yaml:"server_url"`
	ServerName string   `yaml:"server_name"`
	Auth       bool     `yaml:"auth"`
	TokenEnv   string   `yaml:"token_env"`
	Agents     []string `yaml:"agents"` // connected provider names, e.g. ["claude", "opencode"]

	// KnownKBs caches the KB names the server advertised at the last
	// successful connect/sync. It is SERVER-owned: every sync overwrites it
	// wholesale (see cmd/cartographer's runSync). It is not a user
	// preference — that is Clients below. Persisted as `known_kbs`; the
	// legacy `kbs` key written before D169 is still read by Load, and is no
	// longer written by Save.
	KnownKBs []string `yaml:"known_kbs,omitempty"`

	// Clients is the per-provider KB binding (D169): which KBs each connected
	// provider may receive. USER-owned — never written by connect/sync.
	// Always resolve it through BoundKBs, never by reading the map directly:
	// an absent entry, an entry holding an empty list, and an entry holding
	// names are three distinct states, and a nil slice never means "every KB".
	Clients map[string]ClientBinding `yaml:"clients,omitempty"`

	// Trust records the persistent, per-server decision made at connect time:
	// when true, kb:-sourced provisioning artifacts (skill/agent/hook/instructions)
	// are treated as trusted at every sync, without needing the one-time
	// --auto-trust flag (see docs/sync.md §Sicurezza, D54). Defaults to true —
	// both for brand-new configs (Default()) and for existing config files
	// written before this field existed (see yamlConfig.Trust, a *bool, which
	// distinguishes "absent" from an explicit "trust: false").
	Trust bool `yaml:"-"`

	// SearchRoots is where repoindex.Scan looks for local git clones to
	// resolve `{{repo:<key>}}` placeholders (D75 WP1/WP3). "~" is expanded by
	// the consumer (repoindex.expandHome), not here. Defaults to
	// ["~/Documents"] (Default()) and for config files written before this
	// field existed (see Load).
	SearchRoots []string `yaml:"search_roots,omitempty"`

	// SearchDepth bounds how many directory levels repoindex descends from each
	// root (D162). Zero means the default of 4; values above the maximum of 8 are
	// clamped with a warning rather than rejected, since a config value that stops
	// a sync is worse than one adjusted loudly. A workspace organised as
	// <root>/<program>/<area>/<repo> needs 5.
	SearchDepth int `yaml:"search_depth,omitempty"`

	// Paths is the manual `{{path:<nome>}}` mapping (D75 WP1/WP3): a
	// fallback for directories that aren't git repos, and an override for
	// `{{repo:<key>}}` resolution (checked before repoindex's scan/cache,
	// see repoindex.Resolve). Never written by `cartographer connect`/`sync`
	// — this is purely user-maintained, per-machine.
	Paths map[string]string `yaml:"paths,omitempty"`

	// SigningKeys pins Ed25519 public keys by source KB. Pins are configured
	// out of band and are never learned from sync_pull responses.
	SigningKeys map[string][]string `yaml:"signing_keys,omitempty"`
	// MCPApprovals is keyed by source KB then artifact name. Each entry binds
	// consent to one exact descriptor content hash.
	MCPApprovals map[string]map[string]MCPApproval `yaml:"mcp_approvals,omitempty"`
	// Extra retains unknown top-level YAML keys across approval updates.
	Extra map[string]interface{} `yaml:"-"`
}

type MCPApproval struct {
	ContentHash string    `yaml:"content_hash"`
	ApprovedAt  time.Time `yaml:"approved_at"`
}

// ClientBinding is one provider's declared KB set (D169). An entry that exists
// carrying an empty KBs list means "no KBs", not "every KB": returning a
// provider to the default requires deleting the entry (ResetBinding), which is
// why Unbind of the last name leaves the entry in place.
type ClientBinding struct {
	KBs []string `yaml:"kbs"`
}

// yamlConfig mirrors Config for YAML (de)serialization. Trust is a *bool here
// (unlike Config.Trust, a plain bool) so Load can tell an absent `trust` key
// (nil, defaults to true) apart from an explicit `trust: false` written by a
// user who revoked it.
//
// KnownKBs is a *[]string for the same reason (D169): an absent `known_kbs`
// key falls back to the legacy `kbs` alias, while `known_kbs: []` is a
// deliberately empty cache and must win over a stale `kbs` left behind by a
// pre-D169 client. KBs itself is read-only — Save never emits it again, so the
// first write after the upgrade completes the migration.
type yamlConfig struct {
	ServerURL    string                            `yaml:"server_url"`
	ServerName   string                            `yaml:"server_name"`
	Auth         bool                              `yaml:"auth"`
	TokenEnv     string                            `yaml:"token_env"`
	Agents       []string                          `yaml:"agents"`
	KBs          []string                          `yaml:"kbs,omitempty"`
	KnownKBs     *[]string                         `yaml:"known_kbs,omitempty"`
	Clients      map[string]ClientBinding          `yaml:"clients,omitempty"`
	Trust        *bool                             `yaml:"trust,omitempty"`
	SearchRoots  []string                          `yaml:"search_roots,omitempty"`
	SearchDepth  int                               `yaml:"search_depth,omitempty"`
	Paths        map[string]string                 `yaml:"paths,omitempty"`
	SigningKeys  map[string][]string               `yaml:"signing_keys,omitempty"`
	MCPApprovals map[string]map[string]MCPApproval `yaml:"mcp_approvals,omitempty"`
}

// Default returns a Config with the same defaults as configurator.DefaultConfig.
func Default() *Config {
	return &Config{
		ServerURL:   defaultServerURL(),
		ServerName:  "cartographer",
		Auth:        false,
		TokenEnv:    "CARTOGRAPHER_TOKENS",
		Agents:      []string{},
		Trust:       true,
		SearchRoots: []string{"~/Documents"},
	}
}

// defaultServerURL is Default()'s ServerURL: an existing .cartographer.yaml
// (see Load) always wins over this. With no config file yet — a brand-new
// machine, or right after `disconnect` zeroed out agents but kept the file
// (see doDisconnect) — CARTOGRAPHER_SERVER_URL, if set, seeds the connect
// form/CLI default instead of the hardcoded localhost (D64): precedence is
// yaml > env > local default.
func defaultServerURL() string {
	if v := os.Getenv("CARTOGRAPHER_SERVER_URL"); v != "" {
		return v
	}
	return defaults.DefaultMCPURL
}

// Path returns the full path to the client config file inside dir.
func Path(dir string) string {
	return filepath.Join(dir, FileName)
}

// Load reads the client config from dir. Returns (nil, os.ErrNotExist) if the
// file does not exist — callers that want a fresh config should fall back to
// Default() in that case.
func Load(dir string) (*Config, error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("clientconfig: read %s: %w", Path(dir), err)
	}
	var y yamlConfig
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("clientconfig: parse %s: %w", Path(dir), err)
	}
	var extra map[string]interface{}
	if err := yaml.Unmarshal(data, &extra); err != nil {
		return nil, fmt.Errorf("clientconfig: parse extras %s: %w", Path(dir), err)
	}
	for _, key := range []string{"server_url", "server_name", "auth", "token_env", "agents", "kbs", "known_kbs", "clients", "trust", "search_roots", "search_depth", "paths", "signing_keys", "mcp_approvals"} {
		delete(extra, key)
	}
	cfg := Config{
		ServerURL:    y.ServerURL,
		ServerName:   y.ServerName,
		Auth:         y.Auth,
		TokenEnv:     y.TokenEnv,
		Agents:       y.Agents,
		KnownKBs:     y.KBs, // legacy alias; overridden below when known_kbs is present
		Clients:      y.Clients,
		Trust:        true, // absent `trust` key defaults to true, see yamlConfig doc
		SearchRoots:  y.SearchRoots,
		SearchDepth:  y.SearchDepth,
		Paths:        y.Paths,
		SigningKeys:  y.SigningKeys,
		MCPApprovals: y.MCPApprovals,
		Extra:        extra,
	}
	if y.KnownKBs != nil {
		// Present — including present and empty — always wins over the legacy
		// `kbs` alias (D169).
		cfg.KnownKBs = *y.KnownKBs
	}
	if y.Trust != nil {
		cfg.Trust = *y.Trust
	}
	if len(cfg.SearchRoots) == 0 {
		// Absent `search_roots` key: both brand-new configs and files written
		// before this field existed default here, matching Default().
		cfg.SearchRoots = []string{"~/Documents"}
	}
	return &cfg, nil
}

// Save writes cfg to dir, creating the directory if necessary.
func Save(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("clientconfig: mkdir %s: %w", dir, err)
	}
	y := yamlConfig{
		ServerURL:    cfg.ServerURL,
		ServerName:   cfg.ServerName,
		Auth:         cfg.Auth,
		TokenEnv:     cfg.TokenEnv,
		Agents:       cfg.Agents,
		KnownKBs:     &cfg.KnownKBs, // always emitted; the legacy `kbs` key is not written again (D169)
		Clients:      cfg.Clients,
		Trust:        &cfg.Trust,
		SearchRoots:  cfg.SearchRoots,
		SearchDepth:  cfg.SearchDepth,
		Paths:        cfg.Paths,
		SigningKeys:  cfg.SigningKeys,
		MCPApprovals: cfg.MCPApprovals,
	}
	data, err := yaml.Marshal(&y)
	if err != nil {
		return fmt.Errorf("clientconfig: marshal: %w", err)
	}
	var merged map[string]interface{}
	if err := yaml.Unmarshal(data, &merged); err != nil {
		return fmt.Errorf("clientconfig: encode fields: %w", err)
	}
	for key, value := range cfg.Extra {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	data, err = yaml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("clientconfig: marshal merged: %w", err)
	}
	tmp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("clientconfig: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("clientconfig: write temp: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("clientconfig: chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("clientconfig: close temp: %w", err)
	}
	if err := os.Rename(tmpName, Path(dir)); err != nil {
		return fmt.Errorf("clientconfig: replace %s: %w", Path(dir), err)
	}
	return nil
}

// ApproveMCP records the exact descriptor hash for one source/name pair.
func (c *Config) ApproveMCP(sourceKB, name, hash string, at time.Time) error {
	if sourceKB == "" || name == "" || hash == "" {
		return fmt.Errorf("clientconfig: invalid MCP approval")
	}
	if c.MCPApprovals == nil {
		c.MCPApprovals = make(map[string]map[string]MCPApproval)
	}
	if c.MCPApprovals[sourceKB] == nil {
		c.MCPApprovals[sourceKB] = make(map[string]MCPApproval)
	}
	c.MCPApprovals[sourceKB][name] = MCPApproval{ContentHash: hash, ApprovedAt: at.UTC()}
	return nil
}

// RevokeMCP removes an approval. Missing records are intentionally a no-op.
func (c *Config) RevokeMCP(sourceKB, name string) {
	if c.MCPApprovals == nil {
		return
	}
	delete(c.MCPApprovals[sourceKB], name)
	if len(c.MCPApprovals[sourceKB]) == 0 {
		delete(c.MCPApprovals, sourceKB)
	}
}

// ApprovedMCPHashes returns ApplyOptions' compact source/name → hash map.
func (c *Config) ApprovedMCPHashes() map[string]string {
	out := make(map[string]string)
	for source, entries := range c.MCPApprovals {
		for name, approval := range entries {
			out["kb:"+source+"\x00"+name] = approval.ContentHash
		}
	}
	return out
}

// AddSigningKey pins key for kbName unless it is already present.
func (c *Config) AddSigningKey(kbName, key string) error {
	if kbName == "" || kbName != strings.TrimSpace(kbName) || key == "" || key != strings.TrimSpace(key) {
		return fmt.Errorf("clientconfig: invalid signing key pin")
	}
	if _, err := artifactsig.ParsePublicKey(key); err != nil {
		return fmt.Errorf("clientconfig: invalid signing key pin: %w", err)
	}
	if c.SigningKeys == nil {
		c.SigningKeys = make(map[string][]string)
	}
	for _, existing := range c.SigningKeys[kbName] {
		if existing == key {
			return nil
		}
	}
	c.SigningKeys[kbName] = append(c.SigningKeys[kbName], key)
	return nil
}

// HasAgent reports whether name is already listed in cfg.Agents.
func (c *Config) HasAgent(name string) bool {
	for _, a := range c.Agents {
		if a == name {
			return true
		}
	}
	return false
}

// AddAgent appends name to cfg.Agents if not already present.
func (c *Config) AddAgent(name string) {
	if !c.HasAgent(name) {
		c.Agents = append(c.Agents, name)
	}
}

// BoundKBs resolves which KBs a provider may receive (D169), and reports
// whether that answer comes from an explicit binding or from the default.
//
// This is the ONLY place the default is resolved: no caller may re-derive it
// by testing Clients or a returned slice for emptiness, because an explicit
// binding holding no KBs and an absent binding return the same empty slice and
// mean opposite things. A provider with no entry receives every known KB —
// today's behaviour, so an upgrade never strips artifacts from an already
// connected client; default-deny is what declaring an entry buys.
//
// The returned slice is a copy: mutating it cannot corrupt the config.
func (c *Config) BoundKBs(provider string) (kbs []string, explicit bool) {
	if binding, ok := c.Clients[provider]; ok {
		return append([]string(nil), binding.KBs...), true
	}
	return append([]string(nil), c.KnownKBs...), false
}

// Bind adds kb to provider's binding, creating the binding if the provider had
// none — which converts it from "receives every known KB" to "receives only
// this one". Callers must say so in their output. Adding a KB that is already
// bound is a no-op.
//
// A kb absent from KnownKBs is deliberately NOT an error: the KB may be mounted
// later, and configuring must not require a reachable server. Callers that can
// check report it as a warning.
func (c *Config) Bind(provider, kb string) error {
	if provider == "" || provider != strings.TrimSpace(provider) {
		return fmt.Errorf("clientconfig: invalid provider name %q", provider)
	}
	if kb == "" || kb != strings.TrimSpace(kb) {
		return fmt.Errorf("clientconfig: invalid KB name %q", kb)
	}
	if c.Clients == nil {
		c.Clients = make(map[string]ClientBinding)
	}
	binding := c.Clients[provider]
	for _, existing := range binding.KBs {
		if existing == kb {
			return nil
		}
	}
	binding.KBs = append(binding.KBs, kb)
	c.Clients[provider] = binding
	return nil
}

// Unbind removes kb from provider's binding. Removing the last name leaves the
// entry in place holding an empty list — "no KBs" — because deleting it would
// silently restore "receives every known KB". ResetBinding is the explicit way
// back. Unbinding a pair that is not bound is a silent no-op, matching
// RevokeMCP.
func (c *Config) Unbind(provider, kb string) error {
	if provider == "" || kb == "" {
		return fmt.Errorf("clientconfig: invalid unbind (provider %q, kb %q)", provider, kb)
	}
	binding, ok := c.Clients[provider]
	if !ok {
		return nil
	}
	kept := make([]string, 0, len(binding.KBs))
	for _, existing := range binding.KBs {
		if existing != kb {
			kept = append(kept, existing)
		}
	}
	binding.KBs = kept
	c.Clients[provider] = binding
	return nil
}

// ResetBinding deletes provider's binding, returning it to the default (every
// known KB). Missing entries are a no-op.
func (c *Config) ResetBinding(provider string) {
	delete(c.Clients, provider)
	if len(c.Clients) == 0 {
		c.Clients = nil
	}
}

// TargetDir returns the directory where the client config (and generated
// provider configs) should be written: always the user's home directory.
// A single machine-wide connection avoids drift between per-project configs
// (e.g. a provider connected in one repo but not another).
func TargetDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("clientconfig: resolve home dir: %w", err)
	}
	return home, nil
}
