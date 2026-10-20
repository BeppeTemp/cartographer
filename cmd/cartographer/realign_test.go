package main

import (
	"bytes"
	"flag"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// captureLog collects what the log package prints while the test runs.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func serveFlagSet() *flag.FlagSet { return flag.NewFlagSet("serve", flag.ContinueOnError) }

// D903: a configuration that asks for the server git profile does not start,
// whichever layer asks, and the message says what to do instead. The file is not
// rewritten: its evidence stays until the operator has dealt with it.
func TestLoadServeConfigRefusesTheServerGitProfile(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml, env string
	}{
		"global":  {yaml: "git:\n  profile: server\n"},
		"per KB":  {yaml: "kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    git_profile: server\n"},
		"env":     {env: "server"},
		"env any": {env: "Server"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CARTOGRAPHER_GIT_PROFILE", tc.env)
			t.Setenv("CARTOGRAPHER_TOKENS", "")
			path := ""
			if tc.yaml != "" {
				path = filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
			if err == nil {
				t.Fatal("a configuration with the server git profile was accepted")
			}
			for _, want := range []string{"refusing to start", "unprotect the branch", "`branch`"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			if path != "" {
				if got, _ := os.ReadFile(path); string(got) != tc.yaml {
					t.Errorf("the refused file was rewritten:\n%s", got)
				}
				if matches, _ := filepath.Glob(path + ".bak-*"); len(matches) != 0 {
					t.Errorf("a refused file got a backup: %v", matches)
				}
			}
		})
	}

	// The local profile, which was the default, is just a removed key.
	t.Setenv("CARTOGRAPHER_GIT_PROFILE", "local")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("git:\n  profile: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path); err != nil {
		t.Errorf("git.profile: local was refused: %v", err)
	}
}

// D903: a kbs[].git_branch still set does not start either: ignoring it would
// move the KB's writes to the remote's default branch. The message names the
// value and where it goes, and the file keeps its evidence. An empty one is just
// a removed key.
func TestLoadServeConfigRefusesAPerKBGitBranch(t *testing.T) {
	t.Setenv("CARTOGRAPHER_GIT_PROFILE", "")
	t.Setenv("CARTOGRAPHER_TOKENS", "")
	in := "kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    git_branch: team/kb-data\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err == nil {
		t.Fatal("a configuration with a per KB git_branch was accepted")
	}
	for _, want := range []string{"refusing to start", "kbs[0].git_branch: team/kb-data", "branch in the KB's settings.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != in {
		t.Errorf("the refused file was rewritten:\n%s", got)
	}
	if matches, _ := filepath.Glob(path + ".bak-*"); len(matches) != 0 {
		t.Errorf("a refused file got a backup: %v", matches)
	}

	if err := os.WriteFile(path, []byte("kbs:\n  - remote: https://git.example.com/user/kb-a.git\n    git_branch: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path); err != nil {
		t.Errorf("an empty git_branch was refused: %v", err)
	}
}

// The first start of the new version rewrites the server YAML in its commented
// form, keeps the original, and says what it changed; the next start does
// nothing.
func TestAnnounceConfigRealignsTheServerFile(t *testing.T) {
	buf := captureLog(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	old := "http: \":9090\"\ngit:\n  sync: false\n  autocommit: true\ncredentials_dir: /k\nkbs:\n  - path: /srv/kb-a\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARTOGRAPHER_TOKENS", "")
	t.Setenv("CARTOGRAPHER_GIT_SYNC", "")
	cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	announceConfig(cfg, path)

	logged := buf.String()
	for _, want := range []string{
		"warning: git.sync: removed setting, ignored",
		"warning: kbs[0].path: removed setting, ignored",
		"realigned for cartographer",
		"removed git.sync:",
		"removed git.autocommit:",
		"removed kbs[0].path:",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 1 {
		t.Fatalf("backups = %v, want one", backups)
	}
	if kept, _ := os.ReadFile(backups[0]); string(kept) != old {
		t.Errorf("the backup is not the original:\n%s", kept)
	}
	realigned, _ := os.ReadFile(path)
	if strings.Contains(string(realigned), "autocommit: true") || !strings.Contains(string(realigned), "credentials_dir: /k") {
		t.Errorf("realigned file:\n%s", realigned)
	}

	// The second start: nothing to say, nothing written.
	buf.Reset()
	cfg2, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	announceConfig(cfg2, path)
	if strings.Contains(buf.String(), "realigned") || len(cfg2.Warnings) != 0 {
		t.Errorf("the second start reported: %q warnings %v", buf.String(), cfg2.Warnings)
	}
	if again, _ := filepath.Glob(path + ".bak-*"); len(again) != 1 {
		t.Errorf("the second start wrote a backup: %v", again)
	}
}

// clearLegacyEnv keeps the variables a server file is read with, and the ones
// of the settings D905 removed, out of a test.
func clearLegacyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CARTOGRAPHER_TOKENS", "CARTOGRAPHER_GIT_SYNC", "CARTOGRAPHER_CREDENTIALS_DIR", "CARTOGRAPHER_GIT_TOKEN_DIR",
		"CARTOGRAPHER_SOPS_AGE_KEY_DIR", "CARTOGRAPHER_SOPS_AGE_KEY_FILE", "CARTOGRAPHER_AUDIT_KEY"} {
		t.Setenv(k, "")
	}
}

const legacyTestSeed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"

// The secrets of the settings D905 removed are copied into the credentials
// directory before the file is realigned, and no report holds one of them.
func TestAnnounceConfigMovesTheCredentials(t *testing.T) {
	buf := captureLog(t)
	clearLegacyEnv(t)
	dir := t.TempDir()
	credDir := filepath.Join(dir, "credentials")
	sshKey := filepath.Join(dir, "id_ed25519")
	ageKey := filepath.Join(dir, "kb-a.agekey")
	const sshSecret, ageSecret = "ssh-private-key-material", "AGE-SECRET-KEY-1TESTONLY"
	for p, content := range map[string]string{sshKey: sshSecret + "\n", ageKey: ageSecret + "\n"} {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "config.yaml")
	old := "credentials_dir: " + credDir + "\ngit:\n  ssh_key: " + sshKey +
		"\nkbs:\n  - remote: git@example.com:team/kb-a.git\n    sops_age_key_file: " + ageKey +
		"\n    artifact_signing_seed: " + legacyTestSeed + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	announceConfig(cfg, path)

	for name, want := range map[string]string{"default.ssh": sshSecret + "\n", "kb-a.age": ageSecret + "\n", "kb-a.signing": legacyTestSeed + "\n"} {
		p := filepath.Join(credDir, name)
		if got, err := os.ReadFile(p); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
		if fi, err := os.Stat(p); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, fi.Mode().Perm())
		}
	}
	if _, err := os.Stat(sshKey); err != nil {
		t.Errorf("the source key was moved, not copied: %v", err)
	}
	logged := buf.String()
	for _, want := range []string{
		"git.ssh_key: placed as " + filepath.Join(credDir, "default.ssh"),
		"kbs[0].sops_age_key_file: placed as " + filepath.Join(credDir, "kb-a.age"),
		"kbs[0].artifact_signing_seed: placed as " + filepath.Join(credDir, "kb-a.signing"),
		"removed kbs[0].artifact_signing_seed:",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
	for _, secret := range []string{sshSecret, ageSecret, legacyTestSeed} {
		if strings.Contains(logged, secret) {
			t.Errorf("the log holds a secret:\n%s", logged)
		}
	}
	realigned, _ := os.ReadFile(path)
	if strings.Contains(string(realigned), "ssh_key") || strings.Contains(string(realigned), legacyTestSeed) {
		t.Errorf("the file still holds the removed keys:\n%s", realigned)
	}

	// The second start: the keys are gone, nothing to place or to say.
	buf.Reset()
	cfg2, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	announceConfig(cfg2, path)
	if logged := buf.String(); strings.Contains(logged, "credentials of removed settings") || strings.Contains(logged, "realigned") {
		t.Errorf("the second start reported:\n%s", logged)
	}
}

// A credentials directory that cannot be written: the report names the files to
// create, and the server file is not realigned, so it still holds the secrets
// and the next start lists them again.
func TestAnnounceConfigCannotPlaceTheCredentials(t *testing.T) {
	buf := captureLog(t)
	clearLegacyEnv(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	credDir := filepath.Join(blocker, "credentials")
	path := filepath.Join(dir, "config.yaml")
	old := "credentials_dir: " + credDir + "\nkbs:\n  - remote: git@example.com:team/kb-a.git\n    artifact_signing_seed: " + legacyTestSeed + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	announceConfig(cfg, path)

	logged := buf.String()
	for _, want := range []string{
		"kbs[0].artifact_signing_seed: cannot write it",
		"create " + filepath.Join(credDir, "kb-a.signing"),
		"is not realigned until the credentials above are in place",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, legacyTestSeed) {
		t.Errorf("the log holds the seed:\n%s", logged)
	}
	if kept, _ := os.ReadFile(path); string(kept) != old {
		t.Errorf("the file was rewritten:\n%s", kept)
	}
	if backups, _ := filepath.Glob(path + ".bak-*"); len(backups) != 0 {
		t.Errorf("backups = %v, want none", backups)
	}
}

// A service definition written for an older version still starts: the flags of
// removed settings are dropped, with a warning, before the flag set parses.
func TestDropRemovedFlags(t *testing.T) {
	buf := captureLog(t)
	got := dropRemovedFlags([]string{"--git-sync=false", "--http", ":1", "-git-autocommit", "--git-sync", "--data=/d", "not-a-flag"})
	want := []string{"--http", ":1", "--data=/d", "not-a-flag"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("kept %q, want %q", got, want)
	}
	for _, w := range []string{"--git-sync: removed setting", "--git-autocommit: removed setting"} {
		if !strings.Contains(buf.String(), w) {
			t.Errorf("no warning %q:\n%s", w, buf.String())
		}
	}
}

// settings.yaml of a mounted KB: the removed keys go in one commit of the
// server's; a KB that still asks for the server profile does not mount.
func TestRealignKBSettingsCommitsAndRefuses(t *testing.T) {
	buf := captureLog(t)
	k := policyKB(t, "usage_stale_days: 30\nwrite_gate: error\nauto_repair: []\ngit_profile: local\ngit_branch: team/kb-data\n")
	gitCommitAll(t, k)

	if refusal := realignKBSettings(k, "kb-a"); refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	data, _ := os.ReadFile(filepath.Join(k.Root, kb.SettingsFile))
	if strings.Contains(string(data), "git_profile") || strings.Contains(string(data), "git_branch") || strings.Contains(string(data), "write_gate") || strings.Contains(string(data), "auto_repair") || !strings.Contains(string(data), "usage_stale_days: 30") {
		t.Errorf("settings.yaml:\n%s", data)
	}
	if !strings.Contains(buf.String(), "realigned for cartographer") || !strings.Contains(buf.String(), "committed as ") {
		t.Errorf("the log does not say what happened:\n%s", buf.String())
	}
	st, err := k.ReadSettings()
	if err != nil || len(st.Malformed) != 0 || !st.Settings.Has("usage_stale_days") {
		t.Errorf("after the realignment: %+v %v", st, err)
	}

	r := policyKB(t, "git_profile: server\nusage_stale_days: 30\n")
	gitCommitAll(t, r)
	before, _ := os.ReadFile(filepath.Join(r.Root, kb.SettingsFile))
	refusal := realignKBSettings(r, "kb-b")
	for _, want := range []string{"settings.yaml", "git_profile", "unprotect the branch"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal %q lacks %q", refusal, want)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(r.Root, kb.SettingsFile)); string(after) != string(before) {
		t.Errorf("a refused KB's settings.yaml was rewritten:\n%s", after)
	}
}

func gitCommitAll(t *testing.T, k *kb.KB) {
	t.Helper()
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
}

// The mount loop: the KB that asks for the profile is skipped, loudly, and the
// others mount; the sync timings are the constants, and Cloud Run pushes inline.
func TestBootstrapRefusesTheProfileKBAndSetsTheConstants(t *testing.T) {
	buf := captureLog(t)
	data := t.TempDir()
	for name, settings := range map[string]string{"kb-ok": "usage_stale_days: 30\n", "kb-profile": "git_profile: server\n"} {
		k, err := kb.Init(filepath.Join(data, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(k.Root, kb.SettingsFile), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		gitCommitAll(t, k)
	}
	cfg := config.Default()
	cfg.Data = data
	cfg.HTTP = "127.0.0.1:0"

	t.Setenv("K_SERVICE", "")
	bs := bootstrapKBs(cfg, func(string) {})
	closeIndexes := func() {
		for _, ix := range bs.sqlIdxs {
			ix.Close()
		}
	}
	// Windows cannot delete the index file while it is open.
	t.Cleanup(func() { closeIndexes() })
	if len(bs.names) != 1 || bs.names[0] != "kb-ok" {
		t.Fatalf("mounted %v, want only kb-ok", bs.names)
	}
	if !strings.Contains(buf.String(), `KB "kb-profile"`) || !strings.Contains(buf.String(), "refuses to mount") || !strings.Contains(buf.String(), "unprotect the branch") {
		t.Errorf("no clear message for the refused KB:\n%s", buf.String())
	}
	k := bs.kbs[0]
	if k.SyncInWindow != 30*time.Second || k.SyncOutDebounce != 3*time.Second {
		t.Errorf("sync timings = %v / %v, want 30s / 3s", k.SyncInWindow, k.SyncOutDebounce)
	}

	// Cloud Run freezes an idle instance: the push is made before the write returns.
	t.Setenv("K_SERVICE", "cartographer")
	closeIndexes()
	bs = bootstrapKBs(cfg, func(string) {})
	if got := bs.kbs[0].SyncOutDebounce; got != 0 {
		t.Errorf("on Cloud Run the push debounce = %v, want 0 (inline)", got)
	}
}

// A server YAML that cannot be rewritten (a read-only mount) keeps working. It is
// worth a warning only when it holds removed settings, which are being ignored and
// not dropped; one that merely lacks comments would warn on every start.
func TestAnnounceConfigUnwritableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permission bits and a non-root user")
	}
	for name, tc := range map[string]struct {
		yaml     string
		wantWarn bool
	}{
		"removed settings": {yaml: "git:\n  sync: false\n", wantWarn: true},
		"only comments":    {yaml: "http: \":9090\"\n"},
	} {
		t.Run(name, func(t *testing.T) {
			buf := captureLog(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(dir, 0o700)
			t.Setenv("CARTOGRAPHER_TOKENS", "")
			cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
			if err != nil {
				t.Fatal(err)
			}
			announceConfig(cfg, path)
			if got := strings.Contains(buf.String(), "holds removed settings and cannot be rewritten"); got != tc.wantWarn {
				t.Errorf("warned = %v, want %v:\n%s", got, tc.wantWarn, buf.String())
			}
			if after, _ := os.ReadFile(path); string(after) != tc.yaml {
				t.Errorf("the unwritable file changed:\n%s", after)
			}
		})
	}
}
