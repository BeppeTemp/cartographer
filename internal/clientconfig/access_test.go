package clientconfig_test

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// clients.<provider>.access (D1184) round-trips, is not a KB binding, survives
// a binding reset, and any other value is an error naming the key.
func TestClientAccess_RoundTripAndIsNotABinding(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.KnownKBs = []string{"kb-a", "kb-b"}
	if err := cfg.SetAccess("claude", clientconfig.AccessReadMin); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(raw), "access: read-min") {
		t.Fatalf("saved file = %s", raw)
	}
	got, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clients["claude"].Access != clientconfig.AccessReadMin {
		t.Errorf("access = %q after a round trip", got.Clients["claude"].Access)
	}
	if kbs, explicit := got.BoundKBs("claude"); explicit || len(kbs) != 2 {
		t.Errorf("BoundKBs = %v explicit=%v: access alone must not bind", kbs, explicit)
	}
	got.ResetBinding("claude")
	if got.Clients["claude"].Access != clientconfig.AccessReadMin {
		t.Error("ResetBinding dropped the access")
	}
	if err := got.Bind("claude", "kb-a"); err != nil || got.Clients["claude"].Access != clientconfig.AccessReadMin {
		t.Errorf("Bind lost the access: %v", err)
	}
	if err := got.SetAccess("claude", "write"); err == nil {
		t.Error("SetAccess accepted a bogus value")
	}
}

func TestClientAccess_InvalidValueIsALoadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("clients:\n  claude:\n    access: write\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := clientconfig.Load(dir)
	if err == nil || !strings.Contains(err.Error(), "clients.claude.access") {
		t.Fatalf("Load error = %v, want one naming clients.claude.access", err)
	}
}
