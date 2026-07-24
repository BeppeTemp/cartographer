package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestResolveTargetProviders_AgentSubsets(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		csv     string
		want    []string
		wantErr string
	}{
		{"csv subset", "", "claude,codex", []string{"claude", "codex"}, ""},
		{"csv trims and deduplicates", "", "claude, codex,claude", []string{"claude", "codex"}, ""},
		{"invalid csv name", "", "claude,unknown", nil, "unknown provider"},
		{"empty csv item", "", "claude,,codex", nil, "invalid --agents"},
		{"csv plus positional", "claude", "codex", nil, "cannot be used with positional"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTargetProviders(tc.target, tc.csv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTargetProviders: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("providers = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveDisconnectProviders_AgentSubsets(t *testing.T) {
	got, err := resolveDisconnectProviders("", "claude,kiro", []string{"claude", "codex", "kiro"})
	if err != nil {
		t.Fatalf("resolveDisconnectProviders: %v", err)
	}
	if want := []string{"claude", "kiro"}; !reflect.DeepEqual(got, want) {
		t.Errorf("providers = %v, want %v", got, want)
	}
	if _, err := resolveDisconnectProviders("all", "codex", nil); err == nil || !strings.Contains(err.Error(), "cannot be used with positional") {
		t.Errorf("csv+positional error = %v, want conflict", err)
	}
}

// kbSkillManifest builds a single-artifact manifest with one unsigned,
// kb:-sourced skill — the shape sync_pull returns before any trust decision
// is applied.
func kbSkillManifest() provisioning.Manifest {
	return provisioning.Manifest{
		Revision: "rev1",
		Artifacts: []provisioning.Artifact{
			{
				Kind:        "skill",
				Name:        "example",
				Source:      "kb:homelab",
				ContentHash: "hash1",
				Signed:      false,
				Files:       []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("# example")}},
			},
		},
	}
}

func TestUpgradeTrustedManifest_TrustSignsKBArtifacts(t *testing.T) {
	m := kbSkillManifest()
	got := upgradeTrustedManifest(m, true)
	if !got.Artifacts[0].Signed {
		t.Error("expected kb: artifact to be Signed=true when trust=true")
	}
	// original untouched.
	if m.Artifacts[0].Signed {
		t.Error("upgradeTrustedManifest must not mutate its input")
	}
}

func TestUpgradeTrustedManifest_NoTrustLeavesUnsigned(t *testing.T) {
	m := kbSkillManifest()
	got := upgradeTrustedManifest(m, false)
	if got.Artifacts[0].Signed {
		t.Error("expected kb: artifact to stay Signed=false when trust=false")
	}
}

func TestMaterializeForProviders_TrustAvoidsNeedsApproval(t *testing.T) {
	dir := t.TempDir()
	m := kbSkillManifest()

	results, err := materializeForProviders(m, []string{"claude"}, dir, true, true /* dryRun */, nil, nil)
	if err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}
	r := results["claude"]
	if len(r.NeedsApproval) != 0 {
		t.Errorf("expected 0 needs-approval with trust=true, got %d: %+v", len(r.NeedsApproval), r.NeedsApproval)
	}
	if len(r.Written) != 1 {
		t.Errorf("expected 1 written artifact with trust=true, got %d", len(r.Written))
	}
}

func TestMaterializeForProviders_NoTrustNeedsApproval(t *testing.T) {
	dir := t.TempDir()
	m := kbSkillManifest()

	results, err := materializeForProviders(m, []string{"claude"}, dir, false, true /* dryRun */, nil, nil)
	if err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}
	r := results["claude"]
	if len(r.NeedsApproval) != 1 {
		t.Errorf("expected 1 needs-approval with trust=false, got %d", len(r.NeedsApproval))
	}
	if len(r.Written) != 0 {
		t.Errorf("expected 0 written artifacts with trust=false, got %d", len(r.Written))
	}
}

// instructionsManifest builds a single-artifact manifest with one signed,
// kb:-sourced "instructions" artifact (D56) — the shape sync_pull returns for the
// imprinting artifact once decoded client-side (Files populated from base64).
func instructionsManifest(kbName, content string) provisioning.Manifest {
	return provisioning.Manifest{
		Revision: "rev-instr",
		Artifacts: []provisioning.Artifact{
			{
				Kind:        "instructions",
				Name:        kbName,
				Source:      "kb:" + kbName,
				ContentHash: "hash-instr",
				Signed:      true,
				Files:       []provisioning.ArtifactFile{{Path: "instructions.md", Content: []byte(content)}},
			},
		},
	}
}

// TestMaterializeForProviders_Instructions_ClaudeEKiro is the e2e path for the
// "instructions" kind (D56) through the real client entry point used by
// `cartographer connect`/`sync`: it must materialize the managed block into both
// claude's shared CLAUDE.md and kiro's dedicated steering file, and record one
// ManagedFile per provider in the multi-provider lockfile.
func TestMaterializeForProviders_Instructions_ClaudeEKiro(t *testing.T) {
	dir := t.TempDir()
	m := instructionsManifest("homelab", "Contenuto di imprinting per homelab.\n")

	results, err := materializeForProviders(m, []string{"claude", "kiro"}, dir, true, false /* dryRun */, nil, nil)
	if err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}

	claudePath := filepath.Join(dir, ".claude", "CLAUDE.md")
	kiroPath := filepath.Join(dir, ".kiro", "steering", "cartographer.md")

	for provider, path := range map[string]string{"claude": claudePath, "kiro": kiroPath} {
		r := results[provider]
		if len(r.Written) != 1 || r.Written[0].Kind != "instructions" {
			t.Errorf("%s: Written inatteso: %+v", provider, r.Written)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: file non materializzato in %s: %v", provider, path, err)
		}
		content := string(data)
		if !strings.Contains(content, "cartographer:instructions:begin") {
			t.Errorf("%s: marker del blocco assente: %s", provider, content)
		}
		if !strings.Contains(content, "Contenuto di imprinting per homelab.") {
			t.Errorf("%s: contenuto instructions assente: %s", provider, content)
		}
	}

	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("ReadLockFile: %v", err)
	}
	for _, provider := range []string{"claude", "kiro"} {
		lock := lf.ForProvider(provider)
		if len(lock.Managed) != 1 || lock.Managed[0].Kind != "instructions" {
			t.Errorf("%s: lockfile Managed inatteso: %+v", provider, lock.Managed)
		}
	}
}

// TestDiffWithTrust_NoNeedsApproval mirrors what cmdStatus/loadRemoteStatusCmd
// do: upgradeTrustedManifest before ComputeDiff. With trust active, an added
// kb: artifact must report Signed=true in the diff — not a leftover "needs
// approval" — matching the real materialization outcome.
func TestDiffWithTrust_NoNeedsApproval(t *testing.T) {
	m := kbSkillManifest()
	trusted := upgradeTrustedManifest(m, true)
	pm := provisioning.FilterForProvider(trusted, configurator.ProviderClaudeCode)
	d := provisioning.ComputeDiff(pm, provisioning.Lock{})

	if len(d.Added) != 1 {
		t.Fatalf("expected 1 added artifact, got %d", len(d.Added))
	}
	if !d.Added[0].Signed {
		t.Error("expected Added[0].Signed=true with trust active (no leftover needs-approval)")
	}
	if got := formatDiffStatus(d); got != "drift +1 ~0 -0" {
		t.Errorf("formatDiffStatus = %q, want no needs-approval suffix", got)
	}
}

func TestDiffWithoutTrust_NeedsApproval(t *testing.T) {
	m := kbSkillManifest()
	pm := provisioning.FilterForProvider(m, configurator.ProviderClaudeCode)
	d := provisioning.ComputeDiff(pm, provisioning.Lock{})

	got := formatDiffStatus(d)
	if got == "drift +1 ~0 -0" {
		t.Errorf("formatDiffStatus = %q, expected a needs-approval hint without trust", got)
	}
}

// TestDoConnect_PersistsTrust exercises doConnect against an unreachable
// server (so skill materialization is deferred, not fatal — see doConnect's
// doc comment) and checks that the chosen Trust value still ends up in
// .cartographer.yaml (step 3 of doConnect persists it unconditionally).
func TestDoConnect_PersistsTrust(t *testing.T) {
	dir := t.TempDir()

	opts := connectOptions{
		Providers: []string{"claude"},
		Dir:       dir,
		ServerURL: "http://127.0.0.1:1/mcp", // nobody listens here: connection refused
		Name:      "cartographer",
		TokenEnv:  "CARTOGRAPHER_TOKENS",
		Trust:     false,
	}

	res, err := doConnect(opts)
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if !res.Deferred {
		t.Fatal("expected sync to be deferred (unreachable server)")
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trust {
		t.Error("expected persisted Trust=false")
	}

	// Now reconnect choosing Trust=true and confirm it flips.
	opts.Trust = true
	if _, err := doConnect(opts); err != nil {
		t.Fatalf("doConnect (2nd): %v", err)
	}
	cfg, err = clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load (2nd): %v", err)
	}
	if !cfg.Trust {
		t.Error("expected persisted Trust=true after reconnecting with Trust=true")
	}
}
