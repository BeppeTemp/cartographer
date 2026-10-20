// Package config provides the YAML-based server configuration for
// `cartographer serve`, merged with environment variables and CLI flags.
// Precedence (highest first): CLI flag > environment variable > YAML file > default.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"gopkg.in/yaml.v3"
)

// Config is the fully resolved server configuration.
type Config struct {
	HTTP string     // listen address; empty = stdio transport
	Init bool       // initialize missing KBs
	Auth AuthConfig // HTTP bearer-token auth
	Data string     // directory whose direct subdirs are auto-discovered KBs
	// CredentialsDir holds every secret file the server reads: SSH keys, the
	// known_hosts file, git tokens, age keys, artifact signing seeds and the
	// audit key (D905). Empty means <data>/credentials; read it through
	// CredentialsPath.
	CredentialsDir string
	KBs            []KBSpec    // KBs to clone from a git remote, or mounted from a folder by --kb
	Audit          AuditConfig // append-only audit log
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
	// KBAliases maps the old name of a renamed KB to its new one, so a routed
	// `?kbs=<old>` still reaches it (D772). `kb rename` writes it.
	KBAliases []KBAlias
	// Origins records which layer set each setting (D743), keyed by schema
	// path; read it through OriginOf. A key no layer set is absent: default.
	Origins map[string]Origin
	// Warnings are the retired keys, environment variables and entries the
	// layers still carried (D903): each is ignored, and the line names what to
	// use instead. The server logs them at startup.
	Warnings []string
	// Refusals are retired settings whose value cannot be honoured at all: the
	// server git profile (D117, removed by D903). A configuration that has one
	// must not start, because ignoring it would write straight to a branch the
	// operator had protected.
	Refusals []string

	// kbPolicy holds the effective per-KB specs once the KBs' own settings.yaml
	// are laid over kbs[] (D746); see kbsettings.go.
	kbPolicy *kbPolicyStore
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

// envRef matches a value that is exactly a reference to an environment
// variable, "${NAME}" (#680).
var envRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// expandEnvRef resolves a secret field written as "${NAME}" from the
// environment, so the YAML file never has to carry the secret itself. Only
// the whole value is a reference: anything else is returned as written. An
// unset or empty variable is an error rather than an empty secret: an empty
// token or seed would fail open or silently disable signing.
func expandEnvRef(v string) (string, error) {
	m := envRef.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return v, nil
	}
	val := os.Getenv(m[1])
	if val == "" {
		return "", fmt.Errorf("environment variable %s is unset or empty", m[1])
	}
	return val, nil
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

// KBSpec identifies a single KB: one to be cloned from a git remote (Remote)
// into Config.Data before opening, or, from the command line only, a folder
// already on disk (Path). Only Remote and Name are keys of the server file
// (rawKB); the remaining fields are the KB's own policy, read from its
// settings.yaml only (D906). Its secrets (SSH key, token, age key, artifact signing seed)
// are files named after it in the credentials directory (D905).
type KBSpec struct {
	// Path is a folder mounted from the command line: --kb and CARTOGRAPHER_KB
	// (dev and tests; no remote, so no sync). It is not a key of the YAML file:
	// a KB without a remote is a folder under data, which is discovered (D903).
	Path   string `yaml:"-"`
	Remote string `yaml:"remote"`
	// Branch is the effective `branch` of the KB's settings.yaml in the working
	// copy, for the settings view. Sync does not read it: the branch writes go
	// to is the one committed on the remote's default branch (KB.ConfiguredBranch).
	Branch string `yaml:"-"`

	// Name overrides the KB name that would otherwise be derived from the
	// basename of Remote/Path (see cmd/cartographer.resolveKBName). If set,
	// it wins everywhere the name is used: the HTTP endpoint (/mcp/<name>),
	// token scopes (kb:<name>:r|rw), the clone destination under Config.Data,
	// and the names of its files in the credentials directory (<name>.ssh,
	// <name>.token, <name>.age, <name>.signing) (D905).
	Name string `yaml:"name,omitempty"`

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

	// WriteGate makes a content write refuse, and roll back, when it
	// introduces a finding at or above the floor (D350): "off" (default,
	// also empty), "error" (findings of severity error) or "warning" (error
	// and warning). Read it through WriteGateMode.
	WriteGate string `yaml:"write_gate,omitempty"`

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

	// DoctorMode is accepted and ignored (D704): a kb-doctor session starts
	// only when the operator asks for one, so the old "unattended" mode (D358),
	// in which the nudge told the agent to start it by itself, is gone. Still
	// validated, so a typo in a config that sets it keeps failing loudly.
	DoctorMode string `yaml:"doctor_mode,omitempty"`

	// DoctorBudget is how many review items one kb-doctor session decides
	// before it closes (D358): nil means DefaultDoctorBudget. Read it through
	// DoctorBudgetItems.
	DoctorBudget *int `yaml:"doctor_budget,omitempty"`

	// UsageStaleDays is how many days without a client activating a skill or
	// agent before the artifact_unused lint reports it (D326): nil means
	// DefaultUsageStaleDays, and a value under kb.MinUsageStaleDays is read as
	// that minimum (D906). Read it through UsageStale.
	UsageStaleDays *int `yaml:"usage_stale_days,omitempty"`
}

// AuditConfig controls the append-only audit log.
type AuditConfig struct {
	// Log relocates the audit log; empty means <data>/audit/audit.log. The log
	// is always on and signed with audit.key from the credentials directory
	// (D905): read the path through Config.AuditLogPath.
	Log string `yaml:"log"`
	// Mode is "best_effort" (default) or "required" (D119). In best_effort a
	// failed append is logged and the MCP call proceeds; in required the call
	// is rejected before the tool runs, so the log can never be missing an
	// operation that actually happened.
	Mode string `yaml:"mode,omitempty"`
	// MaxSegmentBytes rotates the active segment once it exceeds this size, so
	// retention has segments to delete. DefaultAuditMaxSegmentBytes unless set.
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
	Mode            string `yaml:"mode"`
	MaxSegmentBytes int64  `yaml:"max_segment_bytes"`
	ArchiveDir      string `yaml:"archive_dir"`
	RetentionDays   *int   `yaml:"retention_days"`
}

// DefaultAuditRetentionDays is how long rotated audit segments are kept when
// the operator does not say (D325).
const DefaultAuditRetentionDays = 90

// DefaultAuditMaxSegmentBytes is the size at which the active audit segment is
// rotated when the operator does not say (D905). Without a size the log never
// rotated, and retention never had a segment to delete.
const DefaultAuditMaxSegmentBytes int64 = 64 << 20

// Default returns the configuration used when no YAML file is provided.
func Default() *Config {
	return &Config{
		Auth:         AuthConfig{Mode: "auto"},
		Audit:        AuditConfig{RetentionDays: DefaultAuditRetentionDays, MaxSegmentBytes: DefaultAuditMaxSegmentBytes},
		ToolsProfile: "agent",
		MCP:          MCPConfig{},
		Web:          WebConfig{Enabled: true},
		UpdateCheck:  true,
	}
}

// rawConfig mirrors the YAML shape. Fields whose zero value would silently
// clobber a non-zero default (web.enabled, update_check) are pointers, so
// Load can distinguish "absent from YAML" from "explicitly false".
type rawConfig struct {
	HTTP           string   `yaml:"http"`
	Init           bool     `yaml:"init"`
	Auth           rawAuth  `yaml:"auth"`
	Data           string   `yaml:"data"`
	CredentialsDir string   `yaml:"credentials_dir"`
	KBs            []rawKB  `yaml:"kbs"`
	Audit          rawAudit `yaml:"audit"`
	Tools          rawTools `yaml:"tools"`
	MCP            rawMCP   `yaml:"mcp"`
	Web            rawWeb   `yaml:"web"`
	// UpdateCheck is a pointer for the same reason rawWeb.Enabled is: the
	// default is true.
	UpdateCheck *bool `yaml:"update_check"`
	// KBAliases is a list, like kbs and auth.roles: every structured setting
	// of the schema is one (D772).
	KBAliases []KBAlias `yaml:"kb_aliases"`
}

// rawKB is a kbs[] entry of the server file: the machine's side of a KB, its
// remote and its name (D906). Every other key of a KB lives in the KB's own
// settings.yaml; one met here is a retired key, warned about and dropped by the
// realignment.
type rawKB struct {
	Remote string `yaml:"remote"`
	Name   string `yaml:"name"`
}

// rawWeb mirrors the `web:` block. Enabled is a pointer because the zero value
// of a bool would silently clobber a `true` default, so "absent from YAML" has to be distinguishable from "explicitly
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
	var specs []KBSpec
	for _, r := range raw.KBs {
		specs = append(specs, KBSpec{Remote: r.Remote, Name: r.Name})
	}
	cfg.markFile(data, specs)
	cfg.noteRetired(data, specs)
	cfg.HTTP = raw.HTTP
	cfg.Init = raw.Init
	cfg.Data = raw.Data
	cfg.CredentialsDir = raw.CredentialsDir
	cfg.KBs = specs
	if err := ValidateKBAliases(raw.KBAliases); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	cfg.KBAliases = raw.KBAliases
	cfg.Audit.Log = raw.Audit.Log
	cfg.Audit.Mode = raw.Audit.Mode
	if raw.Audit.MaxSegmentBytes > 0 {
		cfg.Audit.MaxSegmentBytes = raw.Audit.MaxSegmentBytes
	}
	cfg.Audit.ArchiveDir = raw.Audit.ArchiveDir
	if raw.Audit.RetentionDays != nil {
		cfg.Audit.RetentionDays = *raw.Audit.RetentionDays
	}

	if raw.Auth.Mode != "" {
		cfg.Auth.Mode = normalizeAuthMode(raw.Auth.Mode)
	}
	cfg.Auth.Tokens = raw.Auth.Tokens
	for i := range cfg.Auth.Tokens {
		tok, err := expandEnvRef(cfg.Auth.Tokens[i].Token)
		if err != nil {
			return nil, fmt.Errorf("config: auth.tokens[%d].token: %w", i, err)
		}
		cfg.Auth.Tokens[i].Token = tok
	}
	cfg.Auth.Roles = raw.Auth.Roles
	if err := ValidateAuthRoles(cfg.Auth); err != nil {
		return nil, fmt.Errorf("config: auth: %w", err)
	}

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
	cfg.noteRetiredEnv()
	if v := os.Getenv("CARTOGRAPHER_KB"); v != "" {
		for _, p := range splitCSV(v) {
			spec := KBSpec{Path: p}
			cfg.KBs = append(cfg.KBs, spec)
			cfg.markKB(OriginEnv, spec)
		}
	}
	if v := os.Getenv("CARTOGRAPHER_KB_REMOTES"); v != "" {
		for _, r := range splitCSV(v) {
			spec := KBSpec{Remote: r}
			cfg.KBs = append(cfg.KBs, spec)
			cfg.markKB(OriginEnv, spec)
		}
	}
	if v := os.Getenv("CARTOGRAPHER_DATA"); v != "" {
		cfg.mark(OriginEnv, "data")
		cfg.Data = v
	}
	if v := os.Getenv("CARTOGRAPHER_HTTP"); v != "" {
		cfg.mark(OriginEnv, "http")
		cfg.HTTP = v
	}
	if v := os.Getenv("CARTOGRAPHER_TOKENS"); v != "" {
		cfg.mark(OriginEnv, "auth.tokens")
		cfg.Auth.Tokens = parseTokenSpecs(v)
	}
	if v := os.Getenv("CARTOGRAPHER_AUTH"); v != "" {
		cfg.mark(OriginEnv, "auth.mode")
		cfg.Auth.Mode = normalizeAuthMode(v)
	}
	if v := os.Getenv("CARTOGRAPHER_WEB_ENABLED"); v != "" {
		cfg.mark(OriginEnv, "web.enabled")
		cfg.Web.Enabled = parseBool(v, cfg.Web.Enabled)
	}
	if v := os.Getenv("CARTOGRAPHER_UPDATE_CHECK"); v != "" {
		cfg.mark(OriginEnv, "update_check")
		cfg.UpdateCheck = parseBool(v, cfg.UpdateCheck)
	}
	if v := os.Getenv("CARTOGRAPHER_CREDENTIALS_DIR"); v != "" {
		cfg.mark(OriginEnv, "credentials_dir")
		cfg.CredentialsDir = v
	}
	if v := os.Getenv("CARTOGRAPHER_AUDIT_LOG"); v != "" {
		cfg.mark(OriginEnv, "audit.log")
		cfg.Audit.Log = v
	}
	if v := os.Getenv("CARTOGRAPHER_TOOLS_PROFILE"); v != "" {
		cfg.mark(OriginEnv, "tools.profile")
		cfg.ToolsProfile = normalizeToolsProfile(v)
	}
	if v := os.Getenv("CARTOGRAPHER_MCP_ALLOWED_ORIGINS"); v != "" {
		cfg.mark(OriginEnv, "mcp.allowed_origins")
		cfg.MCP.AllowedOrigins = splitCSV(v)
	}
}

// FlagOverrides carries the `cartographer serve` flag values that were
// explicitly passed on the command line (nil = not passed). Applying it via
// ApplyFlags is the last, highest-precedence layer of the merge.
type FlagOverrides struct {
	HTTP         *string
	Init         *bool
	KB           *string // comma-separated folders, mounted as KBSpec{Path: ...} (no remote, so no sync)
	Data         *string
	Tokens       *string // comma-separated, replaces Auth.Tokens
	ToolsProfile *string // "agent" | "full"
	WebEnabled   *bool
	UpdateCheck  *bool
}

// ApplyFlags layers the explicitly-passed serve flags on top of cfg.
//
// KBs accumulate across layers (YAML kbs: + CARTOGRAPHER_KB/_REMOTES + --kb
// all add KBs to mount), matching the pre-existing --kb/--data additive
// behavior. Scalar fields (HTTP, Data, tokens, ...) are replaced outright,
// consistent with the flag > env > YAML > default precedence.
func ApplyFlags(cfg *Config, o FlagOverrides) {
	if o.HTTP != nil {
		cfg.mark(OriginFlag, "http")
		cfg.HTTP = *o.HTTP
	}
	if o.Init != nil {
		cfg.mark(OriginFlag, "init")
		cfg.Init = *o.Init
	}
	if o.KB != nil {
		for _, p := range splitCSV(*o.KB) {
			spec := KBSpec{Path: p}
			cfg.KBs = append(cfg.KBs, spec)
			cfg.markKB(OriginFlag, spec)
		}
	}
	if o.Data != nil {
		cfg.mark(OriginFlag, "data")
		cfg.Data = *o.Data
	}
	if o.Tokens != nil {
		cfg.mark(OriginFlag, "auth.tokens")
		cfg.Auth.Tokens = parseTokenSpecs(*o.Tokens)
	}
	if o.ToolsProfile != nil {
		cfg.mark(OriginFlag, "tools.profile")
		cfg.ToolsProfile = normalizeToolsProfile(*o.ToolsProfile)
	}
	if o.WebEnabled != nil {
		cfg.mark(OriginFlag, "web.enabled")
		cfg.Web.Enabled = *o.WebEnabled
	}
	if o.UpdateCheck != nil {
		cfg.mark(OriginFlag, "update_check")
		cfg.UpdateCheck = *o.UpdateCheck
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

// DefaultDoctorIntervalDays is how long after the last kb-doctor session the
// server proposes the next one when doctor_interval is not set (D299).
const DefaultDoctorIntervalDays = 14

// DefaultDoctorBudget is the review items one kb-doctor session decides before
// it closes (D358): the operator's attention is the limit, not the backlog.
const DefaultDoctorBudget = 10

// ValidateDoctorMode rejects a doctor_mode other than the two values 0.23
// accepted; either is then ignored (D704).
func ValidateDoctorMode(v string) error {
	switch v {
	case "", "unattended", "assisted":
		return nil
	}
	return fmt.Errorf("config: doctor_mode: %s: must be unattended or assisted (ignored since D704)", v)
}

// DoctorBudgetItems resolves DoctorBudget.
func (s KBSpec) DoctorBudgetItems() int {
	if s.DoctorBudget != nil {
		return *s.DoctorBudget
	}
	return DefaultDoctorBudget
}

// DoctorIntervalDays resolves DoctorInterval: DefaultDoctorIntervalDays when
// empty, 0 when disabled.
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

// DefaultAutoRepair is the auto_repair of a KB that does not set one (D323,
// D355): every check whose registered fix may run with no person
// (AutoRepairSafe, D354). Each is mechanical and already guarded; leaving one
// off by default is what keeps imported KBs drifting. broken_link and
// reciprocal_link_item are deliberately absent: they rewrite or drop links,
// and D309 showed a mutual-pair drop can lose hundreds of edges in one commit.
// The list is a literal, pinned equal to the AutoRepairSafe set by a test, so
// a check added to the registry as safe fails that test until someone decides
// it belongs here. Do not mutate the returned slice: use AutoRepairChecks.
var DefaultAutoRepair = []string{
	"nonstandard_field", "tool_param_field", "prose_value", "invalid_field_value",
	"stringified_list", "title_h1_mismatch", "legacy_path", "duplicate_link",
	"index_link_form", "legacy_tool_name", "missing_frontmatter", "unparseable_frontmatter",
	"missing_type", "nonslug_file_name", "missing_title", "repeated_link",
	"value_case_variant", "unmapped_folder",
	// D366: the unambiguous part of three doctor-only checks.
	"index_lists_retired", "link_to_retired", "title_quality",
	// D907: a removed contract key is realigned by one unattended commit.
	"contract_legacy_key",
	// D352, in the order a page needs them: bind it, set its type, its values,
	// reorder its sections, then rename the aliases (a reorder lists headings
	// as written, so it runs before the rename changes them).
	"template_missing", "template_type_mismatch", "template_field_value",
	"template_section_order", "template_section_alias",
}

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

// ValidateWriteGate rejects a write_gate value other than off, error or
// warning (empty is off) (D350).
func ValidateWriteGate(v string) error {
	switch v {
	case "", "off", "error", "warning":
		return nil
	}
	return fmt.Errorf("config: write_gate: %s: must be off, error or warning", v)
}

// WriteGateMode resolves WriteGate: "off" when unset.
func (s KBSpec) WriteGateMode() string {
	if s.WriteGate == "" {
		return "off"
	}
	return s.WriteGate
}

// DefaultUsageStaleDays is the artifact_unused threshold of a KB that does not
// set usage_stale_days: six weeks (D326).
const DefaultUsageStaleDays = kb.DefaultUsageStaleDays

// UsageStale resolves UsageStaleDays: the default when unset, and never less
// than kb.MinUsageStaleDays (D906). There is no value that turns the finding
// off.
func (s KBSpec) UsageStale() int {
	if s.UsageStaleDays == nil || *s.UsageStaleDays <= 0 {
		return DefaultUsageStaleDays
	}
	return max(*s.UsageStaleDays, kb.MinUsageStaleDays)
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
