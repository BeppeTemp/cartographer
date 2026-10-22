package clientconfig_test

import (
	"os"
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
paths:
  tools: /opt/tools
`

// The three things a realignment does: a removed key is dropped, an off value is
// reset to the default, a value a person chose is kept; the original is kept
// beside the file and the report lists what happened.
func TestRealignFileDropsRemovedResetsOffKeepsCustom(t *testing.T) {
	withRetired(t, settingschema.Registry{
		Removed: []settingschema.Removed{{Path: "legacy_mode", Replacement: "update_check", Plan: 903}},
		Off:     []settingschema.Off{{Path: "usage_scan", Value: "false", Plan: 903}},
	})
	dir := t.TempDir()
	writeConfigFile(t, dir, oldClientYAML)

	rep, backup, err := clientconfig.RealignFile(dir, "0.25.0")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{"removed legacy_mode: replaced by update_check", "reset usage_scan: false switched a feature off"} {
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
	for _, kept := range []string{"tools: /opt/tools", "- claude", "# Name under which the server is registered"} {
		if !strings.Contains(raw, kept) {
			t.Errorf("%q was lost:\n%s", kept, raw)
		}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsageScan || cfg.Paths["tools"] != "/opt/tools" || len(cfg.Warnings) != 0 {
		t.Errorf("realigned config = usage_scan %v paths %v warnings %v", cfg.UsageScan, cfg.Paths, cfg.Warnings)
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
	withRetired(t, settingschema.Registry{Removed: []settingschema.Removed{{Path: "legacy_mode", Replacement: "update_check", Plan: 903}}})
	dir := t.TempDir()
	writeConfigFile(t, dir, oldClientYAML)
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("a removed key is not an error: %v", err)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "legacy_mode") || !strings.Contains(cfg.Warnings[0], "use update_check") {
		t.Errorf("warnings = %q", cfg.Warnings)
	}
}

// D906: the realignment carries the values of the keys that moved, drops the
// ones that went, and a second run has nothing to do. Uses the real registry.
const d906ClientYAML = `server_url: http://127.0.0.1:39273/mcp
server_name: cartographer
auth: true
# my token variable
token_env: MY_CARTO_TOKEN
agents:
  - claude
  - codex
sync_timer_opt_out: true
trust: false
search_depth: 6
write_findings_hook: false
update:
  policy: auto-patch
usage_scan: false
`

func TestRealignFileD906(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, d906ClientYAML)

	rep, backup, err := clientconfig.RealignFile(dir, "0.30.0")
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, backup) != d906ClientYAML {
		t.Errorf("the backup is not the original")
	}
	raw := readFile(t, clientconfig.Path(dir))
	if !regexp.MustCompile(`(?m)^token: \$\{MY_CARTO_TOKEN\}`).MatchString(raw) {
		t.Errorf("token_env was not rewritten as token: ${MY_CARTO_TOKEN}:\n%s", raw)
	}
	if !strings.Contains(raw, "# my token variable") {
		t.Errorf("the comment on the renamed key was lost:\n%s", raw)
	}
	for _, gone := range []string{"token_env", "agents", "sync_timer_opt_out", "trust", "search_depth", "write_findings_hook", "update", "mcp_approvals"} {
		if hasKey(raw, gone) {
			t.Errorf("%s survived the realignment:\n%s", gone, raw)
		}
	}
	if !regexp.MustCompile(`(?m)^update_check: true`).MatchString(raw) {
		t.Errorf("update.policy: auto-patch must become update_check: true:\n%s", raw)
	}
	if !regexp.MustCompile(`(?m)^usage_scan: false`).MatchString(raw) {
		t.Errorf("an unrelated setting was touched:\n%s", raw)
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), "token_env") {
		t.Errorf("report does not mention the moved token: %v", rep.Lines())
	}

	// agents and the timer opt-out moved to the state file, they were not dropped.
	state := readFile(t, clientconfig.StatePath(dir))
	if !strings.Contains(state, "claude") || !strings.Contains(state, "codex") || !strings.Contains(state, `"sync_timer_opt_out": true`) {
		t.Errorf("the state file does not hold the moved values:\n%s", state)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 || cfg.TokenEnv() != "MY_CARTO_TOKEN" || !cfg.HasAgent("codex") || !cfg.SyncTimerOptOut || !cfg.UpdateCheckEnabled() {
		t.Errorf("realigned config = %+v", cfg)
	}

	again, backup2, err := clientconfig.RealignFile(dir, "0.30.0")
	if err != nil || !again.Empty() || backup2 != "" || readFile(t, clientconfig.Path(dir)) != raw {
		t.Errorf("second run: %v %q %v", again.Lines(), backup2, err)
	}
}

// update.check: false is the only part of the old block that means something.
func TestRealignFileUpdateCheckFalseSurvives(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://x/mcp\nupdate:\n  check: false\n  policy: notify\n")
	if _, _, err := clientconfig.RealignFile(dir, "0.30.0"); err != nil {
		t.Fatal(err)
	}
	raw := readFile(t, clientconfig.Path(dir))
	if !regexp.MustCompile(`(?m)^update_check: false`).MatchString(raw) || hasKey(raw, "update") {
		t.Errorf("update.check: false was not carried over:\n%s", raw)
	}
}

// An existing update.policy: auto-patch never makes the client replace itself:
// it loads as a notice-only client and the realignment turns it into
// update_check: true (nothing in the update command can apply a release).
func TestAutoPatchPolicyIsGoneEverywhere(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://x/mcp\nupdate:\n  policy: auto-patch\n")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("an old auto-patch policy must still load: %v", err)
	}
	if !cfg.UpdateCheckEnabled() || len(cfg.Warnings) == 0 || !strings.Contains(strings.Join(cfg.Warnings, "\n"), "never replaces itself") {
		t.Errorf("auto-patch must load as a warned, check-only client: %+v", cfg)
	}
}

// A realignment that cannot write the state file leaves the YAML as it was:
// the agents are not lost.
func TestRealignFileKeepsAgentsWhenTheStateCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server_url: http://x/mcp\nagents: [claude]\n")
	if err := os.Mkdir(clientconfig.StatePath(dir), 0o755); err != nil { // a directory where the file goes
		t.Fatal(err)
	}
	if _, _, err := clientconfig.RealignFile(dir, "0.30.0"); err == nil {
		t.Fatal("want an error: the state file cannot be written")
	}
	if raw := readFile(t, clientconfig.Path(dir)); !hasKey(raw, "agents") {
		t.Errorf("agents were dropped without being moved:\n%s", raw)
	}
}
