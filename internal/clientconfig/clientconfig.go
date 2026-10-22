// Package clientconfig reads and writes the client-side `.cartographer.yaml`
// file: the record of which server this machine talks to and which agent
// providers have been connected via `cartographer connect`.
package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"gopkg.in/yaml.v3"
)

// FileName is the client config file name, written in the user's home
// directory (see TargetDir).
const FileName = ".cartographer.yaml"

// Config is the on-disk client configuration.
type Config struct {
	ServerURL  string `yaml:"server_url"`
	ServerName string `yaml:"server_name"`
	Auth       bool   `yaml:"auth"`
	// Token is the bearer token the clients send: a literal, or "${NAME}" to
	// read it from the environment variable NAME (D906). Use TokenEnv and
	// LiteralToken, never the raw string.
	Token string `yaml:"token"`
	// Agents are the connected provider names, e.g. ["claude", "opencode"]. A
	// machine-written record, kept in the state file (D906), not in the YAML.
	Agents []string `yaml:"-"`

	// KnownKBs caches the KB names the server advertised at the last
	// successful connect/sync. It is SERVER-owned: every sync overwrites it
	// wholesale (see cmd/cartographer's runSync). It is not a user
	// preference — that is Clients below. Persisted as `known_kbs`; the
	// legacy `kbs` key written before D169 is still read by Load, and is no
	// longer written by Save.
	KnownKBs []string `yaml:"known_kbs,omitempty"`

	// ServerMountMode records the mount topology the server advertised at the
	// last successful connect/sync (D187): "routed" when it serves every KB
	// through one endpoint with the KB as a tool argument, empty for the
	// historical one-endpoint-per-KB topology. SERVER-owned, like KnownKBs.
	// ServerRoutedPath is that endpoint's path as the *server* named it, so the
	// client never assumes it. Both are what lets `doctor` and `status` derive
	// the expected MCP entries without reaching the network, and what lets
	// `status` report a mode change rather than silently healing it.
	ServerMountMode  string `yaml:"server_mount_mode,omitempty"`
	ServerRoutedPath string `yaml:"server_routed_path,omitempty"`

	// Clients is the per-provider KB binding (D169): which KBs each connected
	// provider may receive. USER-owned — never written by connect/sync.
	// Always resolve it through BoundKBs, never by reading the map directly:
	// an absent entry, an entry holding an empty list, and an entry holding
	// names are three distinct states, and a nil slice never means "every KB".
	Clients map[string]ClientBinding `yaml:"clients,omitempty"`

	// Scopes is the per-provider projection scope (D193): absent or "provider"
	// means the historical global catalogue under $HOME, "workspace" means each
	// bound workspace receives its own KBs in its own project-local
	// directories and nothing KB-sourced is written globally. USER-owned.
	// Always read it through WorkspaceScope: an absent entry is the default,
	// and the default must never change under an existing installation.
	Scopes map[string]string `yaml:"scopes,omitempty"`

	// Workspaces is the per-provider workspace↔KB binding (D193): which KBs a
	// provider receives while working in a given directory. USER-owned. Read it
	// through WorkspaceBindings/ResolveWorkspace, never directly: an unbound
	// workspace is fail-closed (it gets only the transversal bundle) and a
	// bound one holding no KBs is an explicit declaration — two states an
	// emptiness test cannot tell apart.
	Workspaces map[string][]WorkspaceBinding `yaml:"workspaces,omitempty"`

	// derived is the bindings the KBs' paths.yaml imply (D1019): recomputed by
	// every sync, stored apart from this file (derived.go) and never written
	// to it. Read it through WorkspaceBindings/ResolveWorkspace.
	derived DerivedState

	// SearchRoots is where repoindex.Scan looks for local git clones to
	// resolve `{{repo:<key>}}` placeholders (D75 WP1/WP3). "~" is expanded by
	// the consumer (repoindex.expandHome), not here. Defaults to
	// DefaultSearchRoots (Default()) and for config files written before this
	// field existed (see Load); consulted only for a key with no `paths` entry.
	SearchRoots []string `yaml:"search_roots,omitempty"`

	// Paths is the manual `{{path:<nome>}}` mapping (D75 WP1/WP3): a
	// fallback for directories that aren't git repos, and an override for
	// `{{repo:<key>}}` resolution (checked before repoindex's scan/cache,
	// see repoindex.Resolve). Per-machine and user-owned: written only on
	// the operator's explicit answer — `cartographer paths set/unset`, or the
	// placeholder step of an interactive `connect` (D262) — never by `sync`.
	// Keys are stored without the "repo:"/"path:" prefix.
	Paths map[string]string `yaml:"paths,omitempty"`

	// IgnoredPaths are the placeholder ids ("repo:<key>" / "path:<key>", with
	// the prefix) the operator marked as deliberately absent on this machine
	// (D282, `cartographer paths ignore`). Resolution is untouched — an ignored
	// key that does resolve still does — only the reporting changes: the sync
	// warning, `status` and the connect step stop naming it, and `paths list`
	// shows it as ignored. Same authorship rule as Paths: written only on the
	// operator's explicit answer, never by `sync`.
	IgnoredPaths []string `yaml:"ignored_paths,omitempty"`

	// SigningKeys pins Ed25519 public keys by source KB. Pins are configured
	// out of band and are never learned from sync_pull responses.
	SigningKeys map[string][]string `yaml:"signing_keys,omitempty"`
	// Identities names the git author of this machine's writes, as
	// "Name <email>", by KB name or under IdentityDefault (D904); see Identity.
	// User-owned: never written by a command.
	Identities map[string]string `yaml:"identities,omitempty"`
	// MCPApprovals is keyed by source KB then artifact name. Each entry binds
	// consent to one exact descriptor content hash.
	MCPApprovals map[string]map[string]MCPApproval `yaml:"mcp_approvals,omitempty"`
	// UsageScan is the opt-out of the session-transcript scan that tells the
	// server which skills and agents this machine actually loads (D326).
	// Defaults to true — brand-new configs (Default()) and files written
	// before this field existed — and only an explicit `usage_scan: false`
	// turns it off, which is why yamlConfig holds it as a *bool. Off, sync
	// reads no transcript and reports nothing; the server degrades to "no
	// data". USER-owned: sync never writes it.
	UsageScan bool `yaml:"-"`

	// SyncTimerOptOut remembers that the operator ran `service sync-timer
	// uninstall` (D325): connect and setup then stop installing the timer
	// themselves. Cleared by an explicit `sync-timer install`. A removal
	// because no client needs the timer any more does not set it. Kept in the
	// state file with Agents (D906).
	SyncTimerOptOut bool `yaml:"-"`
	// UpdateCheckOff is the `update_check: false` opt-out of the daily release
	// lookup (D254, D906). Held as the negation so a Config built from the zero
	// value checks, as the default is. Use UpdateCheckEnabled.
	UpdateCheckOff bool `yaml:"-"`
	// AllowInsecureHTTP is the persisted opt-in to a non-loopback http://
	// server_url (D1023). Use InsecureHTTPAllowed, which also honours the env var.
	AllowInsecureHTTP bool `yaml:"-"`
	// Extra retains unknown top-level YAML keys across approval updates.
	Extra map[string]interface{} `yaml:"-"`
	// Unsupported lists, sorted, the provider names Load found in the file and
	// dropped because the registry no longer knows them (D898). They are gone
	// from Agents, Clients, Scopes and Workspaces in memory, so no caller can
	// resolve one to a directory; the next Save writes the file without them.
	// Not persisted. Reported once per run through the commands that print it.
	Unsupported []string `yaml:"-"`
	// Warnings are the removed keys Load found in the file (Retired, D903): each
	// is ignored, and the line names what to use instead; and the identities
	// entries that are not a valid "Name <email>", which send no author. Not
	// persisted; `sync` prints them, and its realignment (RealignFile) drops the
	// removed keys.
	Warnings []string `yaml:"-"`
}

type MCPApproval struct {
	ContentHash string    `yaml:"content_hash"`
	ApprovedAt  time.Time `yaml:"approved_at"`
}

// ClientBinding is one provider's per-client declaration. An entry carrying an
// explicit `kbs` key is the provider's KB set (D169): an empty list means "no
// KBs", not "every KB", and returning a provider to the default requires
// deleting the entry (ResetBinding), which is why Unbind of the last name leaves
// the entry in place.
//
// Only the `kbs` key makes a binding explicit (KBsSet). Any other key an entry
// may carry, config_dirs today (D755), leaves the provider on the default KB
// set: otherwise declaring a config dir would silently mean "no KBs".
type ClientBinding struct {
	KBs []string
	// KBsSet records that the file carried a `kbs` key, even an empty one. A
	// binding built in code with a non-empty KBs is explicit too (see Explicit).
	KBsSet bool
	// ConfigDirs lists extra Claude Code config directories (each holding its
	// own .claude.json) that sync writes the MCP entry to, besides $HOME. Only
	// valid under clients.claude (D755).
	ConfigDirs []string
}

// Explicit reports whether the binding declares a KB set.
func (b ClientBinding) Explicit() bool { return b.KBsSet || len(b.KBs) > 0 }

type clientBindingYAML struct {
	KBs        []string `yaml:"kbs"`
	ConfigDirs []string `yaml:"config_dirs,omitempty"`
}

// UnmarshalYAML tells an absent `kbs` key from an empty one.
func (b *ClientBinding) UnmarshalYAML(node *yaml.Node) error {
	var raw clientBindingYAML
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*b = ClientBinding{KBs: raw.KBs, ConfigDirs: raw.ConfigDirs}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "kbs" {
				b.KBsSet = true
			}
		}
	}
	return nil
}

// MarshalYAML emits `kbs` only for an explicit binding, so a config_dirs-only
// entry round-trips without gaining a default-deny key.
func (b ClientBinding) MarshalYAML() (interface{}, error) {
	out := map[string]interface{}{}
	if b.Explicit() {
		kbs := b.KBs
		if kbs == nil {
			kbs = []string{}
		}
		out["kbs"] = kbs
	}
	if len(b.ConfigDirs) > 0 {
		out["config_dirs"] = b.ConfigDirs
	}
	return out, nil
}

// yamlConfig mirrors Config for YAML (de)serialization. UsageScan and
// UpdateCheck are *bool so Load can tell an absent key (the default, true)
// apart from an explicit `false`.
//
// It holds the user-owned keys only: the server-owned caches and the
// machine-written lists (agents, sync_timer_opt_out) live in the state file
// (D747, D906) and are read from the YAML solely through legacyCaches.
type yamlConfig struct {
	ServerURL    string                            `yaml:"server_url"`
	ServerName   string                            `yaml:"server_name"`
	Auth         bool                              `yaml:"auth"`
	Token        string                            `yaml:"token"`
	Clients      map[string]ClientBinding          `yaml:"clients,omitempty"`
	Scopes       map[string]string                 `yaml:"scopes,omitempty"`
	Workspaces   map[string][]WorkspaceBinding     `yaml:"workspaces,omitempty"`
	SearchRoots  []string                          `yaml:"search_roots,omitempty"`
	Paths        map[string]string                 `yaml:"paths,omitempty"`
	IgnoredPaths []string                          `yaml:"ignored_paths,omitempty"`
	SigningKeys  map[string][]string               `yaml:"signing_keys,omitempty"`
	Identities   map[string]string                 `yaml:"identities,omitempty"`
	MCPApprovals map[string]map[string]MCPApproval `yaml:"mcp_approvals,omitempty"`
	UpdateCheck  *bool                             `yaml:"update_check,omitempty"`
	UsageScan    *bool                             `yaml:"usage_scan,omitempty"`

	AllowInsecureHTTP bool `yaml:"allow_insecure_http,omitempty"`
}

// legacyCaches are the keys older versions kept in .cartographer.yaml and that
// now live in the state file (D747). Load reads them only when there is no state
// file. KnownKBs is a *[]string (D169): an absent `known_kbs` key falls back to
// the legacy `kbs` alias, while `known_kbs: []` is a deliberately empty cache and
// must win over a stale `kbs` left behind by a pre-D169 client.
type legacyCaches struct {
	KBs              []string  `yaml:"kbs"`
	KnownKBs         *[]string `yaml:"known_kbs"`
	ServerMountMode  string    `yaml:"server_mount_mode"`
	ServerRoutedPath string    `yaml:"server_routed_path"`
	// The keys D906 took out of the YAML, still honoured on read so a file not
	// yet realigned (a read-only mount) keeps working: agents and
	// sync_timer_opt_out until the state file holds them, token_env and
	// update.check as the keys that replaced them.
	Agents          *[]string `yaml:"agents"`
	SyncTimerOptOut bool      `yaml:"sync_timer_opt_out"`
	TokenEnv        string    `yaml:"token_env"`
	Update          struct {
		Check *bool `yaml:"check"`
	} `yaml:"update"`
}

// InsecureHTTPAllowed reports whether a non-loopback http:// server_url is
// accepted: the allow_insecure_http key, or CARTOGRAPHER_ALLOW_INSECURE_HTTP
// set to 1 or true (D1023). A nil Config is not allowed.
func (c *Config) InsecureHTTPAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP"))) {
	case "1", "true":
		return true
	}
	return c != nil && c.AllowInsecureHTTP
}

// UpdateCheckEnabled reports whether the daily release lookup runs: true unless
// `update_check: false` (D254, D906).
func (c *Config) UpdateCheckEnabled() bool { return c == nil || !c.UpdateCheckOff }

// DefaultTokenEnv is the environment variable the bearer token is read from by
// default, and the one a literal `token` has the agents' MCP entries name.
const DefaultTokenEnv = "CARTOGRAPHER_TOKENS"

// DefaultSearchRoots are the directories scanned for git clones when
// `search_roots` is absent (D906): the usual places a person keeps checkouts.
// A root that does not exist is skipped by the scan. They are consulted only
// for a {{repo:<key>}} with no `paths` entry.
var DefaultSearchRoots = []string{"~/Documents", "~/src", "~/code", "~/projects"}

// ScanRoots is SearchRoots as the scan should walk them. A file that sets
// search_roots is taken as written, so a root that is missing is reported by the
// scan. The defaults are guesses: those that do not exist on this machine are
// left out silently, or every sync would warn about three directories nobody
// asked for (D906).
func (c *Config) ScanRoots() []string {
	if !slices.Equal(c.SearchRoots, DefaultSearchRoots) {
		return c.SearchRoots
	}
	home, _ := os.UserHomeDir()
	var out []string
	for _, r := range c.SearchRoots {
		p := r
		if rest, ok := strings.CutPrefix(r, "~/"); ok && home != "" {
			p = filepath.Join(home, rest)
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, r)
		}
	}
	return out
}

var tokenEnvRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// EnvRefToken is the token value that reads the environment variable name.
func EnvRefToken(name string) string { return "${" + name + "}" }

// IsEnvRef reports whether v is a `${NAME}` reference rather than a literal.
func IsEnvRef(v string) bool { return tokenEnvRef.MatchString(strings.TrimSpace(v)) }

// TokenEnv is the name of the environment variable the clients read the token
// from: the NAME of a `${NAME}` token, DefaultTokenEnv for a literal token
// (the agents' MCP entries can only reference a variable, never carry the
// secret), and "" when no token is configured.
func (c *Config) TokenEnv() string {
	t := strings.TrimSpace(c.Token)
	if t == "" {
		return ""
	}
	if m := tokenEnvRef.FindStringSubmatch(t); m != nil {
		return m[1]
	}
	return DefaultTokenEnv
}

// LiteralToken is the token written in the file, "" when the token is absent
// or a `${NAME}` reference.
func (c *Config) LiteralToken() string {
	t := strings.TrimSpace(c.Token)
	if t == "" || tokenEnvRef.MatchString(t) {
		return ""
	}
	return t
}

// Default returns a Config with the same defaults as configurator.DefaultConfig.
func Default() *Config {
	return &Config{
		ServerURL:   defaultServerURL(),
		ServerName:  "cartographer",
		Auth:        false,
		Token:       EnvRefToken(DefaultTokenEnv),
		Agents:      []string{},
		UsageScan:   true,
		SearchRoots: append([]string(nil), DefaultSearchRoots...),
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
	for provider, b := range y.Clients {
		if len(b.ConfigDirs) > 0 && provider != "claude" {
			return nil, fmt.Errorf("clientconfig: %s: clients.%s.config_dirs is only valid under clients.claude", Path(dir), provider)
		}
	}
	var extra map[string]interface{}
	if err := yaml.Unmarshal(data, &extra); err != nil {
		return nil, fmt.Errorf("clientconfig: parse extras %s: %w", Path(dir), err)
	}
	for _, key := range yamlKeys() {
		delete(extra, key)
	}
	// Keys that are no longer user settings (the caches, D747; doctor_timer_opt_out,
	// D704): not extras, so a file written by an older version drops them on the
	// next save instead of carrying them forever.
	for key := range droppedKeys {
		delete(extra, key)
	}
	var yamlDoc yaml.Node
	_ = yaml.Unmarshal(data, &yamlDoc) // parsed above: it cannot fail here
	var legacy legacyCaches
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("clientconfig: parse caches %s: %w", Path(dir), err)
	}
	cfg := Config{
		// The pre-#329 default is migrated on read, so the next connect or
		// sync writes the working URL into every client's MCP config.
		ServerURL:         defaults.MigrateLegacyMCPURL(y.ServerURL),
		ServerName:        y.ServerName,
		Auth:              y.Auth,
		Token:             y.Token,
		Clients:           y.Clients,
		Scopes:            y.Scopes,
		Workspaces:        y.Workspaces,
		AllowInsecureHTTP: y.AllowInsecureHTTP,
		UsageScan:         true, // absent `usage_scan` key defaults to true

		SearchRoots:  y.SearchRoots,
		Paths:        y.Paths,
		IgnoredPaths: y.IgnoredPaths,
		SigningKeys:  y.SigningKeys,
		Identities:   y.Identities,
		MCPApprovals: y.MCPApprovals,
		Extra:        extra,
	}
	// token_env (removed, D906) is honoured as the token it became until the
	// file is realigned, so a read-only file does not lose its credentials.
	if cfg.Token == "" && tokenEnvRef.MatchString(EnvRefToken(legacy.TokenEnv)) {
		cfg.Token = EnvRefToken(legacy.TokenEnv)
	} else if legacy.TokenEnv != "" && cfg.Token != EnvRefToken(legacy.TokenEnv) {
		// Never lost in silence: token wins, or the name cannot be a variable.
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("token_env is ignored: token is already %s (or its value is not an environment variable name); set token: ${NAME} to read a variable", tokenLabel(cfg.Token)))
	}
	if cfg.LiteralToken() != "" && !fileIsPrivate(Path(dir)) {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s holds a literal token and is readable by other users: run `chmod 600 %s`", Path(dir), Path(dir)))
	}
	if state, ok := loadState(dir); ok {
		cfg.KnownKBs = state.KnownKBs
		cfg.ServerMountMode = state.ServerMountMode
		cfg.ServerRoutedPath = state.ServerRoutedPath
		cfg.SyncTimerOptOut = state.SyncTimerOptOut
		if state.Agents != nil {
			cfg.Agents = *state.Agents
		}
	} else {
		// No state file yet: an installation from before D747 keeps its caches
		// in the YAML. The next Save moves them.
		cfg.KnownKBs = legacy.KBs
		if legacy.KnownKBs != nil {
			// Present — including present and empty — always wins over the
			// legacy `kbs` alias (D169).
			cfg.KnownKBs = *legacy.KnownKBs
		}
		cfg.ServerMountMode = legacy.ServerMountMode
		cfg.ServerRoutedPath = legacy.ServerRoutedPath
	}
	// agents and sync_timer_opt_out moved to the state file (D906); a state file
	// from before that, or none, leaves the YAML's values to be read.
	if cfg.Agents == nil && legacy.Agents != nil {
		cfg.Agents = *legacy.Agents
	}
	cfg.SyncTimerOptOut = cfg.SyncTimerOptOut || legacy.SyncTimerOptOut
	if y.UsageScan != nil {
		cfg.UsageScan = *y.UsageScan
	}
	// update.check (removed, D906) is honoured until the file is realigned;
	// update.policy is not: a client never replaces itself.
	if y.UpdateCheck != nil {
		cfg.UpdateCheckOff = !*y.UpdateCheck
	} else if legacy.Update.Check != nil {
		cfg.UpdateCheckOff = !*legacy.Update.Check
	}
	cfg.dropUnsupportedProviders()
	for _, h := range Retired.Find(&yamlDoc) {
		cfg.Warnings = append(cfg.Warnings, h.Message())
	}
	cfg.Warnings = append(cfg.Warnings, cfg.identityWarnings()...)
	if len(cfg.SearchRoots) == 0 {
		// Absent `search_roots` key: both brand-new configs and files written
		// before this field existed default here, matching Default() (D906).
		cfg.SearchRoots = append([]string(nil), DefaultSearchRoots...)
	}
	cfg.derived = LoadDerived(dir)
	return &cfg, nil
}

// Save writes cfg to dir, creating the directory if necessary.
//
// The user file is a yaml.Node round-trip of the existing one, not a marshal of
// a map: a map has no room for comments, so the previous implementation dropped
// every comment a person wrote on every save. Here the typed keys are set in
// place, unknown keys and comments stay where they are, and the settings schema
// then documents what is still undocumented (D747). A save that changes no
// setting leaves the file untouched. The server-owned caches go to the state
// file instead, so a sync does not rewrite the file a person edits.
func Save(dir string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("clientconfig: mkdir %s: %w", dir, err)
	}
	y := yamlConfig{
		ServerURL:    cfg.ServerURL,
		ServerName:   cfg.ServerName,
		Auth:         cfg.Auth,
		Token:        cfg.Token,
		Clients:      cfg.Clients,
		Scopes:       cfg.Scopes,
		Workspaces:   cfg.Workspaces,
		Paths:        cfg.Paths,
		IgnoredPaths: cfg.IgnoredPaths,
		SigningKeys:  cfg.SigningKeys,
		Identities:   cfg.Identities,
		MCPApprovals: cfg.MCPApprovals,

		AllowInsecureHTTP: cfg.AllowInsecureHTTP,
	}
	// The defaults are not written: a file that never set search_roots keeps
	// getting whatever the defaults are (D906).
	if !slices.Equal(cfg.SearchRoots, DefaultSearchRoots) {
		y.SearchRoots = cfg.SearchRoots
	}
	// Emitted only when off: a default machine's file stays free of them.
	if !cfg.UsageScan {
		off := false
		y.UsageScan = &off
	}
	if cfg.UpdateCheckOff {
		off := false
		y.UpdateCheck = &off
	}
	existing, err := os.ReadFile(Path(dir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clientconfig: read %s: %w", Path(dir), err)
	}
	doc, err := buildDocument(existing, &y, cfg.Extra)
	if err != nil {
		return err
	}
	// The state file first: agents and sync_timer_opt_out leave the YAML in this
	// same save (D906), so a failure here must not cost the values.
	if err := writeState(dir, cfg); err != nil {
		return err
	}
	return writeYAML(dir, doc, cfg.LiteralToken() != "")
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
	// Presence of the `kbs` key decides, not presence of the entry: an entry
	// with only config_dirs (D755) is not a binding.
	if binding, ok := c.Clients[provider]; ok && binding.Explicit() {
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
	binding.KBsSet = true
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
	if !ok || !binding.Explicit() {
		return nil
	}
	kept := make([]string, 0, len(binding.KBs))
	for _, existing := range binding.KBs {
		if existing != kb {
			kept = append(kept, existing)
		}
	}
	binding.KBs = kept
	binding.KBsSet = true
	c.Clients[provider] = binding
	return nil
}

// ResetBinding deletes provider's binding, returning it to the default (every
// known KB). Missing entries are a no-op.
func (c *Config) ResetBinding(provider string) {
	// config_dirs is not part of the KB binding (D755): keep it.
	if b, ok := c.Clients[provider]; ok && len(b.ConfigDirs) > 0 {
		c.Clients[provider] = ClientBinding{ConfigDirs: b.ConfigDirs}
		return
	}
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

// dropUnsupportedProviders removes every provider name the registry no longer
// knows from the agents list, the per-provider bindings, scopes and workspace
// bindings, and records the names in Unsupported (D898).
//
// A name the registry cannot resolve must never reach code that turns it into a
// directory or a prune: a removed client leaves configurations naming it, and the generic answer is to skip it, not to guess. It
// is a rule about the registry, not about one provider, so the next removal is
// covered by the same path. No file is touched here.
func (c *Config) dropUnsupportedProviders() {
	known := func(name string) bool {
		_, ok := configurator.Lookup(configurator.Provider(name))
		return ok
	}
	seen := map[string]bool{}
	kept := c.Agents[:0:0]
	for _, a := range c.Agents {
		if known(a) {
			kept = append(kept, a)
		} else {
			seen[a] = true
		}
	}
	c.Agents = kept
	for name := range c.Clients {
		if !known(name) {
			seen[name] = true
			delete(c.Clients, name)
		}
	}
	for name := range c.Scopes {
		if !known(name) {
			seen[name] = true
			delete(c.Scopes, name)
		}
	}
	for name := range c.Workspaces {
		if !known(name) {
			seen[name] = true
			delete(c.Workspaces, name)
		}
	}
	for name := range seen {
		c.Unsupported = append(c.Unsupported, name)
	}
	sort.Strings(c.Unsupported)
}
