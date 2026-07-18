// Package clientconfig reads and writes the client-side `.cartographer.yaml`
// file: the record of which server this machine talks to and which agent
// providers have been connected via `cartographer connect`.
package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

	// Paths is the manual `{{path:<nome>}}` mapping (D75 WP1/WP3): a
	// fallback for directories that aren't git repos, and an override for
	// `{{repo:<key>}}` resolution (checked before repoindex's scan/cache,
	// see repoindex.Resolve). Never written by `cartographer connect`/`sync`
	// — this is purely user-maintained, per-machine.
	Paths map[string]string `yaml:"paths,omitempty"`
}

// yamlConfig mirrors Config for YAML (de)serialization. Trust is a *bool here
// (unlike Config.Trust, a plain bool) so Load can tell an absent `trust` key
// (nil, defaults to true) apart from an explicit `trust: false` written by a
// user who revoked it.
type yamlConfig struct {
	ServerURL   string            `yaml:"server_url"`
	ServerName  string            `yaml:"server_name"`
	Auth        bool              `yaml:"auth"`
	TokenEnv    string            `yaml:"token_env"`
	Agents      []string          `yaml:"agents"`
	KBs         []string          `yaml:"kbs,omitempty"`
	Trust       *bool             `yaml:"trust,omitempty"`
	SearchRoots []string          `yaml:"search_roots,omitempty"`
	Paths       map[string]string `yaml:"paths,omitempty"`
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
// yaml > env > localhost.
func defaultServerURL() string {
	if v := os.Getenv("CARTOGRAPHER_SERVER_URL"); v != "" {
		return v
	}
	return "http://localhost:8080/mcp"
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
	cfg := Config{
		ServerURL:   y.ServerURL,
		ServerName:  y.ServerName,
		Auth:        y.Auth,
		TokenEnv:    y.TokenEnv,
		Agents:      y.Agents,
		KBs:         y.KBs,
		Trust:       true, // absent `trust` key defaults to true, see yamlConfig doc
		SearchRoots: y.SearchRoots,
		Paths:       y.Paths,
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
		ServerURL:   cfg.ServerURL,
		ServerName:  cfg.ServerName,
		Auth:        cfg.Auth,
		TokenEnv:    cfg.TokenEnv,
		Agents:      cfg.Agents,
		KBs:         cfg.KBs,
		Trust:       &cfg.Trust,
		SearchRoots: cfg.SearchRoots,
		Paths:       cfg.Paths,
	}
	data, err := yaml.Marshal(&y)
	if err != nil {
		return fmt.Errorf("clientconfig: marshal: %w", err)
	}
	if err := os.WriteFile(Path(dir), data, 0o644); err != nil {
		return fmt.Errorf("clientconfig: write %s: %w", Path(dir), err)
	}
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
