package provisioning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// #1502: registering a hook edits a file other tools and the user also write
// (Crush's config, Codex's hooks.json): its key order, tab indent, `&&`, big
// integer and final newline survive; the sorted-map round trip changed all five.
func TestHookRegistration_KeepsTheLayoutOfSharedJSON(t *testing.T) {
	cases := map[string]struct {
		provider configurator.Provider
		rel      string
		user     string
	}{
		"crush": {configurator.ProviderCrush, filepath.Join(".config", "crush", "crush.json"),
			"{\r\n\t\"zeta\": 12345678901234567890,\r\n\t\"alpha\": {\"cmd\": \"a && b > c\"},\r\n\t\"hooks\": {}\r\n}\r\n"},
		"codex": {configurator.ProviderCodex, filepath.Join(".codex", "hooks.json"),
			"{\n\t\"zeta\": 12345678901234567890,\n\t\"alpha\": {\"cmd\": \"a && b > c\"}\n}\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			kbRoot := t.TempDir()
			writeHookKB(t, kbRoot, "guard", "PreToolUse", "Edit|Write", "./notify.sh")
			m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
			if err != nil {
				t.Fatal(err)
			}
			baseDir := t.TempDir()
			cfg := filepath.Join(baseDir, tc.rel)
			if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg, []byte(tc.user), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
				KBRoots: map[string]string{"kb": kbRoot}, Provider: tc.provider, BaseDir: baseDir, Lock: provisioning.Lock{}}); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(cfg)
			s := string(got)
			for _, want := range []string{"12345678901234567890", "a && b > c", "\t\"alpha\""} {
				if !strings.Contains(s, want) {
					t.Errorf("%q lost:\n%s", want, s)
				}
			}
			if strings.Index(s, "\"zeta\"") > strings.Index(s, "\"alpha\"") {
				t.Errorf("key order changed:\n%s", s)
			}
			if !strings.HasSuffix(s, "\n") {
				t.Errorf("final newline lost:\n%q", s)
			}
			if strings.Contains(tc.user, "\r\n") && strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n") {
				t.Errorf("CRLF lost:\n%q", s)
			}
		})
	}
}
