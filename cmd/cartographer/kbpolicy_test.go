package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

func policyKB(t *testing.T, settings string) *kb.KB {
	t.Helper()
	k, err := kb.Init(filepath.Join(t.TempDir(), "kb-a"))
	if err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(k.Root, kb.SettingsFile), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

func TestApplyKBPolicy_KBFileOnly(t *testing.T) {
	k := policyKB(t, "usage_stale_days: 30\n")
	cfg := config.Default()
	// A discovered KB has no kbs[] entry: the zero spec.
	eff := applyKBPolicy(k, cfg, config.KBSpec{}, "kb-a", false)
	if k.Policy().UsageStaleDays != 30 || eff.UsageStale() != 30 {
		t.Fatalf("policy not applied: usage_stale_days=%d", k.Policy().UsageStaleDays)
	}
}

func TestApplyKBPolicy_NoFileIsTheDefaults(t *testing.T) {
	k := policyKB(t, "")
	applyKBPolicy(k, config.Default(), config.KBSpec{Name: "kb-a"}, "kb-a", false)
	if k.Policy().UsageStaleDays != config.DefaultUsageStaleDays {
		t.Fatalf("a KB without settings.yaml runs on the defaults: %+v", k.Policy())
	}
}

// Two servers mounting the same KB resolve the same policy: the KB's own file
// is the only place it is set (D906).
func TestApplyKBPolicy_TwoServersAgree(t *testing.T) {
	k1 := policyKB(t, "usage_stale_days: 30\n")
	k2, err := kb.Open(k1.Root)
	if err != nil {
		t.Fatal(err)
	}
	applyKBPolicy(k1, config.Default(), config.KBSpec{}, "kb-a", false)
	applyKBPolicy(k2, config.Default(), config.KBSpec{Name: "kb-a", Path: k1.Root}, "kb-a", false)
	if fmt.Sprint(k1.Policy()) != fmt.Sprint(k2.Policy()) {
		t.Errorf("policies differ: %+v vs %+v", k1.Policy(), k2.Policy())
	}
	if k2.Policy().UsageStaleDays != 30 {
		t.Errorf("policy = %+v", k2.Policy())
	}
}

// usage_stale_days under the minimum is raised to it, with a warning (D906).
func TestApplyKBPolicy_UsageStaleDaysIsRaisedToTheMinimum(t *testing.T) {
	k := policyKB(t, "usage_stale_days: 3\n")
	applyKBPolicy(k, config.Default(), config.KBSpec{}, "kb-a", false)
	if got := k.Policy().UsageStaleDays; got != 7 {
		t.Errorf("usage_stale_days = %d, want 7", got)
	}
	if f := k.SettingsInfo().Findings; len(f) != 1 || !strings.Contains(f[0], "usage_stale_days") {
		t.Errorf("findings = %v, want the warning", f)
	}
}

// A KB declares secrets by holding a sops file under secrets/ (D906).
func TestApplyKBPolicy_SecretsAreDerivedFromSopsFiles(t *testing.T) {
	k := policyKB(t, "")
	applyKBPolicy(k, config.Default(), config.KBSpec{}, "kb-a", false)
	if c := mcpserver.KBCapabilitiesFor(k)["secrets"]; c.State != "disabled" {
		t.Errorf("no secrets/ dir: secrets = %+v, want disabled", c)
	}
	dir := filepath.Join(k.Root, "secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file with no sops block declares nothing.
	if err := os.WriteFile(filepath.Join(dir, "plain.yaml"), []byte("token: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := mcpserver.KBCapabilitiesFor(k)["secrets"]; c.State != "disabled" {
		t.Errorf("a file without sops metadata: secrets = %+v, want disabled", c)
	}
	if err := os.WriteFile(filepath.Join(dir, "prod.sops.yaml"), []byte("token: ENC[AES256_GCM,data:x]\nsops:\n  version: 3.9.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := mcpserver.KBCapabilitiesFor(k)["secrets"]; c.State != "missing key" {
		t.Errorf("a sops file and no key: secrets = %+v, want missing key", c)
	}
	k.SopsAgeKeyFile = filepath.Join(t.TempDir(), "key.age")
	if c := mcpserver.KBCapabilitiesFor(k)["secrets"]; c.State != "enabled" {
		t.Errorf("secrets with a key = %+v, want enabled", c)
	}
}

func TestApplyKBPolicy_BadFileCostsOnlyItsEntries(t *testing.T) {
	k := policyKB(t, "write_gate: error\nusage_stale_days: 30\n")
	applyKBPolicy(k, config.Default(), config.KBSpec{}, "kb-a", false)
	if k.Policy().UsageStaleDays != 30 {
		t.Errorf("usage_stale_days=%d", k.Policy().UsageStaleDays)
	}
	if len(k.SettingsInfo().Findings) != 1 {
		t.Errorf("findings = %v", k.SettingsInfo().Findings)
	}
}

// A pull (or an edit) that changes settings.yaml is picked up without restart.
func TestApplyKBPolicy_RefreshPicksUpAChangedFile(t *testing.T) {
	k := policyKB(t, "usage_stale_days: 30\n")
	cfg := config.Default()
	server := config.KBSpec{}
	applyKBPolicy(k, cfg, server, "kb-a", false)
	k.SettingsReload = func(k *kb.KB) { applyKBPolicy(k, cfg, server, "kb-a", true) }
	if k.Policy().UsageStaleDays != 30 {
		t.Fatal("not applied")
	}
	path := filepath.Join(k.Root, kb.SettingsFile)
	os.WriteFile(path, []byte("usage_stale_days: 60\n"), 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	k.RefreshSettings()
	if k.Policy().UsageStaleDays != 60 {
		t.Fatalf("after change: usage_stale_days=%d", k.Policy().UsageStaleDays)
	}
	os.Remove(path)
	k.RefreshSettings()
	if k.Policy().UsageStaleDays != config.DefaultUsageStaleDays {
		t.Fatalf("after removal the server defaults return: usage_stale_days=%d", k.Policy().UsageStaleDays)
	}
}

func TestNewKBSettingsFileIsCommentsOnly(t *testing.T) {
	data := newKBSettingsFile()
	s, bad, err := kb.ParseSettings(data)
	if err != nil || len(bad) != 0 || len(s.Keys()) != 0 {
		t.Fatalf("keys=%v bad=%v err=%v\n%s", s.Keys(), bad, err, data)
	}
	// A key typed under the comments must still parse: no "{}" in the file.
	s, bad, err = kb.ParseSettings(append(data, []byte("usage_stale_days: 30\n")...))
	if err != nil || len(bad) != 0 || !s.Has("usage_stale_days") {
		t.Fatalf("typing a key under the comments broke the file: %v %v %v", s.Keys(), bad, err)
	}
}

// `kb create` documents what a KB may carry, in the initial commit and without
// setting anything (D746).
func TestCmdKBCreateWritesACommentedSettingsFile(t *testing.T) {
	dataDir := t.TempDir()
	withStdout(t, func() {
		withNoGuidance(t, func() {
			if code := cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"}); code != 0 {
				t.Fatalf("cmdKBCreate = %d", code)
			}
		})
	})
	root := filepath.Join(dataDir, "alpha")
	out, err := exec.Command("git", "-C", root, "ls-files", kb.SettingsFile).Output()
	if err != nil || strings.TrimSpace(string(out)) != kb.SettingsFile {
		t.Fatalf("settings.yaml is not in the initial commit: %q %v", out, err)
	}
	k, err := kb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := k.ReadSettings()
	if err != nil || !st.Present || len(st.Settings.Keys()) != 0 || len(st.Malformed) != 0 {
		t.Fatalf("state = %+v, %v", st, err)
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Path == kb.SettingsFile {
			t.Errorf("a fresh KB's settings.yaml has a finding: %+v", f)
		}
	}
}
