package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// D906: serve never creates a KB. A path that is not one is an error that
// names `kb create`.
func TestOpenMountedKBMissingNamesKBCreate(t *testing.T) {
	_, err := openMountedKB(filepath.Join(t.TempDir(), "kb-a"))
	if err == nil {
		t.Fatal("a missing KB opened")
	}
	if !strings.Contains(err.Error(), "cartographer kb create") {
		t.Errorf("the error does not name the command that creates a KB: %v", err)
	}
}

// D906: a server that listens beyond this machine with authentication off is
// refused at start; loopback is unaffected, and so is any address with a token.
func TestCheckListenSafety(t *testing.T) {
	for _, tc := range []struct {
		addr   string
		authOn bool
		ok     bool
	}{
		{"127.0.0.1:39273", false, true},
		{"127.0.0.2:39273", false, true},
		{"[::1]:39273", false, true},
		{"localhost:39273", false, true},
		{"0.0.0.0:39273", false, false},
		{":39273", false, false},
		{"[::]:39273", false, false},
		{"10.0.0.5:39273", false, false},
		{"kb.example.com:39273", false, false},
		{"0.0.0.0:39273", true, true},
		{":39273", true, true},
	} {
		err := checkListenSafety(tc.addr, tc.authOn)
		if (err == nil) != tc.ok {
			t.Errorf("checkListenSafety(%q, auth=%v) = %v, want ok=%v", tc.addr, tc.authOn, err, tc.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "authentication off") {
			t.Errorf("the refusal does not say why: %v", err)
		}
	}
}

// The mode that resolveAuth turns into "off" is what the refusal looks at:
// "auto" with no token is off, with one it is on.
func TestListenSafetyFollowsResolveAuth(t *testing.T) {
	for _, tc := range []struct {
		auth config.AuthConfig
		ok   bool
	}{
		{config.AuthConfig{Mode: "off"}, false},
		{config.AuthConfig{Mode: "auto"}, false},
		{config.AuthConfig{Mode: "auto", Tokens: []config.TokenSpec{{Token: "t"}}}, true},
		{config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: "t"}}}, true},
	} {
		on, err := resolveAuth(tc.auth)
		if err != nil {
			t.Fatal(err)
		}
		if got := checkListenSafety("0.0.0.0:1", on) == nil; got != tc.ok {
			t.Errorf("auth %+v: off-loopback allowed = %v, want %v", tc.auth, got, tc.ok)
		}
	}
}

// A service definition written for an older version, with --init and
// --tools-profile <value>, still starts: the value goes with its flag, or it
// would end the flags and hide the ones after it.
func TestDropRemovedFlagsInitAndToolsProfile(t *testing.T) {
	captureLog(t)
	got := dropRemovedFlags([]string{"--init", "--tools-profile", "full", "--http", "127.0.0.1:1", "-tools-profile=agent", "--data=/d"})
	want := []string{"--http", "127.0.0.1:1", "--data=/d"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("kept %q, want %q", got, want)
	}
}

// D906: the removed server keys are ignored with a warning and dropped by the
// realignment, and the aliases a file holds are moved to the state file first.
func TestAnnounceConfigMovesAliasesAndDropsTheRemovedKeys(t *testing.T) {
	buf := captureLog(t)
	clearLegacyEnv(t)
	t.Setenv("CARTOGRAPHER_TOOLS_PROFILE", "")
	t.Setenv("CARTOGRAPHER_MCP_ALLOWED_ORIGINS", "")
	data := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.yaml")
	old := "http: \"127.0.0.1:9090\"\ndata: " + data + "\ninit: true\ntools:\n  profile: full\nmcp:\n  allowed_origins: [\"*\"]\n" +
		"kb_aliases:\n  - from: kb-old\n    to: kb-a\n"
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
		"warning: init: removed setting, ignored",
		"warning: tools.profile: removed setting, ignored",
		"warning: mcp.allowed_origins: removed setting, ignored",
		"warning: kb_aliases: removed setting, ignored",
		"moved to",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
	realigned, _ := os.ReadFile(path)
	for _, gone := range []string{"init:", "profile:", "allowed_origins", "kb_aliases", "kb-old"} {
		if strings.Contains(string(realigned), gone) {
			t.Errorf("the realigned file still holds %q:\n%s", gone, realigned)
		}
	}
	st, err := config.ReadServerState(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.KBAliases) != 1 || st.KBAliases[0] != (config.KBAlias{From: "kb-old", To: "kb-a"}) {
		t.Errorf("the alias was not moved to the state file: %v", st.KBAliases)
	}

	// The restart: the file no longer carries the list, the state file does.
	cfg2, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cfg2.KBAliases(); err != nil || len(got) != 1 {
		t.Errorf("after the restart KBAliases = %v, %v", got, err)
	}
	if len(cfg2.Warnings) != 0 {
		t.Errorf("the second start warned: %v", cfg2.Warnings)
	}
}

// With no data directory the list has nowhere to go: the file keeps it, is not
// realigned, and the aliases still resolve.
func TestAnnounceConfigKeepsAliasesWithoutDataDir(t *testing.T) {
	buf := captureLog(t)
	clearLegacyEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("http: \"127.0.0.1:9090\"\nkb_aliases:\n  - from: kb-old\n    to: kb-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	announceConfig(cfg, path)
	if kept, _ := os.ReadFile(path); !strings.Contains(string(kept), "kb_aliases") {
		t.Errorf("the list was dropped with nowhere to go:\n%s", kept)
	}
	if !strings.Contains(buf.String(), "not realigned") {
		t.Errorf("no word on why:\n%s", buf.String())
	}
	if got, _ := cfg.KBAliases(); len(got) != 1 {
		t.Errorf("KBAliases = %v, want the legacy item", got)
	}
}
