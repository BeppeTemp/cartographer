package main

import (
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
	k := policyKB(t, "allow_artifact_write: true\nwrite_gate: error\n")
	cfg := config.Default()
	// A discovered KB has no kbs[] entry: the zero spec.
	eff := applyKBPolicy(k, cfg, config.KBSpec{}, "kb-a", false)
	if !k.Policy().AllowArtifactWrite || k.Policy().WriteGate != "error" || !eff.AllowArtifactWrite {
		t.Fatalf("policy not applied: artifact=%v gate=%q", k.Policy().AllowArtifactWrite, k.Policy().WriteGate)
	}
	caps := mcpserver.KBCapabilitiesFor(k)
	if c := caps["artifact_write"]; c.State != "enabled" || c.Setting != "settings.yaml allow_artifact_write" {
		t.Errorf("artifact_write = %+v", c)
	}
	if c := caps["write_gate"]; c.Setting != "settings.yaml write_gate" {
		t.Errorf("write_gate = %+v", c)
	}
}

func TestApplyKBPolicy_ServerOnlyIsUnchanged(t *testing.T) {
	k := policyKB(t, "")
	cfg := config.Default()
	spec := config.KBSpec{AllowArtifactWrite: true, WriteGate: "warning"}
	applyKBPolicy(k, cfg, spec, "kb-a", false)
	if !k.Policy().AllowArtifactWrite || k.Policy().WriteGate != "warning" {
		t.Fatalf("a KB without settings.yaml must behave as today: %v %q", k.Policy().AllowArtifactWrite, k.Policy().WriteGate)
	}
	if c := mcpserver.KBCapabilitiesFor(k)["artifact_write"]; c.Setting != "kbs[].allow_artifact_write" || c.IgnoredServerValue != "" {
		t.Errorf("artifact_write = %+v", c)
	}
}

// Acceptance: two servers mounting the same KB report the same artifact_write
// and write_gate even when one of them sets a different value in kbs[].
func TestApplyKBPolicy_TwoServersAgree(t *testing.T) {
	k1 := policyKB(t, "allow_artifact_write: true\nwrite_gate: error\n")
	k2, err := kb.Open(k1.Root)
	if err != nil {
		t.Fatal(err)
	}
	cfg1, cfg2 := config.Default(), config.Default()
	cfg2.KBs = []config.KBSpec{{Name: "kb-a", Path: k1.Root, WriteGate: "warning"}}
	cfg2.Origins = map[string]config.Origin{config.KBOriginKey("kb-a", "write_gate"): config.OriginFile}
	applyKBPolicy(k1, cfg1, config.KBSpec{}, "kb-a", false)
	applyKBPolicy(k2, cfg2, cfg2.KBs[0], "kb-a", false)

	c1, c2 := mcpserver.KBCapabilitiesFor(k1), mcpserver.KBCapabilitiesFor(k2)
	for _, key := range []string{"artifact_write", "write_gate"} {
		if c1[key].State != c2[key].State {
			t.Errorf("%s: %q vs %q", key, c1[key].State, c2[key].State)
		}
	}
	if k2.Policy().WriteGate != "error" || c2["write_gate"].IgnoredServerValue != "warning" {
		t.Errorf("the server that disagrees must report the ignored value: %+v", c2["write_gate"])
	}
	if info := k2.SettingsInfo(); len(info.Ignored) != 1 || info.Ignored[0].Key != "write_gate" {
		t.Errorf("ignored = %+v", info.Ignored)
	}
}

func TestApplyKBPolicy_SecretsWithoutAKey(t *testing.T) {
	k := policyKB(t, "secrets: true\n")
	applyKBPolicy(k, config.Default(), config.KBSpec{}, "kb-a", false)
	if c := mcpserver.KBCapabilitiesFor(k)["secrets"]; c.State != "missing key" {
		t.Errorf("secrets = %+v, want missing key", c)
	}
	k.SopsAgeKeyFile = filepath.Join(t.TempDir(), "key.age")
	if c := mcpserver.KBCapabilitiesFor(k)["secrets"]; c.State != "enabled" {
		t.Errorf("secrets with a key = %+v, want enabled", c)
	}
	// secrets: false (or absent) keeps today's reading.
	k2 := policyKB(t, "secrets: false\n")
	applyKBPolicy(k2, config.Default(), config.KBSpec{}, "kb-a", false)
	if c := mcpserver.KBCapabilitiesFor(k2)["secrets"]; c.State != "disabled" {
		t.Errorf("secrets = %+v, want disabled", c)
	}
}

func TestApplyKBPolicy_BadFileCostsOnlyItsEntries(t *testing.T) {
	k := policyKB(t, "write_gate: loud\nallow_artifact_write: true\n")
	applyKBPolicy(k, config.Default(), config.KBSpec{WriteGate: "warning"}, "kb-a", false)
	if k.Policy().WriteGate != "warning" || !k.Policy().AllowArtifactWrite {
		t.Errorf("gate=%q artifact=%v", k.Policy().WriteGate, k.Policy().AllowArtifactWrite)
	}
	if len(k.SettingsInfo().Findings) != 1 {
		t.Errorf("findings = %v", k.SettingsInfo().Findings)
	}
}

// A pull (or an edit) that changes settings.yaml is picked up without restart.
func TestApplyKBPolicy_RefreshPicksUpAChangedFile(t *testing.T) {
	k := policyKB(t, "write_gate: error\n")
	cfg := config.Default()
	server := config.KBSpec{}
	applyKBPolicy(k, cfg, server, "kb-a", false)
	k.SettingsReload = func(k *kb.KB) { applyKBPolicy(k, cfg, server, "kb-a", true) }
	if k.Policy().WriteGate != "error" {
		t.Fatal("not applied")
	}
	path := filepath.Join(k.Root, kb.SettingsFile)
	os.WriteFile(path, []byte("write_gate: warning\nallow_artifact_write: true\n"), 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	k.RefreshSettings()
	if k.Policy().WriteGate != "warning" || !k.Policy().AllowArtifactWrite {
		t.Fatalf("after change: gate=%q artifact=%v", k.Policy().WriteGate, k.Policy().AllowArtifactWrite)
	}
	os.Remove(path)
	k.RefreshSettings()
	if k.Policy().WriteGate != "off" || k.Policy().AllowArtifactWrite {
		t.Fatalf("after removal the server defaults return: gate=%q artifact=%v", k.Policy().WriteGate, k.Policy().AllowArtifactWrite)
	}
}

func TestNewKBSettingsFileIsCommentsOnly(t *testing.T) {
	data := newKBSettingsFile()
	s, bad, err := kb.ParseSettings(data)
	if err != nil || len(bad) != 0 || len(s.Keys()) != 0 {
		t.Fatalf("keys=%v bad=%v err=%v\n%s", s.Keys(), bad, err, data)
	}
	// A key typed under the comments must still parse: no "{}" in the file.
	s, bad, err = kb.ParseSettings(append(data, []byte("write_gate: error\n")...))
	if err != nil || len(bad) != 0 || !s.Has("write_gate") {
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
