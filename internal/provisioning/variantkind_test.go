package provisioning_test

import (
	"crypto/ed25519"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const variantGood = "schema: 1\nname: fork-cli\ndisplay_name: Fork CLI\nbase: crush\ndetect:\n  binaries: [fork-cli]\nmcp:\n  mode: cli\n"

func variantKB(t *testing.T, files map[string]string, track bool) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if track {
		for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init"}} {
			if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
	}
	return root
}

func variantArtifacts(m provisioning.Manifest) map[string]provisioning.Artifact {
	out := map[string]provisioning.Artifact{}
	for _, a := range m.Artifacts {
		if a.Kind == "variant" {
			out[a.Name] = a
		}
	}
	return out
}

// A valid descriptor is an artifact, signed with the others; an invalid one is
// left out with a named issue and does not stop the KB's skills (Verified #8).
func TestBuildManifest_VariantKind(t *testing.T) {
	root := variantKB(t, map[string]string{
		"variants/fork-cli.yaml": variantGood,
		"variants/broken.yaml":   strings.Replace(variantGood, "schema: 1", "schema: 2", 1),
		"skills/demo/SKILL.md":   "---\nname: demo\ndescription: Use when testing.\n---\nBody.\n",
	}, true)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": root}, provisioning.BuildOptions{Signers: map[string]ed25519.PrivateKey{"kb-a": priv}})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	got := variantArtifacts(m)
	if len(got) != 1 || got["fork-cli"].Source != "kb:kb-a" || got["fork-cli"].ContentHash == "" || got["fork-cli"].Signature == nil {
		t.Fatalf("want one signed fork-cli variant, got %+v", got)
	}
	found := false
	for _, i := range m.Issues {
		found = found || (strings.Contains(i, "broken") && strings.Contains(i, "schema"))
	}
	if !found {
		t.Errorf("no issue names the left-out descriptor: %v", m.Issues)
	}
	skill := false
	for _, a := range m.Artifacts {
		skill = skill || (a.Kind == "skill" && a.Name == "demo")
	}
	if !skill {
		t.Error("a bad descriptor stopped the KB's skills")
	}
	files, err := provisioning.ReadArtifactFiles(got["fork-cli"], nil, map[string]string{"kb-a": root})
	if err != nil || len(files) != 1 || string(files[0].Content) != variantGood {
		t.Fatalf("ReadArtifactFiles = %v, %v", files, err)
	}
}

func TestBuildManifest_UntrackedVariantIsIgnored(t *testing.T) {
	root := variantKB(t, map[string]string{"skills/demo/SKILL.md": "---\nname: demo\ndescription: Use when testing.\n---\nBody.\n"}, true)
	if err := os.MkdirAll(filepath.Join(root, "variants"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "variants", "fork-cli.yaml"), []byte(variantGood), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": root}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := variantArtifacts(m); len(got) != 0 {
		t.Fatalf("an untracked descriptor was served: %+v", got)
	}
}
