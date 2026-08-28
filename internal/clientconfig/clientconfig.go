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
	Agents     []string `yaml:"agents"`        // connected provider names, e.g. ["claude", "opencode"]
	KBs        []string `yaml:"kbs,omitempty"` // optional: KB names to sync (empty = server default single-KB endpoint)

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

// yamlConfig mirrors Config for YAML (de)serialization. Trust is a *bool here
// (unlike Config.Trust, a plain bool) so Load can tell an absent `trust` key
// (nil, defaults to true) apart from an explicit `trust: false` written by a
// user who revoked it.
type yamlConfig struct {
	ServerURL    string                            `yaml:"server_url"`
	ServerName   string                            `yaml:"server_name"`
	Auth         bool                              `yaml:"auth"`
	TokenEnv     string                            `yaml:"token_env"`
	Agents       []string                          `yaml:"agents"`
	KBs          []string                          `yaml:"kbs,omitempty"`
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
	for _, key := range []string{"server_url", "server_name", "auth", "token_env", "agents", "kbs", "trust", "search_roots", "paths", "signing_keys", "mcp_approvals"} {
		delete(extra, key)
	}
	cfg := Config{
		ServerURL:    y.ServerURL,
		ServerName:   y.ServerName,
		Auth:         y.Auth,
		TokenEnv:     y.TokenEnv,
		Agents:       y.Agents,
		KBs:          y.KBs,
		Trust:        true, // absent `trust` key defaults to true, see yamlConfig doc
		SearchRoots:  y.SearchRoots,
		SearchDepth:  y.SearchDepth,
		Paths:        y.Paths,
		SigningKeys:  y.SigningKeys,
		MCPApprovals: y.MCPApprovals,
		Extra:        extra,
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
		KBs:          cfg.KBs,
		Trust:        &cfg.Trust,
		SearchRoots:  cfg.SearchRoots,
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
