package provisioning_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestBuildManifestSignatureVerificationAndRotation(t *testing.T) {
	root := t.TempDir()
	for _, kbName := range []string{"signed", "unsigned"} {
		dir := filepath.Join(root, kbName, "skills", "example")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: example\ndescription: test\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := artifactsig.ParseSeed(strings.Repeat("04", ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	second, err := artifactsig.ParseSeed(strings.Repeat("05", ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{"signed": filepath.Join(root, "signed"), "unsigned": filepath.Join(root, "unsigned")}
	firstManifest, err := provisioning.BuildManifest(nil, roots, provisioning.BuildOptions{Signers: map[string]ed25519.PrivateKey{"signed": first}})
	if err != nil {
		t.Fatal(err)
	}
	rotatedManifest, err := provisioning.BuildManifest(nil, roots, provisioning.BuildOptions{Signers: map[string]ed25519.PrivateKey{"signed": second}})
	if err != nil {
		t.Fatal(err)
	}
	if firstManifest.Revision != rotatedManifest.Revision {
		t.Fatalf("signature rotation changed revision: %s != %s", firstManifest.Revision, rotatedManifest.Revision)
	}
	var signed, unsigned provisioning.Artifact
	for _, artifact := range firstManifest.Artifacts {
		switch artifact.Source {
		case "kb:signed":
			signed = artifact
		case "kb:unsigned":
			unsigned = artifact
		}
	}
	if signed.Signature == nil || signed.Signed {
		t.Fatalf("signed artifact must carry an unverified signature: %+v", signed)
	}
	if unsigned.Signature != nil || unsigned.Signed {
		t.Fatalf("unsigned artifact = %+v", unsigned)
	}
	verified, err := provisioning.VerifiedManifest(firstManifest, map[string][]ed25519.PublicKey{"signed": {first.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range verified.Artifacts {
		if artifact.Source == "kb:signed" && !artifact.Signed {
			t.Fatal("valid signed artifact was not verified")
		}
		if artifact.Source == "kb:unsigned" && artifact.Signed {
			t.Fatal("unsigned artifact became verified")
		}
	}
	if _, err := provisioning.VerifiedManifest(rotatedManifest, map[string][]ed25519.PublicKey{"signed": {second.Public().(ed25519.PublicKey)}}); err != nil {
		t.Fatalf("rotated key did not verify: %v", err)
	}
	if _, err := provisioning.VerifiedManifest(firstManifest, map[string][]ed25519.PublicKey{"signed": {first.Public().(ed25519.PublicKey), first.Public().(ed25519.PublicKey)}}); err == nil || !strings.Contains(err.Error(), "duplicate signing key ID") {
		t.Fatalf("duplicate key ID error = %v", err)
	}
}
