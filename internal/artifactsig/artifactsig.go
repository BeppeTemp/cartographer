// Package artifactsig signs and verifies provisioning artifact envelopes.
package artifactsig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	Algorithm       = "ed25519"
	EnvelopeVersion = 1
	domain          = "cartographer:provisioning-artifact:v1"
)

type Identity struct{ Source, Kind, Name, Version, ContentHash string }
type Signature struct {
	Algorithm       string `json:"algorithm"`
	KeyID           string `json:"key_id"`
	EnvelopeVersion int    `json:"envelope_version"`
	Value           string `json:"value"`
}

func ParseSeed(seed string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(seed)
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, fmt.Errorf("artifact signing seed must be %d-byte hexadecimal", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(b), nil
}
func ParsePublicKey(key string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(key)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("artifact signing public key must be %d-byte hexadecimal", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}
func PublicKeyHex(key ed25519.PublicKey) string { return hex.EncodeToString(key) }
func KeyID(key ed25519.PublicKey) string        { h := sha256.Sum256(key); return hex.EncodeToString(h[:]) }
func Sign(key ed25519.PrivateKey, identity Identity) (*Signature, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid artifact signing private key")
	}
	envelope, err := CanonicalEnvelope(identity)
	if err != nil {
		return nil, err
	}
	public := key.Public().(ed25519.PublicKey)
	return &Signature{Algorithm: Algorithm, KeyID: KeyID(public), EnvelopeVersion: EnvelopeVersion, Value: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, envelope))}, nil
}
func Verify(key ed25519.PublicKey, identity Identity, sig *Signature) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid artifact signing public key")
	}
	if sig == nil {
		return fmt.Errorf("missing artifact signature")
	}
	if sig.Algorithm != Algorithm || sig.EnvelopeVersion != EnvelopeVersion {
		return fmt.Errorf("unsupported artifact signature format")
	}
	if sig.KeyID != KeyID(key) {
		return fmt.Errorf("artifact signature key ID does not match public key")
	}
	value, err := base64.RawStdEncoding.DecodeString(sig.Value)
	if err != nil || len(value) != ed25519.SignatureSize {
		return fmt.Errorf("invalid artifact signature encoding")
	}
	envelope, err := CanonicalEnvelope(identity)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, envelope, value) {
		return fmt.Errorf("invalid artifact signature")
	}
	return nil
}
func CanonicalEnvelope(identity Identity) ([]byte, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	fields := []string{domain, identity.Source, identity.Kind, identity.Name, identity.Version, identity.ContentHash}
	buf := make([]byte, 0, 192)
	for _, field := range fields {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		buf = append(buf, length[:]...)
		buf = append(buf, field...)
	}
	return buf, nil
}
func validateIdentity(identity Identity) error {
	if !strings.HasPrefix(identity.Source, "kb:") || !canonicalField(strings.TrimPrefix(identity.Source, "kb:")) {
		return fmt.Errorf("invalid artifact signature source")
	}
	for _, f := range []string{identity.Kind, identity.Name} {
		if !canonicalField(f) {
			return fmt.Errorf("non-canonical artifact signature identity")
		}
	}
	if identity.Version != strings.TrimSpace(identity.Version) || strings.ContainsRune(identity.Version, '\x00') {
		return fmt.Errorf("non-canonical artifact signature identity")
	}
	if len(identity.ContentHash) != sha256.Size*2 {
		return fmt.Errorf("invalid artifact content hash")
	}
	if _, err := hex.DecodeString(identity.ContentHash); err != nil || strings.ToLower(identity.ContentHash) != identity.ContentHash {
		return fmt.Errorf("invalid artifact content hash")
	}
	return nil
}
func canonicalField(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && !strings.ContainsRune(value, '\x00')
}
