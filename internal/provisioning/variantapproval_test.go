package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

const variantDescriptor = "schema: 1\nname: fork-cli\ndisplay_name: Fork CLI\nbase: crush\ndetect:\n  binaries: [fork-cli]\nmcp:\n  mode: cli\npaths:\n  global:\n    skill:\n      dir: \"{home}/.fork-cli/skills\"\n"

func variantWith(desc string) Artifact {
	files := []ArtifactFile{{Path: "fork-cli.yaml", Content: []byte(desc)}}
	return Artifact{Kind: "variant", Name: "fork-cli", Source: "kb:kb-a", ContentHash: hashArtifactFiles(files), Files: files}
}

// fixedEnv pins the environment a variant's anchors resolve in.
func fixedEnv(t *testing.T, xdg string) {
	t.Helper()
	old := VariantEnv
	VariantEnv = func() clientvariant.Env {
		return clientvariant.Env{GOOS: "linux", Home: "/home/user", Getenv: func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}}
	}
	t.Cleanup(func() { VariantEnv = old })
}

// approvalFor is what `approve variant` records for a in the current environment.
func approvalFor(a Artifact) (hashes, paths map[string]string) {
	key := "kb:kb-a\x00variant/fork-cli"
	return map[string]string{key: a.ContentHash}, map[string]string{key: VariantPathFingerprint(a, ApplyOptions{})}
}

func applyVariant(t *testing.T, a Artifact, approved, paths map[string]string) AppliedResult {
	t.Helper()
	res, err := Apply(Manifest{Revision: a.ContentHash, Artifacts: []Artifact{a}}, ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: t.TempDir(), ApprovedMCP: approved, ApprovedVariantPaths: paths,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// D1695: a variant waits for the hash-bound approval; a descriptor edit that
// changes only a destination path changes the hash, so it asks again.
func TestVariantNeedsApprovalAndAPathChangeAsksAgain(t *testing.T) {
	fixedEnv(t, "")
	v1 := variantWith(variantDescriptor)
	if res := applyVariant(t, v1, nil, nil); len(res.NeedsApproval) != 1 || len(res.Unsupported) != 0 {
		t.Fatalf("an unapproved variant must be pending: %+v", res)
	}
	approved, paths := approvalFor(v1)
	if res := applyVariant(t, v1, approved, paths); len(res.NeedsApproval) != 0 {
		t.Fatalf("an approved variant is still pending: %+v", res.NeedsApproval)
	}
	v2 := variantWith(strings.Replace(variantDescriptor, ".fork-cli/skills", ".fork-cli/elsewhere", 1))
	if v2.ContentHash == v1.ContentHash {
		t.Fatal("a path edit left the hash unchanged")
	}
	if res := applyVariant(t, v2, approved, paths); len(res.NeedsApproval) != 1 {
		t.Fatalf("a changed path must need a new approval: %+v", res.NeedsApproval)
	}
}

const xdgDescriptor = "schema: 1\nname: fork-cli\ndisplay_name: Fork CLI\nbase: crush\ndetect:\n  binaries: [fork-cli]\nmcp:\n  mode: cli\npaths:\n  global:\n    skill:\n      dir: \"{xdg_config}/fork-cli/skills\"\n"

// The approval binds the paths the operator saw: the same hash resolved under
// another XDG_CONFIG_HOME is pending again, under the same one it is authorized,
// and an entry with no fingerprint never authorizes a variant.
func TestVariantApprovalIsBoundToTheResolvedPaths(t *testing.T) {
	v := variantWith(xdgDescriptor)
	fixedEnv(t, "")
	approved, paths := approvalFor(v)
	if res := applyVariant(t, v, approved, paths); len(res.NeedsApproval) != 0 {
		t.Fatalf("same environment: %+v", res.NeedsApproval)
	}
	if res := applyVariant(t, v, approved, nil); len(res.NeedsApproval) != 1 {
		t.Fatalf("an approval with no fingerprint must not authorize: %+v", res.NeedsApproval)
	}
	fixedEnv(t, "/home/user/other-config")
	if res := applyVariant(t, v, approved, paths); len(res.NeedsApproval) != 1 {
		t.Fatalf("another XDG_CONFIG_HOME must need a new approval: %+v", res.NeedsApproval)
	}
}

// A verified signature under a pin is origin, never consent for a variant.
func TestVariantSignedStillNeedsApproval(t *testing.T) {
	v := variantWith(variantDescriptor)
	fixedEnv(t, "")
	v.Signed = true
	if res := applyVariant(t, v, nil, nil); len(res.NeedsApproval) != 1 {
		t.Fatalf("a signed variant must wait for approval: %+v", res.NeedsApproval)
	}
	if !PendingApproval(v, ApplyOptions{}) || ApprovalReason(v, nil) != "client variant" || ApprovalName(v) != "variant/fork-cli" {
		t.Error("the variant is not listed as pending")
	}
	other := Artifact{Kind: "skill", Name: "plain", Source: "kb:kb-a", ContentHash: "h", Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: plain\ndescription: d\n---\n")}}}
	if ArtifactRequiresApproval(other, ApplyOptions{}) {
		t.Error("an ordinary skill must not need approval")
	}
}

// The block names the provider for a cli variant only.
func TestCLIProviderSentence(t *testing.T) {
	s := cliProviderSentence("fork-cli")
	if !strings.Contains(s, "cartographer call --provider fork-cli") || !strings.Contains(s, CLISkillName) {
		t.Errorf("sentence = %q", s)
	}
}
