package provisioning_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

func forgedBundleArtifacts() []provisioning.Artifact {
	hookFiles := []provisioning.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"echo forged"}`)}}
	mcpFiles := []provisioning.ArtifactFile{{Path: "mcp.json", Content: []byte(`{"type":"http","url":"https://example.com/mcp"}`)}}
	return []provisioning.Artifact{
		{Kind: "hook", Name: "forged-hook", Source: "bundle", BuiltIn: true, ContentHash: provisioning.ContentHashFiles(hookFiles), Files: hookFiles},
		{Kind: "mcp", Name: "forged-mcp", Source: "bundle", BuiltIn: true, ContentHash: provisioning.ContentHashFiles(mcpFiles), Files: mcpFiles},
	}
}

// A bundle artifact must match the embedded bundle, with or without pins:
// otherwise it is dropped and reported, and the rest of the manifest stands.
func TestVerifiedManifest_BundleArtifactMustMatchEmbeddedBundle(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pins := map[string][]ed25519.PublicKey{"kb-a": {priv.Public().(ed25519.PublicKey)}}
	for _, p := range []map[string][]ed25519.PublicKey{pins, nil} {
		for _, a := range forgedBundleArtifacts() {
			v, err := provisioning.VerifiedManifest(provisioning.Manifest{Artifacts: []provisioning.Artifact{a}}, p)
			if err != nil {
				t.Fatalf("forged bundle %s/%s failed the sync: %v", a.Kind, a.Name, err)
			}
			if len(v.Artifacts) != 0 || len(v.Skipped) != 1 || !strings.Contains(v.Skipped[0], "skipped") {
				t.Fatalf("forged bundle %s/%s (pins=%v): %+v", a.Kind, a.Name, p != nil, v)
			}
		}
	}
}

// Version skew: a real embedded skill name with another hash is replaced by
// this client's own copy and reported, and a KB artifact in the same manifest
// still installs.
func TestApply_BundleSkewSkippedKBArtifactInstalls(t *testing.T) {
	kbFiles := []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: kb-skill\ndescription: test\n---\n")}}
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{
		{Kind: "skill", Name: "kb-create", Source: "bundle", ContentHash: "other-version"},
		{Kind: "skill", Name: "kb-skill", Source: "kb:kb-a", ContentHash: provisioning.ContentHashFiles(kbFiles), Files: kbFiles},
	}}
	v, err := provisioning.VerifiedManifest(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	res, err := provisioning.Apply(v, provisioning.ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/kb-skill/SKILL.md")); err != nil {
		t.Fatalf("kb skill not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/kb-create/SKILL.md")); err != nil {
		t.Fatalf("embedded version of the skewed skill not installed: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "bundled skill kb-create") && strings.Contains(w, "installing this client's version") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skip not reported: %v", res.Warnings)
	}
}

func TestVerifiedManifest_EmbeddedBundleIsBuiltIn(t *testing.T) {
	m, err := provisioning.BuildManifest(skillbundle.FS, nil, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Artifacts {
		m.Artifacts[i].BuiltIn = false // the server flag is never trusted
	}
	v, err := provisioning.VerifiedManifest(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Artifacts) == 0 {
		t.Fatal("no embedded artifacts")
	}
	for _, a := range v.Artifacts {
		if !a.BuiltIn {
			t.Fatalf("embedded %s/%s not built-in", a.Kind, a.Name)
		}
	}
}

// A forged "bundle" artifact fed to Apply directly is not built-in either:
// BuiltIn is only ever set by VerifiedManifest, and an MCP descriptor from a
// non-KB source still needs approval.
func TestApply_ForgedBundleMCPStillNeedsApproval(t *testing.T) {
	home := t.TempDir()
	a := forgedBundleArtifacts()[1]
	a.BuiltIn = false
	res, err := provisioning.Apply(provisioning.Manifest{Artifacts: []provisioning.Artifact{a}}, provisioning.ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 {
		t.Fatalf("unapproved descriptor written: %+v", res.Written)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
		t.Fatal("MCP config written for an unapproved descriptor")
	}
}
