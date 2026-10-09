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

	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"gopkg.in/yaml.v3"
)

// Config is the fully resolved server configuration.
type Config struct {
	HTTP  string      // listen address; empty = stdio transport
	Init  bool        // initialize missing KBs
	Auth  AuthConfig  // HTTP bearer-token auth
	Data  string      // directory whose direct subdirs are auto-discovered KBs
	KBs   []KBSpec    // explicit KBs (local path or git remote)
	Git   GitConfig   // per-KB git autocommit/sync + SSH identity for remotes
	Audit AuditConfig // append-only audit log
	Sops  SopsConfig  // default SOPS age key for secret refs
	// ToolsProfile selects which tools tools/list advertises: "agent"
	// (default) hides the advanced/operator tools, "full" advertises all.
	// Hidden tools stay callable via tools/call (D65).
	ToolsProfile string
	// MCP controls MCP-protocol-level server behaviour that isn't specific
	// to a single KB (currently: the tool-name prefix default, D102).
	MCP MCPConfig
	// Web controls the embedded read-only Atlas UI (D227).
	Web WebConfig
	// UpdateCheck makes the server look up the latest release once a day and
	// report it in /health and kb_status when newer (D254). Default true;
	// YAML `update_check`, env CARTOGRAPHER_UPDATE_CHECK, flag --update-check.
	// A dev build never checks, whatever this says.
	UpdateCheck bool
}

// WebConfig controls the embedded web UI.
type WebConfig struct {
	// Enabled serves the UI at /ui/ and its JSON API at /api/ui/v1 when the
	// server is in HTTP mode. Default true: the UI is part of what an HTTP
	// server is for, and an operator who does not want the extra surface turns
	// it off in one place. Stdio mode never serves it, whatever this says.
	Enabled bool
}

// MCPConfig controls MCP-protocol-level server behaviour.
type MCPConfig struct {
	// AllowedOrigins lists the browser origins allowed to reach the MCP
	// endpoint, scheme and port included ("https://app.example.com"). Empty
	// (the default) accepts only an Origin matching the request's own Host;
	// "*" accepts any, restoring the pre-D128 behaviour. A request with no
	// Origin header at all — every non-browser client — is unaffected either
	// way. See mcpserver.OriginGuard.
	AllowedOrigins []string
}

// AuthConfig controls HTTP bearer-token authentication.
type AuthConfig struct {
	Mode   string // "auto" | "on" | "off"
	Tokens []TokenSpec
	// Roles are named, reusable permission sets referenced by TokenSpec.Roles
	// (D118). A deployment that only uses `scopes` declares none.
	Roles []RoleSpec `yaml:"roles,omitempty"`
}

// RoleSpec is a named set of allow rules. Roles referenced by one token are
// unioned; there are deliberately no deny rules, so evaluation is
// order-independent.
type RoleSpec struct {
	Name  string     `yaml:"name"`
	Rules []RuleSpec `yaml:"rules"`
}

// RuleSpec is one allow rule inside a role. Empty Maps, Journals and Types are
// wildcards; non-empty selectors are intersected. Access is "r" or "rw".
type RuleSpec struct {
	KB       string   `yaml:"kb"`
	Access   string   `yaml:"access"`
	Maps     []string `yaml:"maps,omitempty"`
	Journals []string `yaml:"journals,omitempty"`
	Types    []string `yaml:"types,omitempty"`
}

// TokenStrings returns the bare token values, discarding scopes. Kept for
// callers that only need the flat token list (e.g. tests, or an
// auth.NewTokenStore full-access setup); `serve` itself now uses the scopes
// via auth.NewScopedTokenStore (see cmd/cartographer/serve.go
// scopedTokensWithRoles).
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
	// Roles references AuthConfig.Roles by name (D118). Roles and scopes may
	// be combined: the resulting permissions are unioned.
	Roles []string `yaml:"roles,omitempty"`
	// ID is a stable, operator-chosen principal identifier used in logs and
	// audit records. When empty a non-secret ID is derived from the token
	// hash, never from a plaintext prefix.
	ID string `yaml:"id,omitempty"`
	// AuthorName/AuthorEmail, when set, are the git author of every commit a
	// write through this token produces, so an agent's edits are attributed
	// to it rather than to the KB's identity. The committer stays the KB's
	// identity (kbs[].committer_*/git.*). Both or neither (D341).
	AuthorName  string `yaml:"author_name,omitempty"`
	AuthorEmail string `yaml:"author_email,omitempty"`
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
	// ArtifactSigningSeed is a 32-byte hexadecimal Ed25519 seed used only to
	// sign provisioning artifacts served from this KB. It is never exposed by
	// health, MCP responses, or diagnostic output.
	ArtifactSigningSeed string `yaml:"artifact_signing_seed,omitempty"`
	// MCPAllowlist is the per-KB operator policy for third-party HTTP MCP
	// descriptors. An absent or empty list intentionally denies every one.
	MCPAllowlist []provisioning.MCPAllowlistEntry `yaml:"mcp_allowlist,omitempty"`
	Path         string                           `yaml:"path"`
	Remote       string                           `yaml:"remote"`

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

	// GitProfile and the server-git fields override their git: counterparts
	// for this KB. Empty values retain the global/local defaults (D117).
	GitProfile       string `yaml:"git_profile,omitempty"`
	GitBaseBranch    string `yaml:"git_base_branch,omitempty"`
	GitWorkingBranch string `yaml:"git_working_branch,omitempty"`
	GitForge         string `yaml:"git_forge,omitempty"`
	GitHubOwner      string `yaml:"github_owner,omitempty"`
	GitHubRepository string `yaml:"github_repository,omitempty"`
	GitHubAPIURL     string `yaml:"github_api_url,omitempty"`
	GitHubTokenEnv   string `yaml:"github_token_env,omitempty"`

	// GitBranch is the branch a local-profile KB writes to, in place of the
	// remote's default branch (D335): the first push creates it on the
	// remote when it is missing. Empty keeps D264 (follow the remote
	// default). Not valid with the server profile, which has its own
	// branch keys; checked by ValidateGitBranch and GitBranchProfileError.
	GitBranch string `yaml:"git_branch,omitempty"`

	// AllowArtifactWrite enables the artifact_write/artifact_delete MCP tools
	// (D71) for this KB — writing a provisioning artifact (skill/agent/hook/
	// mcp) injects instructions a client agent will execute, so the
	// capability is opt-in per-KB rather than implied by an rw token alone.
	// Default false. Propagated to kb.KB.AllowArtifactWrite (see serve.go).
	AllowArtifactWrite bool `yaml:"allow_artifact_write,omitempty"`

	// AutoRepair lists the lint checks whose mechanical fix
	// `cartographer kb repair --apply` may apply without a human reviewing
	// the plan (D299). Each name must be in lint.FixableChecks. There is
	// deliberately no "all": a fixable check added by a later release must be
	// seen as a dry-run plan before it runs unattended. Absent means
	// DefaultAutoRepair; an explicit empty list ("auto_repair: []") means
	// none, so nil and empty are different values here (D323). Read it
	// through AutoRepairChecks.
	AutoRepair []string `yaml:"auto_repair,omitempty"`

	// RepairOnWrite makes a write tool apply the mechanical auto_repair fixes
	// to the concepts it just wrote, in the same commit (D349). Absent follows
	// auto_repair (on when it resolves non-empty); false = timer-only. A
	// pointer so absent and false differ. Read it through RepairOnWriteEnabled.
	RepairOnWrite *bool `yaml:"repair_on_write,omitempty"`

	// DoctorAutoInterval is how often the server runs the auto_repair checks
	// by itself, with no agent session (D323): "<n>d" or "<n>" days, "0"
	// turns the heartbeat off, empty means DefaultDoctorAutoIntervalDays.
	// Read it through DoctorAutoIntervalDays.
	DoctorAutoInterval string `yaml:"doctor_auto_interval,omitempty"`

	// DoctorInterval is how long after the last kb-doctor session the server
	// starts proposing the next one (D299): "<n>d" or "<n>" days, "0"
	// disables it, empty means DefaultDoctorIntervalDays. Read it through
	// DoctorIntervalDays.
	DoctorInterval string `yaml:"doctor_interval,omitempty"`

	// UsageStaleDays is how many days without a client activating a skill or
	// agent before the artifact_unused lint reports it (D326): nil means
	// DefaultUsageStaleDays, 0 disables the finding. Read it through
	// UsageStale.
	UsageStaleDays *int `yaml:"usage_stale_days,omitempty"`
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
	// Profile selects "local" (the backwards-compatible default) or "server"
	// (D117: dedicated working branch + GitHub PR boundary).
	Profile          string
	BaseBranch       string
	WorkingBranch    string
	Forge            string
	GitHubOwner      string
	GitHubRepository string
	GitHubAPIURL     string
	GitHubTokenEnv   string
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

// AuditConfig controls the append-only audit log.
type AuditConfig struct {
	Log     string `yaml:"log"`
	KeySeed string `yaml:"key_seed"`
	// Mode is "best_effort" (default) or "required" (D119). In best_effort a
	// failed append is logged and the MCP call proceeds; in required the call
	// is rejected before the tool runs, so the log can never be missing an
	// operation that actually happened.
	Mode string `yaml:"mode,omitempty"`
	// MaxSegmentBytes rotates the active segment once it exceeds this size.
	// Zero keeps the package default.
	MaxSegmentBytes int64 `yaml:"max_segment_bytes,omitempty"`
	// ArchiveDir holds rotated segments. Empty keeps them beside the log.
	ArchiveDir string `yaml:"archive_dir,omitempty"`
	// RetentionDays deletes a rotated segment once it is older than this many
	// days AND its checkpoint has been durably written — never before, so the
	// chain stays verifiable. Default 90 (D325); an explicit 0 disables
	// retention (keep everything).
	RetentionDays int `yaml:"retention_days,omitempty"`
}

// rawAudit mirrors AuditConfig; RetentionDays is a pointer so an absent key
// (default 90) differs from an explicit 0 (keep forever).
type rawAudit struct {
	Log             string `yaml:"log"`
	KeySeed         string `yaml:"key_seed"`
	Mode            string `yaml:"mode"`
	MaxSegmentBytes int64  `yaml:"max_segment_bytes"`
	ArchiveDir      string `yaml:"archive_dir"`
	RetentionDays   *int   `yaml:"retention_days"`
}

// DefaultAuditRetentionDays is how long rotated audit segments are kept when
// the operator does not say (D325).
const DefaultAuditRetentionDays = 90

// Default returns the configuration used when no YAML file is provided.
func Default() *Config {
	return &Config{
		Auth:  AuthConfig{Mode: "auto"},
		Audit: AuditConfig{RetentionDays: DefaultAuditRetentionDays},
		Git: GitConfig{
			Autocommit:      true,
			Sync:            true,
			SyncInWindow:    30 * time.Second,
			SyncOutDebounce: 3 * time.Second,
		},
		ToolsProfile: "agent",
		MCP:          MCPConfig{},
		Web:          WebConfig{Enabled: true},
		UpdateCheck:  true,
	}
}

// rawConfig mirrors the YAML shape. Fields whose zero value would silently
// clobber a non-zero default (git.autocommit, git.sync) are pointers, so
// Load can distinguish "absent from YAML" from "explicitly false".
type rawConfig struct {
	HTTP  string     `yaml:"http"`
	Init  bool       `yaml:"init"`
	Auth  rawAuth    `yaml:"auth"`
	Data  string     `yaml:"data"`
	KBs   []KBSpec   `yaml:"kbs"`
	Git   rawGit     `yaml:"git"`
	Audit rawAudit   `yaml:"audit"`
	Sops  SopsConfig `yaml:"sops"`
	Tools rawTools   `yaml:"tools"`
	MCP   rawMCP     `yaml:"mcp"`
	Web   rawWeb     `yaml:"web"`
	// UpdateCheck is a pointer for the same reason rawWeb.Enabled is: the
	// default is true.
	UpdateCheck *bool `yaml:"update_check"`
}

// rawWeb mirrors the `web:` block. Enabled is a pointer for the same reason
// git.autocommit is: the zero value of a bool would silently clobber a `true`
// default, so "absent from YAML" has to be distinguishable from "explicitly
// false".
type rawWeb struct {
	Enabled *bool `yaml:"enabled"`
}

type rawTools struct {
	Profile string `yaml:"profile"`
}

type rawMCP struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type rawAuth struct {
	Mode   string      `yaml:"mode"`
	Tokens []TokenSpec `yaml:"tokens"`
	Roles  []RoleSpec  `yaml:"roles"`
}

type rawGit struct {
	Autocommit       *bool  `yaml:"autocommit"`
	Sync             *bool  `yaml:"sync"`
	SSHKey           string `yaml:"ssh_key"`
	KnownHosts       string `yaml:"known_hosts"`
	AuthorName       string `yaml:"author_name"`
	AuthorEmail      string `yaml:"author_email"`
	CommitterName    string `yaml:"committer_name"`
	CommitterEmail   string `yaml:"committer_email"`
	TokenDir         string `yaml:"token_dir"`
	InWindow         string `yaml:"in_window"`
	OutDebounce      string `yaml:"out_debounce"`
	Profile          string `yaml:"profile"`
	BaseBranch       string `yaml:"base_branch"`
	WorkingBranch    string `yaml:"working_branch"`
	Forge            string `yaml:"forge"`
	GitHubOwner      string `yaml:"github_owner"`
	GitHubRepository string `yaml:"github_repository"`
	GitHubAPIURL     string `yaml:"github_api_url"`
	GitHubTokenEnv   string `yaml:"github_token_env"`
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
	for _, spec := range cfg.KBs {
		if err := provisioning.ValidateMCPAllowlist(spec.MCPAllowlist); err != nil {
			return nil, fmt.Errorf("config: mcp_allowlist: %w", err)
		}
		if err := ValidateAutoRepair(spec.AutoRepair); err != nil {
			return nil, fmt.Errorf("config: auto_repair: %w", err)
		}
		if _, err := spec.DoctorIntervalDays(); err != nil {
			return nil, fmt.Errorf("config: doctor_interval: %w", err)
		}
		if _, err := spec.DoctorAutoIntervalDays(); err != nil {
			return nil, fmt.Errorf("config: doctor_auto_interval: %w", err)
		}
		if err := ValidateGitBranch(spec.GitBranch); err != nil {
			return nil, fmt.Errorf("config: kbs[] %q: git_branch: %w", firstNonEmptyStr(spec.Name, spec.Remote, spec.Path), err)
		}
		if spec.UsageStaleDays != nil && *spec.UsageStaleDays < 0 {
			return nil, fmt.Errorf("config: usage_stale_days: %d is negative (a number of days, or 0 to disable)", *spec.UsageStaleDays)
		}
	}
	cfg.Audit.Log = raw.Audit.Log
	cfg.Audit.KeySeed = raw.Audit.KeySeed
	cfg.Audit.Mode = raw.Audit.Mode
	cfg.Audit.MaxSegmentBytes = raw.Audit.MaxSegmentBytes
	cfg.Audit.ArchiveDir = raw.Audit.ArchiveDir
	if raw.Audit.RetentionDays != nil {
		cfg.Audit.RetentionDays = *raw.Audit.RetentionDays
	}

	if raw.Auth.Mode != "" {
		cfg.Auth.Mode = normalizeAuthMode(raw.Auth.Mode)
	}
	cfg.Auth.Tokens = raw.Auth.Tokens
	cfg.Auth.Roles = raw.Auth.Roles
	if err := ValidateAuthRoles(cfg.Auth); err != nil {
		return nil, fmt.Errorf("config: auth: %w", err)
	}

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
	if raw.Git.Profile != "" {
		cfg.Git.Profile = normalizeGitProfile(raw.Git.Profile)
	}
	cfg.Git.BaseBranch = raw.Git.BaseBranch
	cfg.Git.WorkingBranch = raw.Git.WorkingBranch
	cfg.Git.Forge = raw.Git.Forge
	cfg.Git.GitHubOwner = raw.Git.GitHubOwner
	cfg.Git.GitHubRepository = raw.Git.GitHubRepository
	cfg.Git.GitHubAPIURL = raw.Git.GitHubAPIURL
	cfg.Git.GitHubTokenEnv = raw.Git.GitHubTokenEnv
	if raw.Git.InWindow != "" {
		cfg.Git.SyncInWindow = parseDuration(raw.Git.InWindow, cfg.Git.SyncInWindow)
	}
	if raw.Git.OutDebounce != "" {
		cfg.Git.SyncOutDebounce = parseDuration(raw.Git.OutDebounce, cfg.Git.SyncOutDebounce)
	}

	cfg.Sops = raw.Sops

	if raw.Web.Enabled != nil {
		cfg.Web.Enabled = *raw.Web.Enabled
	}
	if raw.UpdateCheck != nil {
		cfg.UpdateCheck = *raw.UpdateCheck
	}
	if raw.Tools.Profile != "" {
		cfg.ToolsProfile = normalizeToolsProfile(raw.Tools.Profile)
	}

	if len(raw.MCP.AllowedOrigins) > 0 {
		cfg.MCP.AllowedOrigins = raw.MCP.AllowedOrigins
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
	if v := os.Getenv("CARTOGRAPHER_WEB_ENABLED"); v != "" {
		cfg.Web.Enabled = parseBool(v, cfg.Web.Enabled)
	}
	if v := os.Getenv("CARTOGRAPHER_UPDATE_CHECK"); v != "" {
		cfg.UpdateCheck = parseBool(v, cfg.UpdateCheck)
	}
	if v := os.Getenv("CARTOGRAPHER_GIT_PROFILE"); v != "" {
		cfg.Git.Profile = normalizeGitProfile(v)
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
	if v := os.Getenv("CARTOGRAPHER_MCP_ALLOWED_ORIGINS"); v != "" {
		cfg.MCP.AllowedOrigins = splitCSV(v)
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
	GitAutocommit *bool
	GitSync       *bool
	ToolsProfile  *string // "agent" | "full"
	WebEnabled    *bool
	UpdateCheck   *bool
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
	if o.GitAutocommit != nil {
		cfg.Git.Autocommit = *o.GitAutocommit
	}
	if o.GitSync != nil {
		cfg.Git.Sync = *o.GitSync
	}
	if o.ToolsProfile != nil {
		cfg.ToolsProfile = normalizeToolsProfile(*o.ToolsProfile)
	}
	if o.WebEnabled != nil {
		cfg.Web.Enabled = *o.WebEnabled
	}
	if o.UpdateCheck != nil {
		cfg.UpdateCheck = *o.UpdateCheck
	}
}

// ValidateGitBranch rejects a kbs[].git_branch that git would not accept as a
// branch name (the rules of `git check-ref-format --branch`), so a typo fails
// at load instead of at the first push. Empty is valid: the key is unset.
func ValidateGitBranch(name string) error {
	if name == "" {
		return nil
	}
	bad := func(why string) error { return fmt.Errorf("%q is not a valid branch name: %s", name, why) }
	switch {
	case name == "HEAD" || name == "@":
		return bad("reserved name")
	case strings.HasPrefix(name, "-"):
		return bad("starts with '-'")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//"):
		return bad("empty path component")
	case strings.HasSuffix(name, "."):
		return bad("ends with '.'")
	case strings.Contains(name, "..") || strings.Contains(name, "@{"):
		return bad("contains '..' or '@{'")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return bad(fmt.Sprintf("contains %q", r))
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return bad("a component starts with '.' or ends with '.lock'")
		}
	}
	return nil
}

// GitBranchProfileError reports a kbs[].git_branch set on a KB whose
// effective git profile is "server" (D335): that profile writes through its
// own base/working branches, and a third branch key would be ignored.
func (s KBSpec) GitBranchProfileError(globalProfile string) error {
	profile := globalProfile
	if s.GitProfile != "" {
		profile = normalizeGitProfile(s.GitProfile)
	}
	if s.GitBranch != "" && profile == "server" {
		return fmt.Errorf("kbs[] %q: git_branch is for the local git profile; the server profile uses git_base_branch and git_working_branch", firstNonEmptyStr(s.Name, s.Remote, s.Path))
	}
	return nil
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// normalizeGitProfile canonicalizes the profile spelling. It intentionally
// leaves invalid values visible so server startup can fail fast rather than
// silently turn a requested review boundary into direct pushes.
func normalizeGitProfile(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
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
		if tok == "" && !hasScopes {
			continue
		}
		// A "|scopes" entry with no token half is NOT skipped (D179): the
		// operator wrote something, and dropping it silently changes the token
		// count that gates enforcement. It is carried through with an empty
		// value so ValidateAuth refuses it by name.
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

// ValidateAuthRoles rejects role and token configurations that would otherwise
// degrade silently into broader access than the operator wrote (D118). It is
// deliberately strict: every diagnostic names the offending role or rule, and
// no diagnostic ever contains a token value.
func ValidateAuthRoles(a AuthConfig) error {
	seen := make(map[string]bool, len(a.Roles))
	for _, role := range a.Roles {
		if role.Name == "" {
			return fmt.Errorf("role with no name")
		}
		if seen[role.Name] {
			return fmt.Errorf("duplicate role %q", role.Name)
		}
		seen[role.Name] = true
		if len(role.Rules) == 0 {
			return fmt.Errorf("role %q has no rules", role.Name)
		}
		for i, rule := range role.Rules {
			if err := validateRule(role.Name, i, rule); err != nil {
				return err
			}
		}
	}
	ids := make(map[string]bool, len(a.Tokens))
	for _, tok := range a.Tokens {
		if tok.ID != "" {
			if ids[tok.ID] {
				return fmt.Errorf("duplicate principal id %q", tok.ID)
			}
			ids[tok.ID] = true
		}
		for _, name := range tok.Roles {
			if !seen[name] {
				return fmt.Errorf("token references unknown role %q", name)
			}
		}
	}
	return nil
}

func validateRule(role string, i int, rule RuleSpec) error {
	where := fmt.Sprintf("role %q rule %d", role, i)
	if rule.KB == "" {
		return fmt.Errorf("%s: empty kb", where)
	}
	switch rule.Access {
	case "r", "rw":
	default:
		return fmt.Errorf("%s: access must be \"r\" or \"rw\", got %q", where, rule.Access)
	}
	// A selector naming the same collection as both a map and a journal is an
	// operator mistake: the two are intersected, so the rule would match
	// nothing while reading as if it granted something.
	journals := make(map[string]bool, len(rule.Journals))
	for _, j := range rule.Journals {
		journals[j] = true
	}
	for _, group := range [][]string{rule.Maps, rule.Journals, rule.Types} {
		for _, sel := range group {
			if sel == "" {
				return fmt.Errorf("%s: empty selector", where)
			}
			// Selectors are matched against concept-ID segments; a traversal
			// component would silently widen the perimeter.
			if sel == "." || sel == ".." || strings.Contains(sel, "/") || strings.Contains(sel, `\`) {
				return fmt.Errorf("%s: invalid selector %q", where, sel)
			}
		}
	}
	for _, m := range rule.Maps {
		if journals[m] {
			return fmt.Errorf("%s: %q declared as both map and journal", where, m)
		}
	}
	return nil
}

// DefaultDoctorIntervalDays is the doctor interval of a KB that does not set
// doctor_interval, including a discovered one (D299).
const DefaultDoctorIntervalDays = 14

// DoctorIntervalDays resolves DoctorInterval: the default when empty, 0 when
// disabled.
func (s KBSpec) DoctorIntervalDays() (int, error) {
	return parseDays(s.DoctorInterval, DefaultDoctorIntervalDays)
}

// DefaultDoctorAutoIntervalDays is how often the server runs a KB's
// auto_repair checks when doctor_auto_interval is not set: daily (D323).
const DefaultDoctorAutoIntervalDays = 1

// DoctorAutoIntervalDays resolves DoctorAutoInterval: the default when empty,
// 0 when the heartbeat is off.
func (s KBSpec) DoctorAutoIntervalDays() (int, error) {
	return parseDays(s.DoctorAutoInterval, DefaultDoctorAutoIntervalDays)
}

// parseDays reads "<n>d" or "<n>" as a number of days, def when empty.
func parseDays(v string, def int) (int, error) {
	raw := v
	v = strings.TrimSpace(v)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a number of days (\"14d\", \"14\", or \"0\" to disable)", raw)
	}
	return n, nil
}

// DefaultAutoRepair is the auto_repair of a KB that does not set one (D323):
// the checks whose fix is deterministic, never rewrites body text and never
// removes a link, so running it unattended cannot lose a graph edge.
// broken_link and reciprocal_link_item are deliberately absent: they rewrite
// or drop links, and D309 showed a mutual-pair drop can lose hundreds of
// edges in one commit. A fixable check added later is not added here without
// a decision. Do not mutate the returned slice: use AutoRepairChecks.
var DefaultAutoRepair = []string{"nonstandard_field", "tool_param_field", "invalid_field_value", "duplicate_link", "prose_value"}

// AutoRepairChecks resolves AutoRepair: DefaultAutoRepair when the key is
// absent (nil), the operator's list when set, none when it is an explicit
// empty list. isDefault says which, so a status line can tell a customised
// KB from one that never chose.
func (s KBSpec) AutoRepairChecks() (checks []string, isDefault bool) {
	if s.AutoRepair == nil {
		return append([]string{}, DefaultAutoRepair...), true
	}
	return s.AutoRepair, false
}

// RepairOnWriteEnabled resolves RepairOnWrite (D349): the explicit value when
// set, otherwise on exactly when auto_repair resolves to a non-empty list.
func (s KBSpec) RepairOnWriteEnabled() bool {
	if s.RepairOnWrite != nil {
		return *s.RepairOnWrite
	}
	checks, _ := s.AutoRepairChecks()
	return len(checks) > 0
}

// DefaultUsageStaleDays is the artifact_unused threshold of a KB that does not
// set usage_stale_days: six weeks (D326).
const DefaultUsageStaleDays = 42

// UsageStale resolves UsageStaleDays: the default when unset, 0 when disabled.
func (s KBSpec) UsageStale() int {
	if s.UsageStaleDays == nil {
		return DefaultUsageStaleDays
	}
	return *s.UsageStaleDays
}

// ValidateAutoRepair rejects a auto_repair entry that is not a
// check with a mechanical fix, naming it.
func ValidateAutoRepair(checks []string) error {
	for _, c := range checks {
		known := false
		for _, f := range lint.FixableChecks {
			known = known || c == f
		}
		if !known {
			return fmt.Errorf("%q is not a check with a mechanical fix (one of: %s; list them by name, there is no \"all\")",
				c, strings.Join(lint.FixableChecks, ", "))
		}
	}
	return nil
}
