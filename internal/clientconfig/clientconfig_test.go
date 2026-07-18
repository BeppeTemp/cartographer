package clientconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func TestLoad_NotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := clientconfig.Load(dir)
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.AddAgent("claude")
	cfg.AddAgent("opencode")
	cfg.Auth = true
	cfg.KBs = []string{"homelab"}

	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, clientconfig.FileName)); err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ServerURL != cfg.ServerURL || loaded.ServerName != cfg.ServerName {
		t.Errorf("round-trip mismatch: got %+v, want %+v", loaded, cfg)
	}
	if !loaded.Auth {
		t.Error("Auth should round-trip as true")
	}
	if len(loaded.Agents) != 2 || !loaded.HasAgent("claude") || !loaded.HasAgent("opencode") {
		t.Errorf("Agents round-trip mismatch: %v", loaded.Agents)
	}
	if len(loaded.KBs) != 1 || loaded.KBs[0] != "homelab" {
		t.Errorf("KBs round-trip mismatch: %v", loaded.KBs)
	}
}

func TestAddAgent_Dedup(t *testing.T) {
	cfg := clientconfig.Default()
	cfg.AddAgent("claude")
	cfg.AddAgent("claude")
	if len(cfg.Agents) != 1 {
		t.Errorf("expected 1 agent after dedup, got %d: %v", len(cfg.Agents), cfg.Agents)
	}
}

func TestDefault_TrustIsTrue(t *testing.T) {
	cfg := clientconfig.Default()
	if !cfg.Trust {
		t.Error("Default().Trust should be true")
	}
}

func TestLoad_TrustAbsent_DefaultsTrue(t *testing.T) {
	dir := t.TempDir()
	// Config file written before the `trust` field existed: no `trust` key at all.
	data := "server_url: http://localhost:8080/mcp\nserver_name: cartographer\nauth: false\ntoken_env: CARTOGRAPHER_TOKENS\nagents: [claude]\n"
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Trust {
		t.Error("Trust should default to true when the `trust` key is absent")
	}
}

func TestLoad_TrustExplicitFalse_StaysFalse(t *testing.T) {
	dir := t.TempDir()
	data := "server_url: http://localhost:8080/mcp\ntrust: false\n"
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Trust {
		t.Error("explicit `trust: false` must round-trip as false, not be overridden by the default")
	}
}

func TestSaveAndLoad_TrustRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.Trust = false
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Trust {
		t.Error("Trust=false should round-trip as false")
	}
}

func TestDefault_ServerURLFallsBackToLocalhost(t *testing.T) {
	cfg := clientconfig.Default()
	if cfg.ServerURL != "http://localhost:8080/mcp" {
		t.Errorf("ServerURL = %q, want http://localhost:8080/mcp with no env set", cfg.ServerURL)
	}
}

func TestDefault_ServerURLFromEnv(t *testing.T) {
	t.Setenv("CARTOGRAPHER_SERVER_URL", "https://wiki.example.com/mcp")
	cfg := clientconfig.Default()
	if cfg.ServerURL != "https://wiki.example.com/mcp" {
		t.Errorf("ServerURL = %q, want the CARTOGRAPHER_SERVER_URL value", cfg.ServerURL)
	}
}

func TestLoad_ExistingYAMLWinsOverServerURLEnv(t *testing.T) {
	t.Setenv("CARTOGRAPHER_SERVER_URL", "https://env.example.com/mcp")
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.ServerURL = "https://yaml.example.com/mcp"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ServerURL != "https://yaml.example.com/mcp" {
		t.Errorf("ServerURL = %q, want the persisted yaml value (yaml > env precedence)", loaded.ServerURL)
	}
}

func TestDefault_SearchRootsDefaultsToDocuments(t *testing.T) {
	cfg := clientconfig.Default()
	if len(cfg.SearchRoots) != 1 || cfg.SearchRoots[0] != "~/Documents" {
		t.Errorf("SearchRoots = %v, want [~/Documents]", cfg.SearchRoots)
	}
}

func TestLoad_SearchRootsAbsent_DefaultsToDocuments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, clientconfig.FileName), []byte("server_url: http://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.SearchRoots) != 1 || loaded.SearchRoots[0] != "~/Documents" {
		t.Errorf("SearchRoots = %v, want [~/Documents]", loaded.SearchRoots)
	}
}

func TestSaveAndLoad_SearchRootsAndPathsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.SearchRoots = []string{"~/code", "/opt/repos"}
	cfg.Paths = map[string]string{"design-assets": "/mnt/shared/design"}

	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.SearchRoots) != 2 || loaded.SearchRoots[0] != "~/code" || loaded.SearchRoots[1] != "/opt/repos" {
		t.Errorf("SearchRoots round-trip mismatch: %v", loaded.SearchRoots)
	}
	if loaded.Paths["design-assets"] != "/mnt/shared/design" {
		t.Errorf("Paths round-trip mismatch: %v", loaded.Paths)
	}
}

func TestTargetDir(t *testing.T) {
	home, err := clientconfig.TargetDir()
	if err != nil {
		t.Fatalf("TargetDir(): %v", err)
	}
	realHome, _ := os.UserHomeDir()
	if home != realHome {
		t.Errorf("TargetDir() = %q, want home %q", home, realHome)
	}
}
