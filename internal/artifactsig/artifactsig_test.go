package artifactsig

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

func testIdentity() Identity {
	return Identity{Source: "kb:homelab", Kind: "skill", Name: "deploy", Version: "1.0.0", ContentHash: strings.Repeat("a", 64)}
}
func TestSignVerifyAndDomainSeparation(t *testing.T) {
	key, err := ParseSeed(strings.Repeat("01", ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity()
	sig, err := Sign(key, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(key.Public().(ed25519.PublicKey), identity, sig); err != nil {
		t.Fatal(err)
	}
	wrongKey, _ := ParseSeed(strings.Repeat("09", ed25519.SeedSize))
	if err := Verify(wrongKey.Public().(ed25519.PublicKey), identity, sig); err == nil {
		t.Fatal("verification succeeded with wrong key")
	}
	wrongHash := identity
	wrongHash.ContentHash = strings.Repeat("b", 64)
	if err := Verify(key.Public().(ed25519.PublicKey), wrongHash, sig); err == nil {
		t.Fatal("verification succeeded with wrong hash")
	}
	for _, altered := range []Identity{{Source: "kb:other", Kind: identity.Kind, Name: identity.Name, Version: identity.Version, ContentHash: identity.ContentHash}, {Source: identity.Source, Kind: "hook", Name: identity.Name, Version: identity.Version, ContentHash: identity.ContentHash}, {Source: identity.Source, Kind: identity.Kind, Name: "other", Version: identity.Version, ContentHash: identity.ContentHash}} {
		if err := Verify(key.Public().(ed25519.PublicKey), altered, sig); err == nil {
			t.Fatalf("verification succeeded for %+v", altered)
		}
	}
}
func TestParsingAndMalformedSignature(t *testing.T) {
	if _, err := ParseSeed("bad"); err == nil {
		t.Fatal("expected invalid seed")
	}
	if _, err := ParsePublicKey("bad"); err == nil {
		t.Fatal("expected invalid public key")
	}
	key, _ := ParseSeed(strings.Repeat("02", ed25519.SeedSize))
	sig, _ := Sign(key, testIdentity())
	sig.Value = "not-base64!"
	if err := Verify(key.Public().(ed25519.PublicKey), testIdentity(), sig); err == nil {
		t.Fatal("expected malformed signature failure")
	}
}
func TestCanonicalEnvelopeDeterministic(t *testing.T) {
	a, err := CanonicalEnvelope(testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalEnvelope(testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("canonical envelope is not deterministic")
	}
	bad := testIdentity()
	bad.Source = "kb: homelab"
	if _, err := CanonicalEnvelope(bad); err == nil {
		t.Fatal("expected non-canonical identity failure")
	}
}
