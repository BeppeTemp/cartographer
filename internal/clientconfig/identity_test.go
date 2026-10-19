package clientconfig_test

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func TestIdentityEntryDecidesAndNeverFallsThrough(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.Identities = map[string]string{
		"default":  "Home Person <home@example.test>",
		"work":     "  Work Person <work@example.test>  ",
		"private":  " ",
		"typo":     "Work Person <work@example.test",
		"notes-kb": "",
	}
	for _, tc := range []struct {
		kb, value string
		found     bool
		problem   bool
	}{
		{kb: "work", value: "Work Person <work@example.test>", found: true},
		{kb: "other", value: "Home Person <home@example.test>", found: true},
		{kb: "", value: "Home Person <home@example.test>", found: true},
		{kb: "private", found: true},
		{kb: "notes-kb", found: true},
		{kb: "typo", found: true, problem: true},
	} {
		value, found, problem := cfg.Identity(tc.kb)
		if value != tc.value || found != tc.found || (problem != "") != tc.problem {
			t.Errorf("Identity(%q) = %q, %v, %q; want %q, %v, problem %v", tc.kb, value, found, problem, tc.value, tc.found, tc.problem)
		}
	}

	delete(cfg.Identities, "default")
	if value, found, _ := cfg.Identity("other"); found || value != "" {
		t.Errorf("no entry and no default = %q, %v; want not found, so the caller may ask git", value, found)
	}
}

// identities is only ever written by hand: a command that saves the file must
// keep it, an empty entry included, and an invalid entry is reported.
func TestIdentitiesSurviveACommandSave(t *testing.T) {
	dir := t.TempDir()
	data := "server_url: http://x/mcp\nidentities:\n  default: Ada Example <ada@example.test>\n  private: \"\"\n  typo: Ada <ada@\n"
	if err := os.WriteFile(clientconfig.Path(dir), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Warnings, "\n"); !strings.Contains(got, "identities.typo: ") || strings.Contains(got, "identities.private") {
		t.Errorf("warnings = %q, want one for identities.typo only", got)
	}
	cfg.AddAgent("claude")
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"default": "Ada Example <ada@example.test>", "private": "", "typo": "Ada <ada@"}
	if len(loaded.Identities) != len(want) {
		t.Fatalf("identities after a save = %q, want %q", loaded.Identities, want)
	}
	for k, v := range want {
		if got, ok := loaded.Identities[k]; !ok || got != v {
			t.Errorf("identities[%q] after a save = %q (present %v), want %q", k, got, ok, v)
		}
	}
}
