package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/execbit"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// remoteOf is the remote the tests clone a KB called name from.
func remoteOf(name string) string { return "https://git.example.com/user/" + name + ".git" }

func applyFor(t *testing.T, cfgPath, name, kbPath string) error {
	t.Helper()
	plan, err := planKBEntry(name, kbPath, remoteOf(name), cfgPath)
	if err != nil {
		return err
	}
	return applyKBEntry(plan)
}

func artifactWriteOf(t *testing.T, cfgPath, name string) (bool, int) {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	n := 0
	got := false
	for _, s := range cfg.KBs {
		if s.MountName() == name {
			n++
			got = s.AllowArtifactWrite
		}
	}
	return got, n
}

func TestApplyKBEntryAppendKeepsCommentsAndUnknownKeys(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "alpha")
	cfg := writeCfg(t, "# my server\nunknown_key: keep-me # inline\nkbs:\n  - remote: https://git.example.com/user/other.git # other kb\n")
	if err := applyFor(t, cfg, "alpha", kbPath); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	for _, want := range []string{"# my server", "unknown_key: keep-me # inline", "# other kb", "user/other.git"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("lost %q:\n%s", want, b)
		}
	}
	if got, n := artifactWriteOf(t, cfg, "alpha"); !got || n != 1 {
		t.Errorf("allow_artifact_write=%v entries=%d, want true/1", got, n)
	}
	// Windows has no POSIX permission bits: os.Stat reports 0666 there.
	if st, _ := os.Stat(cfg); execbit.Supported && st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestApplyKBEntryCreatesKBsSequence(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "alpha")
	cfg := writeCfg(t, "data: /srv/data\n")
	if err := applyFor(t, cfg, "alpha", kbPath); err != nil {
		t.Fatal(err)
	}
	if got, n := artifactWriteOf(t, cfg, "alpha"); !got || n != 1 {
		t.Errorf("allow_artifact_write=%v entries=%d, want true/1", got, n)
	}
}

func TestApplyKBEntryUpdatesExistingByRemoteAndNameIdempotent(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "alpha")
	for name, entry := range map[string]string{
		"by remote": "  - remote: " + remoteOf("alpha") + "\n",
		"by name":   "  - name: alpha\n    remote: " + remoteOf("elsewhere") + "\n",
	} {
		cfg := writeCfg(t, "kbs:\n"+entry)
		for i := 0; i < 2; i++ {
			if err := applyFor(t, cfg, "alpha", kbPath); err != nil {
				t.Fatalf("%s run %d: %v", name, i, err)
			}
		}
		c, _ := config.Load(cfg)
		if len(c.KBs) != 1 || !c.KBs[0].AllowArtifactWrite {
			t.Errorf("%s: kbs = %+v, want one entry with the flag on", name, c.KBs)
		}
	}
}

func TestPlanKBEntryRefusals(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "alpha")
	amb := writeCfg(t, "kbs:\n  - remote: "+remoteOf("alpha")+"\n  - name: alpha\n    remote: "+remoteOf("x")+"\n")
	if _, err := planKBEntry("alpha", kbPath, remoteOf("alpha"), amb); err == nil || !strings.Contains(err.Error(), "matches 2 entries") {
		t.Errorf("ambiguous: err = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	if _, err := planKBEntry("alpha", kbPath, remoteOf("alpha"), missing); !errors.Is(err, errNoServerConfig) {
		t.Errorf("missing: err = %v, want errNoServerConfig", err)
	}
	bad := writeCfg(t, "kbs: [\n")
	if _, err := planKBEntry("alpha", kbPath, remoteOf("alpha"), bad); err == nil || errors.Is(err, errNoServerConfig) {
		t.Errorf("invalid: err = %v", err)
	}
	scalar := writeCfg(t, "kbs: nope\n")
	if err := applyFor(t, scalar, "alpha", kbPath); err == nil {
		t.Error("scalar kbs: accepted")
	}
}

// A new kbs[] entry gets the schema comment of the keys it sets, and nothing the
// operator wrote is touched (D747).
func TestApplyKBEntryCommentsTheNewEntryOnly(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "alpha")
	cfg := writeCfg(t, "kbs:\n  # my other kb\n  - remote: https://git.example.com/user/other.git\n    write_gate: error\n")
	if err := applyFor(t, cfg, "alpha", kbPath); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	got := string(b)
	if !strings.Contains(got, "# Enable artifact_write and artifact_delete on this KB.") {
		t.Errorf("the new entry's allow_artifact_write is not documented:\n%s", got)
	}
	if !strings.Contains(got, "# my other kb") || strings.Contains(got, "# Refuse, and roll back") {
		t.Errorf("an existing entry changed:\n%s", got)
	}
	if _, err := config.Load(cfg); err != nil {
		t.Errorf("the written config does not load: %v", err)
	}
}
