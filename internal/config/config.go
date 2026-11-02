// Package config provides the YAML-based server configuration for
// `cartographer serve`, merged with environment variables and CLI flags.
// Precedence (highest first): CLI flag > environment variable > YAML file > default.
package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"gopkg.in/yaml.v3"
)

// Config is the fully resolved server configuration.
type Config struct {
	HTTP string     // listen address; empty = stdio transport
	Auth AuthConfig // HTTP bearer-token auth
	Data string     // directory whose direct subdirs are auto-discovered KBs
	// CredentialsDir holds every secret file the server reads: SSH keys, the
	// known_hosts file, git tokens, age keys, artifact signing seeds and the
	// audit key (D905). Empty means <data>/credentials; read it through
	// CredentialsPath.
	CredentialsDir string
	KBs            []KBSpec    // KBs to clone from a git remote, or mounted from a folder by --kb
	Audit          AuditConfig // append-only audit log
	// Web controls the embedded read-only Atlas UI (D227).
	Web WebConfig
	// UpdateCheck makes the server look up the latest release once a day and
	// report it in /health and kb_status when newer (D254). Default true;
	// YAML `update_check`, env CARTOGRAPHER_UPDATE_CHECK, flag --update-check.
	// A dev build never checks, whatever this says.
	UpdateCheck bool
	// GitIdentity is the git identity ("Name <email>") the server commits as in
	// every KB without an entry of its own in GitIdentities, and GitIdentities
	// one per KB name, "default" included (D950). Read them through IdentityFor.
	// Empty means the server identity.
	GitIdentity   string
	GitIdentities map[string]string
	// legacyAliases are the kb_aliases the server YAML still carries (D906):
	// the list moved to the server state file, and the realignment moves
	// what the file holds. Until it has, they still resolve (KBAliases).
	legacyAliases []KBAlias
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
	// Artifacts is "rw" (or empty: today's behaviour) or "r": the rule then
	// grants everything it grants except changing artifacts (skills, agents,
	// hooks). A narrower allow, not a deny.
	Artifacts string `yaml:"artifacts,omitempty"`
	// Declared records which selector keys ("maps", "journals", "types") the
	// rule wrote. A key that is present but empty is refused by ValidateAuth:
	// only an omitted key means "all".
	Declared map[string]bool `yaml:"-"`
}

// UnmarshalYAML decodes the rule and records which selector keys it wrote,
// merge keys included.
func (r *RuleSpec) UnmarshalYAML(node *yaml.Node) error {
	type rawRuleSpec RuleSpec // avoid recursing into this UnmarshalYAML
	var raw rawRuleSpec
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*r = RuleSpec(raw)
	keys, _ := mappingPairs(node)
	for _, k := range keys {
		switch k.Value {
		case "maps", "journals", "types":
			if r.Declared == nil {
				r.Declared = map[string]bool{}
			}
			r.Declared[k.Value] = true
		}
	}
	return nil
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
	// ScopesDeclared records that the entry wrote a scope or role list
	// (`scopes:` or `roles:` in YAML, `|` in the env form). A declared list that ends up empty is refused
	// by ValidateAuth: the admin token is the one that writes no scopes at all.
	ScopesDeclared bool `yaml:"-"`
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
	keys, _ := mappingPairs(node) // merge keys count: a merged `scopes: []` is declared too
	for _, k := range keys {
		if k.Value == "scopes" || k.Value == "roles" {
			t.ScopesDeclared = true
		}
	}
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
	// It is not BranchOverride: that one is a key of the server file.
	Branch string `yaml:"-"`
	// BranchOverride is `branch` of this kbs[] entry in the server file: the
	// branch this server alone commits and pushes to, in place of the KB's own
	// `branch` (D1093). It is not Branch above, which is the KB's setting as the
	// settings view shows it. "" means no override. YAML only: the
	// CARTOGRAPHER_KB_REMOTES form has no per-entry option.
	BranchOverride string `yaml:"-"`

	// Name overrides the KB name that would otherwise be derived from the
	// basename of Remote/Path (see cmd/cartographer.resolveKBName). If set,
	// it wins everywhere the name is used: the HTTP endpoint (/mcp/<name>),
	// token scopes (kb:<name>:r|rw), the clone destination under Config.Data,
	// and the names of its files in the credentials directory (<name>.ssh,
	// <name>.token, <name>.age, <name>.signing) (D905).
	Name string `yaml:"name,omitempty"`

	// UsageStaleDays is how many days without a client activating a skill or
	// agent before kb_status.usage counts it as stale (D326): nil means
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
		Auth:        AuthConfig{Mode: "auto"},
		Audit:       AuditConfig{RetentionDays: DefaultAuditRetentionDays, MaxSegmentBytes: DefaultAuditMaxSegmentBytes},
		Web:         WebConfig{Enabled: true},
		UpdateCheck: true,
	}
}

// rawConfig mirrors the YAML shape. Fields whose zero value would silently
// clobber a non-zero default (web.enabled, update_check) are pointers, so
// Load can distinguish "absent from YAML" from "explicitly false".
type rawConfig struct {
	HTTP           string   `yaml:"http"`
	Auth           rawAuth  `yaml:"auth"`
	Data           string   `yaml:"data"`
	CredentialsDir string   `yaml:"credentials_dir"`
	KBs            []rawKB  `yaml:"kbs"`
	Audit          rawAudit `yaml:"audit"`
	Git            rawGit   `yaml:"git"`
	Web            rawWeb   `yaml:"web"`
	// UpdateCheck is a pointer for the same reason rawWeb.Enabled is: the
	// default is true.
	UpdateCheck *bool `yaml:"update_check"`
}

// rawGit mirrors the `git:` block: the identity the server commits as (D950).
// Every other key it once held is retired.
type rawGit struct {
	Identity   string            `yaml:"identity"`
	Identities map[string]string `yaml:"identities"`
}

// rawKB is a kbs[] entry of the server file: the machine's side of a KB, its
// remote and its name (D906). Every other key of a KB lives in the KB's own
// settings.yaml; one met here is a retired key, warned about and dropped by the
// realignment.
type rawKB struct {
	Remote string `yaml:"remote"`
	Name   string `yaml:"name"`
	Branch string `yaml:"branch"`
}

// rawWeb mirrors the `web:` block. Enabled is a pointer because the zero value
// of a bool would silently clobber a `true` default, so "absent from YAML" has to be distinguishable from "explicitly
// false".
type rawWeb struct {
	Enabled *bool `yaml:"enabled"`
}

type rawAuth struct {
	Mode   string      `yaml:"mode"`
	Tokens []TokenSpec `yaml:"tokens"`
	Roles  []RoleSpec  `yaml:"roles"`
}

// checkAuthKeys refuses what would silently drop or widen authentication: an
// unknown key anywhere under `auth:`, and a top-level key that is a misspelled
// section or an auth key written at the root. The rest of the file stays
// lenient (retired keys are warned about and dropped, D906), but a dropped
// auth key changes who may do what: a token whose `scope:` was misspelled would
// carry no scopes, and an `Auth:` section would leave the server unprotected.
// The walk is done on the node tree because TokenSpec decodes through a custom
// unmarshaler, which would lose the decoder's strict mode.
func checkAuthKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil // the typed decode reports syntax errors
	}
	root := doc.Content[0]
	for root.Kind == yaml.AliasNode && root.Alias != nil {
		root = root.Alias
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	sections := map[string]bool{}
	rt := reflect.TypeOf(rawConfig{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("yaml"), ",")
		sections[name] = true
	}
	keys, vals := mappingPairs(root)
	for i, k := range keys {
		switch {
		case sections[k.Value]:
		case k.Value == "tokens" || k.Value == "roles":
			return fmt.Errorf("key %q at the top level (line %d, column %d): it belongs under auth", k.Value, k.Line, k.Column)
		case looksLikeAuth(k.Value):
			return fmt.Errorf("unknown key %q at the top level (line %d, column %d): did you mean %q", k.Value, k.Line, k.Column, "auth")
		default:
			for name := range sections {
				if strings.EqualFold(k.Value, name) {
					return fmt.Errorf("unknown key %q at the top level (line %d, column %d): did you mean %q", k.Value, k.Line, k.Column, name)
				}
			}
		}
		if k.Value == "auth" {
			if err := checkKnownKeys(vals[i], reflect.TypeOf(rawAuth{}), "auth"); err != nil {
				return err
			}
		}
	}
	return nil
}

// looksLikeAuth reports whether an unknown top-level key is probably a
// misspelling of the auth section: it mentions auth or token, or is within
// edit distance 2 of "auth". Left alone it would load silently and the server
// would run without authentication.
func looksLikeAuth(key string) bool {
	k := strings.ToLower(key)
	if strings.Contains(k, "auth") || strings.Contains(k, "token") {
		return true
	}
	a, b := []rune(k), []rune("auth")
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)] <= 2
}

// mappingPairs returns the key and value nodes of a mapping with its merge
// keys (`<<: *a` and `<<: [*a, *b]`) resolved, so a key supplied through a
// merge is seen like a written one.
func mappingPairs(n *yaml.Node) (keys, vals []*yaml.Node) {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	if n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Tag != "!!merge" {
			keys, vals = append(keys, k), append(vals, v)
			continue
		}
		srcs := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			srcs = v.Content
		}
		for _, src := range srcs {
			mk, mv := mappingPairs(src)
			keys, vals = append(keys, mk...), append(vals, mv...)
		}
	}
	return keys, vals
}

func checkKnownKeys(n *yaml.Node, t reflect.Type, at string) error {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch {
	case n.Kind == yaml.SequenceNode && t.Kind() == reflect.Slice:
		for i, c := range n.Content {
			if err := checkKnownKeys(c, t.Elem(), fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case n.Kind == yaml.MappingNode && t.Kind() == reflect.Struct:
		known := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "-" || !f.IsExported() {
				continue
			}
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			known[name] = f.Type
		}
		keys, vals := mappingPairs(n)
		for i, k := range keys {
			ft, ok := known[k.Value]
			if !ok && t == reflect.TypeOf(TokenSpec{}) && (k.Value == "author_name" || k.Value == "author_email") {
				continue // retired (D904): warned about by the realignment, grants nothing
			}
			if !ok {
				return fmt.Errorf("unknown key %q under %s (line %d, column %d)", k.Value, at, k.Line, k.Column)
			}
			if err := checkKnownKeys(vals[i], ft, at+"."+k.Value); err != nil {
				return err
			}
		}
	}
	return nil
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
	if err := checkAuthKeys(data); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}

	cfg := Default()
	var specs []KBSpec
	for i, r := range raw.KBs {
		override := ""
		if r.Branch != "" {
			// Whitespace only is a mistake, not "unset": it must not quietly
			// hand writes back to the KB's own branch.
			override = strings.TrimSpace(r.Branch)
			if override == "" {
				return nil, fmt.Errorf("config: kbs[%d].branch: %q is not a valid branch name: blank", i, r.Branch)
			}
			if err := kb.ValidateBranchName(override); err != nil {
				return nil, fmt.Errorf("config: kbs[%d].branch: %w", i, err)
			}
		}
		specs = append(specs, KBSpec{Remote: r.Remote, Name: r.Name, BranchOverride: override})
	}
	cfg.markFile(data, specs)
	cfg.noteRetired(data, specs)
	cfg.HTTP = raw.HTTP
	cfg.Data = raw.Data
	cfg.CredentialsDir = raw.CredentialsDir
	cfg.KBs = specs
	// kb_aliases left the server file for the state file (D906): the list a
	// file still holds keeps resolving until the realignment has moved it. A
	// removed key never stops a start, so a bad list is dropped, said.
	var legacy struct {
		KBAliases []KBAlias `yaml:"kb_aliases"`
	}
	if yaml.Unmarshal(data, &legacy) == nil {
		if err := ValidateKBAliases(legacy.KBAliases); err != nil {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("kb_aliases is ignored: %v", err))
		} else {
			cfg.legacyAliases = legacy.KBAliases
		}
	}
	cfg.loadGitIdentities(data, raw.Git)
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
		m, err := normalizeAuthMode(raw.Auth.Mode)
		if err != nil {
			return nil, fmt.Errorf("config: auth.mode: %w", err)
		}
		cfg.Auth.Mode = m
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
	return cfg, nil
}

// FromEnv applies CARTOGRAPHER_* environment overrides onto cfg. Only
// variables that are actually set (non-empty) are considered; unset ones
// leave cfg untouched so the YAML/default values remain in effect.
func FromEnv(cfg *Config) error {
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
		m, err := normalizeAuthMode(v)
		if err != nil {
			return fmt.Errorf("CARTOGRAPHER_AUTH: %w", err)
		}
		cfg.mark(OriginEnv, "auth.mode")
		cfg.Auth.Mode = m
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
	return nil
}

// FlagOverrides carries the `cartographer serve` flag values that were
// explicitly passed on the command line (nil = not passed). Applying it via
// ApplyFlags is the last, highest-precedence layer of the merge.
type FlagOverrides struct {
	HTTP        *string
	KB          *string // comma-separated folders, mounted as KBSpec{Path: ...} (no remote, so no sync)
	Data        *string
	Tokens      *string // comma-separated, replaces Auth.Tokens
	WebEnabled  *bool
	UpdateCheck *bool
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
	if o.WebEnabled != nil {
		cfg.mark(OriginFlag, "web.enabled")
		cfg.Web.Enabled = *o.WebEnabled
	}
	if o.UpdateCheck != nil {
		cfg.mark(OriginFlag, "update_check")
		cfg.UpdateCheck = *o.UpdateCheck
	}
}

// normalizeAuthMode maps the legacy boolean-ish spellings (accepted by both
// CARTOGRAPHER_AUTH and auth.mode in YAML) onto the canonical
// "on"/"off"/"auto". An empty value is "auto"; anything else unrecognized is
// an error, so a typo never hides behind the default.
func normalizeAuthMode(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return "off", nil
	case "true", "1", "yes", "on":
		return "on", nil
	case "", "auto":
		return "auto", nil
	default:
		return "", fmt.Errorf("mode %q: want \"auto\", \"on\" or \"off\"", v)
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

// scopeSepSpaces matches the spaces around a "|" or ";" of a token entry.
var scopeSepSpaces = regexp.MustCompile(`[ \t]*([|;])[ \t]*`)

// parseTokenSpecs parses CARTOGRAPHER_TOKENS / --tokens into TokenSpecs.
// Entries are separated by commas and/or whitespace. Each entry is either a
// bare token or "token|scope1;scope2;..." (scopes separated by ';', which
// cannot collide with the whitespace/comma entry separators). Empty entries
// are ignored.
func parseTokenSpecs(v string) []TokenSpec {
	// Spaces around a separator belong to the entry: "tok| kb:a:r" is one token
	// with one scope, not an unscoped token followed by a stray entry.
	v = scopeSepSpaces.ReplaceAllString(v, "$1")
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
		spec := TokenSpec{Token: tok, ScopesDeclared: hasScopes}
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
	switch rule.Artifacts {
	case "", "r":
	case "rw":
		if rule.Access != "rw" {
			return fmt.Errorf("%s: artifacts \"rw\" needs access \"rw\"; omit artifacts for the default", where)
		}
	default:
		return fmt.Errorf("%s: artifacts must be \"r\" or \"rw\", got %q", where, rule.Artifacts)
	}
	for _, sel := range []struct {
		key  string
		vals []string
	}{{"maps", rule.Maps}, {"journals", rule.Journals}, {"types", rule.Types}} {
		key := sel.key
		if rule.Declared[key] && len(sel.vals) == 0 {
			return fmt.Errorf("%s: %s is declared but empty, which would grant every %s; omit the key for all", where, key, strings.TrimSuffix(key, "s"))
		}
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

// DefaultUsageStaleDays is the stale-artifact threshold of a KB that does not
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
