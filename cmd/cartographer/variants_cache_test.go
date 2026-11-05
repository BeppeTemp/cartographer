package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

const cachedVariant = `schema: 1
name: fork-cli
base: crush
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
`

// The cache registers before the config loads: the load drops every provider
// the registry does not know (D898), so the reverse order deletes the variant.
func TestRegisteredVariantSurvivesConfigLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { _ = configurator.ReplaceVariants(nil) })
	_ = configurator.ReplaceVariants(nil)
	c := clientvariant.Cache{"fork-cli": clientvariant.NewCacheEntry("kb-a", []byte(cachedVariant), time.Unix(0, 0))}
	if err := clientvariant.SaveCache(home, c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cartographer.yaml"), []byte("agents: [fork-cli]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	registerCachedVariants(&errw)
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0] != "fork-cli" || len(cfg.Unsupported) != 0 {
		t.Fatalf("agents %v, unsupported %v, stderr %q", cfg.Agents, cfg.Unsupported, errw.String())
	}
}

// A cached variant that fails to register keeps its name in the config for the
// run: no destructive drop on a name a failure left unknown.
func TestUnregisteredCachedNameIsNotDropped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Cleanup(func() { _ = configurator.ReplaceVariants(nil) })
	_ = configurator.ReplaceVariants(nil)
	c := clientvariant.Cache{"fork-cli": clientvariant.NewCacheEntry("kb-a", []byte("{broken"), time.Unix(0, 0))}
	if err := clientvariant.SaveCache(home, c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cartographer.yaml"), []byte("agents: [fork-cli]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	registerCachedVariants(&errw)
	if errw.Len() == 0 {
		t.Fatal("no warning for the broken entry")
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || len(cfg.Unsupported) != 0 {
		t.Fatalf("agents %v, unsupported %v", cfg.Agents, cfg.Unsupported)
	}
}
