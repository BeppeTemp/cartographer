package provisioning_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
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

// A pinned KB accepts only signed artifacts: a response that strips the
// signature must fail the sync, not install unsigned (D906).
func TestVerifiedManifest_PinnedKBRejectsStrippedSignature(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{{Kind: "skill", Name: "s", Source: "kb:kb-a", ContentHash: "h"}}}
	_, err = provisioning.VerifiedManifest(m, map[string][]ed25519.PublicKey{"kb-a": {pub}})
	if err == nil || !strings.Contains(err.Error(), `KB "kb-a"`) || !strings.Contains(err.Error(), "skill/s") {
		t.Fatalf("stripped signature error = %v", err)
	}
	// No pin: an unsigned artifact is still installed (D906).
	if _, err := provisioning.VerifiedManifest(m, nil); err != nil {
		t.Fatalf("unpinned KB: %v", err)
	}
}

// #1282: a pin that does not match is a signature error, distinguishable from
// an unreachable server by errors.Is.
func TestVerifiedManifest_SignatureFailuresWrapErrSignature(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	pubA, privA, _ := ed25519.GenerateKey(nil)
	pubB, _, _ := ed25519.GenerateKey(nil)
	sig, err := artifactsig.Sign(privA, artifactsig.Identity{Source: "kb:kb-a", Kind: "skill", Name: "s", ContentHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{{Kind: "skill", Name: "s", Source: "kb:kb-a", ContentHash: hash, Signature: sig}}}
	if _, err := provisioning.VerifiedManifest(m, map[string][]ed25519.PublicKey{"kb-a": {pubA}}); err != nil {
		t.Fatalf("matching pin: %v", err)
	}
	_, err = provisioning.VerifiedManifest(m, map[string][]ed25519.PublicKey{"kb-a": {pubB}})
	if !errors.Is(err, provisioning.ErrSignature) || !strings.Contains(err.Error(), `KB "kb-a"`) {
		t.Errorf("wrong pin error = %v, want ErrSignature naming the KB", err)
	}
	// Flip a bit of the decoded signature: overwriting the last characters with
	// a fixed value is a no-op whenever the signature already ends that way.
	raw, err := base64.RawStdEncoding.DecodeString(sig.Value)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	sig.Value = base64.RawStdEncoding.EncodeToString(raw)
	if _, err := provisioning.VerifiedManifest(m, map[string][]ed25519.PublicKey{"kb-a": {pubA}}); !errors.Is(err, provisioning.ErrSignature) {
		t.Errorf("tampered signature error = %v, want ErrSignature", err)
	}
}
