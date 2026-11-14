package provisioning_test

// A hook whose registration was deleted is drift, and sync restores it (#2101).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func hookFinding(findings []provisioning.DriftFinding) (provisioning.DriftFinding, bool) {
	for _, f := range findings {
		if f.Kind == "hook" && f.Name == "notify" {
			return f, true
		}
	}
	return provisioning.DriftFinding{}, false
}

type unregCase struct {
	name     string
	provider configurator.Provider
	rel      string // registration file, removed to simulate the deletion
}

var unregCases = []unregCase{
	{"claude-code", configurator.ProviderClaudeCode, filepath.Join(".claude", "settings.json")},
	{"antigravity", configurator.ProviderAntigravity, filepath.Join(".gemini", "config", "hooks.json")},
	{"codex", configurator.ProviderCodex, filepath.Join(".codex", "hooks.json")},
	{"kiro", configurator.ProviderKiro, filepath.Join(".kiro", "hooks", "cartographer.json")},
}

func applyUnregHook(t *testing.T, tc unregCase, event string) (provisioning.ApplyOptions, contract.Manifest, string) {
	t.Helper()
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "notify", event, "", "./notify.sh")
	m, err := contract.BuildManifest(nil, map[string]string{"kb": kbRoot}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	opts := provisioning.ApplyOptions{
		ApprovedMCP: approveAll(m),
		KBRoots:     map[string]string{"kb": kbRoot},
		Provider:    tc.provider,
		BaseDir:     baseDir,
	}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Lock = res.NewLock
	return opts, m, baseDir
}

func countRegistered(t *testing.T, tc unregCase, baseDir string) int {
	t.Helper()
	n, _, err := provisioning.HookRegistrations(baseDir, tc.provider, "notify")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestVerifyManaged_DeletedHookRegistrationIsUnregisteredAndHealed(t *testing.T) {
	for _, tc := range unregCases {
		t.Run(tc.name, func(t *testing.T) {
			opts, m, baseDir := applyUnregHook(t, tc, "SessionStart")
			if _, ok := hookFinding(provisioning.VerifyManaged(opts.Lock, tc.provider, baseDir)); ok {
				t.Fatal("fresh install reported as drift")
			}
			if err := os.Remove(filepath.Join(baseDir, tc.rel)); err != nil {
				t.Fatal(err)
			}
			f, ok := hookFinding(provisioning.VerifyManaged(opts.Lock, tc.provider, baseDir))
			if !ok || f.Reason != provisioning.DriftUnregistered || !f.Healable() {
				t.Fatalf("finding = %+v, %v; want a healable %q", f, ok, provisioning.DriftUnregistered)
			}

			// --no-heal lists it and writes nothing.
			noHeal := opts
			noHeal.NoHeal = true
			res, err := provisioning.Apply(m, noHeal)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := hookFinding(res.Divergent); !ok {
				t.Errorf("--no-heal: Divergent = %+v, want the hook", res.Divergent)
			}
			if n := countRegistered(t, tc, baseDir); n != 0 {
				t.Fatalf("--no-heal wrote the registration (%d)", n)
			}

			// A sync restores exactly one entry, and a second run changes nothing.
			for i := 0; i < 2; i++ {
				res, err = provisioning.Apply(m, opts)
				if err != nil {
					t.Fatal(err)
				}
				opts.Lock = res.NewLock
				if n := countRegistered(t, tc, baseDir); n != 1 {
					t.Fatalf("run %d: %d registrations, want 1", i+1, n)
				}
			}
			if _, ok := hookFinding(provisioning.VerifyManaged(opts.Lock, tc.provider, baseDir)); ok {
				t.Error("still drift after the heal")
			}
		})
	}
}

// The registrar leaves nothing for an event the client never fires: that is not
// drift, or it would show forever.
func TestVerifyManaged_HookWithUnreachableEventIsNotUnregistered(t *testing.T) {
	cases := []struct {
		tc    unregCase
		event string
	}{
		{unregCases[0], "PreInvocation"}, // Antigravity-only event
		{unregCases[1], "SubagentStop"},  // not in Antigravity's table
	}
	for _, c := range cases {
		t.Run(c.tc.name, func(t *testing.T) {
			opts, _, baseDir := applyUnregHook(t, c.tc, c.event)
			if n := countRegistered(t, c.tc, baseDir); n != 0 {
				t.Skipf("event %q is registered by %s; pick another", c.event, c.tc.name)
			}
			if f, ok := hookFinding(provisioning.VerifyManaged(opts.Lock, c.tc.provider, baseDir)); ok {
				t.Errorf("unreachable event reported: %+v", f)
			}
		})
	}
}

func TestVerifyManaged_HookWithMalformedSpecIsNotUnregistered(t *testing.T) {
	for _, tc := range unregCases {
		t.Run(tc.name, func(t *testing.T) {
			opts, _, baseDir := applyUnregHook(t, tc, "SessionStart")
			if err := os.Remove(filepath.Join(baseDir, tc.rel)); err != nil {
				t.Fatal(err)
			}
			dest := contract.DestDirScoped("hook", "notify", tc.provider, contract.ScopeGlobal)
			spec := filepath.Join(baseDir, dest, "hook.json")
			if err := os.WriteFile(spec, []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, f := range provisioning.VerifyManaged(opts.Lock, tc.provider, baseDir) {
				if f.Name == "notify" && f.Reason == provisioning.DriftUnregistered {
					t.Errorf("malformed hook.json reported as unregistered: %+v", f)
				}
			}
		})
	}
}

func TestVerifyManaged_ClaudeHookInSettingsLocalIsRegistered(t *testing.T) {
	tc := unregCases[0]
	opts, _, baseDir := applyUnregHook(t, tc, "SessionStart")
	data, err := os.ReadFile(filepath.Join(baseDir, tc.rel))
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(baseDir, ".claude", "settings.local.json")
	if err := os.WriteFile(local, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, tc.rel), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, ok := hookFinding(provisioning.VerifyManaged(opts.Lock, tc.provider, baseDir)); ok && strings.EqualFold(f.Reason, provisioning.DriftUnregistered) {
		t.Errorf("a registration in settings.local.json reported as unregistered: %+v", f)
	}
}
