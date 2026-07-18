package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if cfg.Search.OllamaModel != "nomic-embed-text" {
		t.Errorf("Search.OllamaModel = %q, want %q", cfg.Search.OllamaModel, "nomic-embed-text")
	}
	if cfg.Git.AuthorName != "cartographer" || cfg.Git.AuthorEmail != "cartographer@localhost" {
		t.Errorf("Git author defaults = %+v", cfg.Git)
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
http: ":8080"
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
		HTTP: ":8080",
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
		Search: SearchConfig{
			OllamaURL:   "http://localhost:11434",
			OllamaModel: "custom-model",
		},
		Audit:        AuditConfig{Log: "/data/audit.log", KeySeed: "deadbeef"},
		Sops:         SopsConfig{AgeKeyFile: "/etc/cartographer/age.key", AgeKeyDir: "/etc/kb-sops-keys"},
		ToolsProfile: "full",
	}

	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoadPartialYAMLKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// Only http set; git.autocommit/sync and search.ollama_model must keep defaults.
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
	if cfg.Search.OllamaModel != "nomic-embed-text" {
		t.Errorf("Search.OllamaModel = %q, want default", cfg.Search.OllamaModel)
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
	t.Setenv("CARTOGRAPHER_OLLAMA", "http://ollama:11434")
	t.Setenv("CARTOGRAPHER_OLLAMA_MODEL", "env-model")
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
	if cfg.Search.OllamaURL != "http://ollama:11434" || cfg.Search.OllamaModel != "env-model" {
		t.Errorf("Search = %+v", cfg.Search)
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
		"CARTOGRAPHER_GIT_TOKEN_DIR", "CARTOGRAPHER_SYNC_IN_WINDOW", "CARTOGRAPHER_OLLAMA",
		"CARTOGRAPHER_OLLAMA_MODEL", "CARTOGRAPHER_AUDIT_LOG",
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
	if err := os.WriteFile(path, []byte(`http: ":8080"`+"\n"), 0o644); err != nil {
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
	cfg.HTTP = ":8080"
	ApplyFlags(cfg, FlagOverrides{})
	if cfg.HTTP != ":8080" {
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
