// Package config provides the YAML-based server configuration for
// `cartographer serve`, merged with environment variables and CLI flags.
// Precedence (highest first): CLI flag > environment variable > YAML file > default.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the fully resolved server configuration.
type Config struct {
	HTTP   string       // listen address; empty = stdio transport
	Init   bool         // initialize missing KBs
	Auth   AuthConfig   // HTTP bearer-token auth
	Data   string       // directory whose direct subdirs are auto-discovered KBs
	KBs    []KBSpec     // explicit KBs (local path or git remote)
	Git    GitConfig    // per-KB git autocommit/sync + SSH identity for remotes
	Search SearchConfig // Ollama-backed semantic search
	Audit  AuditConfig  // append-only audit log
	Sops   SopsConfig   // default SOPS age key for secret refs
	// ToolsProfile selects which tools tools/list advertises: "agent"
	// (default) hides the advanced/operator tools, "full" advertises all.
	// Hidden tools stay callable via tools/call (D65).
	ToolsProfile string
}

// AuthConfig controls HTTP bearer-token authentication.
type AuthConfig struct {
	Mode   string // "auto" | "on" | "off"
	Tokens []TokenSpec
}

// TokenStrings returns the bare token values, discarding scopes. Kept for
// callers that only need the flat token list (e.g. tests, or an
// auth.NewTokenStore full-access setup); `serve` itself now uses the scopes
// via auth.NewScopedTokenStore (see cmd/cartographer/serve.go scopedTokens).
func (a AuthConfig) TokenStrings() []string {
	if len(a.Tokens) == 0 {
		return nil
	}
	out := make([]string, len(a.Tokens))
	for i, t := range a.Tokens {
		out[i] = t.Token
	}
	return out
}

// TokenSpec is a bearer token with optional per-KB scopes ("kb:<name>:r|rw").
// Empty Scopes means full access to every KB (admin).
type TokenSpec struct {
	Token  string   `yaml:"token"`
	Scopes []string `yaml:"scopes,omitempty"`
}

// UnmarshalYAML accepts both a bare scalar ("tok1", legacy `tokens: [...]`
// list-of-strings form) and a mapping ({token: ..., scopes: [...]}).
func (t *TokenSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		t.Token = node.Value
		t.Scopes = nil
		return nil
	}
	type rawTokenSpec TokenSpec // avoid recursing into this UnmarshalYAML
	var raw rawTokenSpec
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*t = TokenSpec(raw)
	return nil
}

// KBSpec identifies a single KB: either already present on disk (Path), or
// to be cloned from a git remote (Remote) into Config.Data before opening.
// Exactly one of Path/Remote is expected to be set. The remaining fields are
// optional per-KB overrides for git identity and SOPS; a zero value falls
// back to the corresponding global GitConfig/SopsConfig setting.
type KBSpec struct {
	Path   string `yaml:"path"`
	Remote string `yaml:"remote"`

	// Name overrides the KB name that would otherwise be derived from the
	// basename of Remote/Path (see cmd/cartographer.resolveKBName). If set,
	// it wins everywhere the name is used: the HTTP endpoint (/mcp/<name>),
	// token scopes (kb:<name>:r|rw), the clone destination under Config.Data,
	// the git token-file convention (GitConfig.TokenDir/<name>.token), and
	// the SOPS age-key convention (SopsConfig.AgeKeyDir/<name>.age) (D53).
	Name string `yaml:"name,omitempty"`

	SSHKey         string `yaml:"ssh_key,omitempty"`
	KnownHosts     string `yaml:"known_hosts,omitempty"`
	AuthorName     string `yaml:"author_name,omitempty"`
	AuthorEmail    string `yaml:"author_email,omitempty"`
	CommitterName  string `yaml:"committer_name,omitempty"`
	CommitterEmail string `yaml:"committer_email,omitempty"`
	SopsAgeKeyFile string `yaml:"sops_age_key_file,omitempty"`

	// AllowArtifactWrite enables the artifact_write/artifact_delete MCP tools
	// (D71) for this KB — writing a provisioning artifact (skill/agent/hook/
	// mcp) injects instructions a client agent will execute, so the
	// capability is opt-in per-KB rather than implied by an rw token alone.
	// Default false. Propagated to kb.KB.AllowArtifactWrite (see serve.go).
	AllowArtifactWrite bool `yaml:"allow_artifact_write,omitempty"`
}

// GitConfig controls per-KB git autocommit/sync, the SSH identity used to
// reach remote KBs during bootstrap, and the default author/committer
// identity used for autocommits.
type GitConfig struct {
	Autocommit     bool
	Sync           bool
	SSHKey         string
	KnownHosts     string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	// TokenDir, if set, is a directory holding one file per KB
	// (<TokenDir>/<name>.token, trimmed content = the token) used as HTTPS
	// git credentials for that KB's remote — convention over per-KB YAML
	// config (D53). Injected via a credential.helper in the process env
	// (cmd/cartographer.gitTokenCredentialEnv); never written to disk/argv.
	TokenDir string
	// SyncInWindow is the freshness window for SyncIn (D76/WP3): within this
	// window after a successful SyncIn, subsequent SyncIn calls are a no-op
	// (see kb.KB.SyncInWindow). Zero disables the window.
	SyncInWindow time.Duration
	// SyncOutDebounce is the debounce window for the async push worker
	// (D76/WP4): a successful write schedules a push instead of pushing
	// inline, and the worker waits SyncOutDebounce after the last write
	// before actually pushing (see kb.KB.SyncOutDebounce). Zero disables
	// the worker: pushes stay synchronous and inline, as before D76/WP4.
	SyncOutDebounce time.Duration
}

// SopsConfig controls the default SOPS age key used to decrypt secret refs.
type SopsConfig struct {
	AgeKeyFile string `yaml:"age_key_file"`
	// AgeKeyDir, if set, is a directory holding one age key file per KB
	// (<AgeKeyDir>/<name>.age) — convention over per-KB YAML config (D53).
	// Resolution order: KBSpec.SopsAgeKeyFile > <AgeKeyDir>/<name>.age (if
	// present) > AgeKeyFile.
	AgeKeyDir string `yaml:"age_key_dir"`
}

// SearchConfig controls the Ollama-backed semantic search.
type SearchConfig struct {
	OllamaURL   string
	OllamaModel string
}

// AuditConfig controls the append-only audit log.
type AuditConfig struct {
	Log     string `yaml:"log"`
	KeySeed string `yaml:"key_seed"`
}

// Default returns the configuration used when no YAML file is provided.
func Default() *Config {
	return &Config{
		Auth: AuthConfig{Mode: "auto"},
		Git: GitConfig{
			Autocommit:      true,
			Sync:            true,
			AuthorName:      "cartographer",
			AuthorEmail:     "cartographer@localhost",
			SyncInWindow:    30 * time.Second,
			SyncOutDebounce: 3 * time.Second,
		},
		Search: SearchConfig{
			OllamaModel: "nomic-embed-text",
		},
		ToolsProfile: "agent",
	}
}

// rawConfig mirrors the YAML shape. Fields whose zero value would silently
// clobber a non-zero default (git.autocommit, git.sync) are pointers, so
// Load can distinguish "absent from YAML" from "explicitly false".
type rawConfig struct {
	HTTP   string      `yaml:"http"`
	Init   bool        `yaml:"init"`
	Auth   rawAuth     `yaml:"auth"`
	Data   string      `yaml:"data"`
	KBs    []KBSpec    `yaml:"kbs"`
	Git    rawGit      `yaml:"git"`
	Search rawSearch   `yaml:"search"`
	Audit  AuditConfig `yaml:"audit"`
	Sops   SopsConfig  `yaml:"sops"`
	Tools  rawTools    `yaml:"tools"`
}

type rawTools struct {
	Profile string `yaml:"profile"`
}

type rawAuth struct {
	Mode   string      `yaml:"mode"`
	Tokens []TokenSpec `yaml:"tokens"`
}

type rawGit struct {
	Autocommit     *bool  `yaml:"autocommit"`
	Sync           *bool  `yaml:"sync"`
	SSHKey         string `yaml:"ssh_key"`
	KnownHosts     string `yaml:"known_hosts"`
	AuthorName     string `yaml:"author_name"`
	AuthorEmail    string `yaml:"author_email"`
	CommitterName  string `yaml:"committer_name"`
	CommitterEmail string `yaml:"committer_email"`
	TokenDir       string `yaml:"token_dir"`
	InWindow       string `yaml:"in_window"`
	OutDebounce    string `yaml:"out_debounce"`
}

type rawSearch struct {
	OllamaURL   string `yaml:"ollama_url"`
	OllamaModel string `yaml:"ollama_model"`
}

// Load parses a YAML config file, layering it on top of Default().
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}

	cfg := Default()
	cfg.HTTP = raw.HTTP
	cfg.Init = raw.Init
	cfg.Data = raw.Data
	cfg.KBs = raw.KBs
	cfg.Audit = raw.Audit

	if raw.Auth.Mode != "" {
		cfg.Auth.Mode = normalizeAuthMode(raw.Auth.Mode)
	}
	cfg.Auth.Tokens = raw.Auth.Tokens

	if raw.Git.Autocommit != nil {
		cfg.Git.Autocommit = *raw.Git.Autocommit
	}
	if raw.Git.Sync != nil {
		cfg.Git.Sync = *raw.Git.Sync
	}
	cfg.Git.SSHKey = raw.Git.SSHKey
	cfg.Git.KnownHosts = raw.Git.KnownHosts
	if raw.Git.AuthorName != "" {
		cfg.Git.AuthorName = raw.Git.AuthorName
	}
	if raw.Git.AuthorEmail != "" {
		cfg.Git.AuthorEmail = raw.Git.AuthorEmail
	}
	cfg.Git.CommitterName = raw.Git.CommitterName
	cfg.Git.CommitterEmail = raw.Git.CommitterEmail
	cfg.Git.TokenDir = raw.Git.TokenDir
	if raw.Git.InWindow != "" {
		cfg.Git.SyncInWindow = parseDuration(raw.Git.InWindow, cfg.Git.SyncInWindow)
	}
	if raw.Git.OutDebounce != "" {
		cfg.Git.SyncOutDebounce = parseDuration(raw.Git.OutDebounce, cfg.Git.SyncOutDebounce)
	}

	cfg.Search.OllamaURL = raw.Search.OllamaURL
	if raw.Search.OllamaModel != "" {
		cfg.Search.OllamaModel = raw.Search.OllamaModel
	}

	cfg.Sops = raw.Sops

	if raw.Tools.Profile != "" {
		cfg.ToolsProfile = normalizeToolsProfile(raw.Tools.Profile)
	}

	return cfg, nil
}

// FromEnv applies CARTOGRAPHER_* environment overrides onto cfg. Only
// variables that are actually set (non-empty) are considered; unset ones
// leave cfg untouched so the YAML/default values remain in effect.
func FromEnv(cfg *Config) {
	if v := os.Getenv("CARTOGRAPHER_KB"); v != "" {
		for _, p := range splitCSV(v) {
			cfg.KBs = append(cfg.KBs, KBSpec{Path: p})
		}
	}
	if v := os.Getenv("CARTOGRAPHER_KB_REMOTES"); v != "" {
		for _, r := range splitCSV(v) {
			cfg.KBs = append(cfg.KBs, KBSpec{Remote: r})
		}
	}
	if v := os.Getenv("CARTOGRAPHER_DATA"); v != "" {
		cfg.Data = v
	}
	if v := os.Getenv("CARTOGRAPHER_HTTP"); v != "" {
		cfg.HTTP = v
	}
	if v := os.Getenv("CARTOGRAPHER_TOKENS"); v != "" {
		cfg.Auth.Tokens = parseTokenSpecs(v)
	}
	if v := os.Getenv("CARTOGRAPHER_AUTH"); v != "" {
		cfg.Auth.Mode = normalizeAuthMode(v)
	}
	if v := os.Getenv("CARTOGRAPHER_GIT_AUTOCOMMIT"); v != "" {
		cfg.Git.Autocommit = parseBool(v, cfg.Git.Autocommit)
	}
	if v := os.Getenv("CARTOGRAPHER_GIT_SYNC"); v != "" {
		cfg.Git.Sync = parseBool(v, cfg.Git.Sync)
	}
	if v := os.Getenv("CARTOGRAPHER_GIT_TOKEN_DIR"); v != "" {
		cfg.Git.TokenDir = v
	}
	if v := os.Getenv("CARTOGRAPHER_SYNC_IN_WINDOW"); v != "" {
		cfg.Git.SyncInWindow = parseDuration(v, cfg.Git.SyncInWindow)
	}
	if v := os.Getenv("CARTOGRAPHER_SYNC_OUT_DEBOUNCE"); v != "" {
		cfg.Git.SyncOutDebounce = parseDuration(v, cfg.Git.SyncOutDebounce)
	}
	if v := os.Getenv("CARTOGRAPHER_OLLAMA"); v != "" {
		cfg.Search.OllamaURL = v
	}
	if v := os.Getenv("CARTOGRAPHER_OLLAMA_MODEL"); v != "" {
		cfg.Search.OllamaModel = v
	}
	if v := os.Getenv("CARTOGRAPHER_AUDIT_LOG"); v != "" {
		cfg.Audit.Log = v
	}
	if v := os.Getenv("CARTOGRAPHER_AUDIT_KEY"); v != "" {
		cfg.Audit.KeySeed = v
	}
	if v := os.Getenv("CARTOGRAPHER_SOPS_AGE_KEY_FILE"); v != "" {
		cfg.Sops.AgeKeyFile = v
	}
	if v := os.Getenv("CARTOGRAPHER_SOPS_AGE_KEY_DIR"); v != "" {
		cfg.Sops.AgeKeyDir = v
	}
	if v := os.Getenv("CARTOGRAPHER_TOOLS_PROFILE"); v != "" {
		cfg.ToolsProfile = normalizeToolsProfile(v)
	}
}

// FlagOverrides carries the `cartographer serve` flag values that were
// explicitly passed on the command line (nil = not passed). Applying it via
// ApplyFlags is the last, highest-precedence layer of the merge.
type FlagOverrides struct {
	HTTP          *string
	Init          *bool
	KB            *string // comma-separated paths, appended as KBSpec{Path: ...}
	Data          *string
	Tokens        *string // comma-separated, replaces Auth.Tokens
	Ollama        *string
	GitAutocommit *bool
	GitSync       *bool
	ToolsProfile  *string // "agent" | "full"
}

// ApplyFlags layers the explicitly-passed serve flags on top of cfg.
//
// KBs accumulate across layers (YAML kbs: + CARTOGRAPHER_KB/_REMOTES + --kb
// all add KBs to mount), matching the pre-existing --kb/--data additive
// behavior. Scalar fields (HTTP, Data, tokens, ...) are replaced outright,
// consistent with the flag > env > YAML > default precedence.
func ApplyFlags(cfg *Config, o FlagOverrides) {
	if o.HTTP != nil {
		cfg.HTTP = *o.HTTP
	}
	if o.Init != nil {
		cfg.Init = *o.Init
	}
	if o.KB != nil {
		for _, p := range splitCSV(*o.KB) {
			cfg.KBs = append(cfg.KBs, KBSpec{Path: p})
		}
	}
	if o.Data != nil {
		cfg.Data = *o.Data
	}
	if o.Tokens != nil {
		cfg.Auth.Tokens = parseTokenSpecs(*o.Tokens)
	}
	if o.Ollama != nil {
		cfg.Search.OllamaURL = *o.Ollama
	}
	if o.GitAutocommit != nil {
		cfg.Git.Autocommit = *o.GitAutocommit
	}
	if o.GitSync != nil {
		cfg.Git.Sync = *o.GitSync
	}
	if o.ToolsProfile != nil {
		cfg.ToolsProfile = normalizeToolsProfile(*o.ToolsProfile)
	}
}

// normalizeToolsProfile maps a tools-profile spelling onto the canonical
// "agent"/"full". Anything unrecognized falls back to "agent" (fail-closed:
// the smaller surface).
func normalizeToolsProfile(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == "full" {
		return "full"
	}
	return "agent"
}

// normalizeAuthMode maps the legacy boolean-ish spellings (accepted by both
// CARTOGRAPHER_AUTH and auth.mode in YAML) onto the canonical
// "on"/"off"/"auto". Anything unrecognized falls back to "auto".
func normalizeAuthMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return "off"
	case "true", "1", "yes", "on":
		return "on"
	default:
		return "auto"
	}
}

// parseBool parses v as a bool, returning fallback if v is not a valid bool.
func parseBool(v string, fallback bool) bool {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// parseDuration parses v as a Go duration (e.g. "30s"), returning fallback
// if v is not a valid duration.
func parseDuration(v string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// splitCSV splits a comma-separated string, trimming whitespace and
// dropping empty entries.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseTokenSpecs parses CARTOGRAPHER_TOKENS / --tokens into TokenSpecs.
// Entries are separated by commas and/or whitespace. Each entry is either a
// bare token or "token|scope1;scope2;..." (scopes separated by ';', which
// cannot collide with the whitespace/comma entry separators). Empty entries
// are ignored.
func parseTokenSpecs(v string) []TokenSpec {
	entries := strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	var out []TokenSpec
	for _, e := range entries {
		if e == "" {
			continue
		}
		tok, scopeStr, hasScopes := strings.Cut(e, "|")
		if tok == "" {
			continue
		}
		spec := TokenSpec{Token: tok}
		if hasScopes {
			for _, s := range strings.Split(scopeStr, ";") {
				if s = strings.TrimSpace(s); s != "" {
					spec.Scopes = append(spec.Scopes, s)
				}
			}
		}
		out = append(out, spec)
	}
	return out
}
