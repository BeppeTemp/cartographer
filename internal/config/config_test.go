package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Auth.Mode != "auto" {
		t.Errorf("Auth.Mode = %q, want %q", cfg.Auth.Mode, "auto")
	}
	if !cfg.Git.Autocommit || !cfg.Git.Sync {
		t.Errorf("Git defaults = %+v, want Autocommit=true Sync=true", cfg.Git)
	}
	if cfg.Git.SyncInWindow != 30*time.Second {
		t.Errorf("Git.SyncInWindow default = %v, want 30s", cfg.Git.SyncInWindow)
	}
	if cfg.Git.SyncOutDebounce != 3*time.Second {
		t.Errorf("Git.SyncOutDebounce default = %v, want 3s", cfg.Git.SyncOutDebounce)
	}
	if cfg.Git.AuthorName != "" || cfg.Git.AuthorEmail != "" {
		t.Errorf("Git author defaults should be unspecified, got %+v", cfg.Git)
	}
	if cfg.Git.CommitterName != "" || cfg.Git.CommitterEmail != "" {
		t.Errorf("Git committer defaults should be empty (fallback to author), got %+v", cfg.Git)
	}
	if cfg.ToolsProfile != "agent" {
		t.Errorf("ToolsProfile = %q, want %q", cfg.ToolsProfile, "agent")
	}
}

func TestToolsProfileEnvAndNormalization(t *testing.T) {
	cfg := Default()
	t.Setenv("CARTOGRAPHER_TOOLS_PROFILE", "FULL")
	FromEnv(cfg)
	if cfg.ToolsProfile != "full" {
		t.Errorf("ToolsProfile from env = %q, want %q", cfg.ToolsProfile, "full")
	}

	// An unrecognized value degrades to "agent" (fail-closed).
	bogus := "operator"
	ApplyFlags(cfg, FlagOverrides{ToolsProfile: &bogus})
	if cfg.ToolsProfile != "agent" {
		t.Errorf("ToolsProfile with unrecognized value = %q, want %q", cfg.ToolsProfile, "agent")
	}
}

const fullYAML = `
http: ":39273"
init: true
auth:
  mode: "on"
  tokens:
    - "tok-a"
    - token: tok-b
      scopes: ["kb:docs:rw", "kb:notes:r"]
data: /data
kbs:
  - remote: ssh://git@gitea.example.com:2222/user/wiki-kb.git
    name: wiki
  - path: /data/kb-locale
    ssh_key: /etc/kb-ssh/kb-locale
    known_hosts: /etc/kb-ssh/kb-locale-known-hosts
    author_name: Kb Locale Bot
    author_email: kb-locale@localhost
    committer_name: Kb Locale Committer
    committer_email: kb-locale-committer@localhost
    sops_age_key_file: /etc/kb-ssh/kb-locale-age.key
git:
  autocommit: false
  sync: false
  ssh_key: /etc/kb-ssh/id_ed25519
  known_hosts: /etc/kb-ssh/known_hosts
  author_name: Cartographer Bot
  author_email: bot@example.com
  committer_name: Cartographer Committer
  committer_email: committer@example.com
  token_dir: /etc/kb-git-tokens
  in_window: 45s
  out_debounce: 5s
# The removed search block (D135) is deliberately kept in this fixture: a
# config file carrying it after an upgrade must still load, ignored.
search:
  ollama_url: http://localhost:11434
  ollama_model: custom-model
audit:
  log: /data/audit.log
  key_seed: deadbeef
sops:
  age_key_file: /etc/cartographer/age.key
  age_key_dir: /etc/kb-sops-keys
tools:
  profile: full
`

func TestLoadFullYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fullYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := &Config{
		HTTP: ":39273",
		Init: true,
		Auth: AuthConfig{Mode: "on", Tokens: []TokenSpec{
			{Token: "tok-a"},
			{Token: "tok-b", Scopes: []string{"kb:docs:rw", "kb:notes:r"}},
		}},
		Data: "/data",
		KBs: []KBSpec{
			{Remote: "ssh://git@gitea.example.com:2222/user/wiki-kb.git", Name: "wiki"},
			{
				Path:           "/data/kb-locale",
				SSHKey:         "/etc/kb-ssh/kb-locale",
				KnownHosts:     "/etc/kb-ssh/kb-locale-known-hosts",
				AuthorName:     "Kb Locale Bot",
				AuthorEmail:    "kb-locale@localhost",
				CommitterName:  "Kb Locale Committer",
				CommitterEmail: "kb-locale-committer@localhost",
				SopsAgeKeyFile: "/etc/kb-ssh/kb-locale-age.key",
			},
		},
		Git: GitConfig{
			Autocommit:      false,
			Sync:            false,
			SSHKey:          "/etc/kb-ssh/id_ed25519",
			KnownHosts:      "/etc/kb-ssh/known_hosts",
			AuthorName:      "Cartographer Bot",
			AuthorEmail:     "bot@example.com",
			CommitterName:   "Cartographer Committer",
			CommitterEmail:  "committer@example.com",
			TokenDir:        "/etc/kb-git-tokens",
			SyncInWindow:    45 * time.Second,
			SyncOutDebounce: 5 * time.Second,
		},
		Audit:        AuditConfig{Log: "/data/audit.log", KeySeed: "deadbeef", RetentionDays: 90},
		Sops:         SopsConfig{AgeKeyFile: "/etc/cartographer/age.key", AgeKeyDir: "/etc/kb-sops-keys"},
		ToolsProfile: "full",
		// The YAML sets neither deprecated mcp key (D288): both stay empty.
		MCP: MCPConfig{},
		// The UI is on by default in HTTP mode; this YAML does not mention it.
		Web:         WebConfig{Enabled: true},
		UpdateCheck: true,
	}

	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadPartialYAMLKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Only http set; git.autocommit/sync must keep defaults.
	if err := os.WriteFile(path, []byte(`http: ":9090"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP != ":9090" {
		t.Errorf("HTTP = %q, want :9090", cfg.HTTP)
	}
	if !cfg.Git.Autocommit || !cfg.Git.Sync {
		t.Errorf("Git = %+v, want defaults (true, true)", cfg.Git)
	}
	if cfg.Auth.Mode != "auto" {
		t.Errorf("Auth.Mode = %q, want default auto", cfg.Auth.Mode)
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	if _, err := Load("/nonexistent/config.yaml"); err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("not: [valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestFromEnv(t *testing.T) {
	cfg := Default()

	t.Setenv("CARTOGRAPHER_KB", "/kb1, /kb2")
	t.Setenv("CARTOGRAPHER_KB_REMOTES", "ssh://git@host/r1.git")
	t.Setenv("CARTOGRAPHER_DATA", "/env-data")
	t.Setenv("CARTOGRAPHER_HTTP", ":7070")
	t.Setenv("CARTOGRAPHER_TOKENS", "t1,t2")
	t.Setenv("CARTOGRAPHER_AUTH", "false")
	t.Setenv("CARTOGRAPHER_GIT_AUTOCOMMIT", "false")
	t.Setenv("CARTOGRAPHER_GIT_SYNC", "false")
	t.Setenv("CARTOGRAPHER_AUDIT_LOG", "/env-audit.log")
	t.Setenv("CARTOGRAPHER_AUDIT_KEY", "cafef00d")
	t.Setenv("CARTOGRAPHER_SOPS_AGE_KEY_FILE", "/env-age.key")
	t.Setenv("CARTOGRAPHER_GIT_TOKEN_DIR", "/env-git-tokens")
	t.Setenv("CARTOGRAPHER_SOPS_AGE_KEY_DIR", "/env-age-keys")
	t.Setenv("CARTOGRAPHER_SYNC_IN_WINDOW", "45s")
	t.Setenv("CARTOGRAPHER_SYNC_OUT_DEBOUNCE", "7s")

	FromEnv(cfg)

	if cfg.Data != "/env-data" || cfg.HTTP != ":7070" {
		t.Errorf("Data/HTTP = %q/%q, want /env-data / :7070", cfg.Data, cfg.HTTP)
	}
	wantKBs := []KBSpec{{Path: "/kb1"}, {Path: "/kb2"}, {Remote: "ssh://git@host/r1.git"}}
	if !reflect.DeepEqual(cfg.KBs, wantKBs) {
		t.Errorf("KBs = %+v, want %+v", cfg.KBs, wantKBs)
	}
	wantTokens := []TokenSpec{{Token: "t1"}, {Token: "t2"}}
	if !reflect.DeepEqual(cfg.Auth.Tokens, wantTokens) {
		t.Errorf("Auth.Tokens = %+v, want %+v", cfg.Auth.Tokens, wantTokens)
	}
	if cfg.Sops.AgeKeyFile != "/env-age.key" {
		t.Errorf("Sops.AgeKeyFile = %q, want /env-age.key", cfg.Sops.AgeKeyFile)
	}
	if cfg.Sops.AgeKeyDir != "/env-age-keys" {
		t.Errorf("Sops.AgeKeyDir = %q, want /env-age-keys", cfg.Sops.AgeKeyDir)
	}
	if cfg.Git.TokenDir != "/env-git-tokens" {
		t.Errorf("Git.TokenDir = %q, want /env-git-tokens", cfg.Git.TokenDir)
	}
	if cfg.Auth.Mode != "off" {
		t.Errorf("Auth.Mode = %q, want off", cfg.Auth.Mode)
	}
	if cfg.Git.Autocommit || cfg.Git.Sync {
		t.Errorf("Git = %+v, want both false", cfg.Git)
	}
	if cfg.Git.SyncInWindow != 45*time.Second {
		t.Errorf("Git.SyncInWindow = %v, want 45s", cfg.Git.SyncInWindow)
	}
	if cfg.Git.SyncOutDebounce != 7*time.Second {
		t.Errorf("Git.SyncOutDebounce = %v, want 7s", cfg.Git.SyncOutDebounce)
	}
	if cfg.Audit.Log != "/env-audit.log" || cfg.Audit.KeySeed != "cafef00d" {
		t.Errorf("Audit = %+v", cfg.Audit)
	}
}

func TestFromEnvUnsetLeavesDefaults(t *testing.T) {
	// Neutralize any CARTOGRAPHER_* vars present in the shell environment
	// (e.g. CARTOGRAPHER_TOKENS on the development Mac): FromEnv treats the
	// empty string as "not set".
	for _, v := range []string{
		"CARTOGRAPHER_KB", "CARTOGRAPHER_KB_REMOTES", "CARTOGRAPHER_DATA",
		"CARTOGRAPHER_HTTP", "CARTOGRAPHER_TOKENS", "CARTOGRAPHER_AUTH",
		"CARTOGRAPHER_GIT_AUTOCOMMIT", "CARTOGRAPHER_GIT_SYNC",
		"CARTOGRAPHER_GIT_TOKEN_DIR", "CARTOGRAPHER_SYNC_IN_WINDOW", "CARTOGRAPHER_AUDIT_LOG",
		"CARTOGRAPHER_AUDIT_KEY", "CARTOGRAPHER_SOPS_AGE_KEY_FILE",
		"CARTOGRAPHER_SOPS_AGE_KEY_DIR",
	} {
		t.Setenv(v, "")
	}
	cfg := Default()
	FromEnv(cfg)
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("FromEnv with no env set mutated cfg: %+v", cfg)
	}
}

func TestApplyFlagsPrecedenceOverEnvAndYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`http: ":39273"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("CARTOGRAPHER_HTTP", ":9090")
	FromEnv(cfg)
	if cfg.HTTP != ":9090" {
		t.Fatalf("after env, HTTP = %q, want :9090", cfg.HTTP)
	}

	flagHTTP := ":6060"
	ApplyFlags(cfg, FlagOverrides{HTTP: &flagHTTP})
	if cfg.HTTP != ":6060" {
		t.Errorf("after flag, HTTP = %q, want :6060 (flag must win)", cfg.HTTP)
	}
}

func TestApplyFlagsNilLeavesCfgUntouched(t *testing.T) {
	cfg := Default()
	cfg.HTTP = ":39273"
	ApplyFlags(cfg, FlagOverrides{})
	if cfg.HTTP != ":39273" {
		t.Errorf("HTTP changed with nil overrides: %q", cfg.HTTP)
	}
}

func TestApplyFlagsKBAppends(t *testing.T) {
	cfg := Default()
	cfg.KBs = []KBSpec{{Path: "/from-yaml"}}
	kbFlag := "/flag1,/flag2"
	ApplyFlags(cfg, FlagOverrides{KB: &kbFlag})
	want := []KBSpec{{Path: "/from-yaml"}, {Path: "/flag1"}, {Path: "/flag2"}}
	if !reflect.DeepEqual(cfg.KBs, want) {
		t.Errorf("KBs = %+v, want %+v", cfg.KBs, want)
	}
}

func TestNormalizeAuthMode(t *testing.T) {
	cases := map[string]string{
		"true": "on", "1": "on", "yes": "on", "on": "on", "ON": "on",
		"false": "off", "0": "off", "no": "off", "off": "off",
		"auto": "auto", "": "auto", "garbage": "auto",
	}
	for in, want := range cases {
		if got := normalizeAuthMode(in); got != want {
			t.Errorf("normalizeAuthMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLoadServerGitProfile pins the YAML surface of the server Git profile
// (D117): the keys are operator-facing, so a silent rename or a missing
// mapping would leave a configured profile inert rather than failing.
func TestLoadServerGitProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("git:\n  profile: server\n  base_branch: main\n  working_branch: cartographer/wiki\n  forge: github\n  github_owner: acme\n  github_repository: wiki\n  github_api_url: https://api.github.test\n  github_token_env: CARTOGRAPHER_GITHUB_TOKEN\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.Profile != "server" || cfg.Git.BaseBranch != "main" || cfg.Git.WorkingBranch != "cartographer/wiki" || cfg.Git.Forge != "github" || cfg.Git.GitHubOwner != "acme" || cfg.Git.GitHubRepository != "wiki" {
		t.Fatalf("server git config = %#v", cfg.Git)
	}
	if cfg.Git.GitHubAPIURL != "https://api.github.test" || cfg.Git.GitHubTokenEnv != "CARTOGRAPHER_GITHUB_TOKEN" {
		t.Fatalf("server git endpoint/token config = %#v", cfg.Git)
	}
}

// D135: the search/Ollama configuration surface is gone. A config file that
// still carries it — and the removed environment variables — must be ignored,
// not fatal: an operator's stale config must not take the server down.
func TestRemovedSearchConfigIsIgnored(t *testing.T) {
	dir := t.TempDir()
	withSearch := filepath.Join(dir, "with-search.yaml")
	without := filepath.Join(dir, "without.yaml")
	base := "http: \":9090\"\ninit: true\n"
	if err := os.WriteFile(withSearch, []byte(base+"search:\n  ollama_url: http://localhost:11434\n  ollama_model: custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(without, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CARTOGRAPHER_OLLAMA", "http://ollama:11434")
	t.Setenv("CARTOGRAPHER_OLLAMA_MODEL", "env-model")

	got, err := Load(withSearch)
	if err != nil {
		t.Fatalf("Load with a stale search block: %v", err)
	}
	want, err := Load(without)
	if err != nil {
		t.Fatalf("Load without it: %v", err)
	}
	FromEnv(got)
	FromEnv(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a stale search block changed the config:\ngot  %+v\nwant %+v", got, want)
	}
}

// TestParseTokenSpecsKeepsTokenlessEntry: "|kb:a:r" is an operator writing
// something, not an absence. Skipping it silently changed the token count that
// gates enforcement; it is carried through so ValidateAuth can refuse it (D179).
func TestParseTokenSpecsKeepsTokenlessEntry(t *testing.T) {
	specs := parseTokenSpecs("|kb:a:r")
	if len(specs) != 1 {
		t.Fatalf("parseTokenSpecs = %+v, want the malformed entry retained", specs)
	}
	if err := ValidateAuth(AuthConfig{Mode: "on", Tokens: specs}); err == nil {
		t.Error("ValidateAuth accepted a token-less entry")
	}
}

// TestParseTokenSpecsIgnoresPureSeparators: an empty entry from ",," is still
// nothing at all, and must not become a rejected record.
func TestParseTokenSpecsIgnoresPureSeparators(t *testing.T) {
	if specs := parseTokenSpecs("a,,b"); len(specs) != 2 {
		t.Errorf("parseTokenSpecs = %+v, want two tokens", specs)
	}
}

// TestRemovedD288KeysAreIgnored pins D325: mcp.mount_mode, mcp.tool_prefix_mode
// and kbs[].tool_prefix no longer exist, but a config that still carries them
// loads without error and without effect (unknown YAML keys are ignored).
func TestRemovedD288KeysAreIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yml := "mcp:\n  mount_mode: per-kb\n  tool_prefix_mode: kb-name\nkbs:\n  - path: /x/wiki\n    tool_prefix: old\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a config with the removed keys must still load: %v", err)
	}
	if len(cfg.KBs) != 1 || cfg.KBs[0].Path != "/x/wiki" {
		t.Errorf("kbs = %+v, want the one entry intact", cfg.KBs)
	}
	t.Setenv("CARTOGRAPHER_MCP_MOUNT_MODE", "routed")
	t.Setenv("CARTOGRAPHER_MCP_TOOL_PREFIX_MODE", "kb-name")
	FromEnv(cfg) // the removed env vars are dead: nothing to read them
}

// web.enabled follows the flag > env > YAML > default precedence, and the
// raw field is a pointer so an explicit `false` is not mistaken for absence —
// the same trap git.autocommit already carries.
func TestWebEnabledPrecedence(t *testing.T) {
	if !Default().Web.Enabled {
		t.Fatal("the UI is on by default in HTTP mode")
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("http: \":39273\"\nweb:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Web.Enabled {
		t.Error("an explicit `web.enabled: false` must survive the default")
	}

	// A YAML with no web block keeps the default.
	bare := filepath.Join(t.TempDir(), "bare.yaml")
	if err := os.WriteFile(bare, []byte("http: \":39273\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(bare)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Web.Enabled {
		t.Error("an absent web block must leave the default alone")
	}

	// Env beats YAML.
	t.Setenv("CARTOGRAPHER_WEB_ENABLED", "false")
	FromEnv(cfg)
	if cfg.Web.Enabled {
		t.Error("CARTOGRAPHER_WEB_ENABLED=false must win over the YAML default")
	}

	// Flag beats env.
	on := true
	ApplyFlags(cfg, FlagOverrides{WebEnabled: &on})
	if !cfg.Web.Enabled {
		t.Error("--web-enabled must win over the environment")
	}
}

// update_check (D254) follows flag > env > YAML > default, default true.
func TestUpdateCheckPrecedence(t *testing.T) {
	if !Default().UpdateCheck {
		t.Fatal("the server update check is on by default")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("update_check: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateCheck {
		t.Error("an explicit `update_check: false` must survive the default")
	}
	bare := filepath.Join(t.TempDir(), "bare.yaml")
	if err := os.WriteFile(bare, []byte("http: \":39273\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(bare); err != nil || !cfg.UpdateCheck {
		t.Fatalf("absent update_check must keep the default: %v", err)
	}
	t.Setenv("CARTOGRAPHER_UPDATE_CHECK", "false")
	FromEnv(cfg)
	if cfg.UpdateCheck {
		t.Error("CARTOGRAPHER_UPDATE_CHECK=false must win over YAML/default")
	}
	on := true
	ApplyFlags(cfg, FlagOverrides{UpdateCheck: &on})
	if !cfg.UpdateCheck {
		t.Error("--update-check must win over the environment")
	}
}

func TestDefaultAuditRetention(t *testing.T) {
	if got := Default().Audit.RetentionDays; got != 90 {
		t.Errorf("Default().Audit.RetentionDays = %d, want 90 (D325)", got)
	}
}

func TestLoadAuditRetention(t *testing.T) {
	cases := []struct {
		name, yaml string
		want       int
	}{
		{"absent", "audit:\n  log: /tmp/a.log\n", 90},
		{"no audit block", "http: \":9090\"\n", 90},
		{"explicit zero keeps everything", "audit:\n  retention_days: 0\n", 0},
		{"explicit value", "audit:\n  retention_days: 30\n", 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(c.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Audit.RetentionDays != c.want {
				t.Errorf("RetentionDays = %d, want %d", cfg.Audit.RetentionDays, c.want)
			}
		})
	}
	// The other audit keys survive the layering.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("audit:\n  log: /tmp/a.log\n  key_seed: abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.Log != "/tmp/a.log" || cfg.Audit.KeySeed != "abc" {
		t.Errorf("Audit = %+v, want log and key_seed kept", cfg.Audit)
	}
}

// D335: kbs[].git_branch is loaded, a malformed name fails at load, and the
// key is refused on a KB whose effective profile is server.
func TestKBGitBranch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("kbs:\n  - path: /data/kb-a\n    git_branch: team/kb-data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.KBs[0].GitBranch; got != "team/kb-data" {
		t.Fatalf("GitBranch = %q", got)
	}
	if err := cfg.KBs[0].GitBranchProfileError("local"); err != nil {
		t.Fatalf("local profile: %v", err)
	}
	if err := cfg.KBs[0].GitBranchProfileError("server"); err == nil || !strings.Contains(err.Error(), "git_branch") {
		t.Fatalf("global server profile: err = %v", err)
	}
	spec := KBSpec{Name: "kb-a", GitBranch: "x", GitProfile: "Server"}
	if err := spec.GitBranchProfileError("local"); err == nil || !strings.Contains(err.Error(), "kb-a") {
		t.Fatalf("per-KB server profile: err = %v", err)
	}
	if err := (KBSpec{GitProfile: "server"}).GitBranchProfileError("server"); err != nil {
		t.Fatalf("unset key: %v", err)
	}

	for _, bad := range []string{"HEAD", "-x", "a..b", "a b", "a~1", "x.lock", ".hidden", "a/", "a//b", "a.", "a@{1}", "a:b"} {
		if ValidateGitBranch(bad) == nil {
			t.Errorf("ValidateGitBranch(%q) accepted", bad)
		}
	}
	if err := os.WriteFile(path, []byte("kbs:\n  - path: /data/kb-a\n    git_branch: \"a..b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "git_branch") {
		t.Fatalf("Load with a malformed branch: err = %v", err)
	}
}

func TestLoadWriteGate(t *testing.T) {
	load := func(v string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		body := "kbs:\n  - path: /tmp/kb\n"
		if v != "" {
			body += "    write_gate: " + v + "\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	for in, want := range map[string]string{"": "off", "off": "off", "error": "error", "warning": "warning"} {
		cfg, err := load(in)
		if err != nil {
			t.Fatalf("write_gate %q: %v", in, err)
		}
		if got := cfg.KBs[0].WriteGateMode(); got != want {
			t.Errorf("write_gate %q resolves to %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"Error", "strict"} {
		_, err := load(bad)
		if err == nil || !strings.Contains(err.Error(), "write_gate: "+bad+": must be off, error or warning") {
			t.Errorf("write_gate %q: err = %v", bad, err)
		}
	}
}
