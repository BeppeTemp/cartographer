package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Auth.Mode != "auto" {
		t.Errorf("Auth.Mode = %q, want %q", cfg.Auth.Mode, "auto")
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
credentials_dir: /etc/cartographer/credentials
kbs:
  - remote: ssh://git@gitea.example.com:2222/user/wiki-kb.git
    name: wiki
  - remote: ssh://git@gitea.example.com:2222/user/kb-locale.git
# The removed search block (D135) is deliberately kept in this fixture: a
# config file carrying it after an upgrade must still load, ignored.
search:
  ollama_url: http://localhost:11434
  ollama_model: custom-model
audit:
  log: /data/audit.log
  key_seed: deadbeef
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
		Data:           "/data",
		CredentialsDir: "/etc/cartographer/credentials",
		KBs: []KBSpec{
			{Remote: "ssh://git@gitea.example.com:2222/user/wiki-kb.git", Name: "wiki"},
			{
				Remote: "ssh://git@gitea.example.com:2222/user/kb-locale.git",
			},
		},
		Audit:        AuditConfig{Log: "/data/audit.log", KeySeed: "deadbeef", RetentionDays: 90},
		ToolsProfile: "full",
		// The YAML sets neither deprecated mcp key (D288): both stay empty.
		MCP: MCPConfig{},
		// The UI is on by default in HTTP mode; this YAML does not mention it.
		Web:         WebConfig{Enabled: true},
		UpdateCheck: true,
	}

	want.Origins = cfg.Origins // origins are covered in origin_test.go
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadPartialYAMLKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Only http set; everything else keeps its default.
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
	t.Setenv("CARTOGRAPHER_AUDIT_LOG", "/env-audit.log")
	t.Setenv("CARTOGRAPHER_AUDIT_KEY", "cafef00d")
	t.Setenv("CARTOGRAPHER_CREDENTIALS_DIR", "/env-creds")

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
	if cfg.CredentialsDir != "/env-creds" {
		t.Errorf("CredentialsDir = %q, want /env-creds", cfg.CredentialsDir)
	}
	if cfg.OriginOf("credentials_dir") != OriginEnv {
		t.Errorf("credentials_dir origin = %s, want env", cfg.OriginOf("credentials_dir"))
	}
	if cfg.Auth.Mode != "off" {
		t.Errorf("Auth.Mode = %q, want off", cfg.Auth.Mode)
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
		"CARTOGRAPHER_GIT_AUTOCOMMIT", "CARTOGRAPHER_GIT_SYNC", "CARTOGRAPHER_GIT_PROFILE",
		"CARTOGRAPHER_SYNC_IN_WINDOW", "CARTOGRAPHER_SYNC_OUT_DEBOUNCE",
		"CARTOGRAPHER_AUDIT_LOG", "CARTOGRAPHER_CREDENTIALS_DIR",
		"CARTOGRAPHER_AUDIT_KEY",
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
	cfg.KBs = []KBSpec{{Remote: "https://git.example.com/user/from-yaml.git"}}
	kbFlag := "/flag1,/flag2"
	ApplyFlags(cfg, FlagOverrides{KB: &kbFlag})
	want := []KBSpec{{Remote: "https://git.example.com/user/from-yaml.git"}, {Path: "/flag1"}, {Path: "/flag2"}}
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

// D903: the server git profile and the other git keys that went are ignored with
// a warning that names what to use. The profile itself, a value that cannot be
// honoured, is a refusal as well: the server must not start and write straight
// to a branch the profile protected. So is a kbs[].git_branch: ignoring it would
// move the KB's writes to the remote's default branch.
func TestLoadRetiredGitKeysWarnAndTheProfileRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("git:\n  autocommit: false\n  sync: false\n  in_window: 5s\n  profile: server\n  base_branch: main\n  github_owner: acme\ncredentials_dir: /creds\nkbs:\n  - remote: https://git.example.com/user/kb-a.git\n    git_profile: local\n    git_branch: team/kb-data\n  - path: /srv/kb-b\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a retired key is a warning, not a load error: %v", err)
	}
	if cfg.CredentialsDir != "/creds" {
		t.Errorf("a live key next to retired ones was lost: %q", cfg.CredentialsDir)
	}
	got := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{
		"git.autocommit: removed setting, ignored: every write is one commit",
		"git.sync: removed setting, ignored: a KB with a remote syncs",
		"git.in_window: removed setting, ignored: it is a constant, 30s",
		"git.profile: removed setting, ignored: the server git profile",
		"git.base_branch: removed setting, ignored",
		"kbs[0].git_profile: removed setting, ignored",
		"kbs[0].git_branch: removed setting, ignored; use branch in the KB's settings.yaml",
		"kbs[1].path: removed setting, ignored: a KB without a remote is a folder under data",
		"kbs[1]: no remote",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings lack %q:\n%s", want, got)
		}
	}
	err = cfg.RefusalError()
	if err == nil || !strings.Contains(err.Error(), "git.profile") || !strings.Contains(err.Error(), "unprotect the branch") {
		t.Fatalf("RefusalError = %v, want the profile named with what to do", err)
	}
	if !strings.Contains(err.Error(), "kbs[0].git_branch: team/kb-data is still set") || !strings.Contains(err.Error(), "set branch in the KB's settings.yaml") {
		t.Errorf("RefusalError = %v, want kbs[0].git_branch named with where its value goes", err)
	}
	if strings.Contains(err.Error(), "kbs[0].git_profile") {
		t.Errorf("git_profile: local was refused: %v", err)
	}
}

// D904: the git identity is not configured any more. A file that still sets
// it loads, with a warning per key that says who the author and the committer
// are now, and the value changes nothing.
func TestLoadRetiredAuthorKeysWarn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("git:\n  author_name: Bot\n  author_email: bot@example.com\n  committer_name: Ci\n  committer_email: ci@example.com\n" +
		"kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    author_name: Kb Bot\n    author_email: kb@example.com\n    committer_name: Kb Ci\n    committer_email: kb-ci@example.com\n" +
		// A token with half an identity used to refuse the start; now it is only
		// a warning.
		"auth:\n  tokens:\n    - literal-token\n    - token: bot-token\n      id: bot\n      author_name: Bot\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a retired key is a warning, not a load error: %v", err)
	}
	if len(cfg.KBs) != 1 || cfg.KBs[0].Remote != "https://git.example.com/user/kb-a.git" {
		t.Errorf("KBs = %+v, want the entry with its remote", cfg.KBs)
	}
	if err := ValidateAuth(cfg.Auth); err != nil {
		t.Errorf("auth with a retired token key: %v", err)
	}
	got := strings.Join(cfg.Warnings, "\n")
	for _, key := range []string{"git.author_name", "git.author_email", "git.committer_name", "git.committer_email",
		"kbs[0].author_name", "kbs[0].author_email", "kbs[0].committer_name", "kbs[0].committer_email",
		"auth.tokens[1].author_name"} {
		if want := key + ": removed setting, ignored: " + kb.AuthorSettingRemoved; !strings.Contains(got, want) {
			t.Errorf("warnings lack %q:\n%s", want, got)
		}
	}
	if err := cfg.RefusalError(); err != nil {
		t.Errorf("an author key is ignored, not refused: %v", err)
	}
}

func TestLoadRetiredKBProfileRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    git_profile: Server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RefusalError(); err == nil || !strings.Contains(err.Error(), "kbs[0].git_profile") {
		t.Fatalf("RefusalError = %v", err)
	}
}

// The environment variables of the removed settings warn the same way, and
// CARTOGRAPHER_GIT_PROFILE=server refuses; their values change nothing.
func TestFromEnvRetiredVariablesWarn(t *testing.T) {
	cfg := Default()
	t.Setenv("CARTOGRAPHER_GIT_AUTOCOMMIT", "false")
	t.Setenv("CARTOGRAPHER_GIT_SYNC", "false")
	t.Setenv("CARTOGRAPHER_SYNC_IN_WINDOW", "45s")
	t.Setenv("CARTOGRAPHER_SYNC_OUT_DEBOUNCE", "0")
	t.Setenv("CARTOGRAPHER_GIT_PROFILE", "local")
	FromEnv(cfg)
	if len(cfg.Warnings) != 5 {
		t.Errorf("warnings = %q, want one per variable", cfg.Warnings)
	}
	if cfg.RefusalError() != nil {
		t.Errorf("CARTOGRAPHER_GIT_PROFILE=local was refused: %v", cfg.RefusalError())
	}
	t.Setenv("CARTOGRAPHER_GIT_PROFILE", "server")
	cfg = Default()
	FromEnv(cfg)
	if err := cfg.RefusalError(); err == nil || !strings.Contains(err.Error(), "CARTOGRAPHER_GIT_PROFILE=server") {
		t.Fatalf("RefusalError = %v", err)
	}
}

// A flag of a removed setting is dropped by serve and named here.
func TestRemovedFlags(t *testing.T) {
	flags := RemovedFlags()
	for _, name := range []string{"git-autocommit", "git-sync"} {
		if flags[name] == "" {
			t.Errorf("--%s is not in RemovedFlags", name)
		}
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
	yml := "mcp:\n  mount_mode: per-kb\n  tool_prefix_mode: kb-name\nkbs:\n  - remote: https://git.example.com/user/wiki.git\n    tool_prefix: old\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a config with the removed keys must still load: %v", err)
	}
	if len(cfg.KBs) != 1 || cfg.KBs[0].Remote != "https://git.example.com/user/wiki.git" {
		t.Errorf("kbs = %+v, want the one entry intact", cfg.KBs)
	}
	t.Setenv("CARTOGRAPHER_MCP_MOUNT_MODE", "routed")
	t.Setenv("CARTOGRAPHER_MCP_TOOL_PREFIX_MODE", "kb-name")
	FromEnv(cfg) // the removed env vars are dead: nothing to read them
}

// web.enabled follows the flag > env > YAML > default precedence, and the
// raw field is a pointer so an explicit `false` is not mistaken for absence.
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

func TestLoadWriteGate(t *testing.T) {
	load := func(v string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		body := "kbs:\n  - remote: https://git.example.com/user/kb-a.git\n"
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

// DefaultAutoRepair stays a literal here (D323), but each entry must be a
// check whose registered fix may run with no person (D354).
func TestDefaultAutoRepairIsAutoRepairSafe(t *testing.T) {
	in := map[string]bool{}
	for _, check := range DefaultAutoRepair {
		in[check] = true
		if !lint.AutoRepairSafe(check) {
			t.Errorf("%s is in DefaultAutoRepair but its lint spec is not AutoRepairSafe", check)
		}
	}
	// D355: the default is every safe check, not a subset that drifts.
	for _, s := range lint.Checks() {
		if s.AutoRepairSafe && !in[s.Name] {
			t.Errorf("%s is AutoRepairSafe but missing from DefaultAutoRepair", s.Name)
		}
	}
}
