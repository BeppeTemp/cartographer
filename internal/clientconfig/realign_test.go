package clientconfig_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// withRetired swaps in a registry for the test: the production one is empty
// until a plan removes a client key. Tests that call it must not be parallel.
func withRetired(t *testing.T, r settingschema.Registry) {
	t.Helper()
	old := clientconfig.Retired
	clientconfig.Retired = r
	t.Cleanup(func() { clientconfig.Retired = old })
}

const oldClientYAML = `server_url: http://127.0.0.1:39273/mcp
server_name: cartographer
auth: false
token_env: CARTOGRAPHER_TOKENS
agents:
  - claude
legacy_mode: fast # a note
usage_scan: false
search_depth: 6
`

// The three things a realignment does: a removed key is dropped, an off value is
// reset to the default, a value a person chose is kept; the original is kept
// beside the file and the report lists what happened.
func TestRealignFileDropsRemovedResetsOffKeepsCustom(t *testing.T) {
	withRetired(t, settingschema.Registry{
		Removed: []settingschema.Removed{{Path: "legacy_mode", Replacement: "update.policy", Plan: 903}},
		Off:     []settingschema.Off{{Path: "usage_scan", Value: "false", Plan: 903}},
	})
	dir := t.TempDir()
	writeConfigFile(t, dir, oldClientYAML)

	rep, backup, err := clientconfig.RealignFile(dir, "0.25.0")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{"removed legacy_mode: replaced by update.policy", "reset usage_scan: false switched a feature off"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if backup != clientconfig.Path(dir)+".bak-0.25.0" || readFile(t, backup) != oldClientYAML {
		t.Errorf("backup = %q, does not hold the original", backup)
	}
	raw := readFile(t, clientconfig.Path(dir))
	for _, key := range []string{"legacy_mode", "usage_scan"} {
		if regexp.MustCompile(`(?m)^` + key + `:`).MatchString(raw) {
			t.Errorf("%s survived as a key:\n%s", key, raw)
		}
	}
	if strings.Contains(raw, "legacy_mode") {
		t.Errorf("the removed key is still named:\n%s", raw)
	}
	if !strings.Contains(raw, "# usage_scan: true") {
		t.Errorf("the reset key is not back in the block of unset settings:\n%s", raw)
	}
	for _, kept := range []string{"search_depth: 6", "- claude", "# Name under which the server is registered"} {
		if !strings.Contains(raw, kept) {
			t.Errorf("%q was lost:\n%s", kept, raw)
		}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsageScan || cfg.SearchDepth != 6 || len(cfg.Warnings) != 0 {
		t.Errorf("realigned config = usage_scan %v depth %d warnings %v", cfg.UsageScan, cfg.SearchDepth, cfg.Warnings)
	}
}

func TestRealignFileSecondRunIsANoOp(t *testing.T) {
	withRetired(t, settingschema.Registry{Removed: []settingschema.Removed{{Path: "legacy_mode", Plan: 903}}})
	dir := t.TempDir()
	writeConfigFile(t, dir, oldClientYAML)
	if _, _, err := clientconfig.RealignFile(dir, "0.25.0"); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, clientconfig.Path(dir))
	rep, backup, err := clientconfig.RealignFile(dir, "0.25.0")
	if err != nil || !rep.Empty() || backup != "" || readFile(t, clientconfig.Path(dir)) != first {
		t.Fatalf("second run: report %v backup %q err %v", rep.Lines(), backup, err)
	}
}

// The file Save writes is already in the form a realignment gives, so sync does
// not rewrite (or back up) the file of a machine that has nothing to drop.
func TestRealignFileOfASavedConfigIsANoOp(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.AddAgent("claude")
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, clientconfig.Path(dir))
	rep, backup, err := clientconfig.RealignFile(dir, "0.25.0")
	if err != nil || !rep.Empty() || backup != "" {
		t.Fatalf("report %v backup %q err %v", rep.Lines(), backup, err)
	}
	if readFile(t, clientconfig.Path(dir)) != before {
		t.Error("a saved config was rewritten")
	}
}

// A machine with no config yet has nothing to realign.
func TestRealignFileWithoutAFile(t *testing.T) {
	rep, backup, err := clientconfig.RealignFile(t.TempDir(), "0.25.0")
	if err != nil || !rep.Empty() || backup != "" {
		t.Fatalf("no file: report %v backup %q err %v", rep.Lines(), backup, err)
	}
}

// A removed key edited back in by hand is a warning that names its replacement,
// never an error, and it changes nothing.
func TestLoadWarnsOfARemovedKey(t *testing.T) {
	withRetired(t, settingschema.Registry{Removed: []settingschema.Removed{{Path: "legacy_mode", Replacement: "update.policy", Plan: 903}}})
	dir := t.TempDir()
	writeConfigFile(t, dir, oldClientYAML)
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("a removed key is not an error: %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "legacy_mode") || !strings.Contains(cfg.Warnings[0], "use update.policy") {
		t.Errorf("warnings = %q", cfg.Warnings)
	}
}
