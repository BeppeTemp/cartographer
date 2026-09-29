package provisioning_test

// A hook.json that cannot be registered is reported at sync, on every
// provider, instead of being skipped in silence (D284).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

var hookProviders = []configurator.Provider{
	configurator.ProviderClaudeCode, configurator.ProviderCodex,
	configurator.ProviderOpenCode, configurator.ProviderAntigravity,
}

func hookManifest(t *testing.T, hookJSON string) (provisioning.Manifest, string) {
	t.Helper()
	kbRoot := t.TempDir()
	dir := filepath.Join(kbRoot, "hooks", "guard")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if hookJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "hook.json"), []byte(hookJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return m, kbRoot
}

func TestApply_MalformedHookJSONWarnsOnEveryProvider(t *testing.T) {
	cases := map[string]string{
		"invalid JSON":    `{"event":`,
		"missing event":   `{"command":"./run.sh"}`,
		"missing command": `{"event":"Stop"}`,
		"no hook.json":    "",
	}
	for name, src := range cases {
		m, kbRoot := hookManifest(t, src)
		for _, p := range hookProviders {
			res, _ := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
			got := warningsMentioning(res, `hook "guard"`)
			if len(got) != 1 || !strings.Contains(got[0], string(p)) || !strings.Contains(got[0], "never fire") {
				t.Errorf("%s / %s: want one warning naming hook and client, got %v", name, p, res.Warnings)
			}
		}
	}
}

func TestApply_ValidHookJSONDoesNotWarn(t *testing.T) {
	m, kbRoot := hookManifest(t, `{"event":"PreToolUse","command":"./run.sh"}`)
	for _, p := range hookProviders {
		res, _ := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
		if w := warningsMentioning(res, `hook "guard"`); len(w) != 0 {
			t.Errorf("%s: valid hook must not warn: %v", p, w)
		}
	}
}

func TestApply_TypoEventWarnsButRegistersOnClaudeAndCodex(t *testing.T) {
	m, kbRoot := hookManifest(t, `{"event":"PostTooluse","command":"./run.sh"}`)
	for p, file := range map[configurator.Provider]string{
		configurator.ProviderClaudeCode: filepath.Join(".claude", "settings.json"),
		configurator.ProviderCodex:      filepath.Join(".codex", "hooks.json"),
	} {
		res, base := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
		if w := warningsMentioning(res, `"PostTooluse"`); len(w) != 1 {
			t.Errorf("%s: want one warning naming the unknown event, got %v", p, res.Warnings)
		}
		data, err := os.ReadFile(filepath.Join(base, file))
		if err != nil || !strings.Contains(string(data), "PostTooluse") {
			t.Errorf("%s: an unknown event is registered as declared (%v)", p, err)
		}
	}
}

func TestApply_AntigravityOnlyEventIsNotRegisteredOnClaudeOrCodex(t *testing.T) {
	m, kbRoot := hookManifest(t, `{"event":"PreInvocation","command":"./run.sh"}`)
	for p, file := range map[configurator.Provider]string{
		configurator.ProviderClaudeCode: filepath.Join(".claude", "settings.json"),
		configurator.ProviderCodex:      filepath.Join(".codex", "hooks.json"),
	} {
		res, base := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
		if w := warningsMentioning(res, `"PreInvocation"`); len(w) != 1 {
			t.Errorf("%s: want one warning, got %v", p, res.Warnings)
		}
		if data, err := os.ReadFile(filepath.Join(base, file)); err == nil && strings.Contains(string(data), "PreInvocation") {
			t.Errorf("%s: PreInvocation must not be registered under an event the client never emits", p)
		}
	}
}
