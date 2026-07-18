package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// TestResolveSopsAgeKeyFile covers the fallback chain (D53): explicit
// spec.SopsAgeKeyFile wins; otherwise <AgeKeyDir>/<name>.age if present;
// otherwise the global Sops.AgeKeyFile.
func TestResolveSopsAgeKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "wiki-kb.age")
	if err := os.WriteFile(keyPath, []byte("age-key-material"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit override wins", func(t *testing.T) {
		spec := config.KBSpec{SopsAgeKeyFile: "/explicit/key.age"}
		sops := config.SopsConfig{AgeKeyDir: dir, AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(spec, sops, "wiki-kb"); got != "/explicit/key.age" {
			t.Errorf("got %q, want /explicit/key.age", got)
		}
	})

	t.Run("age_key_dir convention when file exists", func(t *testing.T) {
		sops := config.SopsConfig{AgeKeyDir: dir, AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(config.KBSpec{}, sops, "wiki-kb"); got != keyPath {
			t.Errorf("got %q, want %q", got, keyPath)
		}
	})

	t.Run("falls back to global when convention file absent", func(t *testing.T) {
		sops := config.SopsConfig{AgeKeyDir: dir, AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(config.KBSpec{}, sops, "no-such-kb"); got != "/global/key.age" {
			t.Errorf("got %q, want /global/key.age", got)
		}
	})

	t.Run("falls back to global when age_key_dir unset", func(t *testing.T) {
		sops := config.SopsConfig{AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(config.KBSpec{}, sops, "wiki-kb"); got != "/global/key.age" {
			t.Errorf("got %q, want /global/key.age", got)
		}
	})
}
